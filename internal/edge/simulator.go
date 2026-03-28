package edge

import (
	"crypto/ed25519"
	"math"
	"math/rand"
)

const (
	// On Earth, 1 degree of latitude is roughly 111 kilometers.
	// That means a horizontal jitter of 0.00004 degrees is about 4.4 meters of drift.
	jitter float64 = 0.00004

	// 20 centimeters of vertical drift mimicking barometric sensor noise.
	altitudeJitter float64 = 0.2

	// To get exactly 20 minutes (1200 seconds) of life from a 100.0 battery: 100 / 1200 = 0.0833
	batteryDrainRate float64 = 0.0833

	// Base speed of 0.00005 degrees/sec translates to roughly 5.55 m/s or 20 km/h.
	baseHorizontalSpeed float64 = 0.00005

	// Ascends or descends at 0.25 meters per second.
	baseVerticalSpeed float64 = 0.25

	northEast float64 = math.Pi / 4
)

type DroneSimulator struct {
	ID         string
	Name       string
	MAC        string
	Lat        float64
	Lon        float64
	Alt        float64
	TargetAlt  float64
	Battery    float64
	PrivateKey ed25519.PrivateKey
	step       float64
	heading    float64
	climbRate  float64
}

func NewDroneSimulator(id string, name string, mac string, privateKey ed25519.PrivateKey) *DroneSimulator {
	return &DroneSimulator{
		ID:   id,
		Name: name,
		MAC:  mac,
		// Munich Marienplatz, Munich, Germany (48.0814,11.3432)
		Lat:        48.0814,
		Lon:        11.3432,
		Alt:        50.0,
		TargetAlt:  50.0,
		Battery:    100.0,
		PrivateKey: privateKey,
		step:       baseHorizontalSpeed,
		heading:    northEast,
		climbRate:  baseVerticalSpeed,
	}
}
func (s *DroneSimulator) Tick() {
	deltaLat := s.step * math.Cos(s.heading)
	deltaLon := s.step * math.Sin(s.heading)

	s.Lat += deltaLat
	s.Lon += deltaLon

	// Apply horizontal sensor noise
	jitterLat := (rand.Float64() - 0.5) * jitter
	jitterLon := (rand.Float64() - 0.5) * jitter
	s.Lat += jitterLat
	s.Lon += jitterLon

	// Vertical movement interpolation
	if math.Abs(s.TargetAlt-s.Alt) > s.climbRate {
		if s.Alt < s.TargetAlt {
			s.Alt += s.climbRate
		} else {
			s.Alt -= s.climbRate
		}
	} else {
		s.Alt = s.TargetAlt
	}

	// Fixed: Use the constant instead of a hardcoded float
	jitterAlt := (rand.Float64() - 0.5) * altitudeJitter
	s.Alt += jitterAlt

	// Hard floor limit
	if s.Alt < 0 {
		s.Alt = 0
	}

	// Simulate battery drain
	s.Battery -= batteryDrainRate
	if s.Battery < 0 {
		s.Battery = 0
	}
}

func (s *DroneSimulator) SpeedUp() {
	s.step += 0.00002 // Increases speed by roughly 8 km/h per call
}

func (s *DroneSimulator) SlowDown() {
	s.step -= 0.00002
	if s.step < 0 {
		s.step = 0 // Allows the drone to hover in place
	}
}

func (s *DroneSimulator) ChangeAltitude(newAlt float64) {
	if newAlt >= 0 {
		s.TargetAlt = newAlt
	}
}

// Head sets the yaw direction in radians
func (s *DroneSimulator) Head(angle float64) {
	s.heading = angle
}
