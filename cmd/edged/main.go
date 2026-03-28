package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"drones/internal/db"
	"drones/internal/edge"
	"drones/types"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	StationEndpoint string
	EdgeName        string
	SendIntervalSec int
	RetryCount      int
	TimeoutSec      int
	cacheSize       int
	BurstSize       int
}
type Edge struct {
	config    Config
	logger    *slog.Logger
	cache     chan types.Event
	retry     int
	client    *http.Client
	simulator *edge.DroneSimulator
}

func NewEdge(c Config, edb db.Edge) Edge {
	privateKeyBytes, _ := base64.StdEncoding.DecodeString(edb.PrivateKey)

	clientCert, err := tls.X509KeyPair([]byte(edb.MTLSCert), []byte(edb.MTLSKey))
	if err != nil {
		log.Fatalf("failed to parse Edge mTLS keys")
	}

	caCert, _ := os.ReadFile("storage/ca.pem")
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	serverCert, _ := os.ReadFile("storage/cert.pem")
	caCertPool.AppendCertsFromPEM(serverCert)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caCertPool,
	}

	secureClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	return Edge{
		config:    c,
		logger:    slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		cache:     make(chan types.Event, c.cacheSize),
		retry:     0,
		client:    secureClient,
		simulator: edge.NewDroneSimulator(edb.ID, edb.Name, edb.MAC, privateKeyBytes),
	}
}

type stateFn func() stateFn

func main() {
	var c Config
	flag.StringVar(&c.StationEndpoint, "station-endpoint", "localhost:5002", "Station server endpoint")
	flag.StringVar(&c.EdgeName, "edge-name", "", "Unique name for this edge")
	flag.IntVar(&c.SendIntervalSec, "send-interval", 2, "Send interval in seconds")
	flag.IntVar(&c.RetryCount, "retry-count", 3, "Retry count on failure")
	flag.IntVar(&c.TimeoutSec, "timeout", 10, "Timeout for station connection")
	flag.IntVar(&c.cacheSize, "burst-buffer-size", 1024, "Number of events to cache")
	flag.IntVar(&c.BurstSize, "burst-size", 10, "Number of events to burst send concurrently")
	flag.Parse()
	if c.EdgeName == "" {
		log.Fatalf("Missing edge name (provide -edge-name)")
	}

	d, err := db.NewDB()
	if err != nil {
		log.Fatalf("Could not open DB: %v", err)
	}
	defer d.Close()
	edgeRow, err := d.GetEdge(c.EdgeName)
	if err != nil {
		log.Fatalf("Could not fetch edge data from DB: %v", err)
	}
	e := NewEdge(c, *edgeRow)
	e.logger.Info("Starting Edge Server")

	initialState := e.connected
	if e.probeStation() != nil {
		initialState = e.autonomous
	}
	for state := initialState; state != nil; {
		state = state()
	}

}
func (e *Edge) probeStation() error {
	get, err := e.client.Get(fmt.Sprintf("https://%s/healthz", e.config.StationEndpoint))
	if err != nil {
		return err
	}
	resp := get.StatusCode
	if resp != http.StatusOK {
		e.logger.Warn("Station is not healthy", "status_code", resp)
		return fmt.Errorf("station unhealthy")
	}
	e.logger.Info("Station is healthy")
	return nil
}
func (e *Edge) connected() stateFn {
	ctx := context.Background()
	tick := time.NewTicker(time.Duration(e.config.SendIntervalSec) * time.Second)
	defer tick.Stop()
	for range tick.C {
		event := e.generateEvent(edge.Connected)
		err := e.sendEvent(event, ctx)
		if err != nil {
			e.logger.Warn("Could not send event to station", "err", err, "state", edge.StateName[edge.Connected])
			e.logger.Debug("Caching event", "state", edge.StateName[edge.Connected])
			select {
			case e.cache <- event:
				e.logger.Debug("Added Event to cache", "retry", e.retry, "state", edge.StateName[edge.Connected])
			default:
				e.logger.Warn("Could not add event to cache (size exceeded?)", "state", edge.StateName[edge.Connected])
			}
			e.retry++
			if e.retry > e.config.RetryCount {
				return e.autonomous
			}
			continue
		}
		e.logger.Info("Event sent successfully", "state", edge.StateName[edge.Connected])
		if e.retry > 0 {
			e.logger.Info("Sending trailing events", "state", edge.StateName[edge.Connected])
			err := e.burstFlush()
			if err != nil {
				e.logger.Warn("Failed to send trailing events", "state", edge.StateName[edge.Connected])
			}
		}
		e.retry = 0
	}
	return nil
}
func (e *Edge) autonomous() stateFn {
	eventTicker := time.NewTicker(time.Duration(e.config.SendIntervalSec) * time.Second)
	probeTicker := time.NewTicker(time.Duration(e.config.SendIntervalSec) * 2 * time.Second)

	defer eventTicker.Stop()
	defer probeTicker.Stop()
	for {
		select {
		case <-eventTicker.C:
			event := e.generateEvent(edge.Autonomous)
			select {
			case e.cache <- event:
				e.logger.Info("Added Event to cache", "state", edge.StateName[edge.Autonomous])
			default:
				e.logger.Warn("Could not add event to cache (size exceeded?)", "state", edge.StateName[edge.Autonomous])
			}
		case <-probeTicker.C:
			if err := e.probeStation(); err == nil {
				return e.degraded
			}
		}
	}
}

func (e *Edge) degraded() stateFn {
	err := e.burstFlush()
	if err == nil {
		e.logger.Info("Successfully flushed stale events to station", "state", edge.StateName[edge.Degraded])
		return e.connected
	}
	e.logger.Error("Error while burst stale events, reverting to autonomous state", "err", err, "state", edge.StateName[edge.Degraded])
	return e.autonomous
}
func (e *Edge) generateEvent(state edge.ServerState) types.Event {
	e.simulator.Tick()
	return types.Event{
		EdgeID:    e.simulator.ID,
		Name:      e.simulator.Name,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		State:     edge.StateName[state],
		Lat:       e.simulator.Lat,
		Lon:       e.simulator.Lon,
		Alt:       e.simulator.Alt,
		Battery:   e.simulator.Battery,
	}
}

func (e *Edge) sendEvent(event types.Event, ctx context.Context) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	s := ed25519.Sign(e.simulator.PrivateKey, body)
	signature := hex.EncodeToString(s)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://"+e.config.StationEndpoint+"/api/v1/events", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", signature)

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("station did not accept event, status code: %d", resp.StatusCode)
	}
	return nil
}

func (e *Edge) burstFlush() error {
	eg, ctx := errgroup.WithContext(context.Background())
	eg.SetLimit(e.config.BurstSize)
	for {
		select {
		case event := <-e.cache:
			ev := event
			eg.Go(func() error {
				return e.sendEvent(ev, ctx)
			})
		default:
			return eg.Wait()
		}
	}
}
