.PHONY: all edged stationd clean run-edge run-station

# Default target
all: edged stationd
# Build edge binary (for laptop)
edged:
	go build -o bin/edged ./cmd/edged

# Build edge binary for Android (ARM64)
edged-android:
	GOOS=android GOARCH=arm64 go build -o bin/edged_arm64 ./cmd/edged

# Build station binary
stationd:
	go build -o bin/stationd ./cmd/stationd
run-http:
	go run ./cmd/http
# Clean build artifacts
clean:
	rm -rf bin/
	rm -f *.db
gen-cert:
	go run /snap/go/current/src/crypto/tls/generate_cert.go --ed25519 --host localhost
	mv cert.pem key.pem ./storage
gen-mtls:
	openssl req -x509 -newkey ed25519 -keyout storage/ca.key -out storage/ca.pem -days 3650 -nodes -subj "/O=Drones Command CA"

# Run edge in standalone mode (for testing)
run-edge:
	go run ./cmd/edged --edge-name="$(name)"

# Run station
init-db:
	go run ./cmd/station init-db
# Run edge connecting to station
run-edge-connected:
	go run ./cmd/edged -station=localhost:50052
run-station:
	go run ./cmd/stationd
# Install dependencies
deps:
	go mod download
	go mod tidy

# Build all binaries
build: deps edged stationd

# Build for Android deployment
android: deps edged-android
	@echo "Binary ready at bin/edged_arm64"
	@echo "Transfer to Android: scp bin/edged_arm64 phone:~/"
