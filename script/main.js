let map = L.map('map', {
  zoomSnap: 0,
  zoomDelta: 0.5,
  doubleClickZoom: true,
  inertia: true,
  zoomAnimation: true
}).setView([51.133481, 10.018343], 7);
window.map = map; // expose map for gesture script
L.tileLayer('http://a.tile.openstreetmap.fr/hot/{z}/{x}/{y}.png', {
  updateWhenZooming: true,   // fetch tiles during zoom animation
  keepBuffer: 3              // keep extra tiles around to reduce visible loads
}).addTo(map);
let markers = L.markerClusterGroup();

// const urlBackend = "http://localhost:8080"
const urlBackend = "https://timoknapp.com/ttf"
const urlGoogleQuery = "https://maps.google.com/maps?q="

const initDateFrom = new Date(Date.now());
const initDateTo = new Date(Date.now()+(7*86400000));

document.getElementById('dateFrom').value = formatDateToInput(initDateFrom);
document.getElementById('dateTo').value = formatDateToInput(initDateTo);

const loadingDiv = document.getElementById('loading');

// Initialize mobile filter visibility
document.addEventListener('DOMContentLoaded', function() {
    initializeMobileFilters();
    setupFederationLimits();
});

function initializeMobileFilters() {
    const filterContainer = document.getElementById('filterContainer');
    const toggleBtn = document.getElementById('filterToggle');
    
    if (filterContainer && toggleBtn) {
        // Check if we're in portrait mode (mobile)
        if (window.matchMedia("(orientation: portrait)").matches) {
            // Show filters by default on mobile
            filterContainer.style.display = 'block';
            toggleBtn.innerHTML = 'Filter ✕';
        } else {
            // Always show filters on desktop/landscape
            filterContainer.style.display = 'block';
            toggleBtn.innerHTML = 'Filter ✕';
        }
    }
}

// Listen for orientation changes
window.addEventListener('orientationchange', function() {
    setTimeout(initializeMobileFilters, 100); // Small delay to ensure orientation change is complete
});

// Remove automatic initial request - user must manually submit
// getTournamentsByDate(initDateFrom, initDateTo, "", getSelectedFederations());

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
                        <a href="#" onclick="toggleCompetitionDetails('${tournament.id}'); return false;" class="popup-info-text" style="color: #0066cc; text-decoration: none;">
                            <span id="toggle-text-${tournament.id}">▼ Konkurrenzen anzeigen (${validEntries.length} Einträge)</span>
                        </a>`;
                    }
                }

                const marker = L.marker([tournament["lat"], tournament["lon"]])
                .bindPopup(`
                <span class="popupTitle">${tournament["title"]}</span><br><br>
                <div class="popup-info-text">
                    <b>Datum:</b> ${tournament["date"]}<br>
                    <b>Adresse:</b> <a target="_blank" href="${urlGoogleQuery+tournament["organizer"]}">${tournament["organizer"]}</a><br>
                </div>
                <div class="button-container">
                    <a href="${tournament["url"]}" target="_blank" class="signup-button">Anmelden</a>
                </div>
                ${competitionDetails}
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
    // Only select the first 2 federations
    checkboxes.forEach((checkbox, index) => {
        checkbox.checked = index < 2;
    });
    updateFederationSelectionState();
}

function deselectAllFederations() {
    const checkboxes = document.querySelectorAll('input[name="federations"]');
    checkboxes.forEach(checkbox => checkbox.checked = false);
    updateFederationSelectionState();
}

function setupFederationLimits() {
    const checkboxes = document.querySelectorAll('input[name="federations"]');
    
    checkboxes.forEach(checkbox => {
        checkbox.addEventListener('change', function() {
            const checkedBoxes = document.querySelectorAll('input[name="federations"]:checked');
            
            if (checkedBoxes.length > 2) {
                // If more than 2 are selected, uncheck the current one
                this.checked = false;
                alert('Sie können maximal 2 Verbände gleichzeitig auswählen, um die Serverbelastung zu reduzieren.');
            }
            
            updateFederationSelectionState();
        });
    });
    
    // Initialize with first 2 federations selected
    checkboxes.forEach((checkbox, index) => {
        checkbox.checked = index < 2;
    });
    updateFederationSelectionState();
}

function updateFederationSelectionState() {
    const checkboxes = document.querySelectorAll('input[name="federations"]');
    const checkedBoxes = document.querySelectorAll('input[name="federations"]:checked');
    
    // Disable unchecked boxes if 2 are already selected
    checkboxes.forEach(checkbox => {
        if (!checkbox.checked && checkedBoxes.length >= 2) {
            checkbox.disabled = true;
        } else {
            checkbox.disabled = false;
        }
    });
}

function toggleFilters() {
    const filterContainer = document.getElementById('filterContainer');
    const toggleBtn = document.getElementById('filterToggle');
    
    if (filterContainer.style.display === 'none') {
        filterContainer.style.display = 'block';
        toggleBtn.innerHTML = 'Filter ✕';
    } else {
        filterContainer.style.display = 'none';
        toggleBtn.innerHTML = 'Filter ⚙️';
    }
}