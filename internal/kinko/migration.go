package kinko

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
)

type migrationOptions struct {
	yes     bool
	jsonOut bool
}

type migrationStep struct {
	name    string
	pending func(meta *vaultMeta) bool
	apply   func(meta *vaultMeta) error
}

type migrationStepResult struct {
	Name    string `json:"name"`
	Pending bool   `json:"pending"`
	Applied bool   `json:"applied"`
}

type migrationResult struct {
	Mode  string                `json:"mode"`
	Steps []migrationStepResult `json:"steps"`
}

func migrationSteps() []migrationStep {
	return []migrationStep{
		{
			name: "assign-machine-id",
			pending: func(meta *vaultMeta) bool {
				return meta.MachineID == ""
			},
			apply: func(meta *vaultMeta) error {
				machineID, err := newMachineID()
				if err != nil {
					return err
				}
				meta.MachineID = machineID
				meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				return nil
			},
		},
	}
}

func runMigrationWithOptions(opts globalOptions, migOpts migrationOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		return migrationMetaLoadError("Failed to load vault metadata.", err)
	}
	if meta.MachineID != "" && !isValidMachineID(meta.MachineID) {
		return newCLIError(exitCodeMetadataInvalid, "Vault machine_id is invalid.", errMetadataInvalid)
	}

	steps := migrationSteps()
	result := buildMigrationResult(steps, meta, migOpts.yes)
	if !hasPendingMigration(result) {
		return writeMigrationResult(stdout, result, migOpts.jsonOut)
	}
	if !migOpts.yes {
		return writeMigrationResult(stdout, result, migOpts.jsonOut)
	}

	input := passwordVerificationInputFor(stdin, isTerminalReader)
	password, err := readMigrationPassword(input, stderr)
	if err != nil {
		return err
	}

	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return newCLIError(exitCodeLockConflict, "Migration could not acquire mutation lock.", err)
	}
	defer release()

	meta, err = loadMeta(opts.dataDir)
	if err != nil {
		return migrationMetaLoadError("Failed to reload vault metadata under mutation lock.", err)
	}
	if meta.MachineID != "" && !isValidMachineID(meta.MachineID) {
		return newCLIError(exitCodeMetadataInvalid, "Vault machine_id is invalid.", errMetadataInvalid)
	}
	if err := verifyMigrationPassword(meta, password); err != nil {
		return err
	}
	result = buildMigrationResult(steps, meta, true)
	for index, step := range steps {
		if !step.pending(meta) {
			continue
		}
		if err := step.apply(meta); err != nil {
			return newCLIError(exitCodeIOFailed, fmt.Sprintf("Migration %q failed.", step.name), err)
		}
		result.Steps[index].Applied = true
	}
	if err := saveMetaAtomically(opts.dataDir, meta); err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to persist migrated vault metadata atomically.", err)
	}
	return writeMigrationResult(stdout, result, migOpts.jsonOut)
}

func buildMigrationResult(steps []migrationStep, meta *vaultMeta, applying bool) migrationResult {
	mode := "preview"
	if applying {
		mode = "apply"
	}
	result := migrationResult{Mode: mode, Steps: make([]migrationStepResult, 0, len(steps))}
	for _, step := range steps {
		result.Steps = append(result.Steps, migrationStepResult{Name: step.name, Pending: step.pending(meta)})
	}
	return result
}

func hasPendingMigration(result migrationResult) bool {
	for _, step := range result.Steps {
		if step.Pending {
			return true
		}
	}
	return false
}

func renderMigrationResult(w io.Writer, result migrationResult, jsonOut bool) error {
	if jsonOut {
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result); err != nil {
			return newCLIError(exitCodeIOFailed, "Failed to write migration JSON output.", err)
		}
		return nil
	}
	if !hasPendingMigration(result) {
		_, err := fmt.Fprintln(w, "no pending migrations")
		return err
	}
	if result.Mode == "preview" {
		if _, err := fmt.Fprintln(w, "migration preview"); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(w, "migrations applied"); err != nil {
		return err
	}
	for _, step := range result.Steps {
		if step.Applied {
			if _, err := fmt.Fprintf(w, "applied %s\n", step.Name); err != nil {
				return err
			}
		} else if step.Pending {
			if _, err := fmt.Fprintf(w, "pending %s\n", step.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeMigrationResult(w io.Writer, result migrationResult, jsonOut bool) error {
	if err := renderMigrationResult(w, result, jsonOut); err != nil {
		return newCLIError(exitCodeIOFailed, "Failed to write migration output.", err)
	}
	return nil
}

func readMigrationPassword(input passwordVerificationInput, stderr io.Writer) (string, error) {
	var (
		password string
		err      error
	)
	if input.terminalSecret {
		password, err = readSecret(input.secretInput, stderr, "Re-enter password: ")
	} else {
		reader, ok := input.secretInput.(*bufio.Reader)
		if !ok {
			return "", newCLIError(exitCodeIOFailed, "Failed to read vault password.", errors.New("password input is not buffered"))
		}
		password, err = readSecretWithPromptBuffered(reader, stderr, "Re-enter password: ")
	}
	if err != nil {
		return "", newCLIError(exitCodeIOFailed, "Failed to read vault password.", err)
	}
	return password, nil
}

func verifyMigrationPassword(meta *vaultMeta, password string) error {
	if _, err := unwrapDEKWithPassword(meta, password); err != nil {
		if errors.Is(err, errDecryptFailed) {
			return newCLIError(exitCodeAuthFailed, "Vault password verification failed.", err)
		}
		return newCLIError(exitCodeMetadataInvalid, "Vault metadata is invalid.", err)
	}
	return nil
}

func migrationMetaLoadError(message string, err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return newCLIError(exitCodeIOFailed, message, err)
	}
	return newCLIError(exitCodeMetadataInvalid, message, err)
}

func newMigrationCommand(ctx *runtimeContext, preflight func() error) *cobra.Command {
	migOpts := migrationOptions{}
	cmd := &cobra.Command{
		Use:   cmdMigration,
		Short: "Preview or apply vault metadata migrations",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return newCLIError(exitCodePolicyFailed, "migration does not accept positional arguments", nil)
			}
			return nil
		},
		RunE: func(*cobra.Command, []string) error {
			if err := preflight(); err != nil {
				return err
			}
			return runMigrationWithOptions(ctx.opts, migOpts, ctx.stdin, ctx.stdout, ctx.stderr)
		},
	}
	cmd.Flags().BoolVarP(&migOpts.yes, "yes", "y", false, "apply pending migrations")
	cmd.Flags().BoolVar(&migOpts.jsonOut, "json", false, "emit JSON output")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newCLIError(exitCodePolicyFailed, "Invalid migration arguments.", err)
	})
	return cmd
}
