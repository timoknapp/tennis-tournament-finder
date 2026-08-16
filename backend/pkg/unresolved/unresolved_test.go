package unresolved

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRecordCountsRepeatsWithoutDuplicating(t *testing.T) {
	Reset()

	Record("TC Beispiel", "BAD", "Baden-Württemberg", []string{"Beispiel"})
	Record("TC Beispiel", "BAD", "Baden-Württemberg", []string{"Beispiel"})
	Record("TC Beispiel", "BAD", "Baden-Württemberg", []string{"Beispiel"})

	snapshot := Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 club, got %d", len(snapshot))
	}
	if snapshot[0].Count != 3 {
		t.Errorf("expected a count of 3, got %d", snapshot[0].Count)
	}
}

func TestSameNameInDifferentFederationsStaysSeparate(t *testing.T) {
	Reset()

	// Club names are not unique across federations, and the correct city can
	// differ, so the federation has to be part of the identity.
	Record("TC Rot-Weiß", "BAD", "Baden-Württemberg", nil)
	Record("TC Rot-Weiß", "WTB", "Baden-Württemberg", nil)

	if got := len(Snapshot()); got != 2 {
		t.Fatalf("expected 2 separate entries, got %d", got)
	}
}

func TestSnapshotOrdersMostAffectedFirst(t *testing.T) {
	Reset()

	Record("Selten", "BAD", "Baden-Württemberg", nil)
	for i := 0; i < 5; i++ {
		Record("Häufig", "BAD", "Baden-Württemberg", nil)
	}
	for i := 0; i < 3; i++ {
		Record("Mittel", "BAD", "Baden-Württemberg", nil)
	}

	snapshot := Snapshot()
	want := []string{"Häufig", "Mittel", "Selten"}
	for i, name := range want {
		if snapshot[i].Organizer != name {
			t.Errorf("position %d: expected %q, got %q", i, name, snapshot[i].Organizer)
		}
	}
}

func TestSnapshotOrderIsStableForEqualCounts(t *testing.T) {
	Reset()

	Record("B-Verein", "WTB", "Baden-Württemberg", nil)
	Record("A-Verein", "WTB", "Baden-Württemberg", nil)
	Record("C-Verein", "BAD", "Baden-Württemberg", nil)

	first := Snapshot()
	for i := 0; i < 5; i++ {
		next := Snapshot()
		for j := range first {
			if first[j].Organizer != next[j].Organizer {
				t.Fatalf("order changed between snapshots at %d: %q then %q",
					j, first[j].Organizer, next[j].Organizer)
			}
		}
	}
}

func TestRegistryIsBounded(t *testing.T) {
	Reset()

	// Organizer names come from upstream and are untrusted, so the registry
	// must not grow without limit.
	for i := 0; i < maxEntries+50; i++ {
		Record(fmt.Sprintf("Verein %d", i), "BAD", "Baden-Württemberg", nil)
	}

	if got := len(Snapshot()); got != maxEntries {
		t.Errorf("expected the registry to cap at %d, got %d", maxEntries, got)
	}
	if Dropped() != 50 {
		t.Errorf("expected 50 dropped entries, got %d", Dropped())
	}
}

func TestKnownClubsStillCountWhenFull(t *testing.T) {
	Reset()

	Record("Bekannter Verein", "BAD", "Baden-Württemberg", nil)
	for i := 0; i < maxEntries; i++ {
		Record(fmt.Sprintf("Verein %d", i), "BAD", "Baden-Württemberg", nil)
	}

	// A club already in the registry must keep counting even once it is full,
	// otherwise the busiest entries stop reflecting reality exactly when the
	// list matters most.
	Record("Bekannter Verein", "BAD", "Baden-Württemberg", nil)

	for _, entry := range Snapshot() {
		if entry.Organizer == "Bekannter Verein" {
			if entry.Count != 2 {
				t.Errorf("expected the known club to reach a count of 2, got %d", entry.Count)
			}
			return
		}
	}
	t.Error("the known club disappeared from the registry")
}

func TestEmptyOrganizerIsIgnored(t *testing.T) {
	Reset()

	Record("", "BAD", "Baden-Württemberg", nil)

	if got := len(Snapshot()); got != 0 {
		t.Errorf("expected an empty organizer to be ignored, got %d entries", got)
	}
}

func TestCandidatesAreCopied(t *testing.T) {
	Reset()

	candidates := []string{"Karlsruhe"}
	Record("TC Beispiel", "BAD", "Baden-Württemberg", candidates)

	// The caller owns that slice and may reuse it for the next tournament.
	candidates[0] = "überschrieben"

	snapshot := Snapshot()
	if snapshot[0].Candidates[0] != "Karlsruhe" {
		t.Errorf("candidates were not copied: got %q", snapshot[0].Candidates[0])
	}
}

func TestSnapshotDoesNotExposeInternalState(t *testing.T) {
	Reset()

	Record("TC Beispiel", "BAD", "Baden-Württemberg", []string{"Beispiel"})

	snapshot := Snapshot()
	snapshot[0].Count = 999
	snapshot[0].Organizer = "geändert"

	fresh := Snapshot()
	if fresh[0].Count != 1 || fresh[0].Organizer != "TC Beispiel" {
		t.Error("mutating a snapshot changed the registry")
	}
}

func TestConcurrentRecording(t *testing.T) {
	Reset()

	// Federations are fetched in parallel, so Record runs from several
	// goroutines at once. Run with -race.
	const workers = 8
	const perWorker = 50

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				// Half the writes contend on one key, half spread out.
				if i%2 == 0 {
					Record("Gemeinsamer Verein", "BAD", "Baden-Württemberg", nil)
				} else {
					Record(fmt.Sprintf("Verein %d-%d", worker, i), "BAD", "Baden-Württemberg", nil)
				}
			}
		}(w)
	}
	wg.Wait()

	var shared int
	for _, entry := range Snapshot() {
		if entry.Organizer == "Gemeinsamer Verein" {
			shared = entry.Count
		}
	}

	if want := workers * perWorker / 2; shared != want {
		t.Errorf("expected %d recordings of the shared club, got %d", want, shared)
	}
}

func TestConcurrentSnapshotDuringRecording(t *testing.T) {
	Reset()

	// Reading while writing is the realistic case: the endpoint can be hit
	// mid-fetch.
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
				Record(fmt.Sprintf("Verein %d", i%20), "BAD", "Baden-Württemberg", nil)
			}
		}
	}()

	for i := 0; i < 100; i++ {
		for _, entry := range Snapshot() {
			if !strings.HasPrefix(entry.Organizer, "Verein") {
				t.Errorf("unexpected entry during concurrent access: %q", entry.Organizer)
			}
		}
	}

	close(done)
	wg.Wait()
}
