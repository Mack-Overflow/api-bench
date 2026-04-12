# Benchmarkr CLI

A command-line tool for API performance testing. Runs benchmarks directly — no server required.

## Build

```bash
cd go
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

| Command   | Description                        |
|-----------|------------------------------------|
| `run`     | Run a benchmark against a target URL |
| `version` | Print version information          |
| `help`    | Show help                          |

## Run Flags

| Flag           | Default     | Description                                      |
|----------------|-------------|--------------------------------------------------|
| `--url`        | *(required)* | Target URL to benchmark                          |
| `--method`     | `GET`       | HTTP method                                      |
| `--concurrency`| `1`         | Number of concurrent workers                     |
| `--duration`   | `10`        | Test duration in seconds                         |
| `--rate-limit` | `0`         | Max requests per second (0 = unlimited)          |
| `--throttle`   | `0`         | Per-request delay in milliseconds                |
| `--cache-mode` | `default`   | Cache mode: `default`, `bypass`, `warm`          |
| `--name`       | URL value   | Benchmark name                                   |
| `--header`     |             | HTTP header, repeatable (`"Key: Value"`)         |
| `--param`      |             | Query parameter, repeatable (`"key=value"`)      |
| `--body`       |             | Request body (JSON string)                       |
| `--json`       | `false`     | Output results as JSON                           |
| `--store`, `-s`| `false`     | Persist results to database (requires `DB_URL`)  |

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
```

### Persist results to database

When running alongside the docker-compose stack, use `--store` to save results:

```bash
export DB_URL="postgres://benchmarkr:secret@localhost:5432/benchmarkr?sslmode=disable"
benchmarkr run --url https://api.example.com/health --store
```


Results are stored in the same database used by the web UI, so they appear in your benchmark history.

## Architecture

The CLI runs the benchmark engine directly in-process. No HTTP server is required.

```
benchmarkr CLI
    └── benchmark/    (engine: workers, metrics, results)
    └── db/           (optional: persistence with --store)
```

The same `benchmark/` package powers both the CLI and the HTTP server used by the web UI.

## Environment Variables

| Variable            | Description                                    |
|---------------------|------------------------------------------------|
| `DB_URL`            | PostgreSQL connection string (for `--store`)   |
