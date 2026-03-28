package types

type Event struct {
	EdgeID    string  `json:"edge_id"`
	Name      string  `json:"name"`
	Timestamp string  `json:"timestamp"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Alt       float64 `json:"alt"`
	State     string  `json:"state"`
	Battery   float64 `json:"battery"`
}
