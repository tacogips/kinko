package kinko

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

type moveDirection string

const (
	moveDirectionLocalToShared moveDirection = moveLocalToShared
	moveDirectionSharedToLocal moveDirection = moveSharedToLocal
)

type moveSecretOptions struct {
	Direction moveDirection
	Key       string
	Overwrite bool
	Yes       bool
}

type moveSecretResult struct {
	Direction moveDirection
	Key       string
	Profile   string
	Path      string
}

func runMove(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	moveOpts, err := parseMoveArgs(args)
	if err != nil {
		return err
	}

	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		return fmt.Errorf("vault mutation in progress: %w", err)
	}
	defer release()

	dek, err := loadUnlockedDEK(opts.dataDir)
	if err != nil {
		return err
	}
	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return err
	}

	source, destination := describeMoveScopes(opts, moveOpts.Direction)
	result, value, err := prepareMoveSecret(vd, opts, moveOpts)
	if err != nil {
		return err
	}
	if !moveOpts.Yes {
		ok, err := confirmMoveSecret(stdin, stderr, result, source, destination)
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintln(stdout, "aborted")
			return nil
		}
	}

	destinationScope := ensureMoveDestinationScope(vd, opts, moveOpts.Direction)
	destinationScope[moveOpts.Key] = value
	sourceScope := moveSourceScope(vd, opts, moveOpts.Direction)
	delete(sourceScope, moveOpts.Key)

	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		return err
	}
	return renderMoveSecretSuccess(stdout, result, source, destination)
}

func parseMoveArgs(args []string) (moveSecretOptions, error) {
	opts := moveSecretOptions{}
	positionals := []string{}
	for _, arg := range args {
		switch arg {
		case "--overwrite":
			opts.Overwrite = true
		case "--yes", "-y":
			opts.Yes = true
		default:
			switch {
			case strings.HasPrefix(arg, "--overwrite="):
				return moveSecretOptions{}, errors.New("move --overwrite does not accept a value")
			case strings.HasPrefix(arg, "--yes="):
				return moveSecretOptions{}, errors.New("move --yes does not accept a value")
			case strings.HasPrefix(arg, "-"):
				return moveSecretOptions{}, fmt.Errorf("move: unknown flag %q", arg)
			default:
				positionals = append(positionals, arg)
			}
		}
	}
	if len(positionals) == 0 {
		return moveSecretOptions{}, errors.New("move requires a direction")
	}
	switch positionals[0] {
	case string(moveDirectionLocalToShared):
		opts.Direction = moveDirectionLocalToShared
	case string(moveDirectionSharedToLocal):
		opts.Direction = moveDirectionSharedToLocal
	default:
		return moveSecretOptions{}, fmt.Errorf("move: unknown direction %q", positionals[0])
	}
	if len(positionals) == 1 {
		return moveSecretOptions{}, errors.New("move requires a key")
	}
	if len(positionals) > 2 {
		return moveSecretOptions{}, errors.New("move requires exactly one key")
	}
	opts.Key = positionals[1]
	if err := validateEnvKey(opts.Key); err != nil {
		return moveSecretOptions{}, err
	}
	return opts, nil
}

func prepareMoveSecret(vd *vaultData, opts globalOptions, moveOpts moveSecretOptions) (moveSecretResult, string, error) {
	sourceScope := moveSourceScope(vd, opts, moveOpts.Direction)
	value, ok := sourceScope[moveOpts.Key]
	if !ok {
		return moveSecretResult{}, "", errors.New("secret not found in source scope")
	}
	if destinationScope := moveExistingDestinationScope(vd, opts, moveOpts.Direction); destinationScope != nil {
		if _, ok := destinationScope[moveOpts.Key]; ok && !moveOpts.Overwrite {
			return moveSecretResult{}, "", errors.New("destination secret already exists (use --overwrite)")
		}
	}
	return moveSecretResult{
		Direction: moveOpts.Direction,
		Key:       moveOpts.Key,
		Profile:   opts.profile,
		Path:      opts.path,
	}, value, nil
}

func moveSourceScope(vd *vaultData, opts globalOptions, direction moveDirection) map[string]string {
	switch direction {
	case moveDirectionLocalToShared:
		if vd.Profiles == nil || vd.Profiles[opts.profile] == nil {
			return nil
		}
		return vd.Profiles[opts.profile][opts.path]
	case moveDirectionSharedToLocal:
		return vd.Shared
	default:
		return nil
	}
}

func moveExistingDestinationScope(vd *vaultData, opts globalOptions, direction moveDirection) map[string]string {
	switch direction {
	case moveDirectionLocalToShared:
		return vd.Shared
	case moveDirectionSharedToLocal:
		if vd.Profiles == nil || vd.Profiles[opts.profile] == nil {
			return nil
		}
		return vd.Profiles[opts.profile][opts.path]
	default:
		return nil
	}
}

func ensureMoveDestinationScope(vd *vaultData, opts globalOptions, direction moveDirection) map[string]string {
	switch direction {
	case moveDirectionLocalToShared:
		if vd.Shared == nil {
			vd.Shared = map[string]string{}
		}
		return vd.Shared
	case moveDirectionSharedToLocal:
		if vd.Profiles == nil {
			vd.Profiles = map[string]map[string]map[string]string{}
		}
		if vd.Profiles[opts.profile] == nil {
			vd.Profiles[opts.profile] = map[string]map[string]string{}
		}
		if vd.Profiles[opts.profile][opts.path] == nil {
			vd.Profiles[opts.profile][opts.path] = map[string]string{}
		}
		return vd.Profiles[opts.profile][opts.path]
	default:
		return nil
	}
}

func describeMoveScopes(opts globalOptions, direction moveDirection) (source string, destination string) {
	local := fmt.Sprintf("profile=%q path=%q", opts.profile, opts.path)
	switch direction {
	case moveDirectionLocalToShared:
		return local, "shared scope"
	case moveDirectionSharedToLocal:
		return "shared scope", local
	default:
		return "unknown scope", "unknown scope"
	}
}

func confirmMoveSecret(stdin io.Reader, stderr io.Writer, result moveSecretResult, source, destination string) (bool, error) {
	return confirmPrompt(stdin, stderr, fmt.Sprintf("Move key %q from %s to %s? [y/N]: ", result.Key, source, destination))
}

func renderMoveSecretSuccess(stdout io.Writer, result moveSecretResult, source, destination string) error {
	_, err := fmt.Fprintf(stdout, "%s moved from %s to %s\n", result.Key, source, destination)
	return err
}
