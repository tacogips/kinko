package kinko

import "testing"

func TestNewMachineIDIsValid(t *testing.T) {
	first, err := newMachineID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newMachineID()
	if err != nil {
		t.Fatal(err)
	}
	if !isValidMachineID(first) || !isValidMachineID(second) {
		t.Fatalf("generated machine ids must be valid: first=%q second=%q", first, second)
	}
	if first == second {
		t.Fatal("two independently generated machine ids unexpectedly matched")
	}
}

func TestMachineIDValidation(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "valid", value: "0123456789abcdef", valid: true},
		{name: "too short", value: "0123456789abcde", valid: false},
		{name: "uppercase", value: "0123456789abcdeF", valid: false},
		{name: "non hex", value: "0123456789abcdeg", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isValidMachineID(test.value); got != test.valid {
				t.Fatalf("isValidMachineID(%q)=%v want %v", test.value, got, test.valid)
			}
		})
	}
}

func TestInitVaultPopulatesMachineIDAndLegacyMetaLoads(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !isValidMachineID(meta.MachineID) {
		t.Fatalf("fresh vault machine_id=%q is invalid", meta.MachineID)
	}

	meta.MachineID = ""
	if err := saveMeta(dataDir, meta); err != nil {
		t.Fatal(err)
	}
	legacy, err := loadMeta(dataDir)
	if err != nil {
		t.Fatalf("legacy metadata must remain loadable: %v", err)
	}
	if legacy.MachineID != "" {
		t.Fatalf("legacy machine_id=%q want empty", legacy.MachineID)
	}
}
