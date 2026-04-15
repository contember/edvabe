.PHONY: build run test lint clean

BINARY := bin/edvabe
PKG    := ./cmd/edvabe

build:
	@mkdir -p bin
	go build -o $(BINARY) $(PKG)

run: build
	$(BINARY) serve

test:
	go test ./...

lint:
	go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "(golangci-lint not installed, skipped)"

clean:
	rm -rf bin coverage.out coverage.html
