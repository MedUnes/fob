package db

import (
	"crypto/ed25519"
	"database/sql"
	"drones/types"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	db *sql.DB
}

func NewDB() (*DB, error) {
	db, err := sql.Open("sqlite3", "./storage/drones.db")
	if err != nil {
		return nil, fmt.Errorf("error opening db connection (%v)", err)
	}
	d := &DB{
		db: db,
	}
	return d, nil
}

func (d *DB) BulkInsertEvents(events []types.Event) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}

	defer tx.Rollback()

	batchSize := 500

	for i := 0; i < len(events); i += batchSize {
		end := i + batchSize
		if end > len(events) {
			end = len(events)
		}

		batch := events[i:end]

		var builder strings.Builder
		builder.WriteString("INSERT INTO events (edge_id, timestamp, state, lat, lon, alt, battery) VALUES ")

		var args []any

		for j, e := range batch {
			builder.WriteString("(?,?,?,?,?,?,?)")

			if j < len(batch)-1 {
				builder.WriteString(",")
			}
			args = append(args, e.EdgeID, e.Timestamp, e.State, e.Lat, e.Lon, e.Alt, e.Battery)
		}

		_, err = tx.Exec(builder.String(), args...)
		if err != nil {
			return fmt.Errorf("error inserting batch starting at index %d: %v", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %v", err)
	}

	return nil
}
func (d *DB) GetEdge(name string) (*Edge, error) {
	rows := d.db.QueryRow("SELECT id,name,mac,private_key,public_key,mtls_cert,mtls_key FROM edges WHERE name = ?", name)
	var edge Edge
	err := rows.Scan(&edge.ID, &edge.Name, &edge.MAC, &edge.PrivateKey, &edge.PublicKey, &edge.MTLSCert, &edge.MTLSKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Fatalf("Could not find edge in database")
		}
		return nil, fmt.Errorf("error reading from db (%v)", err)
	}
	return &edge, nil
}
func (d *DB) LoadPublicKeys() (map[string]ed25519.PublicKey, error) {
	rows, err := d.db.Query("SELECT id, public_key FROM edges")
	if err != nil {
		return nil, fmt.Errorf("failed to query edges: %w", err)
	}
	defer rows.Close()

	keyMap := make(map[string]ed25519.PublicKey)

	for rows.Next() {
		var id string
		var pubKeyBase64 string

		if err := rows.Scan(&id, &pubKeyBase64); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		pubKeyBytes, err := base64.StdEncoding.DecodeString(pubKeyBase64)
		if err != nil {
			return nil, fmt.Errorf("failed to decode public key for %s: %w", id, err)
		}

		keyMap[id] = pubKeyBytes
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return keyMap, nil
}
func (d *DB) GetLatestTelemetry() ([]LiveDrone, error) {
	query := `
		SELECT  ev.timestamp, e.name, ev.lat, ev.lon, ev.alt, ev.battery 
		FROM edges e
		JOIN (
			SELECT timestamp, edge_id, lat, lon, alt, battery,
				   ROW_NUMBER() OVER (PARTITION BY edge_id ORDER BY timestamp DESC) as rn
			FROM events
		) ev ON e.id = ev.edge_id
		WHERE ev.rn = 1;
	`

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var drones []LiveDrone
	var dbTime time.Time
	for rows.Next() {
		var d LiveDrone
		if err := rows.Scan(&dbTime, &d.Name, &d.Lat, &d.Lon, &d.Alt, &d.Battery); err != nil {
			return nil, err
		}
		d.Timestamp = dbTime.Format(time.RFC3339)
		drones = append(drones, d)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return drones, nil
}
func (d *DB) GetTelemetry(edgeName string) ([]Telemetry, error) {
	query := `
		SELECT e.name, ev.state, ev.timestamp ,ev.lat, ev.lon, ev.alt, ev.battery
		FROM edges e
		JOIN events ev ON e.id = ev.edge_id
	`
	if edgeName != "" {
		query += " WHERE e.name = ?"
	}
	query += " ORDER BY e.name,ev.timestamp DESC LIMIT 1000;"

	rows, err := d.db.Query(query, edgeName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var telemetry []Telemetry
	for rows.Next() {
		var t Telemetry
		var dbTime time.Time
		if err := rows.Scan(&t.Name, &t.State, &dbTime, &t.Lat, &t.Lon, &t.Alt, &t.Battery); err != nil {
			return nil, err
		}
		t.Timestamp = dbTime.Format(time.RFC3339)
		telemetry = append(telemetry, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return telemetry, nil
}

func (d *DB) Close() {
	d.db.Close()
}
