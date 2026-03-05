package build

import "testing"

func TestVersion(t *testing.T) {
	if got := Version(); got == "" {
		t.Fatal("version must not be empty")
	}
}
