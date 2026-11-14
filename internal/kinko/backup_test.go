package kinko

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func setupBackupFixture(t *testing.T) globalOptions {
	t.Helper()

	dataDir := t.TempDir()
	configDir := t.TempDir()
	opts := globalOptions{
		dataDir:    dataDir,
		configPath: filepath.Join(configDir, "bootstrap.toml"),
		profile:    defaultProfile,
		path:       filepath.Clean("/tmp/project"),
	}
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := writeBootstrapConfig(opts); err != nil {
		t.Fatal(err)
	}
	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"A=one", "B=two"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runSet(opts, []string{"--shared", "SHARED=shared"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	return opts
}

func TestRunBackup_CreatesPasswordLockedArchive(t *testing.T) {
	opts := setupBackupFixture(t)
	destDir := t.TempDir()
	extraRootFile := filepath.Join(opts.dataDir, "notes.txt")
	if err := os.WriteFile(extraRootFile, []byte("root-note"), 0o600); err != nil {
		t.Fatalf("write extra root file: %v", err)
	}
	extraVaultFile := filepath.Join(opts.dataDir, "vault", "custom-state.json")
	if err := os.WriteFile(extraVaultFile, []byte("{\"mode\":\"extra\"}\n"), 0o600); err != nil {
		t.Fatalf("write extra vault file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(opts.dataDir, "lock", "session.token"), []byte("transient"), 0o600); err != nil {
		t.Fatalf("write transient session token: %v", err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runBackup(opts, []string{"--current-stdin", "--dest-path", destDir}, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatalf("backup failed: %v", err)
	}

	archivePath := strings.TrimSpace(strings.TrimPrefix(out.String(), "backup written: "))
	if archivePath == "" {
		t.Fatalf("missing backup output path: %q", out.String())
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("backup archive missing: %v", err)
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("open backup as zip: %v", err)
	}
	_ = zr.Close()

	entries := readPasswordLockedZipEntries(t, archivePath, "pw")
	if _, ok := entries[filepath.ToSlash(filepath.Join(backupArchiveRoot, "manifest.json"))]; !ok {
		t.Fatalf("manifest entry missing: %#v", sortedMapKeys(entries))
	}

	manifestPath := filepath.ToSlash(filepath.Join(backupArchiveRoot, "manifest.json"))
	var manifest backupManifest
	if err := json.Unmarshal(entries[manifestPath], &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if !manifest.BootstrapPresent {
		t.Fatal("expected manifest to record bootstrap presence")
	}

	wantFiles := []string{
		filepath.ToSlash(filepath.Join(backupArchiveRoot, "notes.txt")),
		filepath.ToSlash(filepath.Join(backupArchiveRoot, "vault", "meta.v1.json")),
		filepath.ToSlash(filepath.Join(backupArchiveRoot, "vault", "vault.v1.bin")),
		filepath.ToSlash(filepath.Join(backupArchiveRoot, "vault", "config.v1.bin")),
		filepath.ToSlash(filepath.Join(backupArchiveRoot, "vault", "custom-state.json")),
		filepath.ToSlash(filepath.Join(backupArchiveRoot, "vault", vaultMarker)),
		filepath.ToSlash(filepath.Join(backupArchiveRoot, "config", filepath.Base(opts.configPath))),
	}
	for _, name := range wantFiles {
		if _, ok := entries[name]; !ok {
			t.Fatalf("missing backup entry %q; got %#v", name, sortedMapKeys(entries))
		}
	}
	if _, ok := entries[filepath.ToSlash(filepath.Join(backupArchiveRoot, "lock", "session.token"))]; ok {
		t.Fatal("session token must not be included in backup archive")
	}
	if _, ok := entries[filepath.ToSlash(filepath.Join(backupArchiveRoot, "vault", mutationLockFileName))]; ok {
		t.Fatal("mutation lock must not be included in backup archive")
	}

	bootstrapName := filepath.ToSlash(filepath.Join(backupArchiveRoot, "config", filepath.Base(opts.configPath)))
	if got := string(entries[bootstrapName]); !strings.Contains(got, "kinko_dir=") {
		t.Fatalf("unexpected bootstrap config payload: %q", got)
	}
	if got := string(entries[filepath.ToSlash(filepath.Join(backupArchiveRoot, "notes.txt"))]); got != "root-note" {
		t.Fatalf("unexpected extra root file payload: %q", got)
	}
	if got := string(entries[filepath.ToSlash(filepath.Join(backupArchiveRoot, "vault", "custom-state.json"))]); !strings.Contains(got, "\"extra\"") {
		t.Fatalf("unexpected extra vault file payload: %q", got)
	}
}

func TestRunBackup_RejectsWrongPassword(t *testing.T) {
	opts := setupBackupFixture(t)

	err := runBackup(opts, []string{"--current-stdin", "--dest-path", t.TempDir()}, strings.NewReader("wrong\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected backup to fail with wrong password")
	}
	if !strings.Contains(err.Error(), "current password is invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunBackup_WorksWhileVaultIsLocked(t *testing.T) {
	opts := setupBackupFixture(t)
	if err := lockSession(opts.dataDir); err != nil {
		t.Fatalf("lock session: %v", err)
	}

	destDir := t.TempDir()
	var out bytes.Buffer
	if err := runBackup(opts, []string{"--current-stdin", "--dest-path", destDir}, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("backup should not require unlocked session: %v", err)
	}

	archivePath := strings.TrimSpace(strings.TrimPrefix(out.String(), "backup written: "))
	entries := readPasswordLockedZipEntries(t, archivePath, "pw")
	if _, ok := entries[filepath.ToSlash(filepath.Join(backupArchiveRoot, "vault", "vault.v1.bin"))]; !ok {
		t.Fatalf("vault payload missing from locked-state backup: %#v", sortedMapKeys(entries))
	}
}

func TestRunBackup_OmitsBootstrapWhenConfigMissing(t *testing.T) {
	opts := setupBackupFixture(t)
	if err := os.Remove(opts.configPath); err != nil {
		t.Fatalf("remove bootstrap config: %v", err)
	}
	destDir := filepath.Join(t.TempDir(), "nested", "backup-output")

	var out bytes.Buffer
	if err := runBackup(opts, []string{"--current-stdin", "--dest-path", destDir}, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("backup failed without bootstrap config: %v", err)
	}

	archivePath := strings.TrimSpace(strings.TrimPrefix(out.String(), "backup written: "))
	if _, err := os.Stat(destDir); err != nil {
		t.Fatalf("destination directory should be created: %v", err)
	}

	entries := readPasswordLockedZipEntries(t, archivePath, "pw")
	manifestPath := filepath.ToSlash(filepath.Join(backupArchiveRoot, "manifest.json"))
	var manifest backupManifest
	if err := json.Unmarshal(entries[manifestPath], &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if manifest.BootstrapPresent {
		t.Fatal("manifest should record absent bootstrap config")
	}

	bootstrapName := filepath.ToSlash(filepath.Join(backupArchiveRoot, "config", filepath.Base(opts.configPath)))
	if _, ok := entries[bootstrapName]; ok {
		t.Fatal("bootstrap config should be omitted when missing")
	}
}

func TestRunBackup_RejectsDestinationInsideDataDir(t *testing.T) {
	opts := setupBackupFixture(t)
	destDir := filepath.Join(opts.dataDir, "exports")

	err := runBackup(opts, []string{"--current-stdin", "--dest-path", destDir}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected backup to reject destination inside data dir")
	}
	if !strings.Contains(err.Error(), "must not be inside kinko data dir") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunBackup_RejectsDestinationSymlinkIntoDataDir(t *testing.T) {
	opts := setupBackupFixture(t)
	outsideRoot := t.TempDir()
	realDest := filepath.Join(opts.dataDir, "exports")
	if err := os.MkdirAll(realDest, 0o700); err != nil {
		t.Fatalf("create real destination: %v", err)
	}
	linkPath := filepath.Join(outsideRoot, "backup-link")
	if err := os.Symlink(realDest, linkPath); err != nil {
		t.Skipf("symlink not supported in test environment: %v", err)
	}

	err := runBackup(opts, []string{"--current-stdin", "--dest-path", linkPath}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected backup to reject destination symlink into data dir")
	}
	if !strings.Contains(err.Error(), "must not be inside kinko data dir") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunBackup_DefaultsToCurrentWorkingDirectory(t *testing.T) {
	opts := setupBackupFixture(t)
	cwd := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if chdirErr := os.Chdir(oldWD); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	}()

	var out bytes.Buffer
	if err := runBackup(opts, []string{"--current-stdin"}, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("backup with default destination failed: %v", err)
	}

	archivePath := strings.TrimSpace(strings.TrimPrefix(out.String(), "backup written: "))
	if filepath.Dir(archivePath) != cwd {
		t.Fatalf("backup should default to cwd: got %q want dir %q", archivePath, cwd)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("backup archive missing: %v", err)
	}
}

func TestCollectBackupSourceFiles_RejectsSymlinkInDataDir(t *testing.T) {
	opts := setupBackupFixture(t)
	target := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	linkPath := filepath.Join(opts.dataDir, "vault", "linked.txt")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlink not supported in test environment: %v", err)
	}

	_, _, err := collectBackupSourceFiles(opts)
	if err == nil {
		t.Fatal("expected symlink-containing backup source to fail")
	}
	if !strings.Contains(err.Error(), "must not contain symlinks") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCollectBackupSourceFiles_RejectsBootstrapSymlink(t *testing.T) {
	opts := setupBackupFixture(t)
	target := filepath.Join(t.TempDir(), "bootstrap.toml")
	if err := os.WriteFile(target, []byte("kinko_dir=/tmp/other\n"), 0o600); err != nil {
		t.Fatalf("write bootstrap symlink target: %v", err)
	}
	if err := os.Remove(opts.configPath); err != nil {
		t.Fatalf("remove original bootstrap config: %v", err)
	}
	if err := os.Symlink(target, opts.configPath); err != nil {
		t.Skipf("symlink not supported in test environment: %v", err)
	}

	_, _, err := collectBackupSourceFiles(opts)
	if err == nil {
		t.Fatal("expected symlink bootstrap config to fail")
	}
	if !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestZipCryptoEncrypt_MatchesPKZIPVector(t *testing.T) {
	password := "pw1234567890"
	plain := []byte{
		0x00, 0x11, 0x22, 0x33,
		0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb,
		0xcc, 0xdd, 0xee, 0xff,
	}
	want := []byte{
		0x7e, 0x8e, 0xc1, 0xdc,
		0x33, 0x75, 0x9f, 0xd2,
		0x3c, 0x5e, 0xd3, 0x0f,
		0x28, 0x8a, 0x89, 0x69,
	}

	if got := zipCryptoEncrypt(password, plain); !bytes.Equal(got, want) {
		t.Fatalf("zip crypto vector mismatch:\n got=%x\nwant=%x", got, want)
	}
}

func readPasswordLockedZipEntries(t *testing.T, path string, password string) map[string][]byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	entries := map[string][]byte{}
	reader := bytes.NewReader(data)
	for {
		var sig uint32
		if err := binary.Read(reader, binary.LittleEndian, &sig); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("read zip signature: %v", err)
		}
		switch sig {
		case zipLocalFileHeaderSignature:
			fixed := make([]byte, 26)
			if _, err := io.ReadFull(reader, fixed); err != nil {
				t.Fatalf("read local header: %v", err)
			}
			flags := binary.LittleEndian.Uint16(fixed[2:4])
			method := binary.LittleEndian.Uint16(fixed[4:6])
			crc := binary.LittleEndian.Uint32(fixed[10:14])
			compressedSize := binary.LittleEndian.Uint32(fixed[14:18])
			uncompressedSize := binary.LittleEndian.Uint32(fixed[18:22])
			nameLen := binary.LittleEndian.Uint16(fixed[22:24])
			extraLen := binary.LittleEndian.Uint16(fixed[24:26])
			name := make([]byte, nameLen)
			if _, err := io.ReadFull(reader, name); err != nil {
				t.Fatalf("read file name: %v", err)
			}
			if _, err := reader.Seek(int64(extraLen), io.SeekCurrent); err != nil {
				t.Fatalf("skip extra field: %v", err)
			}
			payload := make([]byte, compressedSize)
			if _, err := io.ReadFull(reader, payload); err != nil {
				t.Fatalf("read payload: %v", err)
			}
			if flags&zipGeneralPurposeFlagEncrypt == 0 {
				t.Fatalf("expected encrypted flag for %s", name)
			}
			if method != zipCompressionStore {
				t.Fatalf("unexpected compression method %d for %s", method, name)
			}
			plain := zipCryptoDecryptReference([]byte(password), payload)
			if len(plain) < zipCryptoHeaderSize {
				t.Fatalf("payload too short for %s", name)
			}
			if plain[11] != byte(crc>>24) {
				t.Fatalf("password verification byte mismatch for %s: got=0x%02x want=0x%02x", name, plain[11], byte(crc>>24))
			}
			body := plain[zipCryptoHeaderSize:]
			if uint32(len(body)) != uncompressedSize {
				t.Fatalf("unexpected uncompressed size for %s: got=%d want=%d", name, len(body), uncompressedSize)
			}
			if crc32.ChecksumIEEE(body) != crc {
				t.Fatalf("crc mismatch for %s", name)
			}
			entries[string(name)] = body
		case zipCentralDirHeaderSignature, zipEndOfCentralDirSignature:
			return entries
		default:
			t.Fatalf("unexpected zip signature 0x%x", sig)
		}
	}
	return entries
}

func zipCryptoDecryptReference(password []byte, cipherText []byte) []byte {
	keys := [3]uint32{0x12345678, 0x23456789, 0x34567890}
	for _, b := range password {
		zipCryptoUpdateKeysReference(&keys, b)
	}

	out := make([]byte, len(cipherText))
	for i, b := range cipherText {
		mask := zipCryptoMaskReference(keys[2])
		plain := b ^ mask
		out[i] = plain
		zipCryptoUpdateKeysReference(&keys, plain)
	}
	return out
}

func zipCryptoUpdateKeysReference(keys *[3]uint32, b byte) {
	keys[0] = zipCryptoCRC32ByteReference(keys[0], b)
	keys[1] = (keys[1] + uint32(keys[0]&0xff)) & 0xffffffff
	keys[1] = (keys[1]*134775813 + 1) & 0xffffffff
	keys[2] = zipCryptoCRC32ByteReference(keys[2], byte(keys[1]>>24))
}

func zipCryptoMaskReference(key2 uint32) byte {
	temp := uint16(key2) | 2
	return byte((uint32(temp) * uint32(temp^1)) >> 8)
}

func zipCryptoCRC32ByteReference(crc uint32, b byte) uint32 {
	return (crc >> 8) ^ crc32.IEEETable[(crc^uint32(b))&0xff]
}

func sortedMapKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
