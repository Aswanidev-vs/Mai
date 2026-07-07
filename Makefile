# Mai - Offline AI Assistant Makefile
# Requires: Go 1.25+, GCC (for CGo), Git

.PHONY: help build run companion companion-open clean test vet fmt deps update-models

# Default target
help:
	@echo "Mai - Offline AI Assistant Build System"
	@echo ""
	@echo "Available targets:"
	@echo "  build          - Build the main executable"
	@echo "  run            - Build and run the assistant"
	@echo "  companion      - Build and run with companion Web UI (port 8080)"
	@echo "  companion-open - Same as companion, opens browser on start"
	@echo "  clean          - Clean build artifacts"
	@echo "  test           - Run tests"
	@echo "  vet            - Run go vet"
	@echo "  fmt            - Format code"
	@echo "  deps           - Download dependencies"
	@echo "  update-models  - Update model checksums"
	@echo "  setup          - Initial project setup"
	@echo ""

# Detect OS for browser open command
UNAME := $(shell uname -s)
ifeq ($(UNAME),Linux)
  OPEN_CMD := xdg-open
else ifeq ($(UNAME),Darwin)
  OPEN_CMD := open
else
  # Windows (Git Bash / MSYS2)
  OPEN_CMD := cmd.exe /c start
endif

# Build the main executable
build:
	@echo "Building Mai..."
	go build -o mai.exe ./cmd/mai
	@echo "Build complete: mai.exe"

# Build and run
run: build
	@echo "Starting Mai..."
	./mai.exe

# Build and run with companion Web UI enabled
companion: build
	@echo "Starting Mai with Companion UI at http://localhost:8080..."
	./mai.exe --companion

# Build, run, and open browser
companion-open: build
	@echo "Starting Mai with Companion UI..."
	./mai.exe --companion &
	@echo "Waiting for server..."
	@for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do \
		if curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/api/health 2>/dev/null | grep -q "200"; then \
			echo "Server ready!"; \
			break; \
		fi; \
		sleep 1; \
	done
	$(OPEN_CMD) http://localhost:8080
	@echo "Press Ctrl+C to stop"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	go clean ./...
	rm -f mai.exe
	rm -rf data/cache/*
	@echo "Clean complete"

# Run tests
test:
	@echo "Running tests..."
	go test ./...

# Run go vet
vet:
	@echo "Running go vet..."
	go vet ./internal/... ./pkg/...

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Update model checksums (placeholder)
update-models:
	@echo "Updating model checksums..."
	@echo "Note: Model files are not version-controlled."
	@echo "Update sherpa-onnx model directories manually."

# Initial project setup
setup: deps
	@echo "Setting up Mai project..."
	@echo ""
	@echo "Next steps:"
	@echo "1. Copy config.example.yaml to config.yaml"
	@echo "2. Download required models from https://github.com/k2-fsa/sherpa-onnx/releases"
	@echo "3. Update model paths in config.yaml"
	@echo "4. Install Ollama: https://ollama.ai/download"
	@echo "5. Run 'ollama pull gemma3:4b' (or your preferred model)"
	@echo "6. Run 'make build' to compile"
	@echo "7. Run 'make run' to start"