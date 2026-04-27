# Benchmarkr CLI

A command-line and MCP tool for API performance testing. Runs benchmarks directly — no server required.

> **Full documentation:** [benchmarkr-1.onrender.com/docs](https://benchmarkr.dev/docs)

## Install

### Homebrew (macOS/Linux)

```bash
brew tap mack-overflow/tap
brew install benchmarkr
```

### apt (Debian/Ubuntu)

```bash
echo "deb [trusted=yes] https://apt.fury.io/mack-overflow/ /" | sudo tee /etc/apt/sources.list.d/benchmarkr.list
sudo apt update
sudo apt install benchmarkr
```

### yum (RHEL/Fedora)

```bash
sudo tee /etc/yum.repos.d/benchmarkr.repo <<EOF
[benchmarkr]
name=Benchmarkr
baseurl=https://yum.fury.io/mack-overflow/
enabled=1
gpgcheck=0
EOF
sudo yum install benchmarkr
```

### Build from source

```bash
git clone https://github.com/Mack-Overflow/api-bench.git
cd api-bench/go
go build -o benchmarkr ./cmd/benchmarkr/
```

## Run via Docker

If you don't have Go installed locally, you can run the CLI through Docker.

### Build the image

```bash
docker build -t benchmarkr-cli ./go
```

### Run benchmarks

```bash
# Simple GET benchmark
docker run --rm benchmarkr-cli ./benchmarkr run --url https://api.example.com/health

# With concurrency and duration
docker run --rm benchmarkr-cli ./benchmarkr run \
  --url https://api.example.com/users \
  --concurrency 10 \
  --duration 30
```

### Use the running docker-compose container

If the full stack is already running via `docker compose`, exec into the existing container:

```bash
docker compose exec go ./benchmarkr run --url https://api.example.com/health
```

### Persist results to the database from Docker

When the container is on the `benchmarkr` network, it can reach the Postgres service directly:

```bash
docker run --rm --network benchmarkr_benchmarkr \
  -e DB_URL="postgres://benchmarkr:secret@benchmarkr-postgres:5432/benchmarkr?sslmode=disable" \
  benchmarkr-cli ./benchmarkr run --url https://api.example.com/health --store
```

Or via docker compose:

```bash
docker compose exec go ./benchmarkr run --url https://api.example.com/health --store
```

## Quick Start

```bash
# Simple GET benchmark
benchmarkr run --url https://api.example.com/health

# 10 concurrent workers for 30 seconds
benchmarkr run --url https://api.example.com/users --concurrency 10 --duration 30

# POST with headers and body
benchmarkr run \
  --url https://api.example.com/users \
  --method POST \
  --header "Authorization: Bearer tok_xxx" \
  --header "Content-Type: application/json" \
  --body '{"name":"test"}'
```

## Commands

| Command       | Description                                         |
|---------------|-----------------------------------------------------|
| `run`         | Run a benchmark against a target URL or saved endpoint |
| `endpoints`   | Manage local endpoint definitions                   |
| `config`      | Manage storage configuration                        |
| `version`     | Print version information                           |
| `help`        | Show help                                           |

## Run Flags

| Flag                | Default      | Description                                      |
|---------------------|--------------|--------------------------------------------------|
| `--url`             | *(see note)* | Target URL to benchmark                          |
| `--endpoint`, `-e`  |              | Run a saved endpoint by name (from local config) |
| `--version`, `-v`   | `0`          | Pin to a specific endpoint version (best-effort cloud lookup) |
| `--file`            |              | Path to endpoints config (default: discovered)   |
| `--method`          | `GET`        | HTTP method                                      |
| `--concurrency`     | `1`          | Number of concurrent workers                     |
| `--duration`        | `10`         | Test duration in seconds                         |
| `--rate-limit`      | `0`          | Max requests per second (0 = unlimited)          |
| `--throttle`        | `0`          | Per-request delay in milliseconds                |
| `--cache-mode`      | `default`    | Cache mode: `default`, `bypass`, `warm`          |
| `--name`            | URL/name     | Benchmark name                                   |
| `--header`          |              | HTTP header, repeatable (`"Key: Value"`)         |
| `--param`           |              | Query parameter, repeatable (`"key=value"`)      |
| `--body`            |              | Request body (JSON string)                       |
| `--json`            | `false`      | Output results as JSON                           |
| `--store`, `-s`     | `false`      | Persist results to database                      |

> Provide either `--url` or `--endpoint`, not both. CLI flags override values from the endpoint config; headers and params are merged (CLI wins on conflicts).

## Endpoint Config Files

Save endpoints to a project-local YAML or JSON file so you can run them by name with `benchmarkr run -e <name>`. Useful for repeated local testing of a known set of endpoints, sharing test configs with teammates, or pinning configs to git.

### Create a config file

```bash
# In your project root:
benchmarkr endpoints init                # creates ./benchmarkr.yaml
benchmarkr endpoints init --format json  # or ./benchmarkr.json
```

The discovery walks up from CWD, so the file works from any subdirectory of the project (like `.git` or `package.json`).

Recognized filenames: `benchmarkr.yaml`, `benchmarkr.yml`, `benchmarkr.json`, `.benchmarkr.yaml`, `.benchmarkr.yml`, `.benchmarkr.json`.

### Add endpoints

```bash
benchmarkr endpoints add list-users \
  --url 'https://${API_BASE}/users' \
  --header 'Authorization: Bearer ${API_TOKEN}' \
  --param 'limit=50' \
  --concurrency 10 \
  --duration 30

# Group into a collection (collections live only in the file, not the cloud)
benchmarkr endpoints add ping --url https://api.example.com/ping --collection health
```

### List, show, run

```bash
benchmarkr endpoints list                # show all endpoints
benchmarkr endpoints show list-users     # print resolved config (env vars expanded)
benchmarkr endpoints show list-users --raw  # show with placeholders intact

benchmarkr run -e list-users             # run with saved defaults
benchmarkr run -e list-users -v 3        # pin to cloud version 3 (best-effort)
benchmarkr run -e list-users --concurrency 50  # CLI overrides
```

### Env var substitution

Strings in the config support shell-like expansion:

| Syntax              | Behavior                                           |
|---------------------|----------------------------------------------------|
| `${VAR}`            | Required — errors if `VAR` is not set              |
| `${VAR:-default}`   | Falls back to `default` when `VAR` is unset        |

A sibling `.env` file is auto-loaded if present (it does **not** override variables already set in the environment).

```yaml
# benchmarkr.yaml
version: 1
endpoints:
  - name: list-users
    method: GET
    url: ${API_BASE:-http://localhost:8080}/users
    headers:
      Authorization: Bearer ${API_TOKEN}
    defaults:
      concurrency: 10
      duration_seconds: 30
```

```bash
# .env (sibling to benchmarkr.yaml)
API_BASE=https://api.staging.example.com
API_TOKEN=tok_abc123
```

> **Tip:** add `.env` to your `.gitignore` so secrets stay out of git, and keep `benchmarkr.yaml` committed.

### Round-trip with the web UI

Endpoints saved in the dashboard can be exported/imported:

- **Export:** open an endpoint in the dashboard → click **Export** (YAML or JSON). Click **Export all** in the endpoints nav to dump every endpoint to a single file.
- **Import:** click **Import** in the endpoints nav and select a `benchmarkr.yaml`/`.json`. Endpoints upsert by `(user, name)` and create a new version if the config changed.

A typical workflow:

1. Configure an endpoint in the dashboard, save a few benchmark runs.
2. Click **Export** → drop the file into your project root.
3. `benchmarkr run -e <name>` from CI or your terminal.
4. With `--store` set and the cloud API token configured, runs are linked back to the same endpoint in the dashboard.

### Endpoints subcommands

| Subcommand                         | Description                                              |
|------------------------------------|----------------------------------------------------------|
| `endpoints init [--format yaml\|json] [--force]` | Create a starter file in the CWD                         |
| `endpoints add <name> --url ...`   | Append an endpoint (use `--collection` to group)         |
| `endpoints list`                   | List endpoints with method, URL, version pin, collection |
| `endpoints show <name> [--raw] [--format yaml\|json]` | Print resolved or raw endpoint config |

## Examples

### Rate-limited benchmark

```bash
benchmarkr run \
  --url https://api.example.com/search \
  --concurrency 5 \
  --duration 20 \
  --rate-limit 100
```

### Cache bypass testing

```bash
benchmarkr run \
  --url https://cdn.example.com/asset.js \
  --cache-mode bypass \
  --duration 10
```

### JSON output for CI pipelines

```bash
benchmarkr run --url https://api.example.com/health --duration 5 --json
```

```json
{
  "stop_reason": "completed",
  "duration": "5.002s",
  "stored": false,
  "result": {
    "requests": 847,
    "errors_total": 0,
    "avg_ms": 5,
    "p50_ms": 4,
    "p95_ms": 12,
    "p99_ms": 23,
    "min_ms": 2,
    "max_ms": 45,
    "status_2xx": 847,
    "cache": { "hits": 0, "misses": 0 }
  }
}

## MCP Server

Benchmarkr ships an MCP server (`benchmarkr-mcp`) that lets AI agents run benchmarks via natural language. Install it the same way as the CLI:

```bash
brew install mack-overflow/tap/benchmarkr-mcp   # Homebrew
sudo apt install benchmarkr-mcp                  # apt
sudo yum install benchmarkr-mcp                  # yum
```

Then add it to your agent's config:

### Claude Code

Add to `.mcp.json` in your project root directory:

```json
{
  "mcpServers": {
    "benchmarkr": {
      "command": "benchmarkr-mcp"
    }
  }
}
```

Then add a `CLAUDE.md` to your project root so the agent knows to use benchmarkr:

```markdown
# Benchmarking

Use the benchmarkr MCP tools (run_benchmark, compare_endpoints, regression_test) for all API benchmarking tasks.
Do not install or use external tools like hey, ab, or bombardier.
```

### Cursor

Add to `~/.cursor/mcp.json` in your project root directory:

```json
{
  "mcpServers": {
    "benchmarkr": {
      "command": "benchmarkr-mcp"
    }
  }
}
```

Then add a `.cursorrules` to your project root:

```markdown
# Benchmarking

Use the benchmarkr MCP tools (run_benchmark, compare_endpoints, regression_test) for all API benchmarking tasks.
Do not install or use external tools like hey, ab, or bombardier.
```

The MCP server works without a database by default. To enable persistence, set the `DB_URL` environment variable.

## Environment Variables

| Variable              | Description                                                  |
|-----------------------|--------------------------------------------------------------|
| `DB_URL`              | PostgreSQL connection string (for `--store` cloud mode)      |
| `BENCH_CLOUD_TOKEN`   | API token for cloud sync — needed when using `--store` with cloud, or `-v` to pin a cloud version |
| `BENCH_CONFIG`        | Override the path to the storage config (TOML)               |
