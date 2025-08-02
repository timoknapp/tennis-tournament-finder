package federation

import "github.com/timoknapp/tennis-tournament-finder/pkg/models"

func GetFederations() []models.Federation {
	federations := []models.Federation{
		{
			Id:                "BAD",
			Url:               "https://baden.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar",
			Name:              "Badischer Tennisverband",
			Geocoordinates:    models.Geocoordinates{Lat: "49.34003", Lon: "8.68514"},
			State:             "Baden-Württemberg",
			ApiVersion:        "old",
			TrustedProperties: "",
		},
		{
			Id:                "HTV",
			Url:               "https://htv.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar",
			Name:              "Hessischer Tennisverband",
			Geocoordinates:    models.Geocoordinates{Lat: "50.0770372", Lon: "8.7553832"},
			State:             "Hessen",
			ApiVersion:        "old",
			TrustedProperties: "",
		},
		{
			Id:                "RLP",
			Url:               "https://www.rlp-tennis.de/spielbetrieb/turniere/appTournament.html",
			Name:              "Rheinland-Pfälzischer Tennisverband",
			Geocoordinates:    models.Geocoordinates{Lat: "49.8335079", Lon: "8.0138431"},
			State:             "Rheinland-Pfalz",
			ApiVersion:        "new",
			TrustedProperties: "{\"tournamentsFilter\":{\"ageCategory\":1,\"ageGroupJuniors\":1,\"ageGroupSeniors\":1,\"circuit\":1,\"region\":1,\"organizerRegion\":1,\"fedRankValuation\":1,\"nationalValuation\":1,\"fedRank\":1,\"name\":1,\"city\":1,\"startDate\":1,\"endDate\":1,\"firstResult\":1,\"maxResults\":1}}147ad25c14aa9b88f132c65e3c4de2e6992acf37",
		},
		{
			Id:                "STV",
			Url:               "https://stv.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar",
			Name:              "Sächsischer Tennisverband",
			Geocoordinates:    models.Geocoordinates{Lat: "51.3633218", Lon: "12.4132917"},
			State:             "Sachsen",
			ApiVersion:        "old",
			TrustedProperties: "",
		},
		{
			Id:                "TMV",
			Url:               "https://tmv.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar",
			Name:              "Tennisverband Mecklenburg-Vorpommern",
			Geocoordinates:    models.Geocoordinates{Lat: "54.0829601", Lon: "12.0889703"},
			State:             "Mecklenburg-Vorpommern",
			ApiVersion:        "old",
			TrustedProperties: "",
		},
		{
			Id:                "TSA",
			Url:               "https://tsa.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar",
			Name:              "Tennisverband Sachsen-Anhalt",
			Geocoordinates:    models.Geocoordinates{Lat: "52.1063933", Lon: "11.6015097"},
			State:             "Sachsen-Anhalt",
			ApiVersion:        "old",
			TrustedProperties: "",
		},
		{
			Id:                "TTV",
			Url:               "https://ttv.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar",
			Name:              "Thüringer Tennisverband",
			Geocoordinates:    models.Geocoordinates{Lat: "51.0012441", Lon: "11.3327579"},
			State:             "Thüringen",
			ApiVersion:        "old",
			TrustedProperties: "",
		},
		{
			Id:                "TVN",
			Url:               "https://tvn.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar",
			Name:              "Tennisverband Niederrhein",
			Geocoordinates:    models.Geocoordinates{Lat: "51.4784721", Lon: "6.9804422"},
			State:             "Nordrhein-Westfalen",
			ApiVersion:        "old",
			TrustedProperties: "",
		},
		{
			Id:                "WTB",
			Url:               "https://www.wtb-tennis.de/wettkampfsport/turniere/turnierkalender.html",
			Name:              "Württembergischer Tennisbund",
			Geocoordinates:    models.Geocoordinates{Lat: "48.853488", Lon: "9.1373019"},
			State:             "Baden-Württemberg",
			ApiVersion:        "new",
			TrustedProperties: "{\"tournamentsFilter\":{\"ageCategory\":1,\"ageGroupJuniors\":1,\"ageGroupSeniors\":1,\"circuit\":1,\"fedRankValuation\":1,\"nationalValuation\":1,\"type\":1,\"fedRank\":1,\"region\":1,\"name\":1,\"city\":1,\"startDate\":1,\"endDate\":1,\"firstResult\":1,\"maxResults\":1}}159ecd19ddd43b30fbc8e35aea82f7bf7373a592",
		},
	}
	return federations
}
