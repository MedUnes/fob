package main

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"drones/internal/db"
	"drones/types"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Config struct {
	StationPort int
}

var (
	droneBattery = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "drone_battery_percent",
		Help: "Current battery level of the drone",
	}, []string{"edge_id"})

	droneAltitude = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "drone_altitude_meters",
		Help: "Current altitude of the drone",
	}, []string{"edge_id"})

	droneLat = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "drone_latitude",
		Help: "Current latitude of the drone",
	}, []string{"edge_id"})

	droneLon = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "drone_longitude",
		Help: "Current longitude of the drone",
	}, []string{"edge_id"})

	droneState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "drone_status",
		Help: "Current connection state of the drone (1 = active)",
	}, []string{"edge_id", "state"})
)

func main() {
	var c Config
	flag.IntVar(&c.StationPort, "station-port", 5002, "Station server port")
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	flag.Parse()
	// TODO Make sure that only authenticated clients can connect to the server
	mux := http.NewServeMux()
	db, err := db.NewDB()
	if err != nil {
		log.Fatalf("Failed to connect to database (%v)", err)
	}
	defer db.Close()
	logger.Info("Starting Station Server", "station_port", c.StationPort)
	pubKeys, err := db.LoadPublicKeys()
	if err != nil {
		log.Fatalf("Failed to load public keys: %v", err)
	}
	mux.HandleFunc("POST /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Error("Error reading request body", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var event types.Event
		err = json.Unmarshal(body, &event)
		if err != nil {
			logger.Error("Error unmarshalling body", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		sig, err := hex.DecodeString(r.Header.Get("X-Signature"))
		if err != nil {
			logger.Error("Error decoding signature", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ok := ed25519.Verify(pubKeys[event.EdgeID], body, sig)
		if !ok {
			logger.Warn("Received Invalid event", "edge_id", event.EdgeID)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		logger.Info("Received event", "edge_name", event.Name)

		err = db.BulkInsertEvents([]types.Event{event})
		// Reset all states to 0, then set the active one to 1
		droneState.WithLabelValues(event.Name, "CONNECTED").Set(0)
		droneState.WithLabelValues(event.Name, "AUTONOMOUS").Set(0)
		droneState.WithLabelValues(event.Name, "DEGRADED").Set(0)
		droneState.WithLabelValues(event.Name, event.State).Set(1)

		// Set numeric gauges
		droneBattery.WithLabelValues(event.Name).Set(event.Battery)
		droneAltitude.WithLabelValues(event.Name).Set(event.Alt)
		droneLat.WithLabelValues(event.Name).Set(event.Lat)
		droneLon.WithLabelValues(event.Name).Set(event.Lon)

		if err != nil {
			logger.Error("Error inserting event", "error", err)
		}
		w.WriteHeader(http.StatusAccepted)
		if err := r.Body.Close(); err != nil {
			logger.Error("Error closing request body", "error", err)
		}
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		body := struct {
			Status    string `json:"status"`
			StationID string `json:"station_id"`
		}{
			Status:    "OK",
			StationID: "Station-1",
		}
		b, err := json.Marshal(body)
		if err != nil {
			logger.Error("Error while encoding health response", "error", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(b)
		if err != nil {
			logger.Error("Error while writing health response", "error", err)
			return
		}
	})
	muxProm := http.NewServeMux()
	muxProm.Handle("/metrics", promhttp.Handler())

	go func() {
		err = http.ListenAndServe(":"+strconv.Itoa(c.StationPort+1), muxProm)
		if err != nil {
			logger.Error("Error starting Prometheus server", "error", err)
			os.Exit(1)
		}
	}()
	caCert, err := os.ReadFile("./storage/ca.pem")
	if err != nil {
		log.Fatalf("Failed to read CA cert: %v", err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	tlsConfig := &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  caCertPool,
		MinVersion: tls.VersionTLS13,
	}
	server := &http.Server{
		Addr:      ":" + strconv.Itoa(c.StationPort),
		Handler:   mux,
		TLSConfig: tlsConfig,
	}
	err = server.ListenAndServeTLS("storage/cert.pem", "storage/key.pem")
	if err != nil {
		logger.Error("Error starting server", "error", err)
		os.Exit(1)
	}
}
