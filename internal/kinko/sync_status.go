package kinko

import (
	"errors"
	"fmt"
	"sort"
)

type syncStatusOptions struct {
	Provider string
	Online   bool
	JSON     bool
	Selector syncSelector
}

type syncStatusResult struct {
	Online           bool             `json:"online"`
	ProviderIdentity string           `json:"provider_identity,omitempty"`
	SelectorDigest   string           `json:"selector_digest"`
	Formats          []int            `json:"formats"`
	PathMaps         []syncPathMap    `json:"path_maps"`
	BaselineHealth   string           `json:"baseline_health"`
	CheckpointHealth string           `json:"checkpoint_health"`
	Drift            []syncResultItem `json:"drift"`
}

// buildSyncStatusResult produces a value-free snapshot. Passing nil remote is
// an offline status; a non-nil slice is an online, already-pinned provider read.
func buildSyncStatusResult(entries []syncEntry, remote []bwsSecret, envelope syncStateEnvelope, options syncStatusOptions, planContext syncPlanContext, pathMaps []syncPathMap) (syncStatusResult, error) {
	if options.Provider != "" && options.Provider != supportedSyncProvider {
		return syncStatusResult{}, fmt.Errorf("unsupported sync provider %q", options.Provider)
	}
	selector, digest, err := normalizeSyncSelector(options.Selector)
	if err != nil {
		return syncStatusResult{}, err
	}
	result := syncStatusResult{
		Online: options.Online, ProviderIdentity: maintenanceProviderIdentity(envelope),
		SelectorDigest: digest, Formats: []int{}, PathMaps: append([]syncPathMap(nil), pathMaps...),
		BaselineHealth: "absent", CheckpointHealth: "absent", Drift: []syncResultItem{},
	}
	if envelope.Format != 0 {
		result.Formats = append(result.Formats, envelope.Format)
		result.BaselineHealth = "healthy"
	}
	checkpoint, err := checkpointFromEnvelope(envelope)
	if err != nil {
		return syncStatusResult{}, err
	}
	if checkpoint != nil {
		result.CheckpointHealth = string(checkpoint.Phase)
	}
	if !options.Online {
		identities, _, err := resetStateIdentities(envelope)
		if err != nil {
			return syncStatusResult{}, err
		}
		localIdentities := make([]syncIdentity, 0, len(entries))
		for _, entry := range entries {
			identity, err := identityForLocalSyncEntry(entry, planContext)
			if err != nil {
				return syncStatusResult{}, err
			}
			localIdentities = append(localIdentities, identity)
		}
		if _, err := selectSyncIdentities(selector, append(identities, localIdentities...)); err != nil {
			return syncStatusResult{}, err
		}
		return result, nil
	}
	if remote == nil {
		return syncStatusResult{}, errors.New("online sync status requires a pinned provider snapshot")
	}
	plan, err := buildSyncPlanV2WithContext(syncOperationReconcile, entries, remote, envelope, selector, planContext)
	if err != nil {
		if err.Error() == "effective sync selection is empty for a mutating workflow" {
			return result, nil
		}
		return syncStatusResult{}, err
	}
	result.ProviderIdentity = plan.ProviderIdentity
	for _, action := range plan.Actions {
		if action.Kind == syncActionUnchanged || action.Kind == syncActionAdopt {
			continue
		}
		result.Drift = append(result.Drift, syncResultItem{
			Action: syncActionKindName(action.Kind), Profile: action.Identity.Profile,
			Scope: string(action.Identity.Scope), Path: action.Identity.Path,
			Key: action.Identity.Key, Reason: action.Reason,
		})
	}
	sort.Slice(result.Drift, func(i, j int) bool {
		left, right := result.Drift[i], result.Drift[j]
		return left.Scope+left.Profile+left.Path+left.Key < right.Scope+right.Profile+right.Path+right.Key
	})
	return result, nil
}
