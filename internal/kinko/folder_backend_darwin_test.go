//go:build darwin

package kinko

import "testing"

func TestParseHdiutilInfoMountedRequiresExactMountpoint(t *testing.T) {
	info := []byte("/dev/disk4s1\tApple_APFS\t/private/tmp/project/private-other\n")
	if parseHdiutilInfoMounted(info, "/private/tmp/project/private") {
		t.Fatal("substring mountpoint was reported as mounted")
	}
}

func TestParseHdiutilInfoMountedAcceptsExactMountpoint(t *testing.T) {
	info := []byte("/dev/disk4s1\tApple_APFS\t/private/tmp/project/private\n")
	if !parseHdiutilInfoMounted(info, "/private/tmp/project/private") {
		t.Fatal("exact mountpoint was not reported as mounted")
	}
}

func TestParseHdiutilInfoMountedAcceptsLabeledMountpoint(t *testing.T) {
	info := []byte("mount-point: /private/tmp/project/private\n")
	if !parseHdiutilInfoMounted(info, "/private/tmp/project/private") {
		t.Fatal("labeled exact mountpoint was not reported as mounted")
	}
}

func TestParseHdiutilInfoMountedAcceptsLabeledMountpointWithColon(t *testing.T) {
	info := []byte("mount-point: /private/tmp/project/private:with-colon\n")
	if !parseHdiutilInfoMounted(info, "/private/tmp/project/private:with-colon") {
		t.Fatal("labeled exact mountpoint containing colon was not reported as mounted")
	}
}
