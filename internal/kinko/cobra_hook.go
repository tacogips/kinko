package kinko

import "github.com/spf13/cobra"

func newHookCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:   cmdHook,
		Short: "Emit shell code for directory environment lifecycle hooks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	enter := &cobra.Command{
		Use:   hookEnter + " [shell]",
		Short: "Export resolved secrets and their cleanup tracking state",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			opts := hookOptions{shell: hookShellArg(args), enter: true}
			if _, err := normalizeHookShell(opts.shell); err != nil {
				return newCLIError(exitCodePolicyFailed, err.Error(), err)
			}
			if err := preflight(); err != nil {
				return err
			}
			return runHookEnterWithOptions(ctx.opts, opts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}

	leave := &cobra.Command{
		Use:   hookLeave + " [shell]",
		Short: "Unset secrets tracked by the active directory hook",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runHookLeaveWithOptions(hookOptions{shell: hookShellArg(args)}, ctx.stdout)
		},
	}

	root.AddCommand(enter, leave)
	return root
}

func hookShellArg(args []string) string {
	if len(args) == 0 {
		return shellBash
	}
	return args[0]
}
