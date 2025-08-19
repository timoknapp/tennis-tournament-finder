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

// Public constants
const (
	StatsPath     = "/stats"
	DebugVarsPath = "/debug/vars"
	EnvPath       = "/admin/env"
)

// Internal state, protected by mu
var (
	mu sync.RWMutex

	startedAt = time.Now()

	// Totals
	totalRequests  int64
	totalErrors    int64 // http status >= 500
	totalLatencyMs int64

	// Requests per minute ring buffer (last 10 minutes)
	minuteBuckets      [10]int64
	minuteBucketBaseTs int64 // unix minute for index 0 (current minute after advance)

	// Count by "METHOD STATUS" key e.g. "GET 200"
	reqByMethodStatus = map[string]int64{}

	// Latency buckets per method (ms): keys like "10","25",...,"+Inf"
	latencyBoundsMs = []int64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000}
	latencyByMethod = map[string]map[string]int64{}

	// Active users last-seen timestamps; key is user identifier
	lastSeenUsers = map[string]time.Time{}
	activeWindow  = 5 * time.Minute
)

// expvar variables for easy machine consumption via /debug/vars
var (
	evStartedAt        = expvar.NewString("ttf_started_at")
	evUptimeSeconds    = expvar.NewInt("ttf_uptime_seconds")
	evTotalRequests    = expvar.NewInt("ttf_total_requests")
	evTotalErrors      = expvar.NewInt("ttf_total_errors")
	evTotalLatencyMs   = expvar.NewInt("ttf_total_latency_ms")
	evActiveUsers5Min  = expvar.NewInt("ttf_active_users_5m")
)

func init() {
	evStartedAt.Set(startedAt.UTC().Format(time.RFC3339))
	// Add metrics note
	expvar.NewString("ttf_metrics_note").Set("Lightweight app metrics; see ttf_* variables")
	// Publish structured maps via expvar.Func to snapshot under lock.
	expvar.Publish("ttf_requests_by_method_status", expvar.Func(func() interface{} {
		mu.RLock()
		defer mu.RUnlock()
		copy := make(map[string]int64, len(reqByMethodStatus))
		for k, v := range reqByMethodStatus {
			copy[k] = v
		}
		return copy
	}))
	expvar.Publish("ttf_request_duration_ms_buckets", expvar.Func(func() interface{} {
		mu.RLock()
		defer mu.RUnlock()
		outer := make(map[string]map[string]int64, len(latencyByMethod))
		for method, buckets := range latencyByMethod {
			bc := make(map[string]int64, len(buckets))
			for k, v := range buckets {
				bc[k] = v
			}
			outer[method] = bc
		}
		return outer
	}))
	expvar.Publish("ttf_requests_last_10m", expvar.Func(func() interface{} {
		mu.RLock()
		defer mu.RUnlock()
		// Return the 10 buckets with their minute offsets (0 = current minute)
		type bucket struct {
			Offset int   `json:"offset_min"`
			Count  int64 `json:"count"`
		}
		out := make([]bucket, 10)
		for i := 0; i < 10; i++ {
			out[i] = bucket{Offset: i, Count: minuteBuckets[i]}
		}
		return out
	}))
}

// Init starts background upkeep: uptime and active-users pruning.
func Init() {
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for now := range t.C {
			evUptimeSeconds.Set(now.Unix() - startedAt.Unix())
			updateActiveUsers(now)
		}
	}()
}

// Instrument wraps the handler to collect metrics.
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// Track user last-seen (prefer X-User-ID, fallback to client IP)
		if uk := userKeyFromRequest(r); uk != "" {
			mu.Lock()
			lastSeenUsers[uk] = start
			mu.Unlock()
		}

		next.ServeHTTP(rec, r)

		elapsed := time.Since(start)
		status := rec.status
		method := r.Method

		// Update counters under lock
		mu.Lock()
		totalRequests++
		if status >= 500 {
			totalErrors++
		}
		totalLatencyMs += elapsed.Milliseconds()

		// advance ring to current minute and increment current bucket (index 0)
		nowMin := time.Now().Unix() / 60
		advanceToMinLocked(nowMin)
		minuteBuckets[0]++

		// method+status counts
		key := method + " " + strconv.Itoa(status)
		reqByMethodStatus[key]++

		// latency buckets per method
		if _, ok := latencyByMethod[method]; !ok {
			latencyByMethod[method] = make(map[string]int64, len(latencyBoundsMs)+1)
		}
		bucket := bucketLabel(elapsed.Milliseconds())
		latencyByMethod[method][bucket]++
		mu.Unlock()

		// Reflect some counters to expvar (cheap atomic sets)
		evTotalRequests.Set(totalRequests)
		evTotalErrors.Set(totalErrors)
		evTotalLatencyMs.Set(totalLatencyMs)
	})
}

// StatsHandler returns a compact JSON snapshot suitable for humans and simple scripts.
func StatsHandler(w http.ResponseWriter, r *http.Request) {
	type Summary struct {
		StartedAt        string                      `json:"started_at"`
		UptimeSeconds    int64                       `json:"uptime_seconds"`
		TotalRequests    int64                       `json:"total_requests"`
		TotalErrors      int64                       `json:"total_errors"`
		ErrorRatePct     float64                     `json:"error_rate_pct"`
		AvgLatencyMs     float64                     `json:"avg_latency_ms"`
		RequestsLast1m   int64                       `json:"requests_last_1m"`
		RequestsLast5m   int64                       `json:"requests_last_5m"`
		RequestsLast10m  int64                       `json:"requests_last_10m"`
		ActiveUsers5m    int64                       `json:"active_users_5m"`
		ByMethodStatus   map[string]int64            `json:"requests_by_method_status"`
		LatencyBucketsMs map[string]map[string]int64 `json:"latency_buckets_ms"`
	}

	now := time.Now()
	uptime := now.Unix() - startedAt.Unix()

	// Work under write lock to align minute ring before summing and to copy maps safely
	mu.Lock()
	advanceToMinLocked(now.Unix() / 60)
	req1 := sumLastNLocked(1)
	req5 := sumLastNLocked(5)
	req10 := sumLastNLocked(10)
	errRate := 0.0
	if totalRequests > 0 {
		errRate = (float64(totalErrors) / float64(totalRequests)) * 100
	}
	avgLat := 0.0
	if totalRequests > 0 {
		avgLat = float64(totalLatencyMs) / float64(totalRequests)
	}
	au := int64(0)
	cutoff := now.Add(-activeWindow)
	for _, t := range lastSeenUsers {
		if t.After(cutoff) {
			au++
		}
	}
	// Deep copy maps for response
	byMS := make(map[string]int64, len(reqByMethodStatus))
	for k, v := range reqByMethodStatus {
		byMS[k] = v
	}
	lb := make(map[string]map[string]int64, len(latencyByMethod))
	for m, buckets := range latencyByMethod {
		bc := make(map[string]int64, len(buckets))
		for k, v := range buckets {
			bc[k] = v
		}
		lb[m] = bc
	}
	mu.Unlock()

	resp := Summary{
		StartedAt:        startedAt.UTC().Format(time.RFC3339),
		UptimeSeconds:    uptime,
		TotalRequests:    evTotalRequests.Value(),
		TotalErrors:      evTotalErrors.Value(),
		ErrorRatePct:     errRate,
		AvgLatencyMs:     avgLat,
		RequestsLast1m:   req1,
		RequestsLast5m:   req5,
		RequestsLast10m:  req10,
		ActiveUsers5m:    au,
		ByMethodStatus:   byMS,
		LatencyBucketsMs: lb,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// statusRecorder captures status codes.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Helpers below

func userKeyFromRequest(r *http.Request) string {
	// Prefer an auth user ID if you have one.
	if v := r.Header.Get("X-User-ID"); v != "" {
		return v
	}
	// Fallback to client IP (best-effort)
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		parts := strings.Split(fwd, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func bucketLabel(ms int64) string {
	for _, b := range latencyBoundsMs {
		if ms <= b {
			return strconv.FormatInt(b, 10)
		}
	}
	return "+Inf"
}

// advanceToMinLocked aligns the ring buffer to nowMin so that index 0 is the current minute.
func advanceToMinLocked(nowMin int64) {
	if minuteBucketBaseTs == 0 {
		minuteBucketBaseTs = nowMin
		return
	}
	if nowMin < minuteBucketBaseTs {
		// clock moved backward: reset
		for i := range minuteBuckets {
			minuteBuckets[i] = 0
		}
		minuteBucketBaseTs = nowMin
		return
	}
	delta := nowMin - minuteBucketBaseTs
	if delta == 0 {
		return
	}
	if delta >= 10 {
		for i := range minuteBuckets {
			minuteBuckets[i] = 0
		}
		minuteBucketBaseTs = nowMin
		return
	}
	// Slide forward delta minutes; clear newly-current indices
	for i := int64(0); i < delta; i++ {
		idx := int((i + 1) % 10)
		minuteBuckets[idx] = 0
	}
	minuteBucketBaseTs = nowMin
}

func sumLastNLocked(n int) int64 {
	if n <= 0 {
		return 0
	}
	if n > 10 {
		n = 10
	}
	sum := int64(0)
	for i := 0; i < n; i++ {
		sum += minuteBuckets[i]
	}
	return sum
}

func updateActiveUsers(now time.Time) {
	// prune old entries and set expvar
	cutoff := now.Add(-activeWindow)
	count := 0
	mu.Lock()
	for k, t := range lastSeenUsers {
		if t.Before(cutoff) {
			delete(lastSeenUsers, k)
			continue
		}
		count++
	}
	mu.Unlock()
	evActiveUsers5Min.Set(int64(count))
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
	
	response := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"updated":   updated,
		"errors":    errors,
		"message":   "Environment variables updated. Note: Some changes may require component restart to take effect.",
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