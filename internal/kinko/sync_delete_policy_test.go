package kinko

import (
	"strings"
	"testing"
)

func TestApplySyncDeletionPolicy(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    syncDeleteMode
		yes     bool
		allowed bool
		want    syncActionKind
		wantErr string
	}{
		{name: "auto baseline", mode: syncDeleteAuto, allowed: true, want: syncActionDelete},
		{name: "auto refuses unowned", mode: syncDeleteAuto, wantErr: "lacks baseline"},
		{name: "keep suppresses", mode: syncDeleteKeep, want: syncActionIgnore},
		{name: "confirm needs yes", mode: syncDeleteConfirm, allowed: true, wantErr: "requires yes"},
		{name: "confirm accepted", mode: syncDeleteConfirm, allowed: true, yes: true, want: syncActionDelete},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := deletionTestPlan(test.allowed)
			err := applySyncDeletionPolicy(plan, test.mode, test.yes)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || plan.Actions[0].Kind != test.want {
				t.Fatalf("kind=%v error=%v", plan.Actions[0].Kind, err)
			}
			if test.want == syncActionDelete {
				capabilities := plan.Actions[0].RequiredCapabilities
				if len(capabilities) != 1 || capabilities[0] != syncCapabilityDelete {
					t.Fatalf("delete capabilities=%v, want delete only", capabilities)
				}
			}
		})
	}
}

func deletionTestPlan(allowed bool) *syncPlanV2 {
	identity := syncIdentity{Provider: strings.Repeat("a", 64), ProjectID: "project", MachineID: syncTestMachineID, Scope: scopeKindShared, Key: "DELETE_KEY"}
	plan := &syncPlanV2{
		Format:           2,
		Operation:        syncOperationPush,
		ProviderIdentity: identity.Provider,
		SelectorDigest:   strings.Repeat("d", 64),
		Actions: []syncPlannedAction{{
			EntryID: syncEntryID(identity), Kind: syncActionDelete, Identity: identity,
			RemotePresent: true, BaselinePresent: allowed, RemoteDeleteAllowed: allowed,
		}},
		Conflicts: []syncConflict{},
	}
	_ = finalizeSyncPlan(plan)
	return plan
}
