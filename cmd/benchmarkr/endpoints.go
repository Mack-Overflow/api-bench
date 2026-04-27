package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mack-Overflow/api-bench/config"
	"gopkg.in/yaml.v3"
)

func endpointsCmd(args []string) error {
	if len(args) == 0 {
		printEndpointsUsage()
		return nil
	}

	switch args[0] {
	case "init":
		return endpointsInitCmd(args[1:])
	case "add":
		return endpointsAddCmd(args[1:])
	case "list", "ls":
		return endpointsListCmd(args[1:])
	case "show":
		return endpointsShowCmd(args[1:])
	case "help", "--help", "-h":
		printEndpointsUsage()
		return nil
	default:
		printEndpointsUsage()
		return fmt.Errorf("unknown endpoints command: %s", args[0])
	}
}

func printEndpointsUsage() {
	fmt.Print(`benchmarkr endpoints - Manage local endpoint definitions

Usage:
  benchmarkr endpoints <command>

Commands:
  init    Create a new benchmarkr.yaml in the current directory
  add     Add an endpoint to the local config
  list    List all endpoints in the local config
  show    Print a resolved endpoint (with env vars expanded)

Run "benchmarkr endpoints <command> --help" for command-specific options.
`)
}

// --- init ---

func endpointsInitCmd(args []string) error {
	fs := flag.NewFlagSet("endpoints init", flag.ExitOnError)
	format := fs.String("format", "yaml", "File format: yaml or json")
	path := fs.String("path", "", "Output path (default: ./benchmarkr.<format>)")
	force := fs.Bool("force", false, "Overwrite existing file")
	fs.Parse(args)

	if *format != "yaml" && *format != "json" {
		return fmt.Errorf("--format must be 'yaml' or 'json'")
	}

	outPath := *path
	if outPath == "" {
		outPath = "benchmarkr." + *format
	}

	if _, err := os.Stat(outPath); err == nil && !*force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", outPath)
	}

	if *format == "yaml" {
		if err := os.WriteFile(outPath, []byte(starterYAML), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", outPath, err)
		}
	} else {
		starter := config.EndpointFile{Version: 1}
		if err := config.SaveEndpointFile(outPath, &starter); err != nil {
			return err
		}
	}

	abs, _ := filepath.Abs(outPath)
	fmt.Printf("  Endpoints file written to %s\n", abs)
	fmt.Println("  Edit it to add endpoints, or run: benchmarkr endpoints add <name> --url ...")
	return nil
}

const starterYAML = `# benchmarkr endpoints
# Run a saved endpoint with: benchmarkr run -e <name>
# Pin a specific version with: benchmarkr run -e <name> -v <version>
#
# Strings support env var substitution:
#   ${VAR}            — required (errors if unset)
#   ${VAR:-default}   — falls back to default if unset
# A sibling .env file is auto-loaded if present.

version: 1

# Collections are config-file-only groupings; they are not synced to the cloud.
collections: []

# Top-level endpoints (no collection).
endpoints: []
#  - name: example
#    method: GET
#    url: ${API_BASE:-https://httpbin.org}/get
#    headers:
#      Authorization: Bearer ${API_TOKEN}
#    params:
#      limit: "10"
#    defaults:
#      concurrency: 5
#      duration_seconds: 10
`

// --- add ---

func endpointsAddCmd(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage: benchmarkr endpoints add <name> --url ... [flags]")
	}
	name := args[0]
	args = args[1:]

	fs := flag.NewFlagSet("endpoints add", flag.ExitOnError)
	url := fs.String("url", "", "Target URL (required)")
	method := fs.String("method", "GET", "HTTP method")
	collection := fs.String("collection", "", "Collection name (optional)")
	body := fs.String("body", "", "Request body (JSON string)")
	concurrency := fs.Int("concurrency", 0, "Default concurrency (omit to skip)")
	duration := fs.Int("duration", 0, "Default duration in seconds (omit to skip)")
	rateLimit := fs.Int("rate-limit", 0, "Default rate limit (omit to skip)")
	throttle := fs.Int("throttle", 0, "Default throttle in ms (omit to skip)")
	cacheMode := fs.String("cache-mode", "", "Default cache mode (omit to skip)")
	filePath := fs.String("file", "", "Endpoints file (default: discovered from CWD)")

	var headers stringSlice
	var params stringSlice
	fs.Var(&headers, "header", `HTTP header (repeatable, format: "Key: Value")`)
	fs.Var(&params, "param", `Query parameter (repeatable, format: "key=value")`)

	fs.Parse(args)

	if *url == "" {
		return fmt.Errorf("--url is required")
	}

	resolvedPath, err := resolveEndpointFilePath(*filePath, false)
	if err != nil {
		return err
	}
	file, err := config.LoadEndpointFile(resolvedPath)
	if err != nil {
		return err
	}

	headersMap, err := parseHeaders(headers)
	if err != nil {
		return err
	}
	paramsMap, err := parseParams(params)
	if err != nil {
		return err
	}

	endpoint := config.Endpoint{
		Name:    name,
		Method:  strings.ToUpper(*method),
		URL:     *url,
		Headers: headersMap,
		Params:  paramsMap,
	}

	if *body != "" {
		var parsed interface{}
		if err := json.Unmarshal([]byte(*body), &parsed); err != nil {
			return fmt.Errorf("--body must be valid JSON: %w", err)
		}
		endpoint.Body = parsed
	}

	defaults := &config.EndpointDefaults{
		Concurrency:    *concurrency,
		DurationSec:    *duration,
		RateLimit:      *rateLimit,
		ThrottleTimeMs: *throttle,
		CacheMode:      *cacheMode,
	}
	if *defaults != (config.EndpointDefaults{}) {
		endpoint.Defaults = defaults
	}

	if err := config.AddEndpoint(file, *collection, endpoint); err != nil {
		return err
	}

	if err := config.SaveEndpointFile(resolvedPath, file); err != nil {
		return err
	}

	target := name
	if *collection != "" {
		target = *collection + "/" + name
	}
	fmt.Printf("  Added endpoint %s to %s\n", target, resolvedPath)
	return nil
}

// --- list ---

func endpointsListCmd(args []string) error {
	fs := flag.NewFlagSet("endpoints list", flag.ExitOnError)
	filePath := fs.String("file", "", "Endpoints file (default: discovered from CWD)")
	fs.Parse(args)

	resolvedPath, err := resolveEndpointFilePath(*filePath, true)
	if err != nil {
		return err
	}
	file, err := config.LoadEndpointFile(resolvedPath)
	if err != nil {
		return err
	}

	endpoints := config.FlattenEndpoints(file)
	if len(endpoints) == 0 {
		fmt.Printf("  No endpoints in %s\n", resolvedPath)
		return nil
	}

	fmt.Printf("  %s\n\n", resolvedPath)
	for _, e := range endpoints {
		version := "latest"
		if e.Version != nil {
			version = fmt.Sprintf("v%d", *e.Version)
		}
		group := ""
		if e.Collection != "" {
			group = " [" + e.Collection + "]"
		}
		fmt.Printf("  %-24s  %-6s  %s  (%s)%s\n", e.Name, e.Method, e.URL, version, group)
	}
	return nil
}

// --- show ---

func endpointsShowCmd(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage: benchmarkr endpoints show <name> [flags]")
	}
	name := args[0]
	args = args[1:]

	fs := flag.NewFlagSet("endpoints show", flag.ExitOnError)
	filePath := fs.String("file", "", "Endpoints file (default: discovered from CWD)")
	format := fs.String("format", "yaml", "Output format: yaml or json")
	raw := fs.Bool("raw", false, "Skip env var expansion")
	fs.Parse(args)

	resolvedPath, err := resolveEndpointFilePath(*filePath, true)
	if err != nil {
		return err
	}

	file, err := config.LoadEndpointFile(resolvedPath)
	if err != nil {
		return err
	}

	endpoint, err := config.FindEndpointByName(file, name)
	if err != nil {
		return err
	}

	if !*raw {
		config.LoadDotenvForFile(resolvedPath)
		if err := config.ExpandEndpoint(endpoint); err != nil {
			return err
		}
	}

	switch *format {
	case "yaml":
		data, err := yaml.Marshal(endpoint)
		if err != nil {
			return err
		}
		os.Stdout.Write(data)
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(endpoint)
	default:
		return fmt.Errorf("--format must be 'yaml' or 'json'")
	}
	return nil
}

// --- helpers ---

// resolveEndpointFilePath returns the path to use. If explicit is non-empty,
// it is returned as-is. Otherwise the discovery walk runs from CWD. When
// requireExisting is false, a missing file is allowed (init / add use the
// CWD default location to create one).
func resolveEndpointFilePath(explicit string, requireExisting bool) (string, error) {
	if explicit != "" {
		if requireExisting {
			if _, err := os.Stat(explicit); err != nil {
				return "", fmt.Errorf("file not found: %s", explicit)
			}
		}
		return explicit, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	found, err := config.FindEndpointFile(cwd)
	if err != nil {
		return "", err
	}
	if found != "" {
		return found, nil
	}
	if requireExisting {
		return "", fmt.Errorf("no benchmarkr endpoints file found (looked for %s walking up from CWD)\nRun 'benchmarkr endpoints init' to create one", strings.Join(config.CandidateFilenames, ", "))
	}
	return filepath.Join(cwd, "benchmarkr.yaml"), nil
}

func parseHeaders(headers []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, h := range headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid header format %q (expected \"Key: Value\")", h)
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out, nil
}

func parseParams(params []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, p := range params {
		parts := strings.SplitN(p, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid param format %q (expected \"key=value\")", p)
		}
		out[parts[0]] = parts[1]
	}
	return out, nil
}
