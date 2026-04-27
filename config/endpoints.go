package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// EndpointFile is a project-local file describing endpoints to benchmark.
type EndpointFile struct {
	Version     int          `yaml:"version" json:"version"`
	Collections []Collection `yaml:"collections,omitempty" json:"collections,omitempty"`
	Endpoints   []Endpoint   `yaml:"endpoints,omitempty" json:"endpoints,omitempty"`
}

// Collection groups endpoints. Collections exist only in the config file —
// they are not persisted to the cloud database.
type Collection struct {
	Name        string     `yaml:"name" json:"name"`
	Description string     `yaml:"description,omitempty" json:"description,omitempty"`
	Endpoints   []Endpoint `yaml:"endpoints" json:"endpoints"`
}

// Endpoint describes a single benchmark target.
type Endpoint struct {
	Name     string            `yaml:"name" json:"name"`
	Version  *int              `yaml:"version,omitempty" json:"version,omitempty"`
	Method   string            `yaml:"method" json:"method"`
	URL      string            `yaml:"url" json:"url"`
	Headers  map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Params   map[string]string `yaml:"params,omitempty" json:"params,omitempty"`
	Body     interface{}       `yaml:"body,omitempty" json:"body,omitempty"`
	Defaults *EndpointDefaults `yaml:"defaults,omitempty" json:"defaults,omitempty"`

	// Collection is set during flattening; never serialized.
	Collection string `yaml:"-" json:"-"`
}

// EndpointDefaults are the default benchmark settings for an endpoint.
// CLI flags override these at run time.
type EndpointDefaults struct {
	Concurrency    int    `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
	DurationSec    int    `yaml:"duration_seconds,omitempty" json:"duration_seconds,omitempty"`
	RateLimit      int    `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
	ThrottleTimeMs int    `yaml:"throttle_time_ms,omitempty" json:"throttle_time_ms,omitempty"`
	CacheMode      string `yaml:"cache_mode,omitempty" json:"cache_mode,omitempty"`
}

// EndpointFileFormat is yaml or json.
type EndpointFileFormat string

const (
	FormatYAML EndpointFileFormat = "yaml"
	FormatJSON EndpointFileFormat = "json"
)

// CandidateFilenames are searched in order during discovery.
var CandidateFilenames = []string{
	"benchmarkr.yaml",
	"benchmarkr.yml",
	"benchmarkr.json",
	".benchmarkr.yaml",
	".benchmarkr.yml",
	".benchmarkr.json",
}

// FindEndpointFile walks up from startDir looking for an endpoints config.
// Returns the path of the first match, or an empty string if none is found.
func FindEndpointFile(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		for _, name := range CandidateFilenames {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// FormatFromPath infers yaml or json from a file extension.
func FormatFromPath(path string) (EndpointFileFormat, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return FormatYAML, nil
	case ".json":
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("unsupported extension for %s (expected .yaml, .yml, or .json)", path)
	}
}

// LoadEndpointFile reads and parses an endpoints config file.
// It does NOT expand env vars or load .env — call ResolveEndpointFile for that.
func LoadEndpointFile(path string) (*EndpointFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	format, err := FormatFromPath(path)
	if err != nil {
		return nil, err
	}

	var file EndpointFile
	switch format {
	case FormatYAML:
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
	case FormatJSON:
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
	}

	if err := validateEndpointFile(&file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &file, nil
}

// LoadDotenvForFile loads a sibling .env file if present. godotenv.Load does
// not override variables already set in the environment.
func LoadDotenvForFile(path string) {
	envPath := filepath.Join(filepath.Dir(path), ".env")
	if _, err := os.Stat(envPath); err == nil {
		_ = godotenv.Load(envPath)
	}
}

// ResolveEndpointFile loads a file, auto-loads a sibling .env, and expands
// ${VAR} / ${VAR:-default} placeholders across every endpoint. Use this only
// when you need every endpoint resolved at once (e.g. bulk validation). For
// running or showing a single endpoint, prefer LoadEndpointFile +
// LoadDotenvForFile + ExpandEndpoint to avoid failing on unrelated vars.
func ResolveEndpointFile(path string) (*EndpointFile, error) {
	LoadDotenvForFile(path)
	file, err := LoadEndpointFile(path)
	if err != nil {
		return nil, err
	}
	if err := expandFile(file); err != nil {
		return nil, err
	}
	return file, nil
}

// ExpandEndpoint expands env vars in every string field of e.
func ExpandEndpoint(e *Endpoint) error {
	return expandEndpoint(e)
}

// SaveEndpointFile writes an endpoints config file in the format inferred from path.
func SaveEndpointFile(path string, file *EndpointFile) error {
	format, err := FormatFromPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	var data []byte
	switch format {
	case FormatYAML:
		data, err = yaml.Marshal(file)
		if err != nil {
			return fmt.Errorf("encoding yaml: %w", err)
		}
	case FormatJSON:
		data, err = json.MarshalIndent(file, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding json: %w", err)
		}
		data = append(data, '\n')
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// FlattenEndpoints returns every endpoint in the file with its Collection
// field populated. Top-level endpoints have Collection == "".
func FlattenEndpoints(file *EndpointFile) []Endpoint {
	var out []Endpoint
	for _, c := range file.Collections {
		for _, e := range c.Endpoints {
			e.Collection = c.Name
			out = append(out, e)
		}
	}
	for _, e := range file.Endpoints {
		out = append(out, e)
	}
	return out
}

// FindEndpointByName looks up an endpoint by name across all collections and
// the top-level list. Names must be unique within the file.
func FindEndpointByName(file *EndpointFile, name string) (*Endpoint, error) {
	all := FlattenEndpoints(file)
	var matches []Endpoint
	for _, e := range all {
		if e.Name == name {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("endpoint %q not found", name)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("endpoint %q is defined multiple times", name)
	}
}

// AddEndpoint appends an endpoint to the file. If collection is non-empty,
// the endpoint is added to that collection (creating it if needed). Otherwise
// it goes into the top-level Endpoints list. Returns an error if the name is
// already taken anywhere in the file.
func AddEndpoint(file *EndpointFile, collection string, endpoint Endpoint) error {
	if endpoint.Name == "" {
		return fmt.Errorf("endpoint name is required")
	}
	for _, existing := range FlattenEndpoints(file) {
		if existing.Name == endpoint.Name {
			return fmt.Errorf("endpoint %q already exists", endpoint.Name)
		}
	}
	if collection == "" {
		file.Endpoints = append(file.Endpoints, endpoint)
		return nil
	}
	for i := range file.Collections {
		if file.Collections[i].Name == collection {
			file.Collections[i].Endpoints = append(file.Collections[i].Endpoints, endpoint)
			return nil
		}
	}
	file.Collections = append(file.Collections, Collection{
		Name:      collection,
		Endpoints: []Endpoint{endpoint},
	})
	return nil
}

// --- env var expansion ---

// envVarRe matches ${NAME} and ${NAME:-default}.
var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// ExpandEnvVars expands ${VAR} and ${VAR:-default} in s. Returns an error if
// a referenced variable is unset and no default is provided.
func ExpandEnvVars(s string) (string, error) {
	var firstErr error
	out := envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := envVarRe.FindStringSubmatch(match)
		name := groups[1]
		hasDefault := strings.Contains(match, ":-")
		defaultVal := groups[2]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if hasDefault {
			return defaultVal
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("env var %s is not set and no default provided", name)
		}
		return ""
	})
	return out, firstErr
}

// expandFile walks the parsed file and expands env vars in every string value.
func expandFile(file *EndpointFile) error {
	for i := range file.Collections {
		for j := range file.Collections[i].Endpoints {
			if err := expandEndpoint(&file.Collections[i].Endpoints[j]); err != nil {
				return err
			}
		}
	}
	for i := range file.Endpoints {
		if err := expandEndpoint(&file.Endpoints[i]); err != nil {
			return err
		}
	}
	return nil
}

func expandEndpoint(e *Endpoint) error {
	expanded, err := ExpandEnvVars(e.URL)
	if err != nil {
		return fmt.Errorf("endpoint %q url: %w", e.Name, err)
	}
	e.URL = expanded
	for k, v := range e.Headers {
		expanded, err := ExpandEnvVars(v)
		if err != nil {
			return fmt.Errorf("endpoint %q header %s: %w", e.Name, k, err)
		}
		e.Headers[k] = expanded
	}
	for k, v := range e.Params {
		expanded, err := ExpandEnvVars(v)
		if err != nil {
			return fmt.Errorf("endpoint %q param %s: %w", e.Name, k, err)
		}
		e.Params[k] = expanded
	}
	if e.Body != nil {
		expandedBody, err := expandValue(e.Body)
		if err != nil {
			return fmt.Errorf("endpoint %q body: %w", e.Name, err)
		}
		e.Body = expandedBody
	}
	return nil
}

func expandValue(v interface{}) (interface{}, error) {
	switch val := v.(type) {
	case string:
		return ExpandEnvVars(val)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, item := range val {
			expanded, err := expandValue(item)
			if err != nil {
				return nil, err
			}
			out[k] = expanded
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			expanded, err := expandValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = expanded
		}
		return out, nil
	default:
		return v, nil
	}
}

// --- validation ---

func validateEndpointFile(file *EndpointFile) error {
	if file.Version == 0 {
		file.Version = 1
	}
	if file.Version != 1 {
		return fmt.Errorf("unsupported file version %d (expected 1)", file.Version)
	}
	seen := make(map[string]struct{})
	for _, c := range file.Collections {
		if c.Name == "" {
			return fmt.Errorf("collection name is required")
		}
		for _, e := range c.Endpoints {
			if err := validateEndpoint(e); err != nil {
				return err
			}
			if _, dup := seen[e.Name]; dup {
				return fmt.Errorf("endpoint name %q is duplicated", e.Name)
			}
			seen[e.Name] = struct{}{}
		}
	}
	for _, e := range file.Endpoints {
		if err := validateEndpoint(e); err != nil {
			return err
		}
		if _, dup := seen[e.Name]; dup {
			return fmt.Errorf("endpoint name %q is duplicated", e.Name)
		}
		seen[e.Name] = struct{}{}
	}
	return nil
}

func validateEndpoint(e Endpoint) error {
	if e.Name == "" {
		return fmt.Errorf("endpoint name is required")
	}
	if e.URL == "" {
		return fmt.Errorf("endpoint %q: url is required", e.Name)
	}
	if e.Method == "" {
		return fmt.Errorf("endpoint %q: method is required", e.Name)
	}
	return nil
}
