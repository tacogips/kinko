package kinko

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type copyDirection string

const (
	copyDirectionLocalToLocal  copyDirection = copyLocalToLocal
	copyDirectionLocalToShared copyDirection = moveLocalToShared
	copyDirectionSharedToLocal copyDirection = moveSharedToLocal
)

type copySecretOptions struct {
	Direction copyDirection
	Key       string
	FromPath  string
	Overwrite bool
}

type copySecretResult struct {
	Direction   copyDirection
	Keys        []string
	Profile     string
	Source      string
	Destination string
}

func runCopy(opts globalOptions, args []string, stdout io.Writer) error {
	copyOpts, err := parseCopyArgs(args)
	if err != nil {
		return err
	}
	return runCopyWithOptions(opts, copyOpts, stdout)
}

func runCopyWithOptions(opts globalOptions, copyOpts copySecretOptions, stdout io.Writer) error {
	copyOpts, err := validateCopyOptions(copyOpts)
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

	result, err := copySecrets(vd, opts, copyOpts)
	if err != nil {
		return err
	}
	if err := saveVault(opts.dataDir, dek, vd); err != nil {
		return err
	}
	return renderCopySecretSuccess(stdout, result)
}

func parseCopyArgs(args []string) (copySecretOptions, error) {
	opts := copySecretOptions{}
	positionals := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--overwrite":
			opts.Overwrite = true
		case arg == "--from-path":
			if i+1 >= len(args) {
				return copySecretOptions{}, errors.New("copy --from-path requires a value")
			}
			opts.FromPath = args[i+1]
			i++
		case strings.HasPrefix(arg, "--from-path="):
			opts.FromPath = strings.TrimPrefix(arg, "--from-path=")
		case strings.HasPrefix(arg, "--overwrite="):
			return copySecretOptions{}, errors.New("copy --overwrite does not accept a value")
		case strings.HasPrefix(arg, "-"):
			return copySecretOptions{}, fmt.Errorf("copy: unknown flag %q", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 {
		return copySecretOptions{}, errors.New("copy requires a direction")
	}
	switch positionals[0] {
	case string(copyDirectionLocalToLocal):
		opts.Direction = copyDirectionLocalToLocal
	case string(copyDirectionLocalToShared):
		opts.Direction = copyDirectionLocalToShared
	case string(copyDirectionSharedToLocal):
		opts.Direction = copyDirectionSharedToLocal
	default:
		return copySecretOptions{}, fmt.Errorf("copy: unknown direction %q", positionals[0])
	}
	if len(positionals) == 1 {
		return copySecretOptions{}, errors.New("copy requires a key or *")
	}
	if len(positionals) > 2 {
		return copySecretOptions{}, errors.New("copy requires exactly one key or *")
	}
	opts.Key = positionals[1]
	if opts.Key != "*" {
		if err := validateEnvKey(opts.Key); err != nil {
			return copySecretOptions{}, err
		}
	}
	return validateCopyOptions(opts)
}

func validateCopyOptions(opts copySecretOptions) (copySecretOptions, error) {
	switch opts.Direction {
	case copyDirectionLocalToLocal, copyDirectionLocalToShared, copyDirectionSharedToLocal:
	case "":
		return copySecretOptions{}, errors.New("copy requires a direction")
	default:
		return copySecretOptions{}, fmt.Errorf("copy: unknown direction %q", opts.Direction)
	}
	if opts.Key == "" {
		return copySecretOptions{}, errors.New("copy requires a key or *")
	}
	if opts.Key != "*" {
		if err := validateEnvKey(opts.Key); err != nil {
			return copySecretOptions{}, err
		}
	}
	if opts.Direction == copyDirectionLocalToLocal {
		fromPath, err := normalizeCopyPath(opts.FromPath)
		if err != nil {
			return copySecretOptions{}, err
		}
		opts.FromPath = fromPath
	} else if opts.FromPath != "" {
		return copySecretOptions{}, errors.New("copy --from-path is only valid with local-to-local")
	}
	return opts, nil
}

func normalizeCopyPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("copy local-to-local requires --from-path")
	}
	absPath, err := filepath.Abs(normalizePathInput(path))
	if err != nil {
		return "", fmt.Errorf("resolve --from-path: %w", err)
	}
	return filepath.Clean(absPath), nil
}

func copySecrets(vd *vaultData, opts globalOptions, copyOpts copySecretOptions) (copySecretResult, error) {
	source, destination := describeCopyScopes(opts, copyOpts)
	if copyOpts.Direction == copyDirectionLocalToLocal && copyOpts.FromPath == opts.path {
		return copySecretResult{}, errors.New("copy local-to-local source and destination paths must differ")
	}

	sourceScope := copySourceScope(vd, opts, copyOpts)
	values, keys, err := selectCopyValues(sourceScope, copyOpts.Key)
	if err != nil {
		return copySecretResult{}, err
	}

	if destinationScope := copyExistingDestinationScope(vd, opts, copyOpts.Direction); destinationScope != nil && !copyOpts.Overwrite {
		conflicts := []string{}
		for _, key := range keys {
			if _, ok := destinationScope[key]; ok {
				conflicts = append(conflicts, key)
			}
		}
		if len(conflicts) > 0 {
			return copySecretResult{}, copyConflictError(conflicts)
		}
	}

	destinationScope := ensureCopyDestinationScope(vd, opts, copyOpts.Direction)
	for _, key := range keys {
		destinationScope[key] = values[key]
	}

	return copySecretResult{
		Direction:   copyOpts.Direction,
		Keys:        keys,
		Profile:     opts.profile,
		Source:      source,
		Destination: destination,
	}, nil
}

func selectCopyValues(sourceScope map[string]string, key string) (map[string]string, []string, error) {
	if key == "*" {
		if len(sourceScope) == 0 {
			return nil, nil, errors.New("no secrets found in source scope")
		}
		keys := sortedKeys(sourceScope)
		values := make(map[string]string, len(keys))
		for _, key := range keys {
			values[key] = sourceScope[key]
		}
		return values, keys, nil
	}
	value, ok := sourceScope[key]
	if !ok {
		return nil, nil, errors.New("secret not found in source scope")
	}
	return map[string]string{key: value}, []string{key}, nil
}

func copyConflictError(conflicts []string) error {
	if len(conflicts) == 1 {
		return errors.New("destination secret already exists (use --overwrite)")
	}
	return fmt.Errorf("destination secrets already exist: %s (use --overwrite)", strings.Join(conflicts, ","))
}

func copySourceScope(vd *vaultData, opts globalOptions, copyOpts copySecretOptions) map[string]string {
	switch copyOpts.Direction {
	case copyDirectionLocalToLocal:
		if vd.Profiles == nil || vd.Profiles[opts.profile] == nil {
			return nil
		}
		return vd.Profiles[opts.profile][copyOpts.FromPath]
	case copyDirectionLocalToShared:
		if vd.Profiles == nil || vd.Profiles[opts.profile] == nil {
			return nil
		}
		return vd.Profiles[opts.profile][opts.path]
	case copyDirectionSharedToLocal:
		return vd.Shared
	default:
		return nil
	}
}

func copyExistingDestinationScope(vd *vaultData, opts globalOptions, direction copyDirection) map[string]string {
	switch direction {
	case copyDirectionLocalToLocal, copyDirectionSharedToLocal:
		if vd.Profiles == nil || vd.Profiles[opts.profile] == nil {
			return nil
		}
		return vd.Profiles[opts.profile][opts.path]
	case copyDirectionLocalToShared:
		return vd.Shared
	default:
		return nil
	}
}

func ensureCopyDestinationScope(vd *vaultData, opts globalOptions, direction copyDirection) map[string]string {
	switch direction {
	case copyDirectionLocalToLocal, copyDirectionSharedToLocal:
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
	case copyDirectionLocalToShared:
		if vd.Shared == nil {
			vd.Shared = map[string]string{}
		}
		return vd.Shared
	default:
		return nil
	}
}

func describeCopyScopes(opts globalOptions, copyOpts copySecretOptions) (source string, destination string) {
	localDestination := fmt.Sprintf("profile=%q path=%q", opts.profile, opts.path)
	switch copyOpts.Direction {
	case copyDirectionLocalToLocal:
		return fmt.Sprintf("profile=%q path=%q", opts.profile, copyOpts.FromPath), localDestination
	case copyDirectionLocalToShared:
		return localDestination, "shared scope"
	case copyDirectionSharedToLocal:
		return "shared scope", localDestination
	default:
		return "unknown scope", "unknown scope"
	}
}

func renderCopySecretSuccess(stdout io.Writer, result copySecretResult) error {
	_, err := fmt.Fprintf(stdout, "%s copied from %s to %s\n", strings.Join(result.Keys, ","), result.Source, result.Destination)
	return err
}
