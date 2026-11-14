#!/usr/bin/env bash

set -euo pipefail
if [ -z "${KINKO_DEV_E2E_PASSWORD:-}" ]; then
  if command -v openssl >/dev/null 2>&1; then
    KINKO_DEV_E2E_PASSWORD="$(openssl rand -hex 16)"
  else
    KINKO_DEV_E2E_PASSWORD="$(head -c 24 /dev/urandom | base64 | tr -d '\n' | tr '/+' 'ab')"
  fi
fi

# per-run isolated state
E2E_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/kinko-dev-e2e.XXXXXX")"
trap 'rm -rf "$E2E_ROOT"' EXIT
E2E_KINKO_DIR="$E2E_ROOT/data"
E2E_CONFIG_DIR="$E2E_ROOT/config"
E2E_CONFIG_PATH="$E2E_CONFIG_DIR/bootstrap.toml"
E2E_LOG_DIR="$E2E_ROOT/logs"
mkdir -p "$E2E_KINKO_DIR" "$E2E_CONFIG_DIR"
mkdir -p "$E2E_LOG_DIR"

# 1) no-arg invocation shows help
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" 2>&1 | rg '^Usage:'

# 2) init with password confirmation
printf "%s\n%s\n" "$KINKO_DEV_E2E_PASSWORD" "$KINKO_DEV_E2E_PASSWORD" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false init >"$E2E_LOG_DIR/init.out" 2>/dev/null
rg 'initialized' "$E2E_LOG_DIR/init.out"

# 3) re-init must fail
if printf "%s\n%s\n" "$KINKO_DEV_E2E_PASSWORD" "$KINKO_DEV_E2E_PASSWORD" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false init >"$E2E_LOG_DIR/reinit.out" 2>"$E2E_LOG_DIR/reinit.err"; then
  echo "re-init unexpectedly succeeded"
  exit 1
fi
rg 'already initialized' "$E2E_LOG_DIR/reinit.err"

# 4) unlock failure boundary: 3 wrong attempts must fail and stay locked
WRONG_PASS="${KINKO_DEV_E2E_WRONG_PASSWORD:-__wrong_password__}"
if printf "%s\n%s\n%s\n" "$WRONG_PASS" "$WRONG_PASS" "$WRONG_PASS" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" unlock --timeout 5m >"$E2E_LOG_DIR/unlock-wrong.out" 2>"$E2E_LOG_DIR/unlock-wrong.err"; then
  echo "unlock unexpectedly succeeded with wrong password"
  exit 1
fi
rg 'unlock failed after 3 attempts' "$E2E_LOG_DIR/unlock-wrong.err"
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" status | rg '^locked$'

# 5) unlock success path
printf "%s\n" "$KINKO_DEV_E2E_PASSWORD" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" unlock --timeout 5m >"$E2E_LOG_DIR/unlock-ok.out" 2>"$E2E_LOG_DIR/unlock-ok.err"
rg 'unlocked' "$E2E_LOG_DIR/unlock-ok.out"
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" status | rg '^unlocked'

# 6) set/get/show basics
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" set-key FOO --value bar
MASKED="$(go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" get FOO)"
test "$MASKED" != "bar"
REVEAL="$(go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false get --reveal FOO)"
test "$REVEAL" = "bar"
WEIRD_VALUE="a'b \$() \`tick\`"
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" set-key WEIRD --value "$WEIRD_VALUE"
WEIRD_ASSIGN="$(go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false export bash | rg '^export WEIRD=')"
EXPECTED="$WEIRD_VALUE" ASSIGN="$WEIRD_ASSIGN" bash -c 'eval "$ASSIGN"; test "$WEIRD" = "$EXPECTED"'

# 7) bulk delete scope
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" set-key BAR --value baz
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" delete --all --yes
SHOW_OUT="$(go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" show)"
test -z "$SHOW_OUT"

# 8) sensitive output deny path: non-tty redirection without --force must fail
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" set-key FOO --value bar
if go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" get --reveal FOO >"$E2E_LOG_DIR/reveal-denied.out" 2>"$E2E_LOG_DIR/reveal-denied.err"; then
  echo "reveal unexpectedly succeeded without --force on redirected output"
  exit 1
fi
rg 'sensitive output blocked for non-tty/redirection' "$E2E_LOG_DIR/reveal-denied.err"
if go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" export bash >"$E2E_LOG_DIR/export-denied.out" 2>"$E2E_LOG_DIR/export-denied.err"; then
  echo "export unexpectedly succeeded without --force on redirected output"
  exit 1
fi
rg 'sensitive output blocked for non-tty/redirection' "$E2E_LOG_DIR/export-denied.err"

# 9) export/exec success checks
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false export bash | rg '^export FOO='
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" exec --all -- env | rg '^FOO=bar$'

# 10) shared/repo export->import round-trip with default import behavior
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" set --shared SHARED_ONLY=shared DUP=shared
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" set DUP=repo REPO_ONLY=repo
ROUNDTRIP_EXPORT="$(
  go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false export bash
)"
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" delete --all --yes
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" delete --shared --all --yes
SHOW_OUT="$(go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" show)"
test -z "$SHOW_OUT"
printf "%s\n" "$ROUNDTRIP_EXPORT" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" import bash --yes
DUP_RESOLVED="$(go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false get --reveal DUP)"
test "$DUP_RESOLVED" = "repo"
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" delete --yes DUP
DUP_FALLBACK="$(go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false get --reveal DUP)"
test "$DUP_FALLBACK" = "shared"
SHARED_ONLY_VAL="$(go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false get --reveal SHARED_ONLY)"
test "$SHARED_ONLY_VAL" = "shared"
REPO_ONLY_VAL="$(go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false get --reveal REPO_ONLY)"
test "$REPO_ONLY_VAL" = "repo"

# 11) lock + enforcement checks
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" lock
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" status | rg '^locked$'
for command_text in "get FOO" "show" "set-key FOO --value x" "delete --all --yes" "exec --all -- env"; do
  read -r -a command_args <<< "$command_text"
  log_name="${command_text// /-}"
  if go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" "${command_args[@]}" >"$E2E_LOG_DIR/locked-$log_name.out" 2>"$E2E_LOG_DIR/locked-$log_name.err"; then
    echo "locked-state command unexpectedly succeeded: $command_text"
    exit 1
  fi
  rg '^locked$' "$E2E_LOG_DIR/locked-$log_name.err"
done
if go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false export bash >"$E2E_LOG_DIR/locked-export.out" 2>"$E2E_LOG_DIR/locked-export.err"; then
  echo "locked-state export unexpectedly succeeded"
  exit 1
fi
rg '^locked$' "$E2E_LOG_DIR/locked-export.err"

# 12) timeout expiry should auto-lock
printf "%s\n" "$KINKO_DEV_E2E_PASSWORD" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" unlock --timeout 1s >"$E2E_LOG_DIR/unlock-short.out" 2>"$E2E_LOG_DIR/unlock-short.err"
sleep 2
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" status | rg '^locked$'

# 13) unlock for explosion negative and positive scenarios
printf "%s\n" "$KINKO_DEV_E2E_PASSWORD" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" unlock --timeout 5m >"$E2E_LOG_DIR/unlock-exp.out" 2>"$E2E_LOG_DIR/unlock-exp.err"
if printf "%s\ny\nBADTOKEN\n" "$WRONG_PASS" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" explosion >"$E2E_LOG_DIR/explosion-wrong-pass.out" 2>"$E2E_LOG_DIR/explosion-wrong-pass.err"; then
  echo "explosion unexpectedly succeeded with wrong password"
  exit 1
fi
rg 'password verification failed' "$E2E_LOG_DIR/explosion-wrong-pass.err"
test -f "$E2E_KINKO_DIR/vault/meta.v1.json"

printf "%s\nn\n" "$KINKO_DEV_E2E_PASSWORD" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" explosion >"$E2E_LOG_DIR/explosion-cancel-1.out" 2>"$E2E_LOG_DIR/explosion-cancel-1.err"
rg '^aborted$' "$E2E_LOG_DIR/explosion-cancel-1.out"
test -f "$E2E_KINKO_DIR/vault/meta.v1.json"

printf "%s\ny\nWRONGTOKEN\n" "$KINKO_DEV_E2E_PASSWORD" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" explosion >"$E2E_LOG_DIR/explosion-cancel-2.out" 2>"$E2E_LOG_DIR/explosion-cancel-2.err"
rg '^aborted$' "$E2E_LOG_DIR/explosion-cancel-2.out"
test -f "$E2E_KINKO_DIR/vault/meta.v1.json"

EXPLOSION_TOKEN="$(printf 'kinko.explosion.v1:%s' "$E2E_KINKO_DIR" | shasum -a 256 | cut -c1-12 | tr '[:lower:]' '[:upper:]')"
printf "%s\ny\n%s\n" "$KINKO_DEV_E2E_PASSWORD" "$EXPLOSION_TOKEN" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" explosion >"$E2E_LOG_DIR/explosion-ok.out" 2>"$E2E_LOG_DIR/explosion-ok.err"
rg 'explosion completed' "$E2E_LOG_DIR/explosion-ok.out"

# 14) post-reset no-arg invocation shows help
go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" 2>&1 | rg '^Usage:'

# 15) bootstrap tamper should be blocked
printf "%s\n%s\n" "$KINKO_DEV_E2E_PASSWORD" "$KINKO_DEV_E2E_PASSWORD" | go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" --force --confirm=false init >"$E2E_LOG_DIR/init2.out" 2>/dev/null
cp "$E2E_CONFIG_PATH" "$E2E_LOG_DIR/bootstrap.bak"
printf "api_key=\"forbidden\"\n" >> "$E2E_CONFIG_PATH"
if go run "$KINKO_CMD_PATH" --kinko-dir "$E2E_KINKO_DIR" --config "$E2E_CONFIG_PATH" status >"$E2E_LOG_DIR/bootstrap-status.out" 2>"$E2E_LOG_DIR/bootstrap-status.err"; then
  echo "status unexpectedly succeeded with tampered bootstrap config"
  exit 1
fi
rg 'bootstrap config contains sensitive-looking key' "$E2E_LOG_DIR/bootstrap-status.err"
mv "$E2E_LOG_DIR/bootstrap.bak" "$E2E_CONFIG_PATH"

echo "dev-e2e passed"
