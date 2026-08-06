package kinko

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

const (
	envKinkoBWSConfigFile     = "KINKO_BWS_CONFIG_FILE"
	envKinkoBWSProfile        = "KINKO_BWS_PROFILE"
	envKinkoBWSServerURL      = "KINKO_BWS_SERVER_URL"
	envKinkoBWSOrganizationID = "KINKO_BWS_ORGANIZATION_ID"

	configKeyBWSConfigFile     = "sync.bws.config_file"
	configKeyBWSProfile        = "sync.bws.profile"
	configKeyBWSServerURL      = "sync.bws.server_url"
	configKeyBWSOrganizationID = "sync.bws.organization_id"

	defaultBWSProfile = "default"
)

type bwsEndpointSet struct {
	BaseURL     *url.URL
	APIURL      *url.URL
	IdentityURL *url.URL
}

type bwsRuntimeConfig struct {
	ConfigFile       string
	Profile          string
	OrganizationID   string
	ProjectID        string
	Endpoints        bwsEndpointSet
	ProviderIdentity string
}

type bwsConfigOptions struct {
	ConfigFile        string
	Profile           string
	ServerURL         string
	OrganizationID    string
	ProjectID         string
	AllowTestLoopback bool
}

func resolveBWSRuntimeConfig(options bwsConfigOptions, encrypted map[string]string, getenv func(string) string) (bwsRuntimeConfig, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	configFile := firstNonEmpty(options.ConfigFile, getenv(envKinkoBWSConfigFile), encrypted[configKeyBWSConfigFile])
	if configFile == "" {
		candidate := filepath.Join(getenv("HOME"), ".bws", "config")
		if getenv("HOME") != "" {
			if _, err := os.Lstat(candidate); err == nil {
				configFile = candidate
			}
		}
	}
	profile := firstNonEmpty(options.Profile, getenv(envKinkoBWSProfile), encrypted[configKeyBWSProfile], defaultBWSProfile)
	serverURL := firstNonEmpty(options.ServerURL, getenv(envKinkoBWSServerURL), encrypted[configKeyBWSServerURL])
	organizationID := firstNonEmpty(options.OrganizationID, getenv(envKinkoBWSOrganizationID), encrypted[configKeyBWSOrganizationID])
	projectID := firstNonEmpty(options.ProjectID, getenv(envKinkoBWSProjectID), encrypted[configKeyBWSProjectID])

	values := map[string]string{}
	if configFile != "" {
		parsed, err := readSecureBWSConfig(configFile, profile)
		if err != nil {
			return bwsRuntimeConfig{}, err
		}
		values = parsed
	}
	if organizationID == "" {
		organizationID = values["organization_id"]
	}
	if projectID == "" {
		projectID = values["project_id"]
	}

	endpoints, err := configuredBWSEndpoints(serverURL, values)
	if err != nil {
		return bwsRuntimeConfig{}, err
	}
	endpoints, err = canonicalizeBWSEndpoints(endpoints, options.AllowTestLoopback)
	if err != nil {
		return bwsRuntimeConfig{}, err
	}
	return bwsRuntimeConfig{
		ConfigFile: configFile, Profile: profile, OrganizationID: organizationID, ProjectID: projectID,
		Endpoints: endpoints, ProviderIdentity: deriveBWSProviderIdentity(endpoints, organizationID, projectID),
	}, nil
}

func configuredBWSEndpoints(serverURL string, values map[string]string) (bwsEndpointSet, error) {
	if serverURL != "" {
		base, err := url.Parse(serverURL)
		if err != nil {
			return bwsEndpointSet{}, fmt.Errorf("parse BWS server URL: %w", err)
		}
		return bwsEndpointSet{BaseURL: base, APIURL: endpointWithPath(base, "/api"), IdentityURL: endpointWithPath(base, "/identity")}, nil
	}
	baseValue := firstNonEmpty(values["server_base"], "https://vault.bitwarden.com")
	apiValue := firstNonEmpty(values["server_api"], "https://api.bitwarden.com")
	identityValue := firstNonEmpty(values["server_identity"], "https://identity.bitwarden.com")
	base, baseErr := url.Parse(baseValue)
	api, apiErr := url.Parse(apiValue)
	identity, identityErr := url.Parse(identityValue)
	if err := errors.Join(baseErr, apiErr, identityErr); err != nil {
		return bwsEndpointSet{}, fmt.Errorf("parse BWS endpoints: %w", err)
	}
	return bwsEndpointSet{BaseURL: base, APIURL: api, IdentityURL: identity}, nil
}

func endpointWithPath(base *url.URL, path string) *url.URL {
	copyURL := *base
	copyURL.Path = strings.TrimRight(copyURL.Path, "/") + path
	copyURL.RawPath, copyURL.RawQuery, copyURL.Fragment = "", "", ""
	return &copyURL
}

func canonicalizeBWSEndpoints(endpoints bwsEndpointSet, allowTestLoopback bool) (bwsEndpointSet, error) {
	canonical := bwsEndpointSet{}
	for _, item := range []struct {
		name   string
		input  *url.URL
		output **url.URL
	}{{"base", endpoints.BaseURL, &canonical.BaseURL}, {"api", endpoints.APIURL, &canonical.APIURL}, {"identity", endpoints.IdentityURL, &canonical.IdentityURL}} {
		value, err := canonicalizeBWSEndpoint(item.name, item.input, allowTestLoopback)
		if err != nil {
			return bwsEndpointSet{}, err
		}
		*item.output = value
	}
	return canonical, nil
}

func canonicalizeBWSEndpoint(name string, input *url.URL, allowTestLoopback bool) (*url.URL, error) {
	if input == nil || !input.IsAbs() || input.Hostname() == "" {
		return nil, fmt.Errorf("BWS %s endpoint must be an absolute URL", name)
	}
	scheme := strings.ToLower(input.Scheme)
	hostname := strings.ToLower(input.Hostname())
	loopback := allowTestLoopback && (hostname == "localhost" || net.ParseIP(hostname) != nil && net.ParseIP(hostname).IsLoopback())
	if scheme != "https" && !(loopback && scheme == "http") {
		return nil, fmt.Errorf("BWS %s endpoint must use HTTPS", name)
	}
	if input.User != nil || input.RawQuery != "" || input.Fragment != "" {
		return nil, fmt.Errorf("BWS %s endpoint must not contain userinfo, query, or fragment", name)
	}
	port := input.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	path := strings.TrimRight(input.EscapedPath(), "/")
	return &url.URL{Scheme: scheme, Host: host, Path: path}, nil
}

func deriveBWSProviderIdentity(endpoints bwsEndpointSet, organizationID, projectID string) string {
	parts := []string{"kinko.bws.provider.v1", endpointString(endpoints.BaseURL), endpointString(endpoints.APIURL), endpointString(endpoints.IdentityURL), organizationID, projectID}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func endpointString(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func readSecureBWSConfig(path, selectedProfile string) (map[string]string, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect BWS config: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("BWS config must not be a symlink")
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open BWS config: %w", err)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened BWS config: %w", err)
	}
	if !os.SameFile(before, after) {
		return nil, errors.New("BWS config changed while being opened")
	}
	if !after.Mode().IsRegular() || after.Mode().Perm() != 0o600 {
		return nil, errors.New("BWS config must be a regular file with mode 0600")
	}
	if err := requireCurrentUserOwner(after); err != nil {
		return nil, err
	}
	return parseBWSConfig(file, selectedProfile)
}

func requireCurrentUserOwner(info os.FileInfo) error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve current user for BWS config: %w", err)
	}
	wanted, err := strconv.ParseUint(current.Uid, 10, 64)
	if err != nil {
		return fmt.Errorf("parse current user id for BWS config: %w", err)
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	uid := value.FieldByName("Uid")
	if !uid.IsValid() || uid.Uint() != wanted {
		return errors.New("BWS config must be owned by the current user")
	}
	return nil
}

func parseBWSConfig(file *os.File, selectedProfile string) (map[string]string, error) {
	values := map[string]string{}
	seen := map[string]bool{}
	profile := defaultBWSProfile
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			profile = strings.Trim(strings.TrimSpace(line[1:len(line)-1]), `"'`)
			profile = strings.TrimPrefix(profile, "profiles.")
			continue
		}
		key, rawValue, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("parse BWS config line %d: expected key=value", lineNumber)
		}
		key = strings.TrimSpace(key)
		if profile != selectedProfile {
			continue
		}
		if seen[key] {
			return nil, fmt.Errorf("parse BWS config line %d: duplicate key %q in profile %q", lineNumber, key, selectedProfile)
		}
		seen[key] = true
		value := strings.TrimSpace(rawValue)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read BWS config: %w", err)
	}
	return values, nil
}
