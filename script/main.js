let map = L.map('map').setView([51.133481, 10.018343], 7);
L.tileLayer('http://a.tile.openstreetmap.fr/hot/{z}/{x}/{y}.png').addTo(map);
let markers = L.markerClusterGroup();

// const urlBackend = "http://localhost:8080"
const urlBackend = "https://timoknapp.com/ttf"
const urlGoogleQuery = "https://maps.google.com/maps?q="

const initDateFrom = new Date(Date.now());
const initDateTo = new Date(Date.now()+(7*86400000));

document.getElementById('dateFrom').value = formatDateToInput(initDateFrom);
document.getElementById('dateTo').value = formatDateToInput(initDateTo);

const loadingDiv = document.getElementById('loading');

getTournamentsByDate(initDateFrom, initDateTo, "", getSelectedFederations());

function getTournamentsByDate(dateFrom, dateTo, compType, federations) {
    if (dateFrom != "" && dateTo != "") {
        dateFrom = formatDateToAPI(dateFrom);
        dateTo = formatDateToAPI(dateTo);
        getTournaments(dateFrom, dateTo, compType, federations)
        .then(tournaments => {
            map.removeLayer(markers);
            markers = L.markerClusterGroup();
            for (const tournament of tournaments) {
                // Process competition entries for display
                let competitionDetails = "";
                
                if (tournament.entries && tournament.entries.length > 0) {
                    // Create detailed competition list
                    const validEntries = tournament.entries.filter(entry => 
                        entry.competition || (entry.skill_level && entry.skill_level.trim() !== ""));
                    
                    if (validEntries.length > 0) {
                        competitionDetails = `
                        <div id="comp-details-${tournament.id}" style="display: none; max-height: 150px; overflow-y: auto; margin-top: 5px; padding: 5px; background-color: #f9f9f9; border-radius: 3px;">
                            <table style="width: 100%; font-size: 12px;">
                                <tr style="font-weight: bold;"><td>Konkurrenz</td><td>LK</td></tr>
                                ${validEntries.map(entry => `
                                    <tr>
                                        <td style="padding: 2px;">${entry.competition || "-"}</td>
                                        <td style="padding: 2px;">${entry.skill_level || "-"}</td>
                                    </tr>
                                `).join("")}
                            </table>
                        </div>
                        <a href="#" onclick="toggleCompetitionDetails('${tournament.id}'); return false;" style="font-size: 12px; color: #0066cc;">
                            <span id="toggle-text-${tournament.id}">▼ Konkurrenzen anzeigen (${validEntries.length} Einträge)</span>
                        </a>`;
                    }
                }

                const marker = L.marker([tournament["lat"], tournament["lon"]])
                .bindPopup(`
                <span class="popupTitle"><b>${tournament["title"]}</b></span><br><br>
                <b>Datum:</b> ${tournament["date"]}<br>
                <b>Adresse:</b> <a target="_blank" href="${urlGoogleQuery+tournament["organizer"]}">${tournament["organizer"]}</a><br>
                <b>Anmeldung:</b> <a target="_blank" href="${tournament["url"]}">auf tennis.de (login erforderlich)</a><br>
                <br>${competitionDetails}
                `)
                markers.addLayer(marker);
            }
            map.addLayer(markers);
        });
    }
}

async function getTournaments(dateFrom, dateTo, compType, federations) {
    showSpinner();
    let url = urlBackend + `?dateFrom=${dateFrom}&dateTo=${dateTo}`;
    if (compType && compType !== "") {
        url += `&compType=${encodeURIComponent(compType)}`;
    }
    if (federations && federations.length > 0) {
        url += `&federations=${encodeURIComponent(federations.join(','))}`;
    }
    return fetch(url)
        .then(res => res.json())
        .then(result => {
            hideSpinner();
            // console.log(result);
            return result;
        })
        .catch(error => {
            hideSpinner();
            console.log('error', error);
            return [
                {
                    title: "Test Tennis Turnier",
                    url: "https://spieler.tennis.de",
                    date: "01.01. bis 02.01.",
                    location: "Karslruhe",
                    organizer: "MTV Karslruhe",
                    lat: "49.0229711",
                    lon: "8.4179256"
                }
            ]
        });
}

function padTo2Digits(num) {
    return num.toString().padStart(2, '0');
}

function formatDateToAPI(date) {
    if (!(date instanceof Date)) {
        date = new Date (date);
    }
    return [
        padTo2Digits(date.getDate()),
        padTo2Digits(date.getMonth() + 1),
        date.getFullYear(),
    ].join('.');
}

// Function to format date to YYYY-MM-DD
function formatDateToInput(date) {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
}

function showSpinner() {
  loadingDiv.style.visibility = 'visible';
}

function hideSpinner() {
  loadingDiv.style.visibility = 'hidden';
}

function toggleCompetitionDetails(tournamentId) {
    const detailsDiv = document.getElementById(`comp-details-${tournamentId}`);
    const toggleText = document.getElementById(`toggle-text-${tournamentId}`);
    
    if (detailsDiv.style.display === 'none') {
        detailsDiv.style.display = 'block';
        toggleText.innerHTML = '▲ Konkurrenzen ausblenden';
    } else {
        detailsDiv.style.display = 'none';
        const entriesCount = detailsDiv.querySelectorAll('tr').length - 1; // Subtract header row
        toggleText.innerHTML = `▼ Konkurrenzen anzeigen (${entriesCount} Einträge)`;
    }
}

function getSelectedFederations() {
    const checkboxes = document.querySelectorAll('input[name="federations"]:checked');
    return Array.from(checkboxes).map(checkbox => checkbox.value);
}

function selectAllFederations() {
    const checkboxes = document.querySelectorAll('input[name="federations"]');
    checkboxes.forEach(checkbox => checkbox.checked = true);
}

function deselectAllFederations() {
    const checkboxes = document.querySelectorAll('input[name="federations"]');
    checkboxes.forEach(checkbox => checkbox.checked = false);
}