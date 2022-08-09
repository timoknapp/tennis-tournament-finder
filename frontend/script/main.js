let map = L.map('map').setView([51.133481, 10.018343], 7);
L.tileLayer('http://a.tile.openstreetmap.fr/hot/{z}/{x}/{y}.png').addTo(map);
let markers = L.markerClusterGroup();


const urlHTV = "https://htv.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
const urlBAD = "https://baden.liga.nu/cgi-bin/WebObjects/nuLigaTENDE.woa/wa/tournamentCalendar"
const urlBackend = "http://localhost:8080"
const urlGoogleQuery = "https://maps.google.com/maps?q="

const dateFrom = formatDate(new Date(Date.now()));
const dateTo = formatDate(new Date(Date.now()+(30*86400000)));

getTournaments()
    .then(tournaments => {
        for (const tournament of tournaments) {
            const marker = L.marker([tournament["lat"], tournament["lon"]])
                .bindPopup(`
                <b>Titel:</b> ${tournament["title"]}<br>
                <b>Datum:</b> ${tournament["date"]}<br>
                <b>Adresse:</b> <a target="_blank" href="${urlGoogleQuery+tournament["address"]}">${tournament["address"]}</a><br><br>
                <b>Weitere Infos:</b> <a target="_blank" href="${tournament["url"]}">Auf mybigpoint</a><br>
                `)
                // .on('click', function(e){
                //     map.setView([tournament["lat"], tournament["lon"]], 15);
                // });
            markers.addLayer(marker);
        }
        map.addLayer(markers);
    });

async function getTournaments() {
    return fetch(urlBackend+`?dateFrom=${dateFrom}&dateTo=${dateTo}`)
        .then(res => res.json())
        .then(result => {
            console.log(result);
            return result;
        })
        .catch(error => console.log('error', error));
}

function padTo2Digits(num) {
    return num.toString().padStart(2, '0');
}

function formatDate(date) {
    return [
        padTo2Digits(date.getDate()),
        padTo2Digits(date.getMonth() + 1),
        date.getFullYear(),
    ].join('.');
}
