package main

import (
	"log"
	"net/http"

	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

func main() {
	openstreetmap.InitCache()
	http.HandleFunc("/", tournament.GetTournaments)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
