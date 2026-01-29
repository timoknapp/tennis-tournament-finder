# Lightweight backend metrics (no Prometheus)

This backend exposes simple, low-overhead metrics without running a metrics stack. All code uses only the Go standard library.

Endpoints (localhost-only):
- `http://127.0.0.1:9090/stats` – compact JSON summary for humans/scripts
- `http://127.0.0.1:9090/debug/vars` – standard Go "expvar" JSON with detailed counters under `ttf_*`
- `http://127.0.0.1:9090/admin/env` – environment variables management (GET/POST/PUT)

Public API is unchanged and stays on `:8080`.

**Note:** The diagnostics endpoints on port 9090 are intentionally not instrumented to avoid metrics about metrics. Only requests to the public API on port 8080 are tracked.

## Metrics

What's included:
- Total requests, total errors (4xx and 5xx status codes), average latency
- Requests per minute over the last 10 minutes
- Requests by method and status, e.g. "GET 200": 123
- Latency buckets by method (ms): 10,25,50,100,250,500,1000,2500,5000,+Inf (available in `/debug/vars` endpoint only)
- Active users over the last 5 minutes (based on `X-User-ID` header or client IP fallback)
- HTTP method whitelisting: Only standard methods (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS) are tracked individually; non-standard methods are grouped as "OTHER"

**Note:** The `/stats` endpoint provides a compact JSON summary with core metrics (total requests, errors, avg latency, active users, requests by method/status). For detailed latency bucket distributions, use the `/debug/vars` endpoint.

Examples:
- Quick check:
  ```
  curl -s http://127.0.0.1:9090/stats | jq
  ```

  Sample output:
  ```json
  {
    "started_at": "2026-01-15T10:30:00Z",
    "uptime_seconds": 3600,
    "total_requests": 150,
    "total_errors": 5,
    "avg_latency_ms": 42.5,
    "requests_last_10m_newest_first": [15, 12, 18, 10, 14, 16, 11, 13, 17, 14],
    "active_users_5m": 8,
    "requests_by_method_status": {"GET": {"200": 140, "404": 5}, "POST": {"201": 5}}
  }
  ```

- Machine-readable full vars with latency buckets:
  ```
  curl -s http://127.0.0.1:9090/debug/vars | jq '. | with_entries(select(.key|startswith("ttf_")))'
  ```

## Environment Variables Management

The `/admin/env` endpoint provides runtime configuration management through environment variables.

### GET Environment Variables

Returns all current TTF_* environment variables:

```bash
curl -s http://127.0.0.1:9090/admin/env | jq
```

Example response:
```json
{
  "timestamp": "2026-01-15T10:30:00Z",
  "env_vars": {
    "TTF_LOG_LEVEL": "DEBUG",
    "TTF_SCHEDULER_CRON": "0 5 * * *",
    "TTF_SCHEDULER_ENABLED": "true"
  }
}
```

### SET Environment Variables

Updates environment variables and automatically reloads affected components:

```bash
curl -X POST http://127.0.0.1:9090/admin/env \
  -H "Content-Type: application/json" \
  -d '{"TTF_SCHEDULER_ENABLED": "true", "TTF_LOG_LEVEL": "DEBUG"}'
```

Example response:
```json
{
  "timestamp": "2026-01-15T10:30:00Z",
  "updated": {
    "TTF_SCHEDULER_ENABLED": "true",
    "TTF_LOG_LEVEL": "DEBUG"
  },
  "errors": {},
  "message": "Environment variables updated and components reloaded successfully."
}
```

### Supported Environment Variables

The following TTF_* environment variables can be managed:

**Scheduler Configuration:**
- `TTF_SCHEDULER_ENABLED` - Enable/disable scheduler (`true`/`false`)
- `TTF_SCHEDULER_CRON` - Cron schedule (e.g., "0 2 * * *")
- `TTF_SCHEDULER_COMP_TYPE` - Competition type filter (e.g., "Herren+Einzel")
- `TTF_SCHEDULER_FEDERATIONS` - Federation IDs (comma-separated)

**Logger Configuration:**
- `TTF_LOG_LEVEL` - Log level (`DEBUG`, `INFO`, `WARN`, `ERROR`)

**Cache Configuration:**
- `TTF_CACHE_MEMORY` - Enable memory caching (`true`/`false`)
- `TTF_CACHE_PATH` - Path to persistent cache file

### Component Reloading

When environment variables are updated via the API, the following components are automatically reloaded:

1. **Logger**: Log level changes take effect immediately
2. **Scheduler**: Configuration changes trigger automatic restart with new settings
   - Enabling/disabling the scheduler
   - Changing cron schedule
   - Updating competition type and federation filters

**Note:** Environment variable changes affect the entire process globally. Child processes spawned after the change will inherit these new values.

### Security

- All endpoints bind to `127.0.0.1` only - not accessible externally
- Only TTF_* prefixed environment variables are allowed
- Environment variable names must start with a letter or underscore, followed by uppercase letters, numbers, and underscores only
- Detailed validation and error reporting

Notes:
- Diagnostics server binds to `127.0.0.1` so these endpoints are not exposed externally. If you run behind a reverse proxy, do not forward port 9090.
- Keep cardinality low: we never add user IDs or IPs as metric labels; they are used only internally to compute the active-users gauge.
- If the service runs behind a proxy, ensure it sets `X-Forwarded-For` for a better active-users approximation when no authenticated user ID is available.
- HTTP methods are whitelisted to prevent unbounded map growth from custom HTTP methods.
