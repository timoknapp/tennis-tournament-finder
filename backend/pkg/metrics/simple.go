package metrics

import (
	"encoding/json"
	"expvar"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Local diagnostics endpoints (bind them on 127.0.0.1 in your main)
	StatsPath     = "/stats"
	DebugVarsPath = "/debug/vars"
	EnvPath       = "/admin/env"
)

var (
	reloadCallback func() error
	st             = newState()
)

// Init publishes expvar variables and starts any background maintenance if needed.
// Call this once at process startup.
func Init() {
	// Publish expvar variables. These snapshot on access, so no ticker is needed.
	expvar.Publish("ttf_started_at", expvar.Func(func() any {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.startedAt.Format(time.RFC3339)
	}))
	expvar.Publish("ttf_uptime_seconds", expvar.Func(func() any {
		st.mu.Lock()
		defer st.mu.Unlock()
		return int64(time.Since(st.startedAt).Seconds())
	}))
	expvar.Publish("ttf_total_requests", expvar.Func(func() any {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.totalReq
	}))
	expvar.Publish("ttf_total_errors", expvar.Func(func() any {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.totalErr
	}))
	expvar.Publish("ttf_total_latency_ms", expvar.Func(func() any {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.totalLatency.Milliseconds()
	}))
	expvar.Publish("ttf_active_users_5m", expvar.Func(func() any {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.pruneLocked(time.Now())
		return int64(len(st.active))
	}))
	expvar.Publish("ttf_requests_by_method_status", expvar.Func(func() any {
		st.mu.Lock()
		defer st.mu.Unlock()
		// Copy into map[string]map[string]int64 to keep it JSON-friendly.
		out := make(map[string]map[string]int64, len(st.byMethodStatus))
		for m, inner := range st.byMethodStatus {
			o2 := make(map[string]int64, len(inner))
			for code, c := range inner {
				o2[strconv.Itoa(code)] = c
			}
			out[m] = o2
		}
		return out
	}))
	expvar.Publish("ttf_request_duration_ms_buckets", expvar.Func(func() any {
		st.mu.Lock()
		defer st.mu.Unlock()
		// map[method]map[bucketLabel]count
		out := make(map[string]map[string]int64, len(st.durationBuckets))
		for m, inner := range st.durationBuckets {
			o2 := make(map[string]int64, len(inner))
			for bucket, c := range inner {
				o2[bucket] = c
			}
			out[m] = o2
		}
		return out
	}))
	expvar.Publish("ttf_requests_last_10m", expvar.Func(func() any {
		st.mu.Lock()
		defer st.mu.Unlock()
		// Newest-first snapshot
		out := make([]int64, len(st.perMinute))
		copy(out, st.perMinute[:])
		return out
	}))
}

// SetReloadCallback sets the function to call when configuration reload is requested
func SetReloadCallback(callback func() error) {
	reloadCallback = callback
}

// Instrument wraps an http.Handler to record request count, status codes, latency
// buckets, requests-per-minute, and active users (5m window).
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: 0}
		start := time.Now()
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		duration := time.Since(start)
		st.record(r, sw.status, duration)
	})
}

// StatsHandler returns a compact JSON snapshot, suitable for quick human inspection.
func StatsHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	st.mu.Lock()
	defer st.mu.Unlock()

	st.pruneLocked(now)
	avgLatencyMs := float64(0)
	if st.totalReq > 0 {
		avgLatencyMs = float64(st.totalLatency.Milliseconds()) / float64(st.totalReq)
	}

	// Copy RPM newest-first
	rpm := make([]int64, len(st.perMinute))
	copy(rpm, st.perMinute[:])

	// Build minimal counts
	methodStatus := make(map[string]map[string]int64, len(st.byMethodStatus))
	for m, inner := range st.byMethodStatus {
		o2 := make(map[string]int64, len(inner))
		for code, c := range inner {
			o2[strconv.Itoa(code)] = c
		}
		methodStatus[m] = o2
	}

	s := stats{
		StartedAt:                 st.startedAt.Format(time.RFC3339),
		UptimeSeconds:             int64(now.Sub(st.startedAt).Seconds()),
		TotalRequests:             st.totalReq,
		TotalErrors:               st.totalErr,
		AverageLatencyMs:          avgLatencyMs,
		RequestsPerMinuteLast10m:  rpm,
		ActiveUsers5m:             int64(len(st.active)),
		RequestsByMethodAndStatus: methodStatus,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s)
}

// ===== Internals =====

type stats struct {
	StartedAt                 string                      `json:"started_at"`
	UptimeSeconds             int64                       `json:"uptime_seconds"`
	TotalRequests             int64                       `json:"total_requests"`
	TotalErrors               int64                       `json:"total_errors"`
	AverageLatencyMs          float64                     `json:"avg_latency_ms"`
	RequestsPerMinuteLast10m  []int64                     `json:"requests_last_10m_newest_first"`
	ActiveUsers5m             int64                       `json:"active_users_5m"`
	RequestsByMethodAndStatus map[string]map[string]int64 `json:"requests_by_method_status"`
}

type metricsState struct {
	mu sync.Mutex

	startedAt time.Time

	totalReq     int64
	totalErr     int64
	totalLatency time.Duration

	// method -> statusCode -> count
	byMethodStatus map[string]map[int]int64
	// method -> bucketLabel -> count
	durationBuckets map[string]map[string]int64

	// Newest minute is perMinute[0], oldest is perMinute[9]
	perMinute  [10]int64
	lastMinute time.Time

	// active user key -> last seen time
	active map[string]time.Time
}

func newState() *metricsState {
	return &metricsState{
		startedAt:       time.Now(),
		byMethodStatus:  make(map[string]map[int]int64),
		durationBuckets: make(map[string]map[string]int64),
		active:          make(map[string]time.Time),
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *metricsState) record(r *http.Request, statusCode int, d time.Duration) {
	now := time.Now()
	method := r.Method
	if method == "" {
		method = "UNKNOWN"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Totals
	s.totalReq++
	if statusCode >= 400 {
		s.totalErr++
	}
	s.totalLatency += d

	// Method/Status counts
	if _, ok := s.byMethodStatus[method]; !ok {
		s.byMethodStatus[method] = make(map[int]int64)
	}
	s.byMethodStatus[method][statusCode]++

	// Latency buckets per method
	bucket := bucketLabel(d)
	if _, ok := s.durationBuckets[method]; !ok {
		s.durationBuckets[method] = make(map[string]int64)
	}
	s.durationBuckets[method][bucket]++

	// Requests-per-minute ring (newest-first)
	currMinute := now.Truncate(time.Minute)
	if s.lastMinute.IsZero() {
		s.lastMinute = currMinute
	}
	if delta := int(currMinute.Sub(s.lastMinute) / time.Minute); delta > 0 {
		if delta >= len(s.perMinute) {
			for i := range s.perMinute {
				s.perMinute[i] = 0
			}
		} else {
			// shift right by delta; fill zeros at the front
			for i := len(s.perMinute) - 1; i >= 0; i-- {
				j := i + delta
				if j < len(s.perMinute) {
					s.perMinute[j] = s.perMinute[i]
				}
				if i < delta {
					s.perMinute[i] = 0
				}
			}
			for i := 0; i < delta; i++ {
				s.perMinute[i] = 0
			}
		}
		s.lastMinute = currMinute
	}
	s.perMinute[0]++

	// Active users (5m window)
	key := userKey(r)
	s.active[key] = now
	s.pruneLocked(now)
}

func (s *metricsState) pruneLocked(now time.Time) {
	cutoff := now.Add(-5 * time.Minute)
	for k, t := range s.active {
		if t.Before(cutoff) {
			delete(s.active, k)
		}
	}
}

var bucketBounds = []time.Duration{
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	1000 * time.Millisecond,
	2500 * time.Millisecond,
	5000 * time.Millisecond,
}

func bucketLabel(d time.Duration) string {
	for _, b := range bucketBounds {
		if d <= b {
			return "le_" + strconv.FormatInt(b.Milliseconds(), 10) + "ms"
		}
	}
	return "gt_5000ms"
}

func userKey(r *http.Request) string {
	if uid := strings.TrimSpace(r.Header.Get("X-User-ID")); uid != "" {
		return "uid:" + uid
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if idx := strings.Index(xff, ","); idx >= 0 {
			xff = xff[:idx]
		}
		xff = strings.TrimSpace(xff)
		if xff != "" {
			return "ip:" + xff
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return "ip:" + host
	}
	return "ip:" + r.RemoteAddr
}

// EnvHandler provides GET/POST access to environment variables management
func EnvHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		handleGetEnv(w, r)
	case http.MethodPost, http.MethodPut:
		handleSetEnv(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetEnv returns current TTF_* environment variables
func handleGetEnv(w http.ResponseWriter, r *http.Request) {
	envVars := make(map[string]string)

	// Get all environment variables and filter TTF_* ones
	for _, env := range os.Environ() {
		pair := strings.SplitN(env, "=", 2)
		if len(pair) == 2 && strings.HasPrefix(pair[0], "TTF_") {
			envVars[pair[0]] = pair[1]
		}
	}

	response := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"env_vars":  envVars,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// handleSetEnv updates environment variables from JSON request
func handleSetEnv(w http.ResponseWriter, r *http.Request) {
	var request map[string]string

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON request body", http.StatusBadRequest)
		return
	}

	updated := make(map[string]string)
	errors := make(map[string]string)

	for key, value := range request {
		// Security: only allow TTF_* prefixed environment variables
		if !strings.HasPrefix(key, "TTF_") {
			errors[key] = "Only TTF_* prefixed environment variables are allowed"
			continue
		}

		// Validate key format (alphanumeric and underscore only)
		if !isValidEnvVarName(key) {
			errors[key] = "Invalid environment variable name format"
			continue
		}

		// Set the environment variable
		if err := os.Setenv(key, value); err != nil {
			errors[key] = "Failed to set environment variable: " + err.Error()
			continue
		}

		updated[key] = value
	}

	// Trigger component reload if any variables were successfully updated
	reloadMessage := "Environment variables updated. Note: Some changes may require component restart to take effect."
	if len(updated) > 0 && reloadCallback != nil {
		if err := reloadCallback(); err != nil {
			errors["reload"] = "Component reload failed: " + err.Error()
			reloadMessage = "Environment variables updated, but component reload failed. Manual restart may be required."
		} else {
			reloadMessage = "Environment variables updated and components reloaded successfully."
		}
	}

	response := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"updated":   updated,
		"errors":    errors,
		"message":   reloadMessage,
	}

	w.Header().Set("Content-Type", "application/json")
	statusCode := http.StatusOK
	if len(errors) > 0 && len(updated) == 0 {
		statusCode = http.StatusBadRequest
	}
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// isValidEnvVarName checks if the environment variable name is valid
func isValidEnvVarName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for _, char := range name {
		if !((char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return true
}
