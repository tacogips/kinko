# Security Review — 2026-11-14

## Scope and method

Review storage, cryptography, sessions, backup/restore, CLI and shell output,
synchronization/BWS, dependencies, CI and release scripts. Use synthetic evidence,
independent triage and GPT-6.1 Sol fixes. Preserve existing work; no commits.
This is a bounded source review, not a guarantee of absence of vulnerabilities.

## Verified findings

| ID | Priority | Location | Risk and remediation | Status |
|---|---|---|---|---|
| SEC-01 | High | runtime_io_commands.go: writeExportBlock | Newlines in profile/path escape shell comments during export/direnv evaluation. Encode control characters into one comment line; test shell evaluation. Requires populated repo scope and evaluation of exported output. | Fixed; independently verified |
| SEC-02 | Medium | restore.go: stageAndCommitRestoreFiles | Predictable staging paths follow pre-existing symlinks and retain permissive file modes. Use exclusively owned private temporary files; preserve marker-last and rollback. Requires attacker influence over destination filesystem. | Fixed; independently verified |
| SEC-03 | Medium | vault.go: decryptBlobWithResolverAndAAD | Invalid nonce lengths reach GCM.Open and panic on malformed encrypted inputs. Validate nonce length and return decryption error. Requires malformed local/archive data. | Fixed; independently verified |
| SEC-04 | Medium | runtime_mutation.go: parseSetAssignment | Malformed plaintext set input is included verbatim in errors and CLI stderr. Redact malformed assignments and invalid key text while preserving line numbers. Requires malformed secret input and observable stderr/logging. Synthetic overlay tests confirmed disclosure. | Fixed; independently verified |

## SEC-05: Default sync transport bypass (Medium)

Default push/pull in `cobra_runtime_sync.go` dispatches into the legacy runner
unless a completion flag was explicitly changed. The legacy runner uses raw BWS
create/edit calls whose argv contains plaintext secret values, without the new
transport acknowledgement. It also bypasses isolated HTTPS-validated BWS config.
Process observers may capture values during default push. Route every public
default operation through the safe runtime; test default dispatch and policy.
Status: fixed by GPT-6.1 Sol; focused tests pass. All CLI push/pull now use V2.
Compatibility: default mutations require a secure provider or explicit
`--bws-transport=cli-legacy --allow-secret-argv`; V2 state and path mapping rules
now apply even without optional flags. Internal legacy code is unreachable from
the production command path and remains for existing test coverage.

## Remaining improvement candidates

- Bound interactive password reads and archive aggregate input size to limit memory exhaustion (low; input generally user-selected).
- Validate and escape Homebrew formula rendering inputs (low; locally controlled release inputs).
- Add maintained vulnerability scanner coverage; current review cannot establish dependency vulnerability status.

## Deterministic checks and limitations

- go mod verify passed.
- Redacted gitleaks scan: synthetic PKZIP password in backup_test.go is a false positive.
- govulncheck unavailable; no claim that dependencies are vulnerability-free.
- CI action SHA pins and reduced token permissions inspected.

## Final review

All five verified findings fixed by GPT-6.1 Sol subagents. Root inspected the
production changes and tests; independent verification found no unresolved
regression after fixture remediation.

- Full `go test ./...`: PASS (internal/kinko 107.878s).
- Build, vet, formatting, and diff checks: PASS.
- Final `go mod verify`: PASS.
- Final redacted gitleaks: only the documented synthetic test-vector false positive.
- Dependency vulnerability database audit remains unavailable (govulncheck absent).
- Existing missing-secret changes preserved; no staging, commit, or push performed.

Low-priority improvements below are documented residual work, not implemented in
this remediation. Review coverage does not establish that all vulnerabilities
have been eliminated.

## Additional review notes

Restore staging findings require pre-existing malicious entries or attacker control
of the selected destination; fresh private directories restrict other users.
The staging mode defect concerns encrypted artifacts, not plaintext leakage.
A remaining low-priority reliability improvement is propagating output-writer
errors from writeExportBlock rather than reporting success for truncated output.
ZipCrypto backup limitations are already documented; a stronger versioned format
is a future design improvement, not a newly discovered encryption guarantee.

The root reviewer inspected all five production changes and their regression
coverage. Requested an additional end-to-end default-sync test beyond dispatch
stubbing to prove that unsafe mutation commands are never invoked.

## Verification follow-up

Initial full-suite run exposed historical V1 sync fixtures coupled to the removed
public dispatch. Those tests are explicitly labeled internal legacy-engine
regressions. CLI argument validation, default secure transport tests, and V2
executor coverage still exercise their intended paths. A real CLI explicit
legacy-transport success case also checks acknowledgement and V2 persistence.
The root final review caught and requested removal of two unused imports inside
the generated BWS test fixture before the verification rerun.
