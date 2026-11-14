package kinko

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func buildRestoreTestArchive(t *testing.T, dir string, password string, entries []backupArchiveEntry) string {
	t.Helper()
	archivePath := filepath.Join(dir, "backup-test.zip")
	if err := writePasswordLockedZip(archivePath, password, entries); err != nil {
		t.Fatalf("writePasswordLockedZip: %v", err)
	}
	return archivePath
}

func sampleRestoreArchiveEntries() []backupArchiveEntry {
	now := time.Now().UTC()
	return []backupArchiveEntry{
		{name: "kinko-backup/manifest.json", data: []byte(`{"version":1}` + "\n"), modTime: now},
		{name: "kinko-backup/vault/meta.v1.json", data: []byte(`{"meta":true}`), modTime: now},
		{name: "kinko-backup/vault/vault.v1.bin", data: []byte("vault-bytes-payload"), modTime: now},
	}
}

func TestReadPasswordLockedZip_ValidArchiveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	password := "correct-horse"
	entries := sampleRestoreArchiveEntries()
	archivePath := buildRestoreTestArchive(t, dir, password, entries)

	archive, err := readPasswordLockedZip(archivePath, password)
	if err != nil {
		t.Fatalf("readPasswordLockedZip: %v", err)
	}

	if len(archive.order) != len(entries) {
		t.Fatalf("order length=%d want=%d", len(archive.order), len(entries))
	}
	for i, want := range entries {
		if archive.order[i] != want.name {
			t.Fatalf("order[%d]=%q want=%q", i, archive.order[i], want.name)
		}
	}
	for _, want := range entries {
		got, err := archive.entry(want.name)
		if err != nil {
			t.Fatalf("entry(%q): %v", want.name, err)
		}
		if string(got) != string(want.data) {
			t.Fatalf("entry(%q)=%q want=%q", want.name, got, want.data)
		}
	}
}

func TestRestoreZipArchive_EntryMissingReturnsPolicyError(t *testing.T) {
	dir := t.TempDir()
	password := "pw"
	archivePath := buildRestoreTestArchive(t, dir, password, sampleRestoreArchiveEntries())

	archive, err := readPasswordLockedZip(archivePath, password)
	if err != nil {
		t.Fatalf("readPasswordLockedZip: %v", err)
	}

	_, err = archive.entry("kinko-backup/does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing entry")
	}
	var zerr *zipReadError
	if !asZipReadError(err, &zerr) {
		t.Fatalf("expected *zipReadError, got %T: %v", err, err)
	}
	if zerr.kind != zipReadErrorKindPolicy {
		t.Fatalf("kind=%v want=%v", zerr.kind, zipReadErrorKindPolicy)
	}
}

func TestReadPasswordLockedZip_WrongPasswordIsAuthError(t *testing.T) {
	dir := t.TempDir()
	archivePath := buildRestoreTestArchive(t, dir, "correct-password", sampleRestoreArchiveEntries())

	_, err := readPasswordLockedZip(archivePath, "totally-wrong-password")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
	var zerr *zipReadError
	if !asZipReadError(err, &zerr) {
		t.Fatalf("expected *zipReadError, got %T: %v", err, err)
	}
	if zerr.kind != zipReadErrorKindAuth {
		t.Fatalf("kind=%v want=%v (%v)", zerr.kind, zipReadErrorKindAuth, err)
	}
}

func TestReadPasswordLockedZip_TruncatedEOCDIsPolicyError(t *testing.T) {
	dir := t.TempDir()
	archivePath := buildRestoreTestArchive(t, dir, "pw", sampleRestoreArchiveEntries())

	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	// Truncate to well below the 22-byte EOCD record size.
	truncated := data[:10]
	if err := os.WriteFile(archivePath, truncated, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = readPasswordLockedZip(archivePath, "pw")
	assertRestoreZipPolicyError(t, err)
}

func TestReadPasswordLockedZip_GarbageEOCDIsPolicyError(t *testing.T) {
	dir := t.TempDir()
	archivePath := buildRestoreTestArchive(t, dir, "pw", sampleRestoreArchiveEntries())

	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the EOCD signature (first 4 bytes of the last 22-byte record).
	eocdStart := len(data) - 22
	corrupted := append([]byte(nil), data...)
	copy(corrupted[eocdStart:eocdStart+4], []byte{0xde, 0xad, 0xbe, 0xef})
	if err := os.WriteFile(archivePath, corrupted, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = readPasswordLockedZip(archivePath, "pw")
	assertRestoreZipPolicyError(t, err)
}

func TestReadPasswordLockedZip_WrongCompressionMethodIsPolicyError(t *testing.T) {
	dir := t.TempDir()
	password := "pw"
	archivePath := buildRestoreTestArchive(t, dir, password, sampleRestoreArchiveEntries())

	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}

	// Local header compression method is at offset 8 (2 bytes) of the first
	// local file header (offset 0 in the archive).
	patched := append([]byte(nil), data...)
	binary.LittleEndian.PutUint16(patched[8:10], 8) // DEFLATE, unsupported.

	// Central directory compression method for the same (first) entry:
	// locate the central directory offset from the EOCD record (bytes
	// [16:20] of the last 22 bytes), then compression is at header+10.
	eocdStart := len(patched) - 22
	cdOffset := binary.LittleEndian.Uint32(patched[eocdStart+16 : eocdStart+20])
	binary.LittleEndian.PutUint16(patched[int(cdOffset)+10:int(cdOffset)+12], 8)

	if err := os.WriteFile(archivePath, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = readPasswordLockedZip(archivePath, password)
	assertRestoreZipPolicyError(t, err)
}

func TestReadPasswordLockedZip_ZIP64FieldsRejected(t *testing.T) {
	dir := t.TempDir()
	password := "pw"
	archivePath := buildRestoreTestArchive(t, dir, password, sampleRestoreArchiveEntries())

	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	patched := append([]byte(nil), data...)
	eocdStart := len(patched) - 22
	// Set total-entries and entries-on-disk both to the ZIP64 sentinel so
	// the equal-counts check still passes but the sentinel check fires.
	binary.LittleEndian.PutUint16(patched[eocdStart+8:eocdStart+10], 0xFFFF)
	binary.LittleEndian.PutUint16(patched[eocdStart+10:eocdStart+12], 0xFFFF)

	if err := os.WriteFile(archivePath, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = readPasswordLockedZip(archivePath, password)
	assertRestoreZipPolicyError(t, err)
}

func TestReadPasswordLockedZip_ZIP64CentralDirSizeSentinelRejected(t *testing.T) {
	dir := t.TempDir()
	password := "pw"
	archivePath := buildRestoreTestArchive(t, dir, password, sampleRestoreArchiveEntries())

	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	patched := append([]byte(nil), data...)
	eocdStart := len(patched) - 22
	binary.LittleEndian.PutUint32(patched[eocdStart+12:eocdStart+16], 0xFFFFFFFF)

	if err := os.WriteFile(archivePath, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = readPasswordLockedZip(archivePath, password)
	assertRestoreZipPolicyError(t, err)
}

func TestReadPasswordLockedZip_DuplicateEntryNamesRejected(t *testing.T) {
	dir := t.TempDir()
	password := "pw"
	now := time.Now().UTC()
	entries := []backupArchiveEntry{
		{name: "kinko-backup/manifest.json", data: []byte("a"), modTime: now},
		{name: "kinko-backup/manifest.json", data: []byte("b"), modTime: now},
	}
	archivePath := buildRestoreTestArchive(t, dir, password, entries)

	_, err := readPasswordLockedZip(archivePath, password)
	assertRestoreZipPolicyError(t, err)
}

func TestReadPasswordLockedZip_TamperedCiphertextByteIsPolicyNotAuth(t *testing.T) {
	dir := t.TempDir()
	password := "pw"
	// Multiple entries so at least one still verifies while another is
	// tampered with; this must classify as Policy (integrity), not Auth.
	archivePath := buildRestoreTestArchive(t, dir, password, sampleRestoreArchiveEntries())

	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	patched := append([]byte(nil), data...)

	// First local header ends at offset 30 + nameLen; name for the first
	// entry is "kinko-backup/manifest.json" (27 bytes). Flip a byte inside
	// its encrypted payload (after the 12-byte crypto header, in the
	// ciphertext body) so the check byte still may pass or fail, but at
	// least the CRC won't match, and other entries remain untouched.
	nameLen := binary.LittleEndian.Uint16(patched[26:28])
	payloadStart := 30 + int(nameLen)
	// Flip a byte well inside the payload (past the 12-byte crypto header)
	// to disturb CRC without necessarily flipping the check byte itself.
	tamperOffset := payloadStart + zipCryptoHeaderSize + 1
	patched[tamperOffset] ^= 0xFF

	if err := os.WriteFile(archivePath, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = readPasswordLockedZip(archivePath, password)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
	var zerr *zipReadError
	if !asZipReadError(err, &zerr) {
		t.Fatalf("expected *zipReadError, got %T: %v", err, err)
	}
	if zerr.kind != zipReadErrorKindPolicy {
		t.Fatalf("kind=%v want=%v (tampered single-entry ciphertext with other entries intact should be Policy/integrity, not Auth): %v", zerr.kind, zipReadErrorKindPolicy, err)
	}
}

// TestMaxRestoreZipEntrySize_ConstantValue is a lightweight structural check
// that the size cap constant is set to the documented 64 MiB sanity limit.
// A full end-to-end test that allocates and encrypts a 64 MiB+ payload is
// avoided here for test-suite speed; the cap is exercised functionally by
// the central-directory parsing logic in parseOneZipCentralDirEntry, which
// compares uncompressedSize against this same constant.
func TestMaxRestoreZipEntrySize_ConstantValue(t *testing.T) {
	const want = 64 * 1024 * 1024
	if maxRestoreZipEntrySize != want {
		t.Fatalf("maxRestoreZipEntrySize=%d want=%d", maxRestoreZipEntrySize, want)
	}
}

func TestReadPasswordLockedZip_OversizedDeclaredUncompressedSizeRejected(t *testing.T) {
	dir := t.TempDir()
	password := "pw"
	archivePath := buildRestoreTestArchive(t, dir, password, sampleRestoreArchiveEntries())

	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	patched := append([]byte(nil), data...)

	// Patch the first entry's declared uncompressedSize (local header
	// [22:26]) and the matching central directory field to a value above
	// maxRestoreZipEntrySize; the actual payload bytes stay small, so this
	// tests the size-cap rejection path without allocating 64MiB+ of data.
	oversized := uint32(maxRestoreZipEntrySize) + 1
	binary.LittleEndian.PutUint32(patched[22:26], oversized)

	eocdStart := len(patched) - 22
	cdOffset := binary.LittleEndian.Uint32(patched[eocdStart+16 : eocdStart+20])
	binary.LittleEndian.PutUint32(patched[int(cdOffset)+24:int(cdOffset)+28], oversized)

	if err := os.WriteFile(archivePath, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = readPasswordLockedZip(archivePath, password)
	assertRestoreZipPolicyError(t, err)
}

func assertRestoreZipPolicyError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var zerr *zipReadError
	if !asZipReadError(err, &zerr) {
		t.Fatalf("expected *zipReadError, got %T: %v", err, err)
	}
	if zerr.kind != zipReadErrorKindPolicy {
		t.Fatalf("kind=%v want=%v (%v)", zerr.kind, zipReadErrorKindPolicy, err)
	}
}

// asZipReadError mirrors errors.As without importing "errors" into every
// test file that just needs a direct type assertion on a non-wrapped error;
// readPasswordLockedZip/validateRestoreArchiveEntries return *zipReadError
// directly (not wrapped further) in all cases exercised here.
func asZipReadError(err error, target **zipReadError) bool {
	if zerr, ok := err.(*zipReadError); ok {
		*target = zerr
		return true
	}
	return false
}
