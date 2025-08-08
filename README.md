<h1 align="center" style="border-bottom: none;">🎾 Tennis Tournament Finder</h1>
<h3 align="center">A simple Map showing all recent tennis tournaments for passionate tennis players in Germany.</h3>
<p align="center">
    <a href="https://github.com/timoknapp/tennis-tournament-finder/actions/workflows/backend.yml">
        <img alt="Build Backend" src="https://github.com/timoknapp/tennis-tournament-finder/actions/workflows/backend.yml/badge.svg?branch=master">
    </a>
    <a href="https://github.com/timoknapp/tennis-tournament-finder/actions/workflows/pages/pages-build-deployment">
        <img alt="Build Frontend" src="https://github.com/timoknapp/tennis-tournament-finder/actions/workflows/pages/pages-build-deployment/badge.svg?branch=master">
    </a>
</p>
<img width="100%" src="images/demo.jpg">

## Getting Started

[Try it out!](https://timoknapp.github.io/tennis-tournament-finder/)

## Features

* Currently supported tennis federations:
  * [Badischer Tennis Verband (BAD)](https://www.badischertennisverband.de/)
  * [Hessischer Tennis Verband (HTV)](https://www.htv-tennis.de/)
  * [Rheinland-Pfälzischer Tennis Verband (RPTV)](https://www.rlp-tennis.de/)
  * [Sächsischer Tennis Verband (STV)](https://www.stv-tennis.de)
  * [Tennisverband Mecklenburg-Vorpommern (TMV)](https://www.tennis-mv.de)
  * [Tennisverband Sachsen-Anhalt (TSA)](https://www.tennis-tsa.de)
  * [Thüringer Tennis-Verband (TTV)](https://www.ttv-tennis.de)
  * [Tennis-Verband Niederrhein (TVN)](https://www.tvn-tennis.de)
  * [Württembergischer Tennis Bund (WTB)](https://www.wtb-tennis.de/)
* Helps you finding the tournaments around you
* Lets you filter tournaments by date, competition type, and federation
* Short link to the official Tournament at [tennis.de](https://spieler.tennis.de/web/guest/turniersuche) in order to sign up for the tournament
* Link to address on Google Maps.
* PWAs (Progressive Web Apps) support. You can install the app on your phone.

### Soon

* Support for more tennis federations:
  * [Bayerischer Tennis Verband (BTV)](https://www.btv.de)
  * [Tennis-Verband Berlin-Brandenburg (TVBB)](https://www.tvbb.de)
  * [Hamburger Tennis-Verband](https://www.hamburger-tennisverband.de)
  * [Tennisverband Mittelrhein (TVM)](https://www.tvm-tennis.de)
  * [Tennisverband Niedersachsen-Bremen (TNB)](https://www.tnb-tennis.de)
  * [Saarländischer Tennisbund (STB)](https://www.stb-tennis.de)
  * [Tennisverband Schleswig-Holstein (TSH)](https://www.tennis.sh)
  * [Westfälischer Tennis-Verband (WTV)](https://www.wtv.de)
* Store favorite tournaments (locally)

## Backend Development

### Running the Backend Server

The backend is written in Go and provides the API for fetching tournament data from various tennis federations.

```bash
cd backend
go run ./cmd/main.go
```

### Log Level Configuration

The backend supports configurable log levels via the `TTF_LOG_LEVEL` environment variable:

**Available Log Levels:**

* `DEBUG` - Detailed debugging information (shows all logs)
* `INFO` - General information messages (default, recommended for production)
* `WARN` - Warning messages only
* `ERROR` - Error messages only

**Usage Examples:**

```bash
# Debug mode (shows everything)
TTF_LOG_LEVEL=DEBUG go run ./cmd/main.go

# Production mode (default)
TTF_LOG_LEVEL=INFO go run ./cmd/main.go

# Minimal logging (errors only)
TTF_LOG_LEVEL=ERROR go run ./cmd/main.go

# Set persistently in your shell
export TTF_LOG_LEVEL=DEBUG
go run ./cmd/main.go
```

**Log Output Format:**

```text
[2025-08-06T14:23:45.123Z] INFO: Starting Tennis Tournament Finder backend server...
[2025-08-06T14:23:45.124Z] DEBUG: Cache HIT (location): location_key for tournament 12345
[2025-08-06T14:23:45.125Z] INFO: Get Tournaments from: 03.08.2025 to: 10.08.2025
```

All timestamps are in UTC for consistency across time zones.

### Scheduler

Enable the in-process scheduler purely via env vars (no admin endpoint required):

```bash
export TTF_SCHEDULER_ENABLED=true
export TTF_SCHEDULER_CRON="0 2 * * *"         # default: 02:00 daily
export TTF_SCHEDULER_COMP_TYPE=""              # optional
export TTF_SCHEDULER_FEDERATIONS=""            # optional
```

For more details, see docs/scheduler.md.

## FAQ

### 1. Tournament is not shown with the correct location on the map

This is a known issue. The location of the tournament is not always correct. This is due to the fact that [OSM](https://www.openstreetmap.de) is not always capable of performing the geocoding right. There are two potential outcomes:
  
  1. Tournament location falls back to the default address of the corresponding tennis federation. There will then be a list of tournaments associated to the default address.
     * <img width="20%" src="images/geocoordsNotFound.png">
  2. Tournament location is showing a completely different location. In this case please click on the link next to "Adresse". This will then lead you to the address on [Google Maps](http://maps.google.com) and this location is mostly correct.
