package models

type Tournament struct {
	Id        string `json:"id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Date      string `json:"date"`
	Location  string `json:"location"`
	Organizer string `json:"organizer"`
	Lat       string `json:"lat"`
	Lon       string `json:"lon"`
}

type Geocoordinates struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
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
