# Go x509 Security Toolchain Update

**Status**: Completed
**Design Reference**: https://groups.google.com/g/golang-announce/c/66pojueaQek
**Created**: 2026-10-27
**Last Updated**: 2026-10-27

Update build and CI runtime selectors to Go 1.27.3 for CVE-2026-111943.
Deliverables: Go module/toolchain metadata and existing runtime declarations.
Application APIs and Go source types are unchanged.

| Task | Depends on | Status |
| --- | --- | --- |
| Refresh compiler declarations | None | Complete |
| Run existing relevant checks | Refresh | Complete |
| Publish security update | Checks | Complete |

## Completion Criteria

- [x] Go module/runtime declarations select a patched compiler.
- [x] Relevant builds and tests pass or actual blockers are documented.
- [x] Changes published through the accompanying security PR.

## Progress Log

2026-10-27: Compiler declarations updated; appropriate existing checks completed; compiler refusal and incident-chain verification passed. Publication is represented by the accompanying PR.
