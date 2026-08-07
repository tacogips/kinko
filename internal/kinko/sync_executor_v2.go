package kinko

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type syncExecutionOptions struct {
	Resume          syncResumeMode
	Checkpoints     syncCheckpointStore
	RetryPolicy     syncRetryPolicy
	Clock           syncClock
	RetryClassifier syncRetryClassifier
	PathMaps        []syncPathMap
}

type retryingSyncProvider struct {
	syncProvider
	policy     syncRetryPolicy
	clock      syncClock
	classifier syncRetryClassifier
	budget     *syncRetryBudget
}

func (provider retryingSyncProvider) ListProjects(ctx context.Context) ([]bwsProject, error) {
	return withSyncReadRetryBudget(ctx, provider.policy, provider.clock, provider.classifier, provider.budget, provider.syncProvider.ListProjects)
}

func (provider retryingSyncProvider) ListSecrets(ctx context.Context, projectID string) ([]bwsSecret, error) {
	return withSyncReadRetryBudget(ctx, provider.policy, provider.clock, provider.classifier, provider.budget, func(ctx context.Context) ([]bwsSecret, error) {
		return provider.syncProvider.ListSecrets(ctx, projectID)
	})
}

func (provider retryingSyncProvider) GetSecret(ctx context.Context, secretID string) (bwsSecret, error) {
	return withSyncReadRetryBudget(ctx, provider.policy, provider.clock, provider.classifier, provider.budget, func(ctx context.Context) (bwsSecret, error) {
		return provider.syncProvider.GetSecret(ctx, secretID)
	})
}

func executeSyncPlanV2(ctx context.Context, provider syncProvider, plan *syncPlanV2, data *vaultData, state *bwsSyncStateV2, progress syncProgressSink) (syncResult, error) {
	return executeSyncPlanV2WithOptions(ctx, provider, plan, data, state, progress, syncExecutionOptions{})
}

func executeSyncPlanV2WithOptions(ctx context.Context, provider syncProvider, plan *syncPlanV2, data *vaultData, state *bwsSyncStateV2, progress syncProgressSink, options syncExecutionOptions) (syncResult, error) {
	if data == nil || state == nil {
		return syncResult{}, errors.New("sync executor local state is not initialized")
	}
	if progress == nil {
		progress = discardSyncProgress{}
	}
	if err := validateSyncPlanShape(plan); err != nil {
		return syncResult{}, fmt.Errorf("validate executable sync plan: %w", err)
	}
	if len(plan.Conflicts) != 0 {
		return syncResult{}, errors.New("sync executor refuses a plan containing unresolved conflicts")
	}
	if err := requirePlanCapabilities(provider, plan); err != nil {
		return syncResult{}, err
	}
	options, err := normalizeSyncExecutionOptions(options)
	if err != nil {
		return syncResult{}, err
	}
	provider = retryingSyncProvider{syncProvider: provider, policy: options.RetryPolicy, clock: options.Clock, classifier: options.RetryClassifier, budget: &syncRetryBudget{}}
	if err := progress.Emit(syncProgressEvent{Operation: string(plan.Operation), Phase: "preflight", Status: "started"}); err != nil {
		return syncResult{}, err
	}
	workingData := cloneVaultData(data)
	workingState := cloneBWSSyncStateV2(state)
	checkpoint, resumed, err := selectExecutionCheckpoint(workingState.Checkpoint, plan, options.Resume)
	if err != nil {
		return syncResult{}, err
	}
	workingState.Checkpoint = checkpoint
	remote, adoptedOnResume, err := preflightExecution(ctx, provider, plan, workingData, checkpoint, resumed)
	if err != nil {
		return syncResult{}, err
	}
	result := syncResult{Actions: []syncResultItem{}, Conflicts: []string{}}
	checkpoint.Phase = syncCheckpointExecuting
	if planHasRemoteMutation(plan) {
		if err := saveExecutionCheckpoint(options.Checkpoints, checkpoint); err != nil {
			return result, fmt.Errorf("persist checkpoint before remote mutation: %w", err)
		}
	}
	for index, action := range plan.Actions {
		if index < len(checkpoint.Confirmed) {
			confirmed := checkpoint.Confirmed[index]
			updateV2StateForConfirmedAction(workingState, plan, action, confirmed, workingData, remote[action.ActionID], options.PathMaps)
			appendV2Result(&result, action)
			if err := progress.Emit(syncProgressEvent{Operation: string(plan.Operation), Phase: "resume", ActionID: action.ActionID, EntryID: action.EntryID, Status: "confirmed"}); err != nil {
				return setPartial(result, true), err
			}
			continue
		}
		if err := progress.Emit(syncProgressEvent{Operation: string(plan.Operation), Phase: "execute", ActionID: action.ActionID, EntryID: action.EntryID, Status: "started"}); err != nil {
			return setPartial(result, len(checkpoint.Confirmed) > 0), err
		}
		confirmed, alreadyApplied := adoptedOnResume[action.ActionID]
		var actionErr error
		if !alreadyApplied {
			var mutated bwsSecret
			confirmed, mutated, actionErr = executePinnedAction(ctx, provider, plan.Operation, action, workingData, remote[action.ActionID], options.PathMaps)
			if actionErr == nil && mutated.ID != "" {
				// Preflight only fetches a secret for actions that already carry a
				// precondition (RemotePresent), so a freshly confirmed create has no
				// entry in remote yet. Record the just-mutated secret so its
				// authoritative OrganizationID reaches updateV2StateForConfirmedAction.
				remote[action.ActionID] = mutated
			}
		}
		if actionErr != nil {
			return setPartial(result, len(checkpoint.Confirmed) > 0), fmt.Errorf("execute sync action %s: %w", action.ActionID, actionErr)
		}
		updateV2StateForConfirmedAction(workingState, plan, action, confirmed, workingData, remote[action.ActionID], options.PathMaps)
		checkpoint.Confirmed = append(checkpoint.Confirmed, confirmed)
		appendV2Result(&result, action)
		if planHasRemoteMutation(plan) {
			if err := saveExecutionCheckpoint(options.Checkpoints, checkpoint); err != nil {
				return setPartial(result, true), fmt.Errorf("persist checkpoint after confirmed action: %w", err)
			}
		}
		if err := progress.Emit(syncProgressEvent{Operation: string(plan.Operation), Phase: "execute", ActionID: action.ActionID, EntryID: action.EntryID, Status: "confirmed"}); err != nil {
			return setPartial(result, true), err
		}
	}
	checkpoint.Phase = syncCheckpointComplete
	if planHasRemoteMutation(plan) {
		if err := saveExecutionCheckpoint(options.Checkpoints, checkpoint); err != nil {
			return setPartial(result, len(checkpoint.Confirmed) > 0), fmt.Errorf("persist completed checkpoint: %w", err)
		}
	}
	// Pull mutations become visible together. The caller persists this snapshot
	// through the existing atomic encrypted vault writer exactly once.
	*data = *workingData
	*state = *workingState
	if err := progress.Emit(syncProgressEvent{Operation: string(plan.Operation), Phase: "complete", Status: "confirmed"}); err != nil {
		return result, err
	}
	return result, nil
}

func normalizeSyncExecutionOptions(options syncExecutionOptions) (syncExecutionOptions, error) {
	if options.Resume == "" {
		options.Resume = syncResumeAuto
	}
	if options.Resume != syncResumeAuto && options.Resume != syncResumeRequire && options.Resume != syncResumeNever {
		return syncExecutionOptions{}, fmt.Errorf("unsupported sync resume mode %q", options.Resume)
	}
	if options.RetryPolicy == (syncRetryPolicy{}) {
		options.RetryPolicy = defaultSyncRetryPolicy()
	}
	if err := validateSyncRetryPolicy(options.RetryPolicy); err != nil {
		return syncExecutionOptions{}, err
	}
	if options.Clock == nil {
		options.Clock = realSyncClock{}
	}
	if options.RetryClassifier == nil {
		options.RetryClassifier = defaultSyncRetryClassifier{}
	}
	if options.Checkpoints == nil {
		options.Checkpoints = syncCheckpointStoreFunc(func(*syncCheckpoint) error { return nil })
	}
	return options, nil
}

func selectExecutionCheckpoint(existing *syncCheckpoint, plan *syncPlanV2, mode syncResumeMode) (*syncCheckpoint, bool, error) {
	if existing == nil {
		if mode == syncResumeRequire {
			return nil, false, errors.New("sync resume was required but no checkpoint exists")
		}
		checkpoint, err := newSyncCheckpoint(plan)
		return checkpoint, false, err
	}
	validateErr := validateSyncCheckpoint(existing, plan)
	if existing.Phase == syncCheckpointComplete && validateErr != nil {
		// A complete checkpoint has no pending work to resume or lose, so a plan
		// mismatch just means it is a superseded artifact of a finished run, not
		// an in-flight run that --resume is meant to protect.
		if mode == syncResumeRequire {
			return nil, false, errors.New("sync resume was required but the existing checkpoint belongs to a completed run that does not match the current pinned plan")
		}
		checkpoint, err := newSyncCheckpoint(plan)
		return checkpoint, false, err
	}
	if mode == syncResumeNever {
		return nil, false, errors.New("an existing sync checkpoint must be reset before --resume=never")
	}
	if validateErr != nil {
		return nil, false, fmt.Errorf("validate sync resume checkpoint: %w", validateErr)
	}
	return cloneSyncCheckpoint(existing), true, nil
}

func cloneSyncCheckpoint(checkpoint *syncCheckpoint) *syncCheckpoint {
	if checkpoint == nil {
		return nil
	}
	clone := *checkpoint
	clone.Actions = cloneSyncPlanV2(&syncPlanV2{Actions: checkpoint.Actions}).Actions
	clone.Confirmed = append([]syncCheckpointResult(nil), checkpoint.Confirmed...)
	return &clone
}

func saveExecutionCheckpoint(store syncCheckpointStore, checkpoint *syncCheckpoint) error {
	if err := validateSyncCheckpointShape(checkpoint); err != nil {
		return err
	}
	return store.Save(cloneSyncCheckpoint(checkpoint))
}

func planHasRemoteMutation(plan *syncPlanV2) bool {
	if plan.Operation == syncOperationPull || plan.Operation == syncOperationBootstrap {
		return false
	}
	for _, action := range plan.Actions {
		if action.Kind == syncActionCreate || action.Kind == syncActionUpdate || action.Kind == syncActionDelete {
			return true
		}
	}
	return false
}

func requirePlanCapabilities(provider syncProvider, plan *syncPlanV2) error {
	capabilities := []syncCapability{}
	for _, action := range plan.Actions {
		capabilities = append(capabilities, action.RequiredCapabilities...)
	}
	return requireSyncCapabilities(provider, uniqueSortedCapabilities(capabilities)...)
}

func preflightSyncActions(ctx context.Context, provider syncProvider, plan *syncPlanV2) (map[string]bwsSecret, error) {
	remote := map[string]bwsSecret{}
	for _, action := range plan.Actions {
		if action.Precondition == nil {
			if plan.Operation != syncOperationPull && action.Kind == syncActionCreate {
				secrets, err := provider.ListSecrets(ctx, action.Identity.ProjectID)
				if err != nil {
					return nil, fmt.Errorf("preflight create list: %w", err)
				}
				for _, secret := range secrets {
					if secret.ProjectID != action.Identity.ProjectID {
						return nil, errors.New("preflight create list crossed the pinned project boundary")
					}
					if secret.Key == action.IntendedName {
						return nil, errors.New("preflight create name is no longer absent")
					}
				}
			}
			continue
		}
		secret, err := provider.GetSecret(ctx, action.Precondition.SecretID)
		if err != nil {
			return nil, fmt.Errorf("preflight secret %q: %w", action.Precondition.SecretID, err)
		}
		if err := validateSecretPrecondition(action.Precondition, secret); err != nil {
			return nil, err
		}
		remote[action.ActionID] = secret
	}
	return remote, nil
}

func preflightExecution(ctx context.Context, provider syncProvider, plan *syncPlanV2, data *vaultData, checkpoint *syncCheckpoint, resumed bool) (map[string]bwsSecret, map[string]syncCheckpointResult, error) {
	if !resumed {
		remote, err := preflightSyncActions(ctx, provider, plan)
		return remote, map[string]syncCheckpointResult{}, err
	}
	remote := map[string]bwsSecret{}
	adopted := map[string]syncCheckpointResult{}
	confirmed := make(map[string]syncCheckpointResult, len(checkpoint.Confirmed))
	for _, result := range checkpoint.Confirmed {
		confirmed[result.ActionID] = result
	}
	for _, action := range plan.Actions {
		if err := validateResumeLocalValue(data, plan.Operation, action); err != nil {
			return nil, nil, err
		}
		if result, ok := confirmed[action.ActionID]; ok {
			secret, err := validateConfirmedResumeAction(ctx, provider, plan.Operation, action, result)
			if err != nil {
				return nil, nil, err
			}
			remote[action.ActionID] = secret
			continue
		}
		if plan.Operation != syncOperationPull && plan.Operation != syncOperationBootstrap && (action.Kind == syncActionCreate || action.Kind == syncActionUpdate || action.Kind == syncActionDelete) {
			result, retry, err := reconcileAmbiguousMutation(ctx, provider, action)
			if err != nil {
				return nil, nil, fmt.Errorf("reconcile unconfirmed resume action: %w", err)
			}
			if !retry {
				adopted[action.ActionID] = result
				if action.Kind != syncActionDelete {
					secret, getErr := provider.GetSecret(ctx, result.SecretID)
					if getErr != nil {
						return nil, nil, fmt.Errorf("reload reconciled resume secret: %w", getErr)
					}
					remote[action.ActionID] = secret
				}
			}
			continue
		}
		if action.Precondition != nil {
			secret, err := provider.GetSecret(ctx, action.Precondition.SecretID)
			if err != nil {
				return nil, nil, fmt.Errorf("reload resume precondition: %w", err)
			}
			if err := validateSecretPrecondition(action.Precondition, secret); err != nil {
				return nil, nil, err
			}
			remote[action.ActionID] = secret
		}
	}
	return remote, adopted, nil
}

func validateResumeLocalValue(data *vaultData, operation syncOperation, action syncPlannedAction) error {
	if operation == syncOperationPull || operation == syncOperationBootstrap || (action.Kind != syncActionCreate && action.Kind != syncActionUpdate) {
		return nil
	}
	value, ok := localValueForIdentity(data, action.Identity)
	if !ok || valueSHA256(value) != action.IntendedValueSHA256 {
		return errors.New("local value changed since the checkpoint was created")
	}
	return nil
}

func validateConfirmedResumeAction(ctx context.Context, provider syncProvider, operation syncOperation, action syncPlannedAction, result syncCheckpointResult) (bwsSecret, error) {
	if operation == syncOperationPull || operation == syncOperationBootstrap {
		if action.Precondition == nil {
			return bwsSecret{}, nil
		}
		secret, err := provider.GetSecret(ctx, action.Precondition.SecretID)
		if err != nil {
			return bwsSecret{}, fmt.Errorf("reload confirmed local action source: %w", err)
		}
		if err := validateSecretPrecondition(action.Precondition, secret); err != nil {
			return bwsSecret{}, err
		}
		return secret, nil
	}
	if action.Kind == syncActionDelete {
		_, err := provider.GetSecret(ctx, result.SecretID)
		if errors.Is(err, errBWSSyncSecretNotFound) {
			return bwsSecret{}, nil
		}
		if err != nil {
			return bwsSecret{}, fmt.Errorf("reload confirmed deleted secret: %w", err)
		}
		return bwsSecret{}, errors.New("confirmed deleted secret is present during resume")
	}
	if action.Kind == syncActionCreate || action.Kind == syncActionUpdate {
		secret, err := provider.GetSecret(ctx, result.SecretID)
		if err != nil {
			return bwsSecret{}, fmt.Errorf("reload confirmed mutated secret: %w", err)
		}
		if err := validateIntendedRemoteSecret(action, secret); err != nil {
			return bwsSecret{}, err
		}
		if secret.RevisionDate != result.Revision {
			return bwsSecret{}, errors.New("confirmed secret revision changed during resume")
		}
		return secret, nil
	}
	if action.Precondition != nil {
		secret, err := provider.GetSecret(ctx, action.Precondition.SecretID)
		if err != nil {
			return bwsSecret{}, fmt.Errorf("reload confirmed precondition: %w", err)
		}
		if err := validateSecretPrecondition(action.Precondition, secret); err != nil {
			return bwsSecret{}, err
		}
		return secret, nil
	}
	return bwsSecret{}, nil
}

func executePinnedAction(ctx context.Context, provider syncProvider, operation syncOperation, action syncPlannedAction, data *vaultData, remote bwsSecret, pathMaps []syncPathMap) (syncCheckpointResult, bwsSecret, error) {
	remoteMutation := operation != syncOperationPull && operation != syncOperationBootstrap
	if !remoteMutation {
		if err := applyLocalV2Action(data, action, remote, pathMaps); err != nil {
			return syncCheckpointResult{}, bwsSecret{}, err
		}
		return checkpointResult(action, remote), remote, nil
	}
	switch action.Kind {
	case syncActionCreate, syncActionUpdate, syncActionDelete:
		return executeRemoteMutation(ctx, provider, action, data)
	case syncActionAdopt, syncActionUnchanged, syncActionIgnore:
		return checkpointResult(action, remote), remote, nil
	default:
		return syncCheckpointResult{}, bwsSecret{}, fmt.Errorf("unsupported executable action kind %d", action.Kind)
	}
}

// executeRemoteMutation performs the pinned create/update/delete and returns
// the resulting secret alongside its value-free checkpoint result. The secret
// is the zero value for delete (nothing remains to describe) and for an
// ambiguous outcome resolved without a confirmed read-back; callers fall back
// to the plan's provider context in that case.
func executeRemoteMutation(ctx context.Context, provider syncProvider, action syncPlannedAction, data *vaultData) (syncCheckpointResult, bwsSecret, error) {
	if err := preflightRemoteMutation(ctx, provider, action); err != nil {
		return syncCheckpointResult{}, bwsSecret{}, err
	}
	mutate := func() (bwsSecret, error) {
		switch action.Kind {
		case syncActionCreate, syncActionUpdate:
			value, ok := localValueForIdentity(data, action.Identity)
			if !ok || valueSHA256(value) != action.IntendedValueSHA256 {
				return bwsSecret{}, errors.New("local value changed after planning")
			}
			name, note, err := intendedRemoteMetadata(action.Identity)
			if err != nil || name != action.IntendedName || valueSHA256(note) != action.IntendedNoteSHA256 {
				return bwsSecret{}, errors.New("intended remote metadata changed after planning")
			}
			request := bwsMutationRequest{ProjectID: action.Identity.ProjectID, Name: name, Value: value, Note: note}
			if action.Kind == syncActionUpdate {
				request.SecretID = action.Precondition.SecretID
				return provider.UpdateSecret(ctx, request)
			}
			return provider.CreateSecret(ctx, request)
		case syncActionDelete:
			return bwsSecret{}, provider.DeleteSecret(ctx, action.Precondition.SecretID)
		default:
			return bwsSecret{}, errors.New("action is not a remote mutation")
		}
	}
	secret, err := mutate()
	if err == nil {
		if action.Kind == syncActionDelete {
			return syncCheckpointResult{ActionID: action.ActionID, SecretID: action.Precondition.SecretID}, bwsSecret{}, nil
		}
		if err := validateIntendedRemoteSecret(action, secret); err != nil {
			return syncCheckpointResult{}, bwsSecret{}, err
		}
		return checkpointResult(action, secret), secret, nil
	}
	if !mutationOutcomeMayBeAmbiguous(err) {
		return syncCheckpointResult{}, bwsSecret{}, err
	}
	result, retry, reconcileErr := reconcileAmbiguousMutation(ctx, provider, action)
	if reconcileErr != nil {
		return syncCheckpointResult{}, bwsSecret{}, errors.Join(err, reconcileErr)
	}
	if !retry {
		return result, bwsSecret{}, nil
	}
	secret, retryErr := mutate()
	if retryErr == nil {
		if action.Kind == syncActionDelete {
			return syncCheckpointResult{ActionID: action.ActionID, SecretID: action.Precondition.SecretID}, bwsSecret{}, nil
		}
		if err := validateIntendedRemoteSecret(action, secret); err != nil {
			return syncCheckpointResult{}, bwsSecret{}, err
		}
		return checkpointResult(action, secret), secret, nil
	}
	if !mutationOutcomeMayBeAmbiguous(retryErr) {
		return syncCheckpointResult{}, bwsSecret{}, retryErr
	}
	result, retry, reconcileErr = reconcileAmbiguousMutation(ctx, provider, action)
	if reconcileErr != nil {
		return syncCheckpointResult{}, bwsSecret{}, errors.Join(retryErr, reconcileErr)
	}
	if retry {
		return syncCheckpointResult{}, bwsSecret{}, errors.Join(retryErr, errors.New("mutation outcome remained ambiguous after one conditional retry"))
	}
	return result, bwsSecret{}, nil
}

func preflightRemoteMutation(ctx context.Context, provider syncProvider, action syncPlannedAction) error {
	switch action.Kind {
	case syncActionCreate:
		secrets, err := provider.ListSecrets(ctx, action.Identity.ProjectID)
		if err != nil {
			return fmt.Errorf("recheck create target: %w", err)
		}
		for _, secret := range secrets {
			if secret.ProjectID != action.Identity.ProjectID {
				return errors.New("create recheck crossed the pinned project boundary")
			}
			if secret.Key == action.IntendedName {
				return errors.New("create target is no longer absent")
			}
		}
		return nil
	case syncActionUpdate, syncActionDelete:
		if action.Precondition == nil {
			return errors.New("remote mutation is missing its pinned precondition")
		}
		secret, err := provider.GetSecret(ctx, action.Precondition.SecretID)
		if err != nil {
			return fmt.Errorf("recheck remote mutation target: %w", err)
		}
		return validateSecretPrecondition(action.Precondition, secret)
	default:
		return errors.New("action is not a remote mutation")
	}
}

func validateIntendedRemoteSecret(action syncPlannedAction, secret bwsSecret) error {
	if secret.ID == "" || secret.RevisionDate == "" || secret.ProjectID != action.Identity.ProjectID || secret.Key != action.IntendedName || valueSHA256(secret.Note) != action.IntendedNoteSHA256 || valueSHA256(secret.Value) != action.IntendedValueSHA256 {
		return errors.New("remote mutation result does not match the pinned intended content")
	}
	if action.Kind == syncActionUpdate && (action.Precondition == nil || secret.ID != action.Precondition.SecretID) {
		return errors.New("remote update result changed immutable identity")
	}
	return nil
}

func applyLocalV2Action(data *vaultData, action syncPlannedAction, remote bwsSecret, pathMaps []syncPathMap) error {
	ref, err := materializedIdentityScopeRef(action.Identity, pathMaps)
	if err != nil {
		return err
	}
	switch action.Kind {
	case syncActionCreate, syncActionUpdate:
		setLocalSyncValue(data, ref, action.Identity.Key, remote.Value)
	case syncActionDelete:
		deleteLocalSyncValue(data, ref, action.Identity.Key)
	}
	return nil
}

func updateV2StateForConfirmedAction(state *bwsSyncStateV2, plan *syncPlanV2, action syncPlannedAction, confirmed syncCheckpointResult, data *vaultData, remote bwsSecret, pathMaps ...[]syncPathMap) {
	if state.Entries == nil {
		state.Entries = map[string]syncStateEntryV2{}
	}
	if state.Ownership == nil {
		state.Ownership = map[string]syncOwnershipRecord{}
	}
	if action.Kind == syncActionDelete {
		delete(state.Entries, action.EntryID)
		delete(state.Ownership, action.EntryID)
		return
	}
	valueHash := action.IntendedValueSHA256
	if plan.Operation == syncOperationPull || plan.Operation == syncOperationBootstrap || valueHash == "" {
		valueHash = valueSHA256(remote.Value)
	}
	name := action.IntendedName
	if name == "" {
		name = remote.Key
	}
	precondition := action.Precondition
	endpoint := ""
	switch {
	case precondition != nil && precondition.Endpoint != "":
		endpoint = precondition.Endpoint
	case state.Context != nil:
		endpoint = state.Context.Endpoint
	}
	// A create has no precondition (nothing existed remotely to pin), and the
	// pinned context organization is legitimately empty when no organization is
	// configured (see preconditionForSecret). Prefer the just-confirmed remote
	// secret's observed organization over that empty configuration value so a
	// create's state entry does not disagree with entries recorded through a
	// precondition (see inferSyncPlanContext/inferMaintenanceContext, which
	// otherwise treat the mix as more than one provider context).
	organizationID := ""
	switch {
	case precondition != nil && precondition.OrganizationID != "":
		organizationID = precondition.OrganizationID
	case remote.OrganizationID != "":
		organizationID = remote.OrganizationID
	case state.Context != nil:
		organizationID = state.Context.OrganizationID
	}
	entry := syncStateEntryV2{Schema: syncStateEntrySchemaV2, ProviderIdentity: plan.ProviderIdentity, Endpoint: endpoint, OrganizationID: organizationID, ProjectID: action.Identity.ProjectID, MachineID: action.Identity.MachineID, SecretID: confirmed.SecretID, Name: name, Revision: confirmed.Revision, Profile: action.Identity.Profile, Key: action.Identity.Key, ValueSHA256: valueHash, Scope: action.Identity.Scope}
	if strings.HasPrefix(action.Identity.Path, "logical:") {
		entry.LogicalPath = strings.TrimPrefix(action.Identity.Path, "logical:")
		if len(pathMaps) > 0 && len(pathMaps[0]) > 0 {
			if localPath, err := mapLogicalToLocal(entry.LogicalPath, pathMaps[0]); err == nil {
				entry.LocalPath = localPath
			}
		}
	} else if strings.HasPrefix(action.Identity.Path, "local:") {
		entry.LocalPath = strings.TrimPrefix(action.Identity.Path, "local:")
	}
	state.Entries[action.EntryID] = entry
	if confirmed.SecretID != "" && confirmed.Revision != "" {
		state.Ownership[action.EntryID] = syncOwnershipRecord{SecretID: confirmed.SecretID, ProviderIdentity: plan.ProviderIdentity, Revision: confirmed.Revision, Identity: action.Identity}
	}
}

func materializedIdentityScopeRef(identity syncIdentity, pathMaps []syncPathMap) (scopeRef, error) {
	if identity.Scope == scopeKindShared {
		return scopeRef{kind: scopeKindShared}, nil
	}
	if strings.HasPrefix(identity.Path, "logical:") {
		if len(pathMaps) == 0 {
			return scopeRef{}, errors.New("logical sync path has no materialization mapping")
		}
		localPath, err := mapLogicalToLocal(strings.TrimPrefix(identity.Path, "logical:"), pathMaps)
		if err != nil {
			return scopeRef{}, err
		}
		return scopeRef{profile: identity.Profile, kind: identity.Scope, path: localPath}, nil
	}
	return identityScopeRef(identity), nil
}

func appendV2Result(result *syncResult, action syncPlannedAction) {
	item := syncResultItem{Action: syncActionKindName(action.Kind), Profile: action.Identity.Profile, Scope: string(action.Identity.Scope), Key: action.Identity.Key, Reason: action.Reason}
	item.Path = strings.TrimPrefix(strings.TrimPrefix(action.Identity.Path, "local:"), "logical:")
	result.Actions = append(result.Actions, item)
	switch action.Kind {
	case syncActionCreate:
		result.Created++
	case syncActionUpdate:
		result.Updated++
	case syncActionDelete:
		result.Deleted++
	case syncActionAdopt:
		result.Adopted++
	case syncActionUnchanged:
		result.Unchanged++
	}
}

func identityScopeRef(identity syncIdentity) scopeRef {
	return scopeRef{profile: identity.Profile, kind: identity.Scope, path: strings.TrimPrefix(strings.TrimPrefix(identity.Path, "local:"), "logical:")}
}

func localValueForIdentity(data *vaultData, identity syncIdentity) (string, bool) {
	if identity.Scope == scopeKindShared {
		value, ok := data.Shared[identity.Key]
		return value, ok
	}
	value, ok := data.Profiles[identity.Profile][identityScopeRef(identity).path][identity.Key]
	return value, ok
}

func cloneVaultData(data *vaultData) *vaultData {
	clone := &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{}}
	for key, value := range data.Shared {
		clone.Shared[key] = value
	}
	for profile, scopes := range data.Profiles {
		clone.Profiles[profile] = map[string]map[string]string{}
		for path, values := range scopes {
			clone.Profiles[profile][path] = map[string]string{}
			for key, value := range values {
				clone.Profiles[profile][path][key] = value
			}
		}
	}
	return clone
}

func cloneBWSSyncStateV2(state *bwsSyncStateV2) *bwsSyncStateV2 {
	clone := *state
	if state.Context != nil {
		contextCopy := *state.Context
		clone.Context = &contextCopy
	}
	clone.Entries = map[string]syncStateEntryV2{}
	for id, entry := range state.Entries {
		clone.Entries[id] = entry
	}
	clone.Ownership = map[string]syncOwnershipRecord{}
	for id, record := range state.Ownership {
		clone.Ownership[id] = record
	}
	return &clone
}
