package edge

type ServerState int

const (
	Connected ServerState = iota
	Autonomous
	Degraded
)

var StateName = map[ServerState]string{
	Connected:  "CONNECTED",
	Autonomous: "AUTONOMOUS",
	Degraded:   "DEGRADED",
}
