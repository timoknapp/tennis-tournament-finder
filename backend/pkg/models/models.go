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
}

type Geocoordinates struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
	LastAttempt int64  `json:"last_attempt,omitempty"` // Unix timestamp of last geocoding attempt
	FailCount   int    `json:"fail_count,omitempty"`   // Number of consecutive failures
	IsFailed    bool   `json:"is_failed,omitempty"`    // Marks this as a failed geocoding attempt
}

type Federation struct {
	Id                string         `json:"id"`
	Url               string         `json:"url"`
	Name              string         `json:"name"`
	Geocoordinates    Geocoordinates `json:"geocoordinates"`
	State             string         `json:"state"`
	ApiVersion        string         `json:"api_version"`
	TrustedProperties string         `json:"trusted_properties"`
}
