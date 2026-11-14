---
description: "Bump version in flake.nix and related files (arg: major/minor/patch, default=patch)"
---

Bump the application version.

## Arguments

- `$ARGUMENTS`: Version bump type - `major`, `minor`, or `patch` (default: `patch`)

## Instructions

1. **Read current version** from `internal/build/VERSION` file

2. **Parse and bump version** based on argument:
   - If `$ARGUMENTS` is empty or `patch`: increment patch (e.g., 0.1.1 -> 0.1.2)
   - If `$ARGUMENTS` is `minor`: increment minor, reset patch (e.g., 0.1.1 -> 0.2.0)
   - If `$ARGUMENTS` is `major`: increment major, reset minor and patch (e.g., 0.1.1 -> 1.0.0)

3. **Update version** in the following file:
   - `internal/build/VERSION`: Write the new version (without trailing newline)

4. **Display summary**:
   - Show old version -> new version
   - List files updated
   - Remind user to commit the changes

## Notes

The version is stored in `internal/build/VERSION` and read by `flake.nix` using:
```nix
version = builtins.replaceStrings [ "\n" ] [ "" ] (builtins.readFile ./internal/build/VERSION);
```

This approach provides a single source of truth for version management across the project.

## Example Usage

```bash
/bump-ver          # 0.1.1 -> 0.1.2 (patch)
/bump-ver patch    # 0.1.1 -> 0.1.2
/bump-ver minor    # 0.1.1 -> 0.2.0
/bump-ver major    # 0.1.1 -> 1.0.0
```

## Current Argument

Bump type: `$ARGUMENTS` (default to `patch` if empty)
