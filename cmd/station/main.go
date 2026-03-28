package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"drones/internal/edge"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math/big"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Edge struct {
	ID         string `json:"id"` // Changed to string to match SQLite TEXT PRIMARY KEY
	Name       string `json:"name"`
	MAC        string `json:"mac"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	MTLSCert   string `json:"mtls_cert"`
	MTLSKEY    string `json:"mtls_key"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	var edgeCount int
	flag.IntVar(&edgeCount, "edges", 24, "Number of edge devices")
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		printUsageAndExit(args)
	}

	command := args[0]
	switch command {
	case "init-db":
		initDB(edgeCount, logger)
	default:
		logger.Error("Unknown command", "command", command)
		printUsageAndExit(args)
	}
}

func printUsageAndExit(args []string) {
	fmt.Printf("Received args: %v\n", args)
	fmt.Printf("usage: station [flags] <action>\n")
	fmt.Printf("example: station --edges 4 init-db\n")
	os.Exit(1)
}

func initDB(edgeCount int, logger *slog.Logger) {
	edges := make([]Edge, edgeCount)
	for i := 0; i < edgeCount; i++ {
		pub, priv, err := generateCryptoKeys()
		if err != nil {
			log.Fatalf("Failed to generate ed25519 keys: %v", err)
		}
		name, err := generateName(i)
		if err != nil {
			log.Fatalf("Failed to generate name: %v", err)
		}
		mac, err := generateMAC()
		if err != nil {
			log.Fatalf("Failed to generate mac address: %v", err)
		}

		// Use Base64 encoding for cryptographic keys
		pubBase64 := base64.StdEncoding.EncodeToString(pub)
		privBase64 := base64.StdEncoding.EncodeToString(priv)

		mtlsCACertBytes, err := os.ReadFile("storage/ca.pem")
		if err != nil {
			log.Fatalf("Failed to read CA cert: %v", err)
		}
		mtlsCAKeyBytes, err := os.ReadFile("storage/ca.key")
		if err != nil {
			log.Fatalf("Failed to read CA key: %v", err)
		}
		mtlsCert, mtlsKey, err := GenerateEdgeMTLS(mtlsCACertBytes, mtlsCAKeyBytes, name)
		if err != nil {
			log.Fatalf("Failed to generate MTLS keys: %v", err)
		}
		edges[i] = Edge{
			ID:         fmt.Sprintf("edge-%d", i),
			Name:       name,
			MAC:        mac,
			PrivateKey: privBase64,
			PublicKey:  pubBase64,
			MTLSCert:   string(mtlsCert),
			MTLSKEY:    string(mtlsKey),
		}
	}

	logger.Info("Initializing Event Storage Database")

	// Fixed the duplicate foreign key error in the events table
	schemaStatement := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS stations (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL
);
CREATE TABLE IF NOT EXISTS edges (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    mac TEXT UNIQUE NOT NULL,
    private_key TEXT NOT NULL,
    public_key TEXT NOT NULL ,   
    mtls_cert TEXT NOT NULL,
    mtls_key TEXT NOT NULL  
);
CREATE TABLE IF NOT EXISTS events (
    edge_id TEXT NOT NULL,
    timestamp TIMESTAMP NOT NULL,
    state TEXT NOT NULL CHECK( state IN ('%s','%s','%s')),
    lat FLOAT NOT NULL,
    lon FLOAT NOT NULL,
    alt FLOAT NOT NULL,
    battery FLOAT NOT NULL,
    CONSTRAINT fk_edges FOREIGN KEY (edge_id) REFERENCES edges(id) ON UPDATE CASCADE
);`, edge.StateName[edge.Connected], edge.StateName[edge.Degraded], edge.StateName[edge.Autonomous])

	insertStatement := "INSERT INTO `edges` (id, name, mac, public_key, private_key, mtls_cert, mtls_key) VALUES "
	for _, ed := range edges {
		insertStatement += fmt.Sprintf("('%s','%s','%s','%s','%s','%s','%s'),",
			ed.ID, ed.Name, ed.MAC, ed.PublicKey, ed.PrivateKey, ed.MTLSCert, ed.MTLSKEY)
	}
	insertStatement = strings.TrimSuffix(insertStatement, ",") + ";"

	if err := os.MkdirAll("./storage", 0755); err != nil {
		logger.Error("Failed to create storage directory", "error", err)
		os.Exit(1)
	}

	db, err := sql.Open("sqlite3", "./storage/drones.db")
	if err != nil {
		logger.Error("Error opening db connection", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Execute Schema Creation
	_, err = db.Exec(schemaStatement)
	if err != nil {
		logger.Error("Error creating schema", "error", err)
		os.Exit(1)
	}

	// Execute Batch Insert
	_, err = db.Exec(insertStatement)
	if err != nil {
		logger.Error("Error inserting edges", "error", err)
		os.Exit(1)
	}

	logger.Info("Database initialized successfully", "edge_count", edgeCount)
}

func generateCryptoKeys() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}
func GenerateEdgeMTLS(caCertPEM, caKeyPEM []byte, edgeName string) (certPEM, keyPEM []byte, err error) {

	caCertBlock, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA cert: %w", err)
	}

	caKeyBlock, _ := pem.Decode(caKeyPEM)
	caKey, err := x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA key: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: edgeName},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(5, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, caCert, pub, caKey)
	if err != nil {
		return nil, nil, err
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})

	privBytes, _ := x509.MarshalPKCS8PrivateKey(priv)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	return certPEM, keyPEM, nil
}
func generateName(index int) (string, error) {
	droneNamesSlice := []string{
		"Aero", "Bolt", "Cyclone", "Dagger", "Echo", "Falcon",
		"Ghost", "Havoc", "Iron", "Jupiter", "Kestrel", "Lynx",
		"Maverick", "Nimbus", "Obsidian", "Phantom", "Raptor",
		"Shadow", "Titan", "Umbra", "Viper", "Whisper", "Yonder", "Zephyr",
	}
	if index < 0 || index >= len(droneNamesSlice) {
		return "", fmt.Errorf("invalid index for drone names slice")
	}

	return droneNamesSlice[index], nil
}

func generateMAC() (string, error) {
	buf := make([]byte, 6)
	_, err := rand.Read(buf)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
			"%02x:%02x:%02x:%02x:%02x:%02x",
			buf[0], buf[1], buf[2], buf[3], buf[4], buf[5],
		),
		nil
}
