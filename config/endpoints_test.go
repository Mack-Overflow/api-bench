package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("FOO", "bar")
	t.Setenv("EMPTY", "")

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"plain", "no vars here", "no vars here", false},
		{"simple", "${FOO}", "bar", false},
		{"embedded", "x-${FOO}-y", "x-bar-y", false},
		{"default unused", "${FOO:-fallback}", "bar", false},
		{"default used", "${MISSING:-fallback}", "fallback", false},
		{"default empty", "${MISSING:-}", "", false},
		{"empty var no default", "${EMPTY}", "", false},
		{"missing required", "${MISSING}", "", true},
		{"two vars", "${FOO}/${MISSING:-x}", "bar/x", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandEnvVars(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindEndpointFile(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "a", "benchmarkr.yaml")
	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	found, err := FindEndpointFile(nested)
	if err != nil {
		t.Fatalf("FindEndpointFile: %v", err)
	}
	if found != configPath {
		t.Errorf("found = %s, want %s", found, configPath)
	}
}

func TestFindEndpointFileNoneFound(t *testing.T) {
	root := t.TempDir()
	found, err := FindEndpointFile(root)
	if err != nil {
		t.Fatalf("FindEndpointFile: %v", err)
	}
	if found != "" {
		t.Errorf("expected empty result, got %q", found)
	}
}

func TestLoadEndpointFileYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmarkr.yaml")
	content := `version: 1
collections:
  - name: api
    endpoints:
      - name: users
        method: GET
        url: https://example.com/users
        headers:
          Authorization: Bearer token
endpoints:
  - name: health
    method: GET
    url: https://example.com/health
    defaults:
      concurrency: 5
      duration_seconds: 10
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	file, err := LoadEndpointFile(path)
	if err != nil {
		t.Fatalf("LoadEndpointFile: %v", err)
	}
	if file.Version != 1 {
		t.Errorf("version = %d, want 1", file.Version)
	}
	if len(file.Collections) != 1 || file.Collections[0].Name != "api" {
		t.Errorf("collections = %+v", file.Collections)
	}
	if len(file.Endpoints) != 1 || file.Endpoints[0].Name != "health" {
		t.Errorf("endpoints = %+v", file.Endpoints)
	}
	if file.Endpoints[0].Defaults == nil || file.Endpoints[0].Defaults.Concurrency != 5 {
		t.Errorf("defaults = %+v", file.Endpoints[0].Defaults)
	}
}

func TestLoadEndpointFileJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmarkr.json")
	content := `{"version":1,"endpoints":[{"name":"x","method":"GET","url":"https://e.com"}]}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	file, err := LoadEndpointFile(path)
	if err != nil {
		t.Fatalf("LoadEndpointFile: %v", err)
	}
	if len(file.Endpoints) != 1 || file.Endpoints[0].Name != "x" {
		t.Errorf("endpoints = %+v", file.Endpoints)
	}
}

func TestLoadEndpointFileRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmarkr.yaml")
	content := `version: 1
endpoints:
  - {name: dup, method: GET, url: https://a}
  - {name: dup, method: GET, url: https://b}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadEndpointFile(path)
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Errorf("expected duplicate-name error, got %v", err)
	}
}

func TestLoadEndpointFileRequiresFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmarkr.yaml")
	content := `version: 1
endpoints:
  - {name: x, method: GET}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadEndpointFile(path)
	if err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Errorf("expected url-required error, got %v", err)
	}
}

func TestResolveEndpointFileExpandsEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("API_TOKEN=secret-from-env\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("API_BASE", "https://api.example.com")

	path := filepath.Join(dir, "benchmarkr.yaml")
	content := `version: 1
endpoints:
  - name: users
    method: GET
    url: ${API_BASE}/users
    headers:
      Authorization: Bearer ${API_TOKEN}
    params:
      env: ${ENV_NAME:-prod}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	file, err := ResolveEndpointFile(path)
	if err != nil {
		t.Fatalf("ResolveEndpointFile: %v", err)
	}
	got := file.Endpoints[0]
	if got.URL != "https://api.example.com/users" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Headers["Authorization"] != "Bearer secret-from-env" {
		t.Errorf("Authorization = %q", got.Headers["Authorization"])
	}
	if got.Params["env"] != "prod" {
		t.Errorf("env param = %q", got.Params["env"])
	}
}

func TestResolveEndpointFileMissingVar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmarkr.yaml")
	content := `version: 1
endpoints:
  - {name: x, method: GET, url: "${MUST_BE_SET}"}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("MUST_BE_SET")

	_, err := ResolveEndpointFile(path)
	if err == nil || !strings.Contains(err.Error(), "MUST_BE_SET") {
		t.Errorf("expected MUST_BE_SET error, got %v", err)
	}
}

func TestSaveAndReloadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmarkr.yaml")
	v := 3
	file := &EndpointFile{
		Version: 1,
		Endpoints: []Endpoint{
			{Name: "users", Method: "GET", URL: "https://a", Version: &v,
				Headers: map[string]string{"X-A": "1"}},
		},
	}
	if err := SaveEndpointFile(path, file); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadEndpointFile(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Endpoints) != 1 || loaded.Endpoints[0].Name != "users" {
		t.Errorf("loaded = %+v", loaded.Endpoints)
	}
	if loaded.Endpoints[0].Version == nil || *loaded.Endpoints[0].Version != 3 {
		t.Errorf("version = %v", loaded.Endpoints[0].Version)
	}
}

func TestFlattenAndFind(t *testing.T) {
	file := &EndpointFile{
		Version: 1,
		Collections: []Collection{
			{Name: "api", Endpoints: []Endpoint{{Name: "a", Method: "GET", URL: "https://a"}}},
		},
		Endpoints: []Endpoint{{Name: "b", Method: "GET", URL: "https://b"}},
	}
	flat := FlattenEndpoints(file)
	if len(flat) != 2 {
		t.Fatalf("flat = %d, want 2", len(flat))
	}
	if flat[0].Collection != "api" || flat[1].Collection != "" {
		t.Errorf("collections = %q, %q", flat[0].Collection, flat[1].Collection)
	}

	found, err := FindEndpointByName(file, "a")
	if err != nil || found.Name != "a" {
		t.Errorf("find a: %v %+v", err, found)
	}
	if _, err := FindEndpointByName(file, "missing"); err == nil {
		t.Error("expected error for missing endpoint")
	}
}

func TestAddEndpoint(t *testing.T) {
	file := &EndpointFile{Version: 1}

	err := AddEndpoint(file, "", Endpoint{Name: "a", Method: "GET", URL: "https://a"})
	if err != nil {
		t.Fatalf("add a: %v", err)
	}
	if len(file.Endpoints) != 1 {
		t.Errorf("expected 1 top-level endpoint")
	}

	err = AddEndpoint(file, "users", Endpoint{Name: "b", Method: "GET", URL: "https://b"})
	if err != nil {
		t.Fatalf("add b: %v", err)
	}
	if len(file.Collections) != 1 || len(file.Collections[0].Endpoints) != 1 {
		t.Errorf("collection b not added: %+v", file.Collections)
	}

	err = AddEndpoint(file, "users", Endpoint{Name: "c", Method: "GET", URL: "https://c"})
	if err != nil {
		t.Fatalf("add c: %v", err)
	}
	if len(file.Collections[0].Endpoints) != 2 {
		t.Errorf("collection c not appended: %+v", file.Collections[0].Endpoints)
	}

	err = AddEndpoint(file, "", Endpoint{Name: "a", Method: "GET", URL: "https://a"})
	if err == nil {
		t.Error("expected duplicate-name error")
	}
}
