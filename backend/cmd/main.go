package main

import (
	"net/http"

	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/openstreetmap"
	"github.com/timoknapp/tennis-tournament-finder/pkg/tournament"
)

func main() {
	logger.Info("Starting Tennis Tournament Finder backend server...")

	openstreetmap.InitCache()
	logger.Info("OpenStreetMap cache initialized")

	http.HandleFunc("/", tournament.GetTournaments)
	logger.Info("Starting HTTP server on port 8080...")

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		logger.Error("Server failed to start: %v", err)
	}
}
