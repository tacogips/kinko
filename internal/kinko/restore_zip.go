package kinko

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path"
)

// restoreZipEntry is one decrypted, CRC-verified archive entry keyed by its
// cleaned, forward-slash archive name (e.g. "kinko-backup/vault/meta.v1.json").
type restoreZipEntry struct {
	name string
	data []byte
}

// restoreZipArchive is the fully parsed and decrypted contents of a backup
// archive, keyed by cleaned archive-relative entry name.
type restoreZipArchive struct {
	entries map[string]restoreZipEntry
	order   []string // archive order, for deterministic error messages/tests
}

// maxRestoreZipEntrySize bounds per-entry uncompressed size to guard memory
// use against hostile archives (design: 64 MiB sanity cap).
const maxRestoreZipEntrySize = 64 * 1024 * 1024

// zipReadErrorKind distinguishes "wrong password" from "structurally invalid
// archive" so callers can map to exitCodeAuthFailed vs exitCodePolicyFailed.
type zipReadErrorKind int

const (
	zipReadErrorKindPolicy zipReadErrorKind = iota
	zipReadErrorKindAuth
	zipReadErrorKindIO
)

// zipReadError distinguishes "wrong password" from "structurally invalid
// archive" so callers can map to exitCodeAuthFailed vs exitCodePolicyFailed.
type zipReadError struct {
	kind zipReadErrorKind
	msg  string
	err  error
}

func (e *zipReadError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	if e.err != nil {
		return e.err.Error()
	}
	return "zip read failed"
}

func (e *zipReadError) Unwrap() error { return e.err }

// zipReadEndOfCentralDirWindow bounds how far from the end of the file we
// scan for the end-of-central-directory signature. The writer never emits a
// comment, so the record is always exactly the last 22 bytes of the file;
// this window only allows for the record itself, with no comment tolerance.
const zipReadEndOfCentralDirSize = 22

// zip64SentinelU16 and zip64SentinelU32 are the reserved "look in the ZIP64
// extension" values; the writer never emits ZIP64 data, so their presence in
// any relevant field indicates a hostile or malformed archive.
const (
	zip64SentinelU16 = 0xFFFF
	zip64SentinelU32 = 0xFFFFFFFF
)

// zip64EndOfCentralDirLocatorSignature is the ZIP64 EOCD locator signature;
// its presence anywhere in the trailing region is rejected structurally.
const zip64EndOfCentralDirLocatorSignature = 0x07064b50

// readPasswordLockedZip opens, strictly parses (EOCD, central directory,
// local headers; store-only, ZipCrypto-only, no ZIP64, no archive comment),
// decrypts every entry with password, and verifies each entry's ZipCrypto
// check byte and CRC32 against the local/central header values. It returns
// zipReadError with kind=Auth when the failure pattern is consistent with a
// wrong password (check-byte mismatch on all entries), kind=Policy for any
// other structural violation.
func readPasswordLockedZip(path string, password string) (*restoreZipArchive, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &zipReadError{kind: zipReadErrorKindIO, msg: fmt.Sprintf("read backup archive %s: %v", path, err), err: err}
	}

	eocd, err := locateZipEndOfCentralDir(data)
	if err != nil {
		return nil, err
	}

	centralEntries, err := parseZipCentralDirectory(data, eocd)
	if err != nil {
		return nil, err
	}

	return decryptZipCentralDirectoryEntries(data, centralEntries, password)
}

// entry returns the decrypted bytes for a required archive-relative name, or
// a policy zipReadError if absent.
func (a *restoreZipArchive) entry(name string) ([]byte, error) {
	e, ok := a.entries[name]
	if !ok {
		return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("archive entry missing: %s", name)}
	}
	return e.data, nil
}

// zipEndOfCentralDir holds the fields of the end-of-central-directory record
// needed to locate and validate the central directory.
type zipEndOfCentralDir struct {
	totalEntries  uint16
	centralDirLen uint32
	centralDirOff uint32
	recordStart   int
}

// locateZipEndOfCentralDir scans the tail of the archive for the EOCD
// signature, requiring it to be exactly the last 22 bytes of the file (no
// archive comment) with no multi-disk fields and consistent entry counts.
func locateZipEndOfCentralDir(data []byte) (zipEndOfCentralDir, error) {
	if len(data) < zipReadEndOfCentralDirSize {
		return zipEndOfCentralDir{}, &zipReadError{kind: zipReadErrorKindPolicy, msg: "archive too small to contain end of central directory record"}
	}

	recordStart := len(data) - zipReadEndOfCentralDirSize
	record := data[recordStart:]
	sig := binary.LittleEndian.Uint32(record[0:4])
	if sig != zipEndOfCentralDirSignature {
		return zipEndOfCentralDir{}, &zipReadError{kind: zipReadErrorKindPolicy, msg: "end of central directory signature not found at expected offset (archive comment or trailing data not supported)"}
	}

	diskNumber := binary.LittleEndian.Uint16(record[4:6])
	cdStartDisk := binary.LittleEndian.Uint16(record[6:8])
	entriesOnDisk := binary.LittleEndian.Uint16(record[8:10])
	totalEntries := binary.LittleEndian.Uint16(record[10:12])
	cdSize := binary.LittleEndian.Uint32(record[12:16])
	cdOffset := binary.LittleEndian.Uint32(record[16:20])
	commentLen := binary.LittleEndian.Uint16(record[20:22])

	if diskNumber != 0 || cdStartDisk != 0 {
		return zipEndOfCentralDir{}, &zipReadError{kind: zipReadErrorKindPolicy, msg: "multi-disk archives are not supported"}
	}
	if commentLen != 0 {
		return zipEndOfCentralDir{}, &zipReadError{kind: zipReadErrorKindPolicy, msg: "archive comment is not supported"}
	}
	if entriesOnDisk != totalEntries {
		return zipEndOfCentralDir{}, &zipReadError{kind: zipReadErrorKindPolicy, msg: "end of central directory entry counts disagree"}
	}
	if entriesOnDisk == zip64SentinelU16 || totalEntries == zip64SentinelU16 || cdSize == zip64SentinelU32 || cdOffset == zip64SentinelU32 {
		return zipReadEOCDZip64Rejected()
	}
	if err := rejectZip64LocatorInTrailingRegion(data, recordStart); err != nil {
		return zipEndOfCentralDir{}, err
	}

	if int64(cdOffset)+int64(cdSize) != int64(recordStart) {
		return zipEndOfCentralDir{}, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory does not immediately precede end of central directory record"}
	}
	if int(cdOffset) < 0 || int(cdOffset) > recordStart {
		return zipEndOfCentralDir{}, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory offset out of range"}
	}

	return zipEndOfCentralDir{
		totalEntries:  totalEntries,
		centralDirLen: cdSize,
		centralDirOff: cdOffset,
		recordStart:   recordStart,
	}, nil
}

func zipReadEOCDZip64Rejected() (zipEndOfCentralDir, error) {
	return zipEndOfCentralDir{}, &zipReadError{kind: zipReadErrorKindPolicy, msg: "ZIP64 archives are not supported"}
}

// rejectZip64LocatorInTrailingRegion rejects archives that carry a ZIP64
// end-of-central-directory locator immediately before the EOCD record. The
// locator record is 20 bytes; scan the 20 bytes preceding recordStart (if
// present) for its signature.
func rejectZip64LocatorInTrailingRegion(data []byte, recordStart int) error {
	const zip64LocatorSize = 20
	if recordStart < zip64LocatorSize {
		return nil
	}
	candidate := data[recordStart-zip64LocatorSize : recordStart]
	sig := binary.LittleEndian.Uint32(candidate[0:4])
	if sig == zip64EndOfCentralDirLocatorSignature {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: "ZIP64 end of central directory locator present"}
	}
	return nil
}

// zipCentralDirEntry holds the parsed, validated fields of one central
// directory record plus the raw (uncleaned) name bytes as read.
type zipCentralDirEntry struct {
	name              string
	flags             uint16
	compression       uint16
	crc32             uint32
	compressedSize    uint32
	uncompressedSize  uint32
	localHeaderOffset uint32
}

// parseZipCentralDirectory parses every central directory record, validating
// each against the writer's fixed encoding and cross-checking the matching
// local file header.
func parseZipCentralDirectory(data []byte, eocd zipEndOfCentralDir) ([]zipCentralDirEntry, error) {
	start := int(eocd.centralDirOff)
	end := eocd.recordStart
	if start < 0 || end > len(data) || start > end {
		return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory bounds out of range"}
	}

	entries := make([]zipCentralDirEntry, 0, eocd.totalEntries)
	seenNames := make(map[string]bool, eocd.totalEntries)
	offset := start
	for i := 0; i < int(eocd.totalEntries); i++ {
		entry, next, err := parseOneZipCentralDirEntry(data, offset, end)
		if err != nil {
			return nil, err
		}
		if seenNames[entry.name] {
			return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("duplicate archive entry name: %s", entry.name)}
		}
		seenNames[entry.name] = true

		if err := verifyZipLocalHeaderMatches(data, entry); err != nil {
			return nil, err
		}

		entries = append(entries, entry)
		offset = next
	}
	if offset != end {
		return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory length does not match declared entries"}
	}

	return entries, nil
}

const zipCentralDirHeaderFixedSize = 46

func parseOneZipCentralDirEntry(data []byte, offset int, end int) (zipCentralDirEntry, int, error) {
	if offset < 0 || offset+zipCentralDirHeaderFixedSize > end {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "truncated central directory header"}
	}
	header := data[offset : offset+zipCentralDirHeaderFixedSize]

	sig := binary.LittleEndian.Uint32(header[0:4])
	if sig != zipCentralDirHeaderSignature {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory header signature mismatch"}
	}

	flags := binary.LittleEndian.Uint16(header[8:10])
	compression := binary.LittleEndian.Uint16(header[10:12])
	crc := binary.LittleEndian.Uint32(header[16:20])
	compressedSize := binary.LittleEndian.Uint32(header[20:24])
	uncompressedSize := binary.LittleEndian.Uint32(header[24:28])
	nameLen := binary.LittleEndian.Uint16(header[28:30])
	extraLen := binary.LittleEndian.Uint16(header[30:32])
	commentLen := binary.LittleEndian.Uint16(header[32:34])
	diskNumberStart := binary.LittleEndian.Uint16(header[34:36])
	localHeaderOffset := binary.LittleEndian.Uint32(header[42:46])

	if nameLen == zip64SentinelU16 || extraLen == zip64SentinelU16 ||
		compressedSize == zip64SentinelU32 || uncompressedSize == zip64SentinelU32 ||
		localHeaderOffset == zip64SentinelU32 {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "ZIP64 fields present in central directory entry"}
	}
	if compression != zipCompressionStore {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "unsupported compression method in central directory entry"}
	}
	if flags&zipGeneralPurposeFlagEncrypt == 0 {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory entry missing required encryption flag"}
	}
	if flags&(1<<3) != 0 {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory entry uses unsupported data descriptor flag"}
	}
	if extraLen != 0 {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory entry has unsupported extra field"}
	}
	if commentLen != 0 {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory entry has unsupported comment"}
	}
	if diskNumberStart != 0 {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "central directory entry references unsupported disk number"}
	}
	if uncompressedSize > maxRestoreZipEntrySize {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("archive entry exceeds maximum allowed size: %d bytes", uncompressedSize)}
	}

	nameStart := offset + zipCentralDirHeaderFixedSize
	nameEnd := nameStart + int(nameLen)
	if nameEnd > end {
		return zipCentralDirEntry{}, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: "truncated central directory entry name"}
	}
	rawName := string(data[nameStart:nameEnd])
	cleanName := cleanZipArchiveName(rawName)

	next := nameEnd + int(extraLen) + int(commentLen)

	return zipCentralDirEntry{
		name:              cleanName,
		flags:             flags,
		compression:       compression,
		crc32:             crc,
		compressedSize:    compressedSize,
		uncompressedSize:  uncompressedSize,
		localHeaderOffset: localHeaderOffset,
	}, next, nil
}

const zipLocalFileHeaderFixedSize = 30

// verifyZipLocalHeaderMatches reads the local file header at the entry's
// recorded offset and verifies its crc32/compressedSize/uncompressedSize/
// compression/flags/name all match the central directory entry exactly.
func verifyZipLocalHeaderMatches(data []byte, entry zipCentralDirEntry) error {
	offset := int(entry.localHeaderOffset)
	if offset < 0 || offset+zipLocalFileHeaderFixedSize > len(data) {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("local header offset out of range for %s", entry.name)}
	}
	header := data[offset : offset+zipLocalFileHeaderFixedSize]

	sig := binary.LittleEndian.Uint32(header[0:4])
	if sig != zipLocalFileHeaderSignature {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("local header signature mismatch for %s", entry.name)}
	}

	flags := binary.LittleEndian.Uint16(header[6:8])
	compression := binary.LittleEndian.Uint16(header[8:10])
	crc := binary.LittleEndian.Uint32(header[14:18])
	compressedSize := binary.LittleEndian.Uint32(header[18:22])
	uncompressedSize := binary.LittleEndian.Uint32(header[22:26])
	nameLen := binary.LittleEndian.Uint16(header[26:28])
	extraLen := binary.LittleEndian.Uint16(header[28:30])

	if flags != entry.flags {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("local header flags mismatch for %s", entry.name)}
	}
	if compression != entry.compression {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("local header compression mismatch for %s", entry.name)}
	}
	if crc != entry.crc32 {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("local header crc32 mismatch for %s", entry.name)}
	}
	if compressedSize != entry.compressedSize {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("local header compressed size mismatch for %s", entry.name)}
	}
	if uncompressedSize != entry.uncompressedSize {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("local header uncompressed size mismatch for %s", entry.name)}
	}
	if extraLen != 0 {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("local header has unsupported extra field for %s", entry.name)}
	}

	nameStart := offset + zipLocalFileHeaderFixedSize
	nameEnd := nameStart + int(nameLen)
	if nameEnd > len(data) {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("truncated local header name for %s", entry.name)}
	}
	rawName := string(data[nameStart:nameEnd])
	if cleanZipArchiveName(rawName) != entry.name {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("local header name mismatch for %s", entry.name)}
	}

	payloadStart := nameEnd
	payloadEnd := payloadStart + int(compressedSize)
	if payloadEnd > len(data) {
		return &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("truncated local header payload for %s", entry.name)}
	}

	return nil
}

// zipLocalPayloadBounds returns the byte range of an entry's encrypted
// payload (12-byte ZipCrypto header + ciphertext) within the archive.
func zipLocalPayloadBounds(data []byte, entry zipCentralDirEntry) (int, int, error) {
	offset := int(entry.localHeaderOffset)
	header := data[offset : offset+zipLocalFileHeaderFixedSize]
	nameLen := binary.LittleEndian.Uint16(header[26:28])
	payloadStart := offset + zipLocalFileHeaderFixedSize + int(nameLen)
	payloadEnd := payloadStart + int(entry.compressedSize)
	if payloadEnd > len(data) {
		return 0, 0, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("truncated payload for %s", entry.name)}
	}
	return payloadStart, payloadEnd, nil
}

// decryptZipCentralDirectoryEntries decrypts every entry, verifies the
// ZipCrypto check byte and CRC32, and classifies the resulting error as
// kind=Auth (wrong password) only when the check byte failed on every entry.
func decryptZipCentralDirectoryEntries(data []byte, centralEntries []zipCentralDirEntry, password string) (*restoreZipArchive, error) {
	archive := &restoreZipArchive{
		entries: make(map[string]restoreZipEntry, len(centralEntries)),
		order:   make([]string, 0, len(centralEntries)),
	}

	type decodeResult struct {
		name          string
		checkByteOK   bool
		crcOK         bool
		plain         []byte
		integrityErrs []string
	}

	results := make([]decodeResult, 0, len(centralEntries))
	for _, entry := range centralEntries {
		if entry.compressedSize < zipCryptoHeaderSize {
			return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("encrypted payload too small for %s", entry.name)}
		}
		payloadStart, payloadEnd, err := zipLocalPayloadBounds(data, entry)
		if err != nil {
			return nil, err
		}
		ciphertext := data[payloadStart:payloadEnd]
		plainWithHeader := zipCryptoDecrypt(password, ciphertext)
		checkByte := plainWithHeader[zipCryptoHeaderSize-1]
		checkByteOK := checkByte == byte(entry.crc32>>24)

		body := plainWithHeader[zipCryptoHeaderSize:]
		if uint32(len(body)) != entry.uncompressedSize {
			return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("decrypted size mismatch for %s", entry.name)}
		}
		crcOK := crc32.ChecksumIEEE(body) == entry.crc32

		results = append(results, decodeResult{
			name:        entry.name,
			checkByteOK: checkByteOK,
			crcOK:       crcOK,
			plain:       body,
		})
	}

	allCheckBytesFailed := len(results) > 0
	anyIntegrityFailure := false
	var firstFailure string
	for _, r := range results {
		if r.checkByteOK {
			allCheckBytesFailed = false
		}
		if !r.checkByteOK || !r.crcOK {
			anyIntegrityFailure = true
			if firstFailure == "" {
				firstFailure = r.name
			}
		}
	}

	if allCheckBytesFailed {
		return nil, &zipReadError{kind: zipReadErrorKindAuth, msg: "failed to decrypt archive: incorrect password"}
	}
	if anyIntegrityFailure {
		return nil, &zipReadError{kind: zipReadErrorKindPolicy, msg: fmt.Sprintf("archive integrity check failed for entry: %s", firstFailure)}
	}

	for _, r := range results {
		archive.entries[r.name] = restoreZipEntry{name: r.name, data: r.plain}
		archive.order = append(archive.order, r.name)
	}

	return archive, nil
}

// zipCryptoDecrypt reverses zipCryptoEncrypt (internal/kinko/backup.go): for
// each ciphertext byte it computes the current keystream mask, recovers the
// plaintext byte, and then advances the key state using the recovered
// plaintext byte (standard ZipCrypto stream cipher decryption).
func zipCryptoDecrypt(password string, cipherText []byte) []byte {
	keys := newZipCryptoKeys(password)
	out := make([]byte, len(cipherText))
	for i, b := range cipherText {
		mask := zipCryptoMask(keys[2])
		plainByte := b ^ mask
		out[i] = plainByte
		zipCryptoUpdateKeys(&keys, plainByte)
	}
	return out
}

// cleanZipArchiveName normalizes a raw ZIP entry name using forward-slash
// path semantics (ZIP names are always "/"-separated per spec, regardless of
// host OS), via the "path" package rather than "path/filepath".
func cleanZipArchiveName(raw string) string {
	return path.Clean(raw)
}
