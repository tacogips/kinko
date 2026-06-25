package kinko

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type pathPruneMissingOptions struct {
	AllProfiles bool
	Yes         bool
	JSON        bool
}

type pathPruneMissingMode string

const (
	pathPruneMissingModePreview pathPruneMissingMode = "preview"
	pathPruneMissingModePrune   pathPruneMissingMode = "prune"
)

type pathPruneCandidate struct {
	Profile  string
	RawPath  string
	Path     string
	KeyCount int
}

type pathPruneSkippedDiagnostic struct {
	Profile string
	RawPath string
	Reason  string
}

type pathPruneMissingResult struct {
	Mode        pathPruneMissingMode
	Candidates  []pathPruneCandidate
	Skipped     []pathPruneSkippedDiagnostic
	TotalScopes int
	TotalKeys   int
}

type pathExistenceClassifier interface {
	ClassifyDirectory(path string) (pathPrunePathState, string)
}

type pathPrunePathState string

const (
	pathPrunePathStateStale   pathPrunePathState = "stale"
	pathPrunePathStateKept    pathPrunePathState = "kept"
	pathPrunePathStateSkipped pathPrunePathState = "skipped"
)

type osPathExistenceClassifier struct{}

func runPathPruneMissing(opts globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	pruneOpts, err := parsePathPruneMissingArgs(args)
	if err != nil {
		return err
	}

	input := passwordVerificationInputFor(stdin, isTerminalReader)
	dek, err := readVaultPasswordDEK(opts, input, stderr, "Re-enter password: ")
	if err != nil {
		return err
	}

	classifier := osPathExistenceClassifier{}
	resultMode := pathPruneMissingModePreview
	if pruneOpts.Yes {
		resultMode = pathPruneMissingModePrune
		release, err := acquireMutationLock(opts.dataDir)
		if err != nil {
			return fmt.Errorf("vault mutation in progress: %w", err)
		}
		defer release()

		vd, err := loadVault(opts.dataDir, dek)
		if err != nil {
			return err
		}
		result, err := buildPathPruneMissingResult(vd, opts.profile, pruneOpts.AllProfiles, classifier)
		if err != nil {
			return err
		}
		result.Mode = resultMode
		pruneMissingPathScopes(vd, result.Candidates)
		if err := saveVault(opts.dataDir, dek, vd); err != nil {
			return err
		}
		return renderPathPruneMissing(stdout, result, pruneOpts.JSON)
	}

	vd, err := loadVault(opts.dataDir, dek)
	if err != nil {
		return err
	}
	result, err := buildPathPruneMissingResult(vd, opts.profile, pruneOpts.AllProfiles, classifier)
	if err != nil {
		return err
	}
	result.Mode = resultMode
	return renderPathPruneMissing(stdout, result, pruneOpts.JSON)
}

func parsePathPruneMissingArgs(args []string) (pathPruneMissingOptions, error) {
	fs := flag.NewFlagSet("path prune-missing", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := pathPruneMissingOptions{}
	fs.BoolVar(&opts.AllProfiles, "all-profiles", false, "scan every stored profile")
	fs.BoolVar(&opts.Yes, "yes", false, "prune missing path scopes")
	fs.BoolVar(&opts.Yes, "y", false, "prune missing path scopes")
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON output")
	if err := fs.Parse(args); err != nil {
		return pathPruneMissingOptions{}, err
	}
	if fs.NArg() != 0 {
		return pathPruneMissingOptions{}, errors.New("path prune-missing does not accept positional arguments")
	}
	return opts, nil
}

func readVaultPasswordDEK(opts globalOptions, input passwordVerificationInput, stderr io.Writer, prompt string) ([]byte, error) {
	var password string
	var err error
	if input.terminalSecret {
		password, err = readSecret(input.secretInput, stderr, prompt)
	} else {
		reader, ok := input.secretInput.(*bufio.Reader)
		if !ok {
			return nil, errors.New("password verification input is not buffered")
		}
		password, err = readSecretWithPromptBuffered(reader, stderr, prompt)
	}
	if err != nil {
		return nil, err
	}
	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		return nil, fmt.Errorf("cannot verify password: %w", err)
	}
	dek, err := unwrapDEKWithPassword(meta, password)
	if err != nil {
		return nil, errors.New("password verification failed")
	}
	return dek, nil
}

func buildPathPruneMissingResult(vd *vaultData, selectedProfile string, allProfiles bool, classifier pathExistenceClassifier) (pathPruneMissingResult, error) {
	result := pathPruneMissingResult{}
	if vd == nil || vd.Profiles == nil {
		return result, nil
	}

	for _, profile := range selectPathPruneProfiles(vd, selectedProfile, allProfiles) {
		scopes := vd.Profiles[profile]
		rawPaths := make([]string, 0, len(scopes))
		for rawPath := range scopes {
			rawPaths = append(rawPaths, rawPath)
		}
		sort.Strings(rawPaths)

		rawByNormalized := map[string]string{}
		collidingRaw := map[string]struct{}{}
		for _, rawPath := range rawPaths {
			normalizedPath, err := normalizeStoredScopePathForPrune(rawPath)
			if err != nil {
				continue
			}
			if existingRawPath, exists := rawByNormalized[normalizedPath]; exists {
				collidingRaw[rawPath] = struct{}{}
				collidingRaw[existingRawPath] = struct{}{}
				continue
			}
			rawByNormalized[normalizedPath] = rawPath
		}

		for _, rawPath := range rawPaths {
			if _, collides := collidingRaw[rawPath]; collides {
				result.Skipped = append(result.Skipped, pathPruneSkippedDiagnostic{
					Profile: profile,
					RawPath: rawPath,
					Reason:  "normalized path collision",
				})
				continue
			}

			state, normalizedPath, reason := classifyStoredPathScope(rawPath, classifier)
			if state == pathPrunePathStateSkipped {
				result.Skipped = append(result.Skipped, pathPruneSkippedDiagnostic{
					Profile: profile,
					RawPath: rawPath,
					Reason:  reason,
				})
				continue
			}
			if state != pathPrunePathStateStale {
				continue
			}

			candidate := pathPruneCandidate{
				Profile:  profile,
				RawPath:  rawPath,
				Path:     normalizedPath,
				KeyCount: len(scopes[rawPath]),
			}
			result.Candidates = append(result.Candidates, candidate)
			result.TotalScopes++
			result.TotalKeys += candidate.KeyCount
		}
	}

	sort.Slice(result.Candidates, func(i, j int) bool {
		return pathPruneCandidateKey(result.Candidates[i]) < pathPruneCandidateKey(result.Candidates[j])
	})
	sort.Slice(result.Skipped, func(i, j int) bool {
		if result.Skipped[i].Profile != result.Skipped[j].Profile {
			return result.Skipped[i].Profile < result.Skipped[j].Profile
		}
		if result.Skipped[i].RawPath != result.Skipped[j].RawPath {
			return result.Skipped[i].RawPath < result.Skipped[j].RawPath
		}
		return result.Skipped[i].Reason < result.Skipped[j].Reason
	})
	return result, nil
}

func selectPathPruneProfiles(vd *vaultData, selectedProfile string, allProfiles bool) []string {
	if vd == nil || vd.Profiles == nil {
		return nil
	}
	if !allProfiles {
		if _, ok := vd.Profiles[selectedProfile]; !ok {
			return nil
		}
		return []string{selectedProfile}
	}
	profiles := make([]string, 0, len(vd.Profiles))
	for profile := range vd.Profiles {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	return profiles
}

func classifyStoredPathScope(rawPath string, classifier pathExistenceClassifier) (pathPrunePathState, string, string) {
	normalizedPath, err := normalizeStoredScopePathForPrune(rawPath)
	if err != nil {
		return pathPrunePathStateSkipped, "", err.Error()
	}
	state, reason := classifier.ClassifyDirectory(normalizedPath)
	return state, normalizedPath, reason
}

func normalizeStoredScopePathForPrune(path string) (string, error) {
	p := normalizePathInput(path)
	if p == "" {
		return "", fmt.Errorf("stored path %q is invalid", path)
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("stored path is relative")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("normalize stored path %q: %w", path, err)
	}
	return filepath.Clean(abs), nil
}

func (osPathExistenceClassifier) ClassifyDirectory(path string) (pathPrunePathState, string) {
	info, statErr := os.Stat(path)
	if statErr == nil {
		if info.IsDir() {
			return pathPrunePathStateKept, ""
		}
		return pathPrunePathStateSkipped, "path exists but is not a directory"
	}
	if errors.Is(statErr, os.ErrNotExist) {
		if linkInfo, lstatErr := os.Lstat(path); lstatErr == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
			return pathPrunePathStateSkipped, "broken symlink"
		}
		return pathPrunePathStateStale, ""
	}
	if errors.Is(statErr, os.ErrPermission) {
		return pathPrunePathStateSkipped, "permission denied"
	}
	return pathPrunePathStateSkipped, fmt.Sprintf("ambiguous filesystem error: %v", statErr)
}

func pruneMissingPathScopes(vd *vaultData, candidates []pathPruneCandidate) {
	for _, candidate := range candidates {
		if vd.Profiles == nil || vd.Profiles[candidate.Profile] == nil {
			continue
		}
		delete(vd.Profiles[candidate.Profile], candidate.RawPath)
	}
}

func pathPruneCandidateKey(candidate pathPruneCandidate) string {
	return candidate.Profile + "\x00" + candidate.Path + "\x00" + candidate.RawPath
}

type pathPruneMissingJSONOutput struct {
	Mode        string                        `json:"mode"`
	Pruned      []pathPruneMissingJSONScope   `json:"pruned,omitempty"`
	Candidates  []pathPruneMissingJSONScope   `json:"candidates,omitempty"`
	Skipped     []pathPruneMissingJSONSkipped `json:"skipped,omitempty"`
	TotalScopes int                           `json:"totalScopes"`
	TotalKeys   int                           `json:"totalKeys"`
}

type pathPruneMissingJSONScope struct {
	Profile  string `json:"profile"`
	Path     string `json:"path"`
	KeyCount int    `json:"keyCount"`
}

type pathPruneMissingJSONSkipped struct {
	Profile string `json:"profile"`
	Path    string `json:"path"`
	Reason  string `json:"reason"`
}

func renderPathPruneMissing(w io.Writer, result pathPruneMissingResult, jsonOutput bool) error {
	if jsonOutput {
		return renderPathPruneMissingJSON(w, result)
	}
	return renderPathPruneMissingText(w, result)
}

func renderPathPruneMissingText(w io.Writer, result pathPruneMissingResult) error {
	switch result.Mode {
	case pathPruneMissingModePrune:
		if _, err := fmt.Fprintln(w, "path prune-missing pruned"); err != nil {
			return err
		}
	default:
		if _, err := fmt.Fprintln(w, "path prune-missing preview"); err != nil {
			return err
		}
	}

	label := "candidate"
	if result.Mode == pathPruneMissingModePrune {
		label = "pruned"
	}
	for _, candidate := range result.Candidates {
		if _, err := fmt.Fprintf(w, "%s profile=%s path=%s keys=%d\n", label, candidate.Profile, candidate.Path, candidate.KeyCount); err != nil {
			return err
		}
	}
	for _, skipped := range result.Skipped {
		if _, err := fmt.Fprintf(w, "skipped profile=%s path=%s reason=%s\n", skipped.Profile, skipped.RawPath, skipped.Reason); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "total scopes=%d keys=%d\n", result.TotalScopes, result.TotalKeys); err != nil {
		return err
	}
	return nil
}

func renderPathPruneMissingJSON(w io.Writer, result pathPruneMissingResult) error {
	out := pathPruneMissingJSONOutput{
		Mode:        string(result.Mode),
		TotalScopes: result.TotalScopes,
		TotalKeys:   result.TotalKeys,
	}
	scopes := make([]pathPruneMissingJSONScope, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		scopes = append(scopes, pathPruneMissingJSONScope{
			Profile:  candidate.Profile,
			Path:     candidate.Path,
			KeyCount: candidate.KeyCount,
		})
	}
	if result.Mode == pathPruneMissingModePrune {
		out.Pruned = scopes
	} else {
		out.Candidates = scopes
	}
	for _, skipped := range result.Skipped {
		out.Skipped = append(out.Skipped, pathPruneMissingJSONSkipped{
			Profile: skipped.Profile,
			Path:    skipped.RawPath,
			Reason:  skipped.Reason,
		})
	}
	encoder := json.NewEncoder(w)
	return encoder.Encode(out)
}
