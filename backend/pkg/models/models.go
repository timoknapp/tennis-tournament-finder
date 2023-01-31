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
