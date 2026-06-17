.PHONY: all build build-agent build-server clean release

# Build targets
build: build-agent build-server

build-agent:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/simpleprobe-agent-linux-amd64 ./cmd/agent/
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dist/simpleprobe-agent-linux-arm64 ./cmd/agent/

build-server:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/simpleprobe-server-linux-amd64 ./cmd/server/
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dist/simpleprobe-server-linux-arm64 ./cmd/server/

# Build for local testing
build-local:
	go build -o dist/simpleprobe-agent ./cmd/agent/
	go build -o dist/simpleprobe-server ./cmd/server/

clean:
	rm -rf dist/

# Run server locally for development
run-server:
	go run ./cmd/server/ -c server.yml

# Run agent locally for development
run-agent:
	go run ./cmd/agent/ -c agent.yml