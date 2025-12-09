.PHONY: all build run clean web test

# Build all
all: build web

# Build Go backend
build:
	go build -o bin/proxy ./cmd/proxy

# Run the proxy server
run: build
	./bin/proxy

# Install web dependencies
web-deps:
	cd web && npm install

# Build web UI
web: web-deps
	cd web && npm run build

# Run web development server
web-dev:
	cd web && npm run dev

# Clean build artifacts
clean:
	rm -rf bin/
	rm -rf web/public/assets

# Run tests
test:
	go test -v ./...

# Development: run both backend and frontend
dev:
	@echo "Starting development servers..."
	@echo "Backend: go run ./cmd/proxy"
	@echo "Frontend: cd web && npm run dev"
