// Package unresolved records clubs whose location could not be determined, so
// they can be corrected in club-locations.json instead of silently sitting on
// their federation's default pin.
//
// Federations publish an organizer name and nothing else. Most names contain a
// usable place ("TC Karlsruhe"), but some do not ("Lohausener Sport-Verein"),
// and those tournaments end up pinned at the middle of the federation's state.
// That looked like a working map rather than a failure, so nobody noticed.
//
// This registry makes the failures visible while the service runs, which is
// what turns fixing them into routine maintenance rather than an investigation.
package unresolved

import (
	"sort"
	"sync"
	"time"
)

// maxEntries bounds the registry. Organizer names come from upstream, so this
// is untrusted input and must not be allowed to grow without limit. The real
// number of distinct clubs is in the hundreds; anything beyond this is a sign
// something is wrong upstream, and dropping the excess is safer than growing
// forever.
const maxEntries = 500

// Entry is one club that could not be placed.
type Entry struct {
	// Organizer is the club name exactly as the federation published it, so it
	// can be pasted into club-locations.json unchanged.
	Organizer string `json:"organizer"`
	// Federation and State say where the fallback pin landed.
	Federation string `json:"federation"`
	State      string `json:"state"`
	// Candidates are the place names that were tried, which usually shows why
	// the extraction failed.
	Candidates []string `json:"candidates,omitempty"`
	// Count is how many tournaments were affected since the process started.
	Count int `json:"count"`
	// FirstSeen and LastSeen bound the observations.
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

type registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	dropped int
}

var reg = &registry{entries: make(map[string]*Entry)}

// Record notes that a tournament fell back to its federation's default pin.
// It is safe to call from the goroutines that fetch federations in parallel.
func Record(organizer, federation, state string, candidates []string) {
	if organizer == "" {
		return
	}

	now := time.Now().UTC()
	key := federation + "|" + organizer

	reg.mu.Lock()
	defer reg.mu.Unlock()

	if entry, ok := reg.entries[key]; ok {
		entry.Count++
		entry.LastSeen = now
		return
	}

	if len(reg.entries) >= maxEntries {
		reg.dropped++
		return
	}

	// Copy the candidates: the caller owns that slice and may reuse it.
	cands := make([]string, len(candidates))
	copy(cands, candidates)

	reg.entries[key] = &Entry{
		Organizer:  organizer,
		Federation: federation,
		State:      state,
		Candidates: cands,
		Count:      1,
		FirstSeen:  now,
		LastSeen:   now,
	}
}

// Snapshot returns the recorded clubs, most affected first, so the entries
// worth fixing come first.
func Snapshot() []Entry {
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	list := make([]Entry, 0, len(reg.entries))
	for _, entry := range reg.entries {
		list = append(list, *entry)
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		// Stable order for equal counts, so repeated calls agree.
		if list[i].Federation != list[j].Federation {
			return list[i].Federation < list[j].Federation
		}
		return list[i].Organizer < list[j].Organizer
	})

	return list
}

// Dropped reports how many distinct clubs were discarded because the registry
// was full. A non-zero value means the snapshot is incomplete.
func Dropped() int {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return reg.dropped
}

// Reset clears the registry. Used by tests.
func Reset() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.entries = make(map[string]*Entry)
	reg.dropped = 0
}
