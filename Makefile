.PHONY: all build run clean test

all: build

# Build the proxy binary
build:
	go build -o bin/proxy ./cmd/proxy

# Run the proxy server
run: build
	./bin/proxy

# Clean build artifacts
clean:
	rm -rf bin/

# Run tests
test:
	go test -v ./...
