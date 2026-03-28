const map = L.map('map').setView([48.165, 11.427], 12);

L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
    attribution: '&copy; OpenStreetMap contributors &copy; CARTO',
    maxZoom: 19
}).addTo(map);

// Define the three icon states
const icons = {
    green: L.divIcon({ className: '', html: '<div class="icon-dot dot-green"></div>', iconSize: [16, 16], iconAnchor: [8, 8] }),
    orange: L.divIcon({ className: '', html: '<div class="icon-dot dot-orange"></div>', iconSize: [16, 16], iconAnchor: [8, 8] }),
    red: L.divIcon({ className: '', html: '<div class="icon-dot dot-red"></div>', iconSize: [16, 16], iconAnchor: [8, 8] })
};

// We now store more than just the marker. We store the last known state and timestamp.
const fleet = {};

async function updateDashboard() {
    try {
        const response = await fetch('/api/v1/drones/live');
        if (!response.ok) return;

        const liveDrones = await response.json();
        const now = Date.now();

        // 1. Process incoming data
        liveDrones.forEach(drone => {
            const popupText = `<b>ID:</b> ${drone.Name}<br><b>Alt:</b> ${drone.Alt.toFixed(1)}m<br><b>Bat:</b> ${drone.Battery.toFixed(1)}%`;

            if (fleet[drone.Name]) {
                const state = fleet[drone.Name];

                // If the telemetry actually changed, update the LastSeen timer
                if (state.timestamp !== drone.Lat || state.lastLon !== drone.Lon || state.lastAlt !== drone.Alt) {
                    state.lastSeen = now;
                    state.timestamp = drone.Lat;
                    state.lastLon = drone.Lon;
                    state.lastAlt = drone.Alt;

                    state.marker.setLatLng([drone.Lat, drone.Lon]);
                    state.marker.setPopupContent(popupText);
                }
            } else {
                // First time seeing this drone
                const marker = L.marker([drone.Lat, drone.Lon], {icon: icons.green}).addTo(map);
                marker.bindPopup(popupText);
                marker.bindTooltip(drone.Name, { permanent: true, direction: 'right', className: 'drone-label', offset: [10, 0] });

                fleet[drone.Name] = {
                    marker: marker,
                    lastSeen: now,
                    lastLat: drone.Lat,
                    lastLon: drone.Lon,
                    lastAlt: drone.Alt
                };
            }
        });

        // 2. Evaluate staleness for the entire fleet and change colors
        Object.keys(fleet).forEach(name => {
            const state = fleet[name];
            const ageInSeconds = (now - state.lastSeen) / 1000;

            if (ageInSeconds > 120) {
                state.marker.setIcon(icons.red);
            } else if (ageInSeconds > 60) {
                state.marker.setIcon(icons.orange);
            } else {
                state.marker.setIcon(icons.green);
            }
        });

    } catch (err) {
        console.error("Failed to fetch telemetry:", err);
    }
}

setInterval(updateDashboard, 1000);
updateDashboard();