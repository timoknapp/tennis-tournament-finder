package openstreetmap

import (
	"os"
	"sync"

	"github.com/timoknapp/tennis-tournament-finder/pkg/clublocations"
)

// resetRateLimiterForTest rebuilds the process-wide geocoding limiter so tests
// can exercise different intervals. It is only compiled into test binaries.
func resetRateLimiterForTest() {
	geocodingLimiterOnce = sync.Once{}
	geocodingLimiter = nil
}

// resetClubLocationsForTest clears the memoized override table so a test can
// point TTF_CLUB_LOCATIONS at its own fixture.
func resetClubLocationsForTest() {
	clublocations.ResetForTest()
}

// osWriteFile is a tiny helper so tests avoid importing os in several files.
func osWriteFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
