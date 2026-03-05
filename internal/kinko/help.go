package kinko

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type helpDefaults struct {
	profile string
	path    string
	dataDir string
	config  string
}

func tryHandleHelp(args []string, stdout, stderr io.Writer) (bool, error) {
	if !isHelpRequest(args) {
		return false, nil
	}

	root := newHelpCommand(stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		return true, err
	}
	return true, nil
}

func isHelpRequest(args []string) bool {
	for i, a := range args {
		if a == "--" {
			break
		}
		if a == "--help" || a == "-h" {
			return true
		}
		if i == 0 && a == "help" {
			return true
		}
	}
	return false
}

func newHelpCommand(stdout, stderr io.Writer) *cobra.Command {
	d := defaultHelpValues()

	root := &cobra.Command{
		Use:   "kinko",
		Short: "Encrypted local secret vault with scope-aware environment workflows",
		Long: fmt.Sprintf(`kinko manages encrypted secrets with shared and repository-specific scopes.

TTY and safety constraints:
  - Sensitive output (e.g. show --reveal, export) is blocked when stdout is redirected unless --force is set.
  - When --confirm=true (default), sensitive output on TTY asks for explicit confirmation.
  - import with --confirm-with-values requires TTY stderr unless --yes is used.
  - password change interactive mode requires TTY stdin unless --force-tty is explicitly set.

Scope precedence:
  - Shared keys apply to all repositories/profiles.
  - Repository-specific keys (profile + path) override shared keys with the same name.

Global defaults:
  --profile=%s
  --path=%s
  --kinko-dir=%s
  --config=%s
  --keychain-preflight=required

Environment overrides:
  - KINKO_PROFILE overrides the default for --profile.
  - KINKO_PATH overrides the default for --path.
  - KINKO_DATA_DIR overrides the default for --kinko-dir.
  - KINKO_CONFIG overrides the default for --config.
  - KINKO_KEYCHAIN_PREFLIGHT overrides the default for --keychain-preflight.`, d.profile, d.path, d.dataDir, d.config),
		Example: `  kinko init
  kinko unlock --timeout 9h
  kinko set API_KEY=secret DB_URL=postgres://localhost
  kinko set --shared ORG_NAME=my-org
  kinko show
  kinko show --all-scopes
  kinko show --reveal --force
  kinko export bash --exclude AWS_SECRET_ACCESS_KEY
  kinko direnv export
  kinko import --file .env.export --yes
  kinko exec --env API_KEY,DB_URL -- go test ./...
  kinko password change --current-stdin --new-stdin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().String("profile", d.profile, "Profile name for repository-specific secrets (env: KINKO_PROFILE)")
	root.PersistentFlags().String("path", d.path, "Repository path scope used for repository-specific secrets (env: KINKO_PATH)")
	root.PersistentFlags().String("kinko-dir", d.dataDir, "Data directory for encrypted vault/session state (env: KINKO_DATA_DIR)")
	root.PersistentFlags().String("config", d.config, "Bootstrap config path (env: KINKO_CONFIG)")
	root.PersistentFlags().String("keychain-preflight", "required", "Keychain preflight mode: required|best-effort|off (env: KINKO_KEYCHAIN_PREFLIGHT)")
	root.PersistentFlags().Bool("force", false, "Override non-TTY/redirection guardrails for sensitive operations")
	root.PersistentFlags().Bool("confirm", true, "Prompt on sensitive TTY output")

	root.AddCommand(
		helpCmdInit(),
		helpCmdUnlock(),
		helpCmdLock(),
		helpCmdStatus(),
		helpCmdVersion(),
		helpCmdSet(),
		helpCmdSetKey(),
		helpCmdDelete(),
		helpCmdExplosion(),
		helpCmdGet(),
		helpCmdShow(),
		helpCmdConfig(),
		helpCmdExport(),
		helpCmdImport(),
		helpCmdExec(),
		helpCmdDirenv(),
		helpCmdPassword(),
	)

	return root
}

func defaultHelpValues() helpDefaults {
	cwd := "."
	if v, err := os.Getwd(); err == nil {
		cwd = v
	}
	home := "~"
	if v, err := os.UserHomeDir(); err == nil {
		home = v
	}

	return helpDefaults{
		profile: envOrDefault("KINKO_PROFILE", defaultProfile),
		path:    envOrDefault("KINKO_PATH", cwd),
		dataDir: envOrDefault("KINKO_DATA_DIR", filepath.Join(home, ".local", "kinko")),
		config:  envOrDefault("KINKO_CONFIG", filepath.Join(home, ".config", "kinko", "bootstrap.toml")),
	}
}

func passthroughHelp(cmd *cobra.Command, args []string) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return cmd.Help()
		}
	}
	return nil
}

func helpCmdInit() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize encrypted vault metadata and local storage",
		Long: `Creates vault data structures and bootstrap config.

This command prompts for a new vault password and confirms it.
When keychain preflight mode is enabled, availability checks run before initialization.`,
		Example: `  kinko init
  kinko --keychain-preflight best-effort init`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
	}
}

func helpCmdUnlock() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock session key material for vault operations",
		Long:  "Unlocks the vault session after password verification and sets an auto-lock timeout.",
		Example: `  kinko unlock
  kinko unlock --timeout 4h`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
	}
	cmd.Flags().Duration("timeout", 9, "Unlock timeout duration (e.g. 30m, 2h, 9h)")
	return cmd
}

func helpCmdLock() *cobra.Command {
	return &cobra.Command{
		Use:   "lock",
		Short: "Lock active session immediately",
		Args:  cobra.ArbitraryArgs,
		RunE:  passthroughHelp,
	}
}

func helpCmdStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current lock status",
		Args:  cobra.ArbitraryArgs,
		RunE:  passthroughHelp,
	}
}

func helpCmdVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Args:  cobra.ArbitraryArgs,
		RunE:  passthroughHelp,
	}
}

func helpCmdSet() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [--shared] KEY=VALUE [KEY=VALUE ...]",
		Short: "Set one or more secrets",
		Long: `Sets secrets in repository scope by default.
Use --shared to write into shared scope.

Input modes:
  - Positional KEY=VALUE assignments.
  - Non-interactive stdin lines (KEY=VALUE), when stdin is piped.`,
		Example: `  kinko set API_KEY=secret DB_URL=postgres://localhost
  echo 'TOKEN=abc' | kinko set
  kinko set --shared ORG_NAME=my-org`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
	}
	cmd.Flags().Bool("shared", false, "Write keys into shared scope")
	return cmd
}

func helpCmdSetKey() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-key [--shared] KEY --value VALUE",
		Short: "Set a single key using explicit value mode",
		Args:  cobra.ArbitraryArgs,
		RunE:  passthroughHelp,
		Example: `  kinko set-key API_KEY --value secret
  printf 'secret\n' | kinko set-key API_KEY`,
	}
	cmd.Flags().Bool("shared", false, "Write key into shared scope")
	cmd.Flags().String("value", "", "Secret value (can be omitted to read one line from stdin)")
	return cmd
}

func helpCmdDelete() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [--shared] KEY|--all [--yes]",
		Short: "Delete a key or all keys in a scope",
		Args:  cobra.ArbitraryArgs,
		RunE:  passthroughHelp,
		Example: `  kinko delete API_KEY
  kinko delete --all --yes
  kinko delete --shared ORG_TOKEN`,
	}
	cmd.Flags().Bool("shared", false, "Delete from shared scope")
	cmd.Flags().Bool("all", false, "Delete all keys in resolved scope")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts")
	return cmd
}

func helpCmdExplosion() *cobra.Command {
	return &cobra.Command{
		Use:   "explosion",
		Short: "Irreversibly destroy vault data",
		Long:  "DANGEROUS: permanently destroys vault data and metadata after multi-step confirmation.",
		Args:  cobra.ArbitraryArgs,
		RunE:  passthroughHelp,
	}
}

func helpCmdGet() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get KEY [--reveal]",
		Short: "Get a single secret",
		Long: `By default output is masked.
Use --reveal to print plaintext.

TTY constraint:
  reveal output is blocked on redirected stdout unless --force is set.`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
		Example: `  kinko get API_KEY
  kinko get API_KEY --reveal --force`,
	}
	cmd.Flags().Bool("reveal", false, "Show plaintext value")
	return cmd
}

func helpCmdShow() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [--reveal] [--all-scopes]",
		Short: "List secrets grouped by scope",
		Long: `Displays grouped scope sections.
Default output includes:
  - # shared
  - # path=<resolved path>
With --all-scopes, output includes:
  - # profile=<profile>
  - # shared
  - every stored # path=<path> section in current profile.

TTY constraints:
  - --reveal shows plaintext and is blocked on redirected stdout unless --force is set.
  - With --confirm=true, reveal mode prompts for confirmation on TTY.`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
		Example: `  kinko show
  kinko show --all-scopes
  kinko show --reveal --force
  kinko --profile prod show --all-scopes`,
	}
	cmd.Flags().Bool("reveal", false, "Show plaintext values")
	cmd.Flags().Bool("all-scopes", false, "Show shared and every path scope in current profile (ignores --path)")
	return cmd
}

func helpCmdConfig() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage encrypted config key-values",
		Args:  cobra.ArbitraryArgs,
		RunE:  passthroughHelp,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "List config key-values",
			Args:  cobra.ArbitraryArgs,
			RunE:  passthroughHelp,
		},
		&cobra.Command{
			Use:     "set KEY VALUE",
			Short:   "Set a config key-value",
			Args:    cobra.ArbitraryArgs,
			RunE:    passthroughHelp,
			Example: "  kinko config set editor vim",
		},
	)
	return cmd
}

func helpCmdExport() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export [shell]",
		Short: "Export secrets as shell assignments",
		Long: `Export output can contain plaintext secrets.

TTY constraint:
  export is blocked on redirected stdout unless --force is set.`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
		Example: `  kinko export
  kinko export bash
  kinko export fish --exclude API_KEY
  kinko export nu --with-scope-comments=false`,
	}
	cmd.Flags().Bool("with-scope-comments", true, "Include # kinko:scope markers")
	cmd.Flags().StringSlice("exclude", nil, "Comma-separated key denylist to omit (repeatable)")
	return cmd
}

func helpCmdImport() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import [shell]",
		Short: "Import secrets from shell assignment content",
		Long: `Imports assignments from stdin or --file.
By default this supports shared scope markers (# kinko:scope=shared).
Posix input accepts export KEY=value, export KEY='value', export KEY="value", and KEY=value.

TTY constraints:
  - interactive confirmation uses stderr.
  - --confirm-with-values on redirected stderr requires --yes or --force behavior.`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
		Example: `  kinko import --file .env.export
  kinko import --yes --file .env.export
  cat .env.export | kinko import bash
  kinko import --allow-shared=false --file env.txt`,
	}
	cmd.Flags().String("file", "", "Import source file path (stdin pipe alternative)")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation flow")
	cmd.Flags().Bool("confirm-with-values", false, "Show plaintext values in confirmation output")
	cmd.Flags().Bool("allow-shared", true, "Allow shared-scope markers in import input")
	return cmd
}

func helpCmdExec() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec (--all|--env KEY[,KEY...]) -- command [args...]",
		Short: "Run subprocess with selected secrets in environment",
		Long: `Injects selected secrets into child process environment.

Selection rules:
  - Use --all to inject every resolved secret.
  - Use --env to inject a specific allowlist.
  - --all and --env are mutually exclusive.`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
		Example: `  kinko exec --all -- env | grep API
  kinko exec --env API_KEY,DB_URL -- go test ./...
  kinko exec --env API_KEY -- bash -lc 'echo $API_KEY'`,
	}
	cmd.Flags().Bool("all", false, "Inject all resolved secrets")
	cmd.Flags().String("env", "", "Comma-separated key allowlist")
	return cmd
}

func helpCmdDirenv() *cobra.Command {
	root := &cobra.Command{
		Use:   "direnv",
		Short: "direnv-focused helper commands",
		Args:  cobra.ArbitraryArgs,
		RunE:  passthroughHelp,
	}

	exportCmd := &cobra.Command{
		Use:   "export [shell]",
		Short: "Export secrets for direnv eval with auto-detected scope",
		Long: `Intended for use inside .envrc.

Behavior:
  - scope path is derived from DIRENV_DIR when available.
  - forces non-interactive-safe mode (--force, --confirm=false semantics).
  - defaults shell to bash for direnv eval compatibility.`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
		Example: `  eval "$(kinko direnv export)"
  eval "$(kinko direnv export bash --exclude AWS_SECRET_ACCESS_KEY)"`,
	}
	exportCmd.Flags().Bool("with-scope-comments", true, "Include # kinko:scope markers")
	exportCmd.Flags().StringSlice("exclude", nil, "Comma-separated key denylist to omit (repeatable)")

	root.AddCommand(exportCmd)
	return root
}

func helpCmdPassword() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "password",
		Short: "Password management operations",
		Args:  cobra.ArbitraryArgs,
		RunE:  passthroughHelp,
	}
	change := &cobra.Command{
		Use:   "change",
		Short: "Rotate vault password and invalidate active sessions",
		Long: `Changes the vault password while preserving encrypted data.

Input modes:
  - Interactive TTY prompts (default)
  - --current-stdin + --new-stdin (non-interactive stdin)
  - --current-fd + --new-fd (descriptor-based)

TTY constraint:
  interactive mode requires TTY stdin unless --force-tty is set.`,
		Args: cobra.ArbitraryArgs,
		RunE: passthroughHelp,
		Example: `  kinko password change
  printf 'old\nnew\n' | kinko password change --current-stdin --new-stdin
  kinko password change --current-fd 3 --new-fd 4`,
	}
	change.Flags().Bool("current-stdin", false, "Read current password from stdin")
	change.Flags().Bool("new-stdin", false, "Read new password from stdin")
	change.Flags().Bool("force-tty", false, "Allow interactive prompts with redirected stdin")
	change.Flags().Int("current-fd", -1, "Read current password from file descriptor")
	change.Flags().Int("new-fd", -1, "Read new password from file descriptor")

	cmd.AddCommand(change)
	return cmd
}

func subcommandFromArgs(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == "help" {
			continue
		}
		return a
	}
	return ""
}
