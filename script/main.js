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

getTournamentsByDate(initDateFrom, initDateTo);

function getTournamentsByDate(dateFrom, dateTo) {
    if (dateFrom != "" && dateTo != "") {
        dateFrom = formatDateToAPI(dateFrom);
        dateTo = formatDateToAPI(dateTo);
        getTournaments(dateFrom, dateTo)
        .then(tournaments => {
            map.removeLayer(markers);
            markers = L.markerClusterGroup();
            for (const tournament of tournaments) {
                const marker = L.marker([tournament["lat"], tournament["lon"]])
                .bindPopup(`
                <span class="popupTitle"><b>${tournament["title"]}</b></span><br><br>
                <b>Datum:</b> ${tournament["date"]}<br>
                <b>Adresse:</b> <a target="_blank" href="${urlGoogleQuery+tournament["organizer"]}">${tournament["organizer"]}</a><br><br>
                <b>Weitere Infos:</b> <a target="_blank" href="${tournament["url"]}">Auf mybigpoint</a><br>
                `)
                markers.addLayer(marker);
            }
            map.addLayer(markers);
        });
    }
}

async function getTournaments(dateFrom, dateTo) {
    showSpinner();
    return fetch(urlBackend+`?dateFrom=${dateFrom}&dateTo=${dateTo}`)
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