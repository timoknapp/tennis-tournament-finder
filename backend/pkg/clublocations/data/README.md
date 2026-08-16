# Correcting a map pin

Federations publish a club name and nothing else. Most names contain a usable
place ("TC Karlsruhe"); some do not ("Lohausener Sport-Verein"). When no place
can be extracted, the tournament is pinned at the middle of the federation's
state, which looks like a working map rather than a failure.

`club-locations.json` is where those are corrected, without touching any code.

## Finding what needs fixing

The running service records every club it could not place:

    curl -s http://127.0.0.1:9090/stats/unresolved-clubs | jq

The response includes `club_locations_stubs`, which are entries ready to paste
into this file with the city left as `TODO`. It is bound to the diagnostics
port, which listens on localhost only.

For a deliberate sweep rather than whatever the service happened to serve:

    go run ./cmd/pincheck -days 45          # table of affected clubs
    go run ./cmd/pincheck -days 45 -json    # the same as override stubs

That makes live requests and is rate limited by Nominatim, so a full run across
all federations takes a while. It is never run by CI.

## Writing an entry

    {
      "contains": "Lohausener Sport-Verein",
      "city": "Düsseldorf",
      "state": "Nordrhein-Westfalen",
      "note": "Lohausen is a district of Düsseldorf; not derivable from the name."
    }

* `contains` matches anywhere in the organizer name; `match` compares the whole
  string. Both ignore case, punctuation and extra spaces.
* Exact matches win over `contains`; among `contains` entries the longest
  pattern wins, so a specific rule beats a generic one.
* Prefer `city`, which is geocoded like any other place name. Use `lat`/`lon`
  only when even the city is ambiguous.
* `state` restricts the geocoding result and is worth setting whenever the same
  place name exists in several states.
* Write a `note` saying *why* the name is not resolvable. The next person
  cannot tell a district from a typo without it.

Leave the city as `TODO` rather than guessing: a wrong pin is worse than an
obvious gap, because it looks resolved.
