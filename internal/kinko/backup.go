package kinko

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	zipLocalFileHeaderSignature  = 0x04034b50
	zipCentralDirHeaderSignature = 0x02014b50
	zipEndOfCentralDirSignature  = 0x06054b50
	zipVersionNeeded             = 20
	zipGeneralPurposeFlagEncrypt = 1 << 0
	zipCompressionStore          = 0
	zipCryptoHeaderSize          = 12
	backupManifestVersion        = 1
	backupArchiveRoot            = "kinko-backup"
	backupArchiveNameTimeLayout  = "20060102T150405Z"
)

type backupInputOptions struct {
	currentStdin bool
	currentFD    int
	forceTTY     bool
}

type backupSourceFile struct {
	sourcePath  string
	archivePath string
}

type backupArchiveEntry struct {
	name    string
	data    []byte
	modTime time.Time
}

type backupManifest struct {
	Version          int      `json:"version"`
	CreatedAtUTC     string   `json:"created_at_utc"`
	BootstrapPresent bool     `json:"bootstrap_present"`
	Files            []string `json:"files"`
}

func runBackup(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var input backupInputOptions
	destPath := "."
	input.currentFD = -1
	fs.BoolVar(&input.currentStdin, "current-stdin", false, "read current password from stdin")
	fs.IntVar(&input.currentFD, "current-fd", -1, "read current password from file descriptor")
	fs.BoolVar(&input.forceTTY, "force-tty", false, "allow interactive prompts with redirected stdin")
	fs.StringVar(&destPath, "dest-path", ".", "destination directory for backup archive")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("invalid backup arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return errors.New("backup does not accept positional arguments; use --dest-path to override the destination directory")
	}

	destDir, err := filepath.Abs(destPath)
	if err != nil {
		return fmt.Errorf("resolve destination directory: %w", err)
	}
	destDir = filepath.Clean(destDir)
	dataDirAbs, err := filepath.Abs(opts.dataDir)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	dataDirAbs = filepath.Clean(dataDirAbs)

	password, err := readBackupPasswordInput(stdin, stderr, input)
	if err != nil {
		return err
	}
	password, err = sanitizePasswordValue(password)
	if err != nil {
		return fmt.Errorf("current password is invalid: %w", err)
	}

	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return fmt.Errorf("backup could not acquire mutation lock: %w", err)
	}
	defer release()

	if err := verifyCurrentBackupPassword(opts.dataDir, password); err != nil {
		return err
	}

	entries, err := collectBackupArchiveEntries(opts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	if err := validateBackupDestinationDir(destDir, dataDirAbs); err != nil {
		return err
	}

	archivePath := filepath.Join(destDir, backupArchiveFileName())
	if _, err := os.Stat(archivePath); err == nil {
		return fmt.Errorf("backup archive already exists: %s", archivePath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check backup archive path: %w", err)
	}

	if err := writePasswordLockedZip(archivePath, password, entries); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "backup written: %s\n", archivePath)
	return nil
}

func readBackupPasswordInput(stdin io.Reader, stderr io.Writer, opts backupInputOptions) (string, error) {
	useFD := opts.currentFD >= 0
	useStdin := opts.currentStdin

	switch {
	case useFD && useStdin:
		return "", errors.New("mixed stdin/fd password input modes are not supported")
	case useFD:
		return readPasswordFromFD(opts.currentFD)
	case useStdin:
		if isTerminalReader(stdin) {
			return "", errors.New("stdin is a TTY; non-interactive stdin mode is not allowed")
		}
		reader := bufio.NewReader(stdin)
		password, err := readPasswordLine(reader)
		if err != nil {
			return "", fmt.Errorf("read current password: %w", err)
		}
		return password, nil
	default:
		return readSinglePasswordInteractive(stdin, stderr, opts.forceTTY)
	}
}

func readSinglePasswordInteractive(stdin io.Reader, stderr io.Writer, forceTTY bool) (string, error) {
	if isTerminalReader(stdin) {
		return readSecretNoTrim(stdin, stderr, "Current password: ")
	}
	if !forceTTY {
		return "", errors.New("interactive password prompts require a TTY; use --current-stdin or --current-fd")
	}
	reader := bufio.NewReader(stdin)
	password, err := readPasswordLineWithPrompt(reader, stderr, "Current password: ")
	if err != nil {
		return "", err
	}
	return password, nil
}

func verifyCurrentBackupPassword(dataDir string, password string) error {
	meta, err := loadMeta(dataDir)
	if err != nil {
		return fmt.Errorf("load vault metadata: %w", err)
	}
	if _, err := unwrapDEKWithPassword(meta, password); err != nil {
		switch {
		case errors.Is(err, errMetadataInvalid):
			return fmt.Errorf("metadata/KDF parameters rejected by safety validation: %w", err)
		case isCredentialMismatchError(err):
			return errors.New("current password is invalid")
		default:
			return fmt.Errorf("verify current password: %w", err)
		}
	}
	return nil
}

func collectBackupArchiveEntries(opts globalOptions) ([]backupArchiveEntry, error) {
	sources, bootstrapPresent, err := collectBackupSourceFiles(opts)
	if err != nil {
		return nil, err
	}

	entries := make([]backupArchiveEntry, 0, len(sources)+1)
	fileNames := make([]string, 0, len(sources))
	for _, source := range sources {
		data, modTime, err := readBackupSourceFile(source.sourcePath)
		if err != nil {
			return nil, err
		}
		entries = append(entries, backupArchiveEntry{
			name:    source.archivePath,
			data:    data,
			modTime: modTime,
		})
		fileNames = append(fileNames, source.archivePath)
	}

	manifestData, err := json.MarshalIndent(backupManifest{
		Version:          backupManifestVersion,
		CreatedAtUTC:     time.Now().UTC().Format(time.RFC3339),
		BootstrapPresent: bootstrapPresent,
		Files:            fileNames,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal backup manifest: %w", err)
	}
	entries = append([]backupArchiveEntry{{
		name:    filepath.Join(backupArchiveRoot, "manifest.json"),
		data:    append(manifestData, '\n'),
		modTime: time.Now().UTC(),
	}}, entries...)

	return entries, nil
}

func collectBackupSourceFiles(opts globalOptions) ([]backupSourceFile, bool, error) {
	sources, err := walkBackupDataFiles(opts.dataDir)
	if err != nil {
		return nil, false, err
	}
	bootstrapPresent := false
	if info, err := os.Lstat(opts.configPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, false, fmt.Errorf("bootstrap config must not be a symlink: %s", opts.configPath)
		}
		if info.IsDir() {
			return nil, false, fmt.Errorf("bootstrap config path is a directory: %s", opts.configPath)
		}
		if !info.Mode().IsRegular() {
			return nil, false, fmt.Errorf("bootstrap config must be a regular file: %s", opts.configPath)
		}
		sources = append(sources, backupSourceFile{
			sourcePath:  opts.configPath,
			archivePath: filepath.Join(backupArchiveRoot, "config", filepath.Base(opts.configPath)),
		})
		bootstrapPresent = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("stat bootstrap config: %w", err)
	}

	return sources, bootstrapPresent, nil
}

func walkBackupDataFiles(dataDir string) ([]backupSourceFile, error) {
	required := map[string]bool{
		filepath.Join("vault", "meta.v1.json"):  false,
		filepath.Join("vault", "vault.v1.bin"):  false,
		filepath.Join("vault", "config.v1.bin"): false,
		filepath.Join("vault", vaultMarker):     false,
	}

	sources := []backupSourceFile{}
	err := filepath.WalkDir(dataDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk backup source tree: %w", walkErr)
		}
		if path == dataDir {
			return nil
		}

		relPath, err := filepath.Rel(dataDir, path)
		if err != nil {
			return fmt.Errorf("resolve backup source path: %w", err)
		}
		relPath = filepath.Clean(relPath)
		if relPath == "." {
			return nil
		}
		if isTransientBackupPath(relPath, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup source must not contain symlinks: %s", path)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("backup source must be a regular file: %s", path)
		}

		sources = append(sources, backupSourceFile{
			sourcePath:  path,
			archivePath: filepath.Join(backupArchiveRoot, filepath.ToSlash(relPath)),
		})
		if _, ok := required[relPath]; ok {
			required[relPath] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for relPath, seen := range required {
		if !seen {
			return nil, fmt.Errorf("backup required source missing: %s", filepath.Join(dataDir, relPath))
		}
	}

	sort.Slice(sources, func(i, j int) bool {
		return filepath.ToSlash(sources[i].archivePath) < filepath.ToSlash(sources[j].archivePath)
	})
	return sources, nil
}

func isTransientBackupPath(relPath string, d os.DirEntry) bool {
	relPath = filepath.Clean(relPath)
	if relPath == filepath.Join("lock") {
		return d.IsDir()
	}
	if relPath == filepath.Join("vault", mutationLockFileName) {
		return true
	}
	return false
}

func readBackupSourceFile(path string) ([]byte, time.Time, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("stat backup source %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, time.Time{}, fmt.Errorf("backup source must not be a symlink: %s", path)
	}
	if info.IsDir() {
		return nil, time.Time{}, fmt.Errorf("backup source is a directory: %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, time.Time{}, fmt.Errorf("backup source must be a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("read backup source %s: %w", path, err)
	}
	return data, info.ModTime(), nil
}

func validateBackupDestinationDir(destDir string, dataDir string) error {
	resolvedDest, err := filepath.EvalSymlinks(destDir)
	if err != nil {
		return fmt.Errorf("resolve destination directory symlinks: %w", err)
	}
	resolvedDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return fmt.Errorf("resolve kinko data dir symlinks: %w", err)
	}
	if isWithinBase(resolvedDest, resolvedDataDir) {
		return fmt.Errorf("destination directory must not be inside kinko data dir: %s", destDir)
	}
	return nil
}

func backupArchiveFileName() string {
	return "kinko-backup-" + time.Now().UTC().Format(backupArchiveNameTimeLayout) + ".zip"
}

func writePasswordLockedZip(path string, password string, entries []backupArchiveEntry) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create backup archive: %w", err)
	}
	success := false
	defer func() {
		_ = f.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()

	position := uint32(0)
	centralDir := bytes.Buffer{}
	for _, entry := range entries {
		if err := validateBackupArchivePath(entry.name); err != nil {
			return err
		}
		offset := position
		written, err := writePasswordLockedZipEntry(f, &centralDir, offset, password, entry)
		if err != nil {
			return err
		}
		position += written
	}

	centralDirOffset := position
	if _, err := f.Write(centralDir.Bytes()); err != nil {
		return fmt.Errorf("write central directory: %w", err)
	}
	position += uint32(centralDir.Len())

	endRecord := make([]byte, 22)
	binary.LittleEndian.PutUint32(endRecord[0:4], zipEndOfCentralDirSignature)
	binary.LittleEndian.PutUint16(endRecord[8:10], uint16(len(entries)))
	binary.LittleEndian.PutUint16(endRecord[10:12], uint16(len(entries)))
	binary.LittleEndian.PutUint32(endRecord[12:16], uint32(centralDir.Len()))
	binary.LittleEndian.PutUint32(endRecord[16:20], centralDirOffset)
	if _, err := f.Write(endRecord); err != nil {
		return fmt.Errorf("write end of central directory: %w", err)
	}
	position += uint32(len(endRecord))

	if err := f.Truncate(int64(position)); err != nil {
		return fmt.Errorf("finalize backup archive: %w", err)
	}
	success = true
	return nil
}

func validateBackupArchivePath(path string) error {
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return fmt.Errorf("invalid backup archive path %q", path)
	}
	return nil
}

func writePasswordLockedZipEntry(w io.Writer, centralDir *bytes.Buffer, offset uint32, password string, entry backupArchiveEntry) (uint32, error) {
	crc := crc32.ChecksumIEEE(entry.data)
	headerPlain, err := zipCryptoHeaderPlain(crc)
	if err != nil {
		return 0, fmt.Errorf("build encrypted zip header for %s: %w", entry.name, err)
	}
	encryptedPayload := zipCryptoEncrypt(password, append(headerPlain, entry.data...))
	encryptedHeader := encryptedPayload[:zipCryptoHeaderSize]
	encryptedData := encryptedPayload[zipCryptoHeaderSize:]
	compressedSize := uint32(len(encryptedHeader) + len(encryptedData))
	uncompressedSize := uint32(len(entry.data))
	modTime, modDate := zipDOSDateTime(entry.modTime)
	nameBytes := []byte(filepath.ToSlash(entry.name))

	localHeader := make([]byte, 30)
	binary.LittleEndian.PutUint32(localHeader[0:4], zipLocalFileHeaderSignature)
	binary.LittleEndian.PutUint16(localHeader[4:6], zipVersionNeeded)
	binary.LittleEndian.PutUint16(localHeader[6:8], zipGeneralPurposeFlagEncrypt)
	binary.LittleEndian.PutUint16(localHeader[8:10], zipCompressionStore)
	binary.LittleEndian.PutUint16(localHeader[10:12], modTime)
	binary.LittleEndian.PutUint16(localHeader[12:14], modDate)
	binary.LittleEndian.PutUint32(localHeader[14:18], crc)
	binary.LittleEndian.PutUint32(localHeader[18:22], compressedSize)
	binary.LittleEndian.PutUint32(localHeader[22:26], uncompressedSize)
	binary.LittleEndian.PutUint16(localHeader[26:28], uint16(len(nameBytes)))
	binary.LittleEndian.PutUint16(localHeader[28:30], 0)

	if _, err := w.Write(localHeader); err != nil {
		return 0, fmt.Errorf("write local header for %s: %w", entry.name, err)
	}
	if _, err := w.Write(nameBytes); err != nil {
		return 0, fmt.Errorf("write local header name for %s: %w", entry.name, err)
	}
	if _, err := w.Write(encryptedHeader); err != nil {
		return 0, fmt.Errorf("write encrypted header for %s: %w", entry.name, err)
	}
	if _, err := w.Write(encryptedData); err != nil {
		return 0, fmt.Errorf("write encrypted payload for %s: %w", entry.name, err)
	}

	centralHeader := make([]byte, 46)
	binary.LittleEndian.PutUint32(centralHeader[0:4], zipCentralDirHeaderSignature)
	binary.LittleEndian.PutUint16(centralHeader[4:6], 20)
	binary.LittleEndian.PutUint16(centralHeader[6:8], zipVersionNeeded)
	binary.LittleEndian.PutUint16(centralHeader[8:10], zipGeneralPurposeFlagEncrypt)
	binary.LittleEndian.PutUint16(centralHeader[10:12], zipCompressionStore)
	binary.LittleEndian.PutUint16(centralHeader[12:14], modTime)
	binary.LittleEndian.PutUint16(centralHeader[14:16], modDate)
	binary.LittleEndian.PutUint32(centralHeader[16:20], crc)
	binary.LittleEndian.PutUint32(centralHeader[20:24], compressedSize)
	binary.LittleEndian.PutUint32(centralHeader[24:28], uncompressedSize)
	binary.LittleEndian.PutUint16(centralHeader[28:30], uint16(len(nameBytes)))
	binary.LittleEndian.PutUint16(centralHeader[38:40], 0)
	binary.LittleEndian.PutUint32(centralHeader[42:46], offset)
	if _, err := centralDir.Write(centralHeader); err != nil {
		return 0, fmt.Errorf("write central directory header for %s: %w", entry.name, err)
	}
	if _, err := centralDir.Write(nameBytes); err != nil {
		return 0, fmt.Errorf("write central directory name for %s: %w", entry.name, err)
	}

	return uint32(len(localHeader) + len(nameBytes) + len(encryptedHeader) + len(encryptedData)), nil
}

func zipCryptoHeaderPlain(crc uint32) ([]byte, error) {
	header := make([]byte, zipCryptoHeaderSize)
	if _, err := rand.Read(header[:11]); err != nil {
		return nil, err
	}
	header[11] = byte(crc >> 24)
	return header, nil
}

func zipCryptoEncrypt(password string, plain []byte) []byte {
	keys := newZipCryptoKeys(password)
	out := make([]byte, len(plain))
	for i, b := range plain {
		mask := zipCryptoMask(keys[2])
		out[i] = b ^ mask
		zipCryptoUpdateKeys(&keys, b)
	}
	return out
}

func newZipCryptoKeys(password string) [3]uint32 {
	keys := [3]uint32{0x12345678, 0x23456789, 0x34567890}
	for i := 0; i < len(password); i++ {
		zipCryptoUpdateKeys(&keys, password[i])
	}
	return keys
}

func zipCryptoUpdateKeys(keys *[3]uint32, b byte) {
	keys[0] = zipCryptoCRC32Byte(keys[0], b)
	keys[1] = keys[1] + uint32(keys[0]&0xff)
	keys[1] = keys[1]*134775813 + 1
	keys[2] = zipCryptoCRC32Byte(keys[2], byte(keys[1]>>24))
}

func zipCryptoMask(key2 uint32) byte {
	temp := uint16(key2) | 2
	return byte((uint32(temp) * uint32(temp^1)) >> 8)
}

func zipCryptoCRC32Byte(crc uint32, b byte) uint32 {
	return (crc >> 8) ^ crc32.IEEETable[(crc^uint32(b))&0xff]
}

func zipDOSDateTime(ts time.Time) (uint16, uint16) {
	if ts.IsZero() {
		ts = time.Unix(0, 0).UTC()
	}
	utc := ts.UTC()
	year := utc.Year()
	if year < 1980 {
		year = 1980
	}
	if year > 2107 {
		year = 2107
	}
	dosTime := uint16(utc.Second()/2) | uint16(utc.Minute())<<5 | uint16(utc.Hour())<<11
	dosDate := uint16(utc.Day()) | uint16(utc.Month())<<5 | uint16(year-1980)<<9
	return dosTime, dosDate
}
