# Kinko Secure Runtime MVP Implementation Plan

**Status**: Completed
**Design Reference**: design-docs/specs/command.md, design-docs/specs/architecture.md, design-docs/specs/notes.md
**Created**: 2026-03-02
**Last Updated**: 2026-03-02

---

## Design Document Reference

**Source**:
- `design-docs/specs/command.md`
- `design-docs/specs/architecture.md`
- `design-docs/specs/notes.md`
- `design-docs/user-qa/qa-kinko-mvp-decisions.md`

### Summary
Implement secure local runtime commands for `kinko` with encrypted-at-rest vault/config, shared unlock session artifact, exact path-only lookup, and guarded `export`/`exec` behavior.

### Scope
**Included**:
- `init`, `unlock`, `lock`, `status`
- `set`, `get`, `show`
- `export <shell>`, `exec -- <cmd...>`
- Encrypted vault/config storage
- Shared unlock via signed session artifact
- Export/reveal guardrails (`--force`, `--confirm`)
- Exact path-only secret resolution

**Excluded**:
- Full TUI implementation
- Daemonized key custody (`kinkod`)
- OS keychain integration
- Mnemonic standard compliance (BIP39 exact list)

---

## Modules

### 1. CLI and Command Routing

#### internal/kinko/app.go

**Status**: DONE

```go
type globalOptions struct {
    profile string
    path    string
    dataDir string
    force   bool
    confirm bool
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) error
func parseGlobalOptions(args []string) (globalOptions, []string, error)
```

**Checklist**:
- [x] Add command handlers for `init`, `unlock`, `lock`, `status`, `set`, `get`, `show`, `config`, `export`, `exec`
- [x] Add global guardrail flags (`--force`, `--confirm`)
- [x] Keep usage text aligned with commands

### 2. Vault Crypto and Persistence

#### internal/kinko/vault.go

**Status**: DONE

```go
type vaultMeta struct {
    Version            int    `json:"version"`
    SaltPasswordB64    string `json:"salt_password_b64"`
    WrappedDEKPassB64  string `json:"wrapped_dek_pass_b64"`
    SessionPubKeyB64   string `json:"session_pub_key_b64"`
    EncSessionPrivB64  string `json:"enc_session_priv_key_b64"`
}

type vaultData struct {
    Profiles map[string]map[string]map[string]string `json:"profiles"`
}

type encryptedBlob struct {
    NonceB64      string `json:"nonce_b64"`
    CiphertextB64 string `json:"ciphertext_b64"`
}

func loadVault(dataDir string, dek []byte) (*vaultData, error)
func saveVault(dataDir string, dek []byte, data *vaultData) error
func loadMeta(dataDir string) (*vaultMeta, error)
func saveConfig(dataDir string, dek []byte, cfg map[string]string) error
func loadConfig(dataDir string, dek []byte) (map[string]string, error)
```

**Checklist**:
- [x] Implement AEAD encryption/decryption helpers
- [x] Implement dual-wrap DEK persistence
- [x] Persist encrypted config payload
- [x] Unit tests for encrypt/decrypt round trip

### 3. Unlock Session

#### internal/kinko/session.go

**Status**: DONE

```go
type sessionPayload struct {
    ExpiresAtUnix int64  `json:"expires_at_unix"`
    DEKB64        string `json:"dek_b64"`
}

type sessionFile struct {
    PayloadB64 string `json:"payload_b64"`
    SigB64     string `json:"sig_b64"`
}

func lockSession(dataDir string) error
func sessionStatus(dataDir string) (locked bool, expiresAt time.Time, err error)
func loadUnlockedDEK(dataDir string) ([]byte, error)
```

**Checklist**:
- [x] Implement signed shared session artifact
- [x] Verify signature + expiry on every secret-access command
- [x] Remove session artifact on `lock`
- [ ] Unit tests for expiry/verification failures

### 4. Secret Operations and Runtime

#### internal/kinko/runtime.go

**Status**: DONE

```go
func getSecret(opts globalOptions, key string) (string, bool, error)
func showSecrets(opts globalOptions) (map[string]string, error)
func runSet(opts globalOptions, args []string, stdin io.Reader) error
func runExport(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
func runExec(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error
```

**Checklist**:
- [x] Enforce exact path-only lookup
- [x] Implement masked-by-default output for `get`/`show`
- [x] Enforce export/reveal guardrails
- [x] Keep stdout assignment-only for `export`

---

## Module Status

| Module | File Path | Status | Tests |
|--------|-----------|--------|-------|
| CLI routing | `internal/kinko/app.go` | DONE | Added |
| Vault crypto | `internal/kinko/vault.go` | DONE | Added |
| Session management | `internal/kinko/session.go` | DONE | Partial |
| Runtime commands | `internal/kinko/runtime.go` | DONE | Added |

## Dependencies

| Feature | Depends On | Status |
|---------|------------|--------|
| Session unlock | Vault crypto | READY |
| Secret set/get/show | Vault crypto + session | READY |
| Export/exec | Secret resolution + guardrails | READY |

## Completion Criteria

- [x] Encrypted vault and encrypted config are persisted
- [x] `unlock/lock/status` commands work with shared session artifact
- [x] `set/get/show` work with exact path-only lookup
- [x] `export <shell>` supports aliases and guardrails
- [x] `exec -- <cmd...>` injects env without shell export
- [x] `go test ./...` passes
- [x] `go build ./...` passes

## Progress Log

### Session: 2026-03-02 00:00
**Tasks Completed**: Created implementation plan; started implementation.
**Tasks In Progress**: CLI routing, encrypted vault/session runtime.
**Blockers**: None.
**Notes**: Existing plaintext prototype will be replaced with encrypted/session-aware MVP.

### Session: 2026-03-02 15:00
**Tasks Completed**: Implemented encrypted vault/config, signed shared unlock, exact path secret resolution, guarded export/reveal, exec environment injection, and config show/set.
**Tasks In Progress**: None.
**Blockers**: None.
**Notes**: Session verification edge-case tests remain a hardening follow-up.

## Related Plans

- **Previous**: None
- **Next**: Optional `kinkod` daemon hardening plan
- **Depends On**: `design-docs/specs/command.md`, `design-docs/specs/architecture.md`
