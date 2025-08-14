# Lightweight backend metrics (no Prometheus)

This backend exposes simple, low-overhead metrics without running a metrics stack. All code uses only the Go standard library.

Endpoints (localhost-only):
- `http://127.0.0.1:9090/stats` – compact JSON summary for humans/scripts
- `http://127.0.0.1:9090/debug/vars` – standard Go "expvar" JSON with detailed counters under `ttf_*`

Public API is unchanged and stays on `:8080`.

What's included:
- Total requests, total errors, average latency
- Requests per minute over the last 10 minutes
- Requests by method and status, e.g. "GET 200": 123
- Latency buckets by method (ms): 10,25,50,100,250,500,1000,2500,5000,+Inf
- Active users over the last 5 minutes (based on `X-User-ID` header or client IP fallback)

Notes:
- Diagnostics server binds to `127.0.0.1` so these endpoints are not exposed externally. If you run behind a reverse proxy, do not forward port 9090.
- Keep cardinality low: we never add user IDs or IPs as metric labels; they are used only internally to compute the active-users gauge.
- If the service runs behind a proxy, ensure it sets `X-Forwarded-For` for a better active-users approximation when no authenticated user ID is available.

Examples:
- Quick check:
  ```
  curl -s http://127.0.0.1:9090/stats | jq
  ```
- Machine-readable full vars:
  ```
  curl -s http://127.0.0.1:9090/debug/vars | jq '. | with_entries(select(.key|startswith("ttf_")))'
  ```