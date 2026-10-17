package models

type CompetitionEntry struct {
	Competition string `json:"competition"` // Konkurrenz
	SkillLevel  string `json:"skill_level"` // LK
}

type Tournament struct {
	Id        string             `json:"id"`
	Title     string             `json:"title"`
	URL       string             `json:"url"`
	Date      string             `json:"date"`
	Location  string             `json:"location"`
	Organizer string             `json:"organizer"`
	Lat       string             `json:"lat"`
	Lon       string             `json:"lon"`
	Entries   []CompetitionEntry `json:"entries"` // Competition-SkillLevel pairs
	// ApproximateLocation marks a tournament pinned at its federation's default
	// rather than at a place derived from its name.
	//
	// This cannot be inferred by comparing coordinates: several federation
	// defaults sit exactly on a major city, so a club that geocodes correctly
	// to München is indistinguishable from one that failed and fell back to the
	// BTV default. Recording the outcome where it is known is the only reliable
	// way to tell them apart.
	ApproximateLocation bool `json:"approximate_location,omitempty"`
}

type Geocoordinates struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
	// Address holds Nominatim's structured address (requires
	// addressdetails=1). Verifying the state via this field is far more
	// reliable than a substring match on DisplayName, which frequently omits
	// the state for districts and small settlements.
	Address     Address `json:"address,omitempty"`
	LastAttempt int64   `json:"last_attempt,omitempty"` // Unix timestamp of last geocoding attempt
	FailCount   int     `json:"fail_count,omitempty"`   // Number of consecutive failures
	IsFailed    bool    `json:"is_failed,omitempty"`    // Marks this as a failed geocoding attempt
}

// Address is the subset of Nominatim's structured address we rely on.
type Address struct {
	State        string `json:"state,omitempty"`
	City         string `json:"city,omitempty"`
	Town         string `json:"town,omitempty"`
	Village      string `json:"village,omitempty"`
	Municipality string `json:"municipality,omitempty"`
	Country      string `json:"country,omitempty"`
	CountryCode  string `json:"country_code,omitempty"`
}

// Place returns the most specific settlement name available.
func (a Address) Place() string {
	for _, v := range []string{a.City, a.Town, a.Village, a.Municipality} {
		if v != "" {
			return v
		}
	}
	return ""
}

type Federation struct {
	Id             string         `json:"id"`
	Url            string         `json:"url"`
	Name           string         `json:"name"`
	Geocoordinates Geocoordinates `json:"geocoordinates"`
	// State is the federation's primary state, kept for backwards
	// compatibility and used as the default when States is empty.
	State string `json:"state"`
	// States lists every state the federation covers. Several federations
	// span more than one (TVBB covers Berlin and Brandenburg, TNB covers
	// Niedersachsen and Bremen); accepting only the primary state made
	// tournaments in the other one fall back to default coordinates.
	States            []string `json:"states,omitempty"`
	ApiVersion        string   `json:"api_version"`
	TrustedProperties string   `json:"trusted_properties"`
}

// AcceptedStates returns every state a geocoding result may belong to.
func (f Federation) AcceptedStates() []string {
	if len(f.States) > 0 {
		return f.States
	}
	if f.State != "" {
		return []string{f.State}
	}
	return nil
}
