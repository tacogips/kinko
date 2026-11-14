package kinko

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type syncStubPaths struct {
	success       string
	stateful      string
	garbage       string
	nonzero       string
	slow          string
	remote        string
	partial       string
	partialDelete string
	journal       string
	callLog       string
	remoteData    string
	stateData     string
}

func buildStubBWS(t *testing.T, remotePayload ...string) syncStubPaths {
	t.Helper()
	remoteResponse := "[]"
	if len(remotePayload) == 1 {
		remoteResponse = remotePayload[0]
	}
	directory := t.TempDir()
	journal := filepath.Join(directory, "mutations.log")
	callLog := filepath.Join(directory, "calls.log")
	remoteData := filepath.Join(directory, "remote.json")
	stateData := filepath.Join(directory, "state.json")
	if err := os.WriteFile(remoteData, []byte(remoteResponse), 0o600); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(directory, "stub_bws.go")
	source := fmt.Sprintf(`package main
import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "time"
)
func main() {
 if os.Getenv("BWS_ACCESS_TOKEN") != "fixture-kinko-token" || os.Getenv("KINKO_BWS_ACCESS_TOKEN") != "" || os.Getenv("PARENT_ONLY_MARKER") != "" {
  fmt.Fprintln(os.Stderr, "isolated child environment check failed")
  os.Exit(9)
 }
 scenario := filepath.Base(os.Args[0])
 args := os.Args[1:]
 call()
 if len(args) < 6 || strings.Join(args[len(args)-4:], " ") != "--output json --color no" {
  fmt.Fprintln(os.Stderr, "required output/color argv missing")
  os.Exit(5)
 }
 if strings.Contains(scenario, "slow") { time.Sleep(2*time.Second) }
 if strings.Contains(scenario, "nonzero") { fmt.Fprintln(os.Stderr, "provider rate limit: retry after 30 seconds; token="+os.Getenv("BWS_ACCESS_TOKEN")); os.Exit(4) }
 if strings.Contains(scenario, "garbage") { fmt.Print("{"); return }
 if len(args) < 2 { os.Exit(8) }
 switch args[0]+" "+args[1] {
 case "project list":
  fmt.Print("[{\"id\":\"fixture-project\",\"name\":\"fixture\"}]")
 case "secret list":
  if strings.Contains(scenario, "remote") {
   data, _ := os.ReadFile(%q); fmt.Print(string(data))
  } else if strings.Contains(scenario, "stateful") || strings.Contains(scenario, "partial") {
   data, err := os.ReadFile(%q); if os.IsNotExist(err) { fmt.Print("[]") } else if err != nil { panic(err) } else { fmt.Print(string(data)) }
  } else { fmt.Print("[]") }
 case "secret get":
  var records []secretRecord
  if strings.Contains(scenario, "remote") {
   data, err := os.ReadFile(%q); if err != nil { panic(err) }; if err := json.Unmarshal(data, &records); err != nil { panic(err) }
  } else if strings.Contains(scenario, "stateful") || strings.Contains(scenario, "partial") {
   records = loadState()
  }
  for _, record := range records { if record.ID == args[2] { respond(record); return } }
  fmt.Fprintln(os.Stderr, "fixture secret not found")
  os.Exit(4)
 case "secret create":
  if len(args) < 11 || args[5] != "--note" || !json.Valid([]byte(args[6])) { os.Exit(7) }
  if strings.Contains(scenario, "partial") && len(loadState()) > 0 { fmt.Fprintln(os.Stderr, "fixture provider failure"); os.Exit(4) }
  journal("create "+args[2])
  record := secretRecord{ID:"id-"+args[2], ProjectID:args[4], Key:args[2], Value:args[3], Note:args[6], RevisionDate:"revision-create"}
  if strings.Contains(scenario, "stateful") || strings.Contains(scenario, "partial") { records := loadState(); records = append(records, record); saveState(records) }
  respond(record)
 case "secret edit":
  if len(args) < 11 || args[3] != "--value" || args[5] != "--note" || !json.Valid([]byte(args[6])) { os.Exit(7) }
  journal("edit "+args[2])
  records := loadState()
  var record secretRecord
  for i := range records { if records[i].ID == args[2] { records[i].Value=args[4]; records[i].Note=args[6]; records[i].RevisionDate="revision-edit"; record=records[i] } }
  if record.ID == "" { os.Exit(7) }
  saveState(records); respond(record)
 case "secret delete":
  journal("delete")

  deleteCount := len(args)-6
  if strings.Contains(scenario, "partial-delete") && len(loadState()) <= 2 { fmt.Fprintln(os.Stderr, "provider rate limit: retry after 30 seconds; token="+os.Getenv("BWS_ACCESS_TOKEN")); os.Exit(4) }
  if strings.Contains(scenario, "stateful") || strings.Contains(scenario, "partial-delete") {
   deleting := map[string]bool{}; for _, id := range args[2:len(args)-4] { deleting[id]=true }
   records := loadState(); kept := records[:0]; for _, record := range records { if !deleting[record.ID] { kept=append(kept, record) } }; saveState(kept)
  }
  if deleteCount == 1 { fmt.Print("1 secret deleted successfully.") } else { fmt.Printf("%%d secrets deleted successfully.", deleteCount) }
 default:
  os.Exit(6)
 }
}
type secretRecord struct { ID string `+"`json:\"id\"`"+`; OrganizationID string `+"`json:\"organizationId\"`"+`; ProjectID string `+"`json:\"projectId\"`"+`; Key string `+"`json:\"key\"`"+`; Value string `+"`json:\"value\"`"+`; Note string `+"`json:\"note\"`"+`; RevisionDate string `+"`json:\"revisionDate\"`"+` }
func loadState() []secretRecord { var records []secretRecord; data, err := os.ReadFile(%q); if os.IsNotExist(err) { return records }; if err != nil { panic(err) }; if err := json.Unmarshal(data, &records); err != nil { panic(err) }; return records }
func saveState(records []secretRecord) { data, err := json.Marshal(records); if err != nil { panic(err) }; if err := os.WriteFile(%q, data, 0600); err != nil { panic(err) } }
func journal(line string) {
 file, err := os.OpenFile(%q, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
 if err != nil { panic(err) }
 defer file.Close()
 fmt.Fprintln(file, line)
}
func call() {
 file, err := os.OpenFile(%q, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
 if err != nil { panic(err) }
 defer file.Close()
 fmt.Fprintln(file, "call")
}
func respond(record secretRecord) {
 _ = json.NewEncoder(os.Stdout).Encode(record)
}
`, remoteData, stateData, remoteData, stateData, stateData, journal, callLog)
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	success := filepath.Join(directory, "stub-success")
	command := exec.Command("go", "build", "-o", success, sourcePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build stub bws: %v: %s", err, output)
	}
	garbage := filepath.Join(directory, "stub-garbage")
	stateful := filepath.Join(directory, "stub-stateful")
	nonzero := filepath.Join(directory, "stub-nonzero")
	slow := filepath.Join(directory, "stub-slow")
	remote := filepath.Join(directory, "stub-remote")
	partial := filepath.Join(directory, "stub-partial")
	partialDelete := filepath.Join(directory, "stub-partial-delete")
	if err := os.Link(success, garbage); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(success, stateful); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(success, nonzero); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(success, slow); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(success, remote); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(success, partial); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(success, partialDelete); err != nil {
		t.Fatal(err)
	}
	return syncStubPaths{success: success, stateful: stateful, garbage: garbage, nonzero: nonzero, slow: slow, remote: remote, partial: partial, partialDelete: partialDelete, journal: journal, callLog: callLog, remoteData: remoteData, stateData: stateData}
}

func setupSyncE2EVault(t *testing.T) (string, string, []byte) {
	t.Helper()
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "fixture-password"); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	dek, err := unwrapDEKWithPassword(meta, "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture-scope")
	secondPath := filepath.Join(t.TempDir(), "second-scope")
	data := &vaultData{
		Shared: map[string]string{
			sharedKeyBWSAccessToken: "fixture-kinko-token",
			"SHARED_KEY":            "fixture-shared",
		},
		Profiles: map[string]map[string]map[string]string{
			"fixture-profile": {path: {"LOCAL_KEY": "fixture-local"}},
			"second-profile":  {secondPath: {"SECOND_KEY": "fixture-second"}},
		},
	}
	if err := saveVault(dataDir, dek, data); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	config[configKeyBWSProjectID] = "fixture-project"
	if err := saveConfig(dataDir, dek, config); err != nil {
		t.Fatal(err)
	}
	return dataDir, filepath.Join(t.TempDir(), "bootstrap.toml"), dek
}

func setupEmptySyncE2EVault(t *testing.T, machineID string) (string, string, []byte) {
	t.Helper()
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "fixture-password"); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	meta.MachineID = machineID
	if err := saveMetaAtomically(dataDir, meta); err != nil {
		t.Fatal(err)
	}
	dek, err := unwrapDEKWithPassword(meta, "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := saveVault(dataDir, dek, &vaultData{Profiles: map[string]map[string]map[string]string{}, Shared: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(dataDir, dek)
	if err != nil {
		t.Fatal(err)
	}
	config[configKeyBWSProjectID] = "fixture-project"
	if err := saveConfig(dataDir, dek, config); err != nil {
		t.Fatal(err)
	}
	return dataDir, filepath.Join(t.TempDir(), "bootstrap.toml"), dek
}

func countVaultSyncKeys(data *vaultData) int {
	count := len(data.Shared)
	for _, scopes := range data.Profiles {
		for _, values := range scopes {
			count += len(values)
		}
	}
	return count
}

func assertNoSyncFixtureLeak(t *testing.T, output string) {
	t.Helper()
	for _, sensitive := range []string{"fixture-kinko-token", "fixture-parent-token", "fixture-shared", "fixture-local", "fixture-second", "fixture-remote", "fixture-updated", "fixture-divergent"} {
		if strings.Contains(output, sensitive) {
			t.Fatalf("sync output leaked sensitive fixture")
		}
	}
}

func mustReadSyncTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustReadOptionalSyncTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}
