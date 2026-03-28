package db

type Edge struct {
	ID         string
	Name       string
	MAC        string
	PrivateKey string
	PublicKey  string
	MTLSCert   string
	MTLSKey    string
}

type LiveDrone struct {
	Name      string
	Timestamp string `json:"timestamp"`
	Lat       float64
	Lon       float64
	Alt       float64
	Battery   float64
}

type Telemetry struct {
	Name      string  `json:"name"`
	State     string  `json:"state"`
	Timestamp string  `json:"timestamp"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Alt       float64 `json:"alt"`
	Battery   float64 `json:"battery"`
}
