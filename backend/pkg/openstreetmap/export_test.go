package openstreetmap

import "sync"

// resetRateLimiterForTest rebuilds the process-wide geocoding limiter so tests
// can exercise different intervals. It is only compiled into test binaries.
func resetRateLimiterForTest() {
	geocodingLimiterOnce = sync.Once{}
	geocodingLimiter = nil
}
