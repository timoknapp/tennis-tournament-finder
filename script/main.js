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
let markers = createMarkerClusterGroup();

// const urlBackend = "http://localhost:8080"
const urlBackend = "https://timoknapp.com/ttf"
const urlGoogleQuery = "https://maps.google.com/maps?q="

const initDateFrom = new Date(Date.now());
const initDateTo = new Date(Date.now() + (7 * 86400000));
// Results are cached per federation on the server, so querying many at once no
// longer means scraping them all live.
const MAX_SELECTED_FEDERATIONS = 0; // 0 = no limit
const FILTER_AUTO_CLOSE_BREAKPOINT = 1024; // px

document.getElementById('dateFrom').value = formatDateToInput(initDateFrom);
document.getElementById('dateTo').value = formatDateToInput(initDateTo);

const loadingDiv = document.getElementById('loading');

// Initialize mobile filter visibility
document.addEventListener('DOMContentLoaded', function() {
    // Force filters visible on first load so users immediately see the options
    initializeMobileFilters(true);
    setupFederationLimits();
    registerMapFilterAutoClose();
});

function initializeMobileFilters(forceShow = false) {
    const filterContainer = document.getElementById('filterContainer');
    const toggleBtn = document.getElementById('filterToggle');

    if (!filterContainer || !toggleBtn) {
        return;
    }

    if (forceShow) {
        filterContainer.style.display = 'block';
        toggleBtn.innerHTML = 'Filter ▲';
        return;
    }

    if (shouldAutoCloseFilters()) {
        filterContainer.style.display = 'none';
        toggleBtn.innerHTML = 'Filter ▼';
    } else {
        filterContainer.style.display = 'block';
        toggleBtn.innerHTML = 'Filter ▲';
    }
}

// Listen for orientation or viewport changes that might affect the breakpoint
window.addEventListener('orientationchange', function() {
    setTimeout(initializeMobileFilters, 100); // Small delay to ensure orientation change is complete
});
window.addEventListener('resize', function() {
    initializeMobileFilters();
});

function shouldAutoCloseFilters() {
    return window.matchMedia(`(max-width: ${FILTER_AUTO_CLOSE_BREAKPOINT}px)`).matches;
}

function closeFiltersForMapInteraction() {
    const filterContainer = document.getElementById('filterContainer');
    const toggleBtn = document.getElementById('filterToggle');

    if (!filterContainer || !toggleBtn) {
        return;
    }

    if (!shouldAutoCloseFilters()) {
        return;
    }

    const isHidden = window.getComputedStyle(filterContainer).display === 'none';
    if (isHidden) {
        return;
    }

    filterContainer.style.display = 'none';
    toggleBtn.innerHTML = 'Filter ▼';
}

function registerMapFilterAutoClose() {
    if (!window.map || typeof window.map.on !== 'function') {
        return;
    }

    const interactionEvents = ['click', 'dragstart', 'zoomstart'];
    interactionEvents.forEach(eventName => {
        window.map.on(eventName, closeFiltersForMapInteraction);
    });
}

// Remove automatic initial request - user must manually submit
// getTournamentsByDate(initDateFrom, initDateTo, "", getSelectedFederations());

// Tournaments from the most recent search, shared by the map and list views so
// both always show the same data.
let currentTournaments = [];
// Marker instances keyed by tournament id, so the list can open the matching
// popup on the map.
let markerById = new Map();
let currentView = 'map';

function getTournamentsByDate(dateFrom, dateTo, compType, federations) {
    if (dateFrom != "" && dateTo != "") {
        dateFrom = formatDateToAPI(dateFrom);
        dateTo = formatDateToAPI(dateTo);
        getTournaments(dateFrom, dateTo, compType, federations)
        .then(response => {
            const tournaments = response.tournaments || [];
            renderDataNotice(response, tournaments.length);

            currentTournaments = tournaments;
            markerById = new Map();

            map.removeLayer(markers);
            markers = createMarkerClusterGroup();
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
                if (tournament["id"]) {
                    markerById.set(String(tournament["id"]), marker);
                }
            }
            map.addLayer(markers);
            renderTournamentList();
        });
    }
}

async function getTournaments(dateFrom, dateTo, compType, federations) {
    showSpinner();
    let url = urlBackend + `?dateFrom=${dateFrom}&dateTo=${dateTo}&format=full`;
    if (compType && compType !== "") {
        url += `&compType=${encodeURIComponent(compType)}`;
    }
    if (federations && federations.length > 0) {
        url += `&federations=${encodeURIComponent(federations.join(','))}`;
    }
    return fetch(url)
        .then(res => {
            if (!res.ok) {
                throw new Error(`Server antwortete mit Status ${res.status}`);
            }
            return res.json();
        })
        .then(result => {
            hideSpinner();
            // The backend still returns a bare array for older clients; accept both.
            if (Array.isArray(result)) {
                return { tournaments: result, federations: [], partial: false };
            }
            return {
                tournaments: result.tournaments || [],
                federations: result.federations || [],
                partial: Boolean(result.partial)
            };
        })
        .catch(error => {
            hideSpinner();
            console.error('Failed to load tournaments:', error);
            // Showing invented data would be worse than showing nothing: the
            // user cannot tell a real tournament from a placeholder.
            return { tournaments: [], federations: [], partial: true, failed: true };
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
    checkboxes.forEach((checkbox, index) => {
        checkbox.checked = MAX_SELECTED_FEDERATIONS <= 0 || index < MAX_SELECTED_FEDERATIONS;
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
        checkbox.addEventListener('change', function () {
            const checkedBoxes = document.querySelectorAll('input[name="federations"]:checked');

            if (MAX_SELECTED_FEDERATIONS > 0 && checkedBoxes.length > MAX_SELECTED_FEDERATIONS) {
                // If more than the allowed number are selected, uncheck the current one
                this.checked = false;
                alert(`Sie können maximal ${MAX_SELECTED_FEDERATIONS} Verbände gleichzeitig auswählen.`);
            }

            updateFederationSelectionState();
        });
    });

    // Respect the pre-selection from the markup, but never exceed the limit.
    // Falling back to "the first N checkboxes" would silently change which
    // federations are queried whenever the list order changes.
    if (MAX_SELECTED_FEDERATIONS > 0) {
        const preselected = Array.from(checkboxes).filter(cb => cb.checked);
        if (preselected.length === 0 || preselected.length > MAX_SELECTED_FEDERATIONS) {
            const keep = (preselected.length ? preselected : Array.from(checkboxes))
                .slice(0, MAX_SELECTED_FEDERATIONS);
            checkboxes.forEach(cb => {
                cb.checked = keep.includes(cb);
            });
        }
    }
    updateFederationSelectionState();
}

function updateFederationSelectionState() {
    const checkboxes = document.querySelectorAll('input[name="federations"]');
    const checkedBoxes = document.querySelectorAll('input[name="federations"]:checked');

    // Disable unchecked boxes only when a limit is configured
    checkboxes.forEach(checkbox => {
        if (MAX_SELECTED_FEDERATIONS > 0 && !checkbox.checked &&
            checkedBoxes.length >= MAX_SELECTED_FEDERATIONS) {
            checkbox.disabled = true;
        } else {
            checkbox.disabled = false;
        }
    });
}

// renderDataNotice surfaces stale or failed federations.
//
// Silently returning fewer tournaments is the worst outcome: the user cannot
// tell "no tournaments match" from "this federation is down".
function renderDataNotice(response, tournamentCount) {
    const notice = document.getElementById('dataNotice');
    if (!notice) {
        return;
    }

    if (response.failed) {
        notice.textContent = 'Turnierdaten konnten nicht geladen werden. Bitte später erneut versuchen.';
        notice.className = 'data-notice error';
        notice.style.display = 'block';
        return;
    }

    const problems = (response.federations || []).filter(f => f.status === 'error' || f.status === 'stale');
    if (problems.length === 0) {
        notice.style.display = 'none';
        notice.textContent = '';
        return;
    }

    const failed = problems.filter(f => f.status === 'error').map(f => f.id);
    const stale = problems.filter(f => f.status === 'stale').map(f => f.id);

    const parts = [];
    if (failed.length > 0) {
        parts.push(`${failed.join(', ')} nicht erreichbar`);
    }
    if (stale.length > 0) {
        parts.push(`${stale.join(', ')} zeigt ältere Daten`);
    }

    notice.textContent = `Hinweis: ${parts.join(' · ')}. Angezeigt werden ${tournamentCount} Turniere.`;
    notice.className = 'data-notice warning';
    notice.style.display = 'block';
}

function toggleFilters() {
    const filterContainer = document.getElementById('filterContainer');
    const toggleBtn = document.getElementById('filterToggle');
    
    if (filterContainer.style.display === 'none') {
        filterContainer.style.display = 'block';
        toggleBtn.innerHTML = 'Filter ▲';
    } else {
        filterContainer.style.display = 'none';
        toggleBtn.innerHTML = 'Filter ▼';
    }
}

function createMarkerClusterGroup() {
    const group = L.markerClusterGroup();
    attachFilterAutoCloseToMarkers(group);
    return group;
}

function attachFilterAutoCloseToMarkers(group) {
    if (!group || typeof group.on !== 'function') {
        return;
    }

    const markerEvents = ['click', 'popupopen', 'clusterclick'];
    markerEvents.forEach(eventName => {
        group.on(eventName, closeFiltersForMapInteraction);
    });
}
// ===== List view =====
//
// The map alone makes it hard to compare dates and is awkward to use with a
// keyboard or screen reader. The list shows the same data from the same state,
// so the two views can never disagree.

// parseTournamentDate extracts a sortable start date from the free-text date
// string the federations publish.
//
// Formats seen in live data vary per federation, e.g.:
//   "22.08. bis 23.08."          (old API, no year)
//   "Sa, 15.8.2026 abgesagt"     (new API, weekday + trailing note)
//   "So, 16.8. – Fr, 21.8.2026"  (new API, range)
//
// Returns null when nothing usable is found, so sorting can put those last
// instead of guessing a wrong date.
function parseTournamentDate(dateText) {
    if (!dateText) {
        return null;
    }

    // First day/month pair wins: it is the tournament's start in every format.
    const match = String(dateText).match(/(\d{1,2})\.\s*(\d{1,2})\.\s*(\d{4})?/);
    if (!match) {
        return null;
    }

    const day = parseInt(match[1], 10);
    const month = parseInt(match[2], 10);
    if (!day || !month || month > 12 || day > 31) {
        return null;
    }

    // Without a year, assume the search window: use the current year and roll
    // over to the next one when the date already passed by more than a month.
    let year = match[3] ? parseInt(match[3], 10) : new Date().getFullYear();
    const parsed = new Date(year, month - 1, day);
    if (!match[3]) {
        const monthAgo = new Date();
        monthAgo.setMonth(monthAgo.getMonth() - 1);
        if (parsed < monthAgo) {
            parsed.setFullYear(year + 1);
        }
    }

    return isNaN(parsed.getTime()) ? null : parsed;
}

function compareTournaments(a, b, sortBy) {
    if (sortBy === 'title') {
        return (a.title || '').localeCompare(b.title || '', 'de');
    }
    if (sortBy === 'organizer') {
        return (a.organizer || '').localeCompare(b.organizer || '', 'de');
    }

    // Date: unparseable entries sort last rather than to 1970.
    const da = parseTournamentDate(a.date);
    const db = parseTournamentDate(b.date);
    if (!da && !db) {
        return (a.title || '').localeCompare(b.title || '', 'de');
    }
    if (!da) {
        return 1;
    }
    if (!db) {
        return -1;
    }
    if (da.getTime() !== db.getTime()) {
        return da - db;
    }
    return (a.title || '').localeCompare(b.title || '', 'de');
}

function escapeHtml(value) {
    return String(value == null ? '' : value)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function renderTournamentList() {
    const list = document.getElementById('tournamentList');
    const empty = document.getElementById('listEmpty');
    const count = document.getElementById('listCount');
    const sortSelect = document.getElementById('listSort');

    if (!list || !empty || !count) {
        return;
    }

    const sortBy = sortSelect ? sortSelect.value : 'date';
    const sorted = currentTournaments.slice().sort((a, b) => compareTournaments(a, b, sortBy));

    count.textContent = sorted.length === 1
        ? '1 Turnier'
        : `${sorted.length} Turniere`;

    list.innerHTML = '';

    if (sorted.length === 0) {
        empty.style.display = 'block';
        return;
    }
    empty.style.display = 'none';

    const fragment = document.createDocumentFragment();

    for (const tournament of sorted) {
        const entries = (tournament.entries || []).filter(entry =>
            entry.competition || (entry.skill_level && entry.skill_level.trim() !== ''));

        const item = document.createElement('li');
        item.className = 'tournament-item';

        const competitions = entries.length > 0
            ? `<ul class="tournament-competitions">${entries.map(entry => `
                    <li><span class="competition-name">${escapeHtml(entry.competition || '-')}</span>
                        <span class="competition-lk">${escapeHtml(entry.skill_level || '')}</span></li>
               `).join('')}</ul>`
            : '';

        const hasCoords = tournament.lat && tournament.lon;

        item.innerHTML = `
            <h3 class="tournament-title">${escapeHtml(tournament.title)}</h3>
            <dl class="tournament-meta">
                <dt>Datum</dt><dd>${escapeHtml(tournament.date)}</dd>
                <dt>Veranstalter</dt>
                <dd><a target="_blank" rel="noopener"
                       href="${urlGoogleQuery + encodeURIComponent(tournament.organizer || '')}">
                       ${escapeHtml(tournament.organizer)}</a></dd>
            </dl>
            ${competitions}
            <div class="tournament-actions">
                <a href="${escapeHtml(tournament.url)}" target="_blank" rel="noopener"
                   class="signup-button">Anmelden</a>
                ${hasCoords ? `<button type="button" class="map-button"
                    data-tournament-id="${escapeHtml(tournament.id)}">Auf Karte zeigen</button>` : ''}
            </div>
        `;

        if (hasCoords) {
            const mapButton = item.querySelector('.map-button');
            if (mapButton) {
                mapButton.addEventListener('click', () => showTournamentOnMap(tournament));
            }
        }

        fragment.appendChild(item);
    }

    list.appendChild(fragment);
}

// showTournamentOnMap switches to the map and opens the matching popup, so
// selecting an entry in the list stays connected to the map view.
function showTournamentOnMap(tournament) {
    setView('map');

    const lat = parseFloat(tournament.lat);
    const lon = parseFloat(tournament.lon);
    if (isNaN(lat) || isNaN(lon)) {
        return;
    }

    map.setView([lat, lon], Math.max(map.getZoom(), 12));

    const marker = markerById.get(String(tournament.id));
    if (!marker) {
        return;
    }

    // Markers live in a cluster group, so the cluster has to be expanded
    // before the popup can open.
    if (typeof markers.zoomToShowLayer === 'function') {
        markers.zoomToShowLayer(marker, () => marker.openPopup());
    } else {
        marker.openPopup();
    }
}

function setView(view) {
    const mapEl = document.getElementById('map');
    const listEl = document.getElementById('listView');
    const mapBtn = document.getElementById('viewMapBtn');
    const listBtn = document.getElementById('viewListBtn');

    if (!mapEl || !listEl || !mapBtn || !listBtn) {
        return;
    }

    currentView = view === 'list' ? 'list' : 'map';
    const showList = currentView === 'list';

    // The sidebar floats above the map by design. In the list view that would
    // cover the first results, so the page switches to a normal document flow
    // with the filters above the list.
    document.body.classList.toggle('list-mode', showList);

    mapEl.style.display = showList ? 'none' : 'block';
    listEl.style.display = showList ? 'block' : 'none';

    mapBtn.classList.toggle('active', !showList);
    listBtn.classList.toggle('active', showList);
    mapBtn.setAttribute('aria-pressed', String(!showList));
    listBtn.setAttribute('aria-pressed', String(showList));

    if (showList) {
        renderTournamentList();
    } else {
        // Leaflet renders a grey area when the container was hidden while it
        // resized, so the map has to be told its size changed.
        setTimeout(() => map.invalidateSize(), 0);
    }
}
