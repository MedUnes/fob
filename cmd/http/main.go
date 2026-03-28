package main

import (
	"drones/internal/db"
	"encoding/json"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
)

type Config struct {
	Port int
}

func main() {
	var c Config
	flag.IntVar(&c.Port, "port", 8080, "HTTP Port")
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	flag.Parse()

	// TODO Make sure that only authenticated clients can connect to the server
	mux := http.NewServeMux()
	db, err := db.NewDB()
	if err != nil {
		log.Fatalf("Failed to connect to database (%v)", err)
	}
	defer db.Close()

	logger.Info("Starting HTTP Server", "port", c.Port)
	fs := http.FileServer(http.Dir("./public"))
	mux.Handle("/", fs)
	mux.HandleFunc("/api/v1/drones/live", func(w http.ResponseWriter, r *http.Request) {
		events, err := db.GetLatestTelemetry()
		if err != nil {
			logger.Error("Failed to load events from DB", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		body, err := json.Marshal(events)
		if err != nil {
			logger.Error("Error while encoding events", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_, err = w.Write(body)
		if err != nil {
			logger.Error("Error while writing events", "error", err)
			return
		}
	})
	mux.HandleFunc("/api/v1/telemetry", func(w http.ResponseWriter, r *http.Request) {
		events, err := db.GetTelemetry(r.URL.Query().Get("drone"))
		if err != nil {
			logger.Error("Failed to load telemetry events from DB", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		body, err := json.Marshal(events)
		if err != nil {
			logger.Error("Error while encoding telemetry events", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_, err = w.Write(body)
		if err != nil {
			logger.Error("Error while writing events", "error", err)
			return
		}
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		body := struct {
			Status    string `json:"status"`
			StationID string `json:"station_id"`
		}{
			Status: "OK",
		}

		b, err := json.Marshal(body)
		if err != nil {
			logger.Error("Error while encoding health response", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_, err = w.Write(b)
		if err != nil {
			logger.Error("Error while writing health response", "error", err)
			return
		}
	})

	err = http.ListenAndServe(":"+strconv.Itoa(c.Port), mux)
	if err != nil {
		logger.Error("Error starting server", "error", err)
		os.Exit(1)
	}
}
