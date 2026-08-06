package kinko

import (
	"strings"
	"testing"
)

func TestApplySyncConflictRulesValidationAndDirection(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation syncOperation
		force     bool
		policy    syncConflictPolicy
		rules     map[string]syncResolution
		wantKind  syncActionKind
		wantErr   string
	}{
		{name: "push force local wins", operation: syncOperationPush, force: true, wantKind: syncActionUpdate},
		{name: "pull force remote wins", operation: syncOperationPull, force: true, wantKind: syncActionUpdate},
		{name: "explicit skip", operation: syncOperationPush, rules: map[string]syncResolution{strings.Repeat("b", 64): syncResolveSkip}, wantKind: syncActionIgnore},
		{name: "force incompatible", operation: syncOperationPush, force: true, policy: syncConflictLocal, wantErr: "cannot be combined"},
		{name: "unmatched", operation: syncOperationPush, rules: map[string]syncResolution{strings.Repeat("c", 64): syncResolveSkip}, wantErr: "matches no"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := conflictTestPlan(test.operation, true, true, true)
			rules := test.rules
			if _, ok := rules[strings.Repeat("b", 64)]; ok {
				rules = map[string]syncResolution{plan.Actions[0].EntryID: rules[strings.Repeat("b", 64)]}
			}
			err := applySyncConflictRules(plan, test.policy, rules, test.force)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || plan.Actions[0].Kind != test.wantKind {
				t.Fatalf("kind=%v error=%v", plan.Actions[0].Kind, err)
			}
		})
	}
}

func TestApplySyncConflictRulesRejectsAbsentSideDeletion(t *testing.T) {
	for _, test := range []struct {
		name       string
		local      bool
		remote     bool
		resolution syncResolution
	}{
		{name: "local", remote: true, resolution: syncResolveDeleteLocal},
		{name: "remote", local: true, resolution: syncResolveDeleteRemote},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := conflictTestPlan(syncOperationPush, test.local, test.remote, true)
			if err := applySyncConflictRules(plan, syncConflictFail, map[string]syncResolution{plan.Actions[0].EntryID: test.resolution}, false); err == nil || !strings.Contains(err.Error(), "absent") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func conflictTestPlan(operation syncOperation, local, remote, remoteDeleteAllowed bool) *syncPlanV2 {
	identity := syncIdentity{Provider: strings.Repeat("a", 64), ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "CONFLICT_KEY"}
	entryID := syncEntryID(identity)
	plan := &syncPlanV2{Format: 2, Operation: operation, ProviderIdentity: identity.Provider, SelectorDigest: strings.Repeat("d", 64), Actions: []syncPlannedAction{{EntryID: entryID, Kind: syncActionConflict, Identity: identity, LocalPresent: local, RemotePresent: remote, RemoteDeleteAllowed: remoteDeleteAllowed}}, Conflicts: []syncConflict{{EntryID: entryID, LocalPresent: local, RemotePresent: remote, RemoteDeleteAllowed: remoteDeleteAllowed}}}
	_ = finalizeSyncPlan(plan)
	return plan
}
