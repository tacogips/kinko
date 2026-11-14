# Runtime Security Baseline

**Status**: Completed
**Design Reference**: https://groups.google.com/g/golang-announce/c/66pojueaQek
**Created**: 2026-10-28
**Last Updated**: 2026-10-28

## Deliverables

Pin the mise runtime selector to the verified security release in mise.toml.
No application source, interfaces, dependency graph, or persisted data change.

## Completion Criteria

- [x] TOML parses and the runtime selector names the verified patched release.
- [x] The selected native runtime reports the expected version.
- [x] Publish through the accompanying security PR.

## Progress Log

2026-10-28: Replace the floating latest/lts selector with a reviewed security baseline.
