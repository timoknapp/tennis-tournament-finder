package metrics

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/timoknapp/tennis-tournament-finder/pkg/unresolved"
)

// UnresolvedClubsPath serves the clubs whose location could not be determined.
const UnresolvedClubsPath = "/stats/unresolved-clubs"

// unresolvedResponse is the shape served at UnresolvedClubsPath.
type unresolvedResponse struct {
	// Count is the number of distinct clubs currently recorded.
	Count int `json:"count"`
	// Dropped is non-zero when the registry filled up, meaning the list is
	// incomplete. Surfaced rather than hidden so a truncated list is never
	// mistaken for a short one.
	Dropped int `json:"dropped,omitempty"`
	// Clubs is ordered by how many tournaments each one affects.
	Clubs []unresolved.Entry `json:"clubs"`
	// Stubs are ready to paste into club-locations.json, with the city left as
	// TODO so it has to be filled in deliberately rather than guessed.
	Stubs []stub `json:"club_locations_stubs"`
}

// stub mirrors an entry in club-locations.json.
type stub struct {
	Contains string `json:"contains"`
	City     string `json:"city"`
	State    string `json:"state,omitempty"`
	Note     string `json:"note,omitempty"`
}

// UnresolvedClubsHandler reports the clubs that fell back to their federation's
// default pin, together with paste-ready overrides.
//
// Wrong pins used to be invisible: a tournament sitting in the middle of a
// state looks like a working map, not a failure. This makes the backlog
// explicit, so correcting it is routine maintenance.
func UnresolvedClubsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clubs := unresolved.Snapshot()

	stubs := make([]stub, 0, len(clubs))
	for _, club := range clubs {
		note := "fell back to the " + club.Federation + " default pin"
		if len(club.Candidates) > 0 {
			note += "; tried: " + strings.Join(club.Candidates, ", ")
		} else {
			note += "; no place name could be extracted from the club name"
		}

		stubs = append(stubs, stub{
			// The organizer string is matched with `contains` so minor
			// differences in how a federation writes the name still match.
			Contains: club.Organizer,
			City:     "TODO",
			State:    club.State,
			Note:     note,
		})
	}

	response := unresolvedResponse{
		Count:   len(clubs),
		Dropped: unresolved.Dropped(),
		Clubs:   clubs,
		Stubs:   stubs,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(response); err != nil {
		// The status is already written by this point, so the client sees a
		// truncated body; logging is all that is left.
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
