# In-Process Scheduler (Cache Warmup)

This backend can warm up geocoding caches nightly without any admin endpoint or external trigger.

## Enable

Set environment variables and start the backend:

```bash
# Enable scheduler
export TTF_SCHEDULER_ENABLED=true

# Run daily at 02:00 (server local time)
export TTF_SCHEDULER_CRON="0 2 * * *"

# Optional: filter by competition type (same values accepted by the API)
export TTF_SCHEDULER_COMP_TYPE=""

# Optional: filter by federations (comma-separated federation IDs; empty = all)
export TTF_SCHEDULER_FEDERATIONS=""
```

Start the server:

```bash
cd backend
go run ./cmd/main.go
```

## How it works
- The scheduler calls `tournament.Warmup()`, which fetches tournaments (using the same code path as the HTTP handler) for the next 14 days by default.
- During fetch, geocoding is performed/cached, so daytime requests are served faster from cache.

## Notes
- The cron expression uses server local time.
- Logs will show warmup start/finish and counts.