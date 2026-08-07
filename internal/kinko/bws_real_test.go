//go:build bws_real && !race

package kinko

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
)

const (
	envTestRealBWS        = "KINKO_TEST_REAL_BWS"
	envTestBWSAccessToken = "KINKO_TEST_BWS_ACCESS_TOKEN"
	envTestBWSProjectID   = "KINKO_TEST_BWS_PROJECT_ID"
)

type realBWSTestOwnership struct {
	ProjectID    string
	RunPrefix    string
	SnapshotIDs  map[string]struct{}
	CreatedIDs   map[string]struct{}
	manifestPath string
}

type realBWSCleanupManifest struct {
	ProjectID string   `json:"project_id"`
	RunPrefix string   `json:"run_prefix"`
	SecretIDs []string `json:"secret_ids"`
}

type realBWSOwnershipNote struct {
	Owner     string `json:"owner"`
	RunPrefix string `json:"run_prefix"`
}

func TestRealBWSCRUDOwnershipScoped(t *testing.T) {
	ownership := requireRealBWSTest(t)
	provider := newRealBWSTestProvider(t)

	secrets, err := provider.ListSecrets(context.Background(), ownership.ProjectID)
	if err != nil {
		t.Fatalf("snapshot real BWS project: %v", err)
	}
	for _, secret := range secrets {
		ownership.SnapshotIDs[secret.ID] = struct{}{}
	}
	if err := persistRealBWSCleanupManifest(&ownership); err != nil {
		t.Fatalf("create value-free cleanup manifest: %v", err)
	}
	t.Logf("real BWS cleanup manifest: %s", ownership.manifestPath)
	t.Cleanup(func() { cleanupRealBWSTest(t, provider, &ownership) })

	noteBytes, err := json.Marshal(realBWSOwnershipNote{Owner: "kinko-real-bws-test", RunPrefix: ownership.RunPrefix})
	if err != nil {
		t.Fatalf("encode ownership note: %v", err)
	}
	request := bwsMutationRequest{
		ProjectID: ownership.ProjectID,
		Name:      ownership.RunPrefix + "-crud",
		Value:     "kinko-real-bws-test-create",
		Note:      string(noteBytes),
	}
	created, err := createRealBWSSecret(context.Background(), t, provider, &ownership, request)
	if err != nil {
		t.Fatalf("create real BWS canary: %v", err)
	}

	fetched, err := provider.GetSecret(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("read real BWS canary: %v", err)
	}
	if fetched.ProjectID != ownership.ProjectID || fetched.Key != request.Name || fetched.Note != request.Note {
		t.Fatal("real BWS create/read response crossed the ownership boundary")
	}

	request.SecretID = created.ID
	request.Value = "kinko-real-bws-test-update"
	updated, err := provider.UpdateSecret(context.Background(), request)
	if err != nil {
		t.Fatalf("update real BWS canary: %v", err)
	}
	if updated.ID != created.ID || updated.Value != request.Value {
		t.Fatal("real BWS update response did not match the canary")
	}
}

func requireRealBWSTest(t *testing.T) realBWSTestOwnership {
	t.Helper()
	if os.Getenv(envTestRealBWS) != "1" {
		t.Skip("real BWS test requires KINKO_TEST_REAL_BWS=1 and the bws_real build tag")
	}
	token := os.Getenv(envTestBWSAccessToken)
	projectID := os.Getenv(envTestBWSProjectID)
	if token == "" || projectID == "" {
		t.Fatalf("real BWS test requires %s and %s", envTestBWSAccessToken, envTestBWSProjectID)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("generate real BWS ownership prefix: %v", err)
	}
	manifest, err := os.CreateTemp("", "kinko-bws-real-cleanup-*.json")
	if err != nil {
		t.Fatalf("reserve cleanup manifest: %v", err)
	}
	manifestPath := manifest.Name()
	if err := manifest.Close(); err != nil {
		t.Fatalf("close cleanup manifest: %v", err)
	}
	if err := os.Chmod(manifestPath, 0o600); err != nil {
		t.Fatalf("protect cleanup manifest: %v", err)
	}
	ownership := realBWSTestOwnership{
		ProjectID: projectID, RunPrefix: "kinko-real-test-" + hex.EncodeToString(random),
		SnapshotIDs: map[string]struct{}{}, CreatedIDs: map[string]struct{}{}, manifestPath: manifestPath,
	}
	t.Cleanup(func() {
		if len(ownership.CreatedIDs) == 0 {
			_ = os.Remove(ownership.manifestPath)
		}
	})
	return ownership
}

func newRealBWSTestProvider(t *testing.T) syncProvider {
	t.Helper()
	client, err := newBWSClient(os.Getenv(envTestBWSAccessToken), io.Discard)
	if err != nil {
		t.Fatalf("initialize real BWS client: %v", err)
	}
	gate, err := inspectBWSVersion(context.Background(), client)
	if err != nil {
		t.Fatalf("inspect real BWS version: %v", err)
	}
	if !gate.MutationAllowed {
		t.Fatalf("real BWS mutation requires the tested CLI version 2.0.0; found %q", gate.Version)
	}
	return &bwsCLIAdapter{client: client, gate: gate, stderr: io.Discard}
}

func createRealBWSSecret(ctx context.Context, t *testing.T, provider syncProvider, ownership *realBWSTestOwnership, request bwsMutationRequest) (bwsSecret, error) {
	t.Helper()
	created, err := provider.CreateSecret(ctx, request)
	if err == nil {
		recordRealBWSCreatedID(t, ownership, created)
		return created, nil
	}
	secrets, listErr := provider.ListSecrets(ctx, ownership.ProjectID)
	if listErr != nil {
		return bwsSecret{}, fmt.Errorf("create failed (%v) and discovery failed: %w", err, listErr)
	}
	for _, secret := range secrets {
		if realBWSSecretOwned(secret, ownership) {
			recordRealBWSCreatedID(t, ownership, secret)
		}
	}
	return bwsSecret{}, err
}

func recordRealBWSCreatedID(t *testing.T, ownership *realBWSTestOwnership, secret bwsSecret) {
	t.Helper()
	if !realBWSSecretOwned(secret, ownership) {
		t.Fatal("refusing to record a real BWS secret outside the current run ownership boundary")
	}
	if _, existed := ownership.SnapshotIDs[secret.ID]; existed {
		t.Fatal("refusing to claim a pre-existing real BWS secret")
	}
	ownership.CreatedIDs[secret.ID] = struct{}{}
	if err := persistRealBWSCleanupManifest(ownership); err != nil {
		t.Fatalf("record real BWS cleanup id: %v", err)
	}
}

func cleanupRealBWSTest(t *testing.T, provider syncProvider, ownership *realBWSTestOwnership) {
	t.Helper()
	ids := sortedRealBWSCreatedIDs(ownership.CreatedIDs)
	for _, id := range ids {
		if _, existed := ownership.SnapshotIDs[id]; existed {
			t.Errorf("cleanup refused pre-existing real BWS id %s", id)
			continue
		}
		secret, err := provider.GetSecret(context.Background(), id)
		if err != nil {
			if errors.Is(err, errBWSSyncSecretNotFound) {
				delete(ownership.CreatedIDs, id)
				_ = persistRealBWSCleanupManifest(ownership)
				continue
			}
			t.Errorf("re-read real BWS cleanup id %s: %v", id, err)
			continue
		}
		if !realBWSSecretOwned(secret, ownership) {
			t.Errorf("cleanup refused real BWS id %s after ownership changed", id)
			continue
		}
		if err := provider.DeleteSecret(context.Background(), id); err != nil {
			t.Errorf("delete real BWS cleanup id %s: %v", id, err)
			continue
		}
		delete(ownership.CreatedIDs, id)
		if err := persistRealBWSCleanupManifest(ownership); err != nil {
			t.Errorf("update real BWS cleanup manifest: %v", err)
		}
	}
	if len(ownership.CreatedIDs) == 0 {
		if err := os.Remove(ownership.manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove empty real BWS cleanup manifest: %v", err)
		}
		return
	}
	t.Logf("real BWS cleanup remains allowlisted in 0600 manifest %s", ownership.manifestPath)
}

func realBWSSecretOwned(secret bwsSecret, ownership *realBWSTestOwnership) bool {
	if secret.ID == "" || secret.ProjectID != ownership.ProjectID || !strings.HasPrefix(secret.Key, ownership.RunPrefix+"-") {
		return false
	}
	var note realBWSOwnershipNote
	if json.Unmarshal([]byte(secret.Note), &note) != nil {
		return false
	}
	return note.Owner == "kinko-real-bws-test" && note.RunPrefix == ownership.RunPrefix
}

func persistRealBWSCleanupManifest(ownership *realBWSTestOwnership) error {
	manifest := realBWSCleanupManifest{ProjectID: ownership.ProjectID, RunPrefix: ownership.RunPrefix, SecretIDs: sortedRealBWSCreatedIDs(ownership.CreatedIDs)}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	file, err := os.OpenFile(ownership.manifestPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func sortedRealBWSCreatedIDs(ids map[string]struct{}) []string {
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}
