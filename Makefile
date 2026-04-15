.PHONY: build run test lint clean test-e2e-python

BINARY := bin/edvabe
PKG    := ./cmd/edvabe

E2E_PORT   ?= 3000
E2E_PY_DIR := test/e2e/python
E2E_PY_VENV:= $(E2E_PY_DIR)/.venv

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

# Python E2E: boot `edvabe serve`, wait for /health, run pytest, tear down.
# Uses a local venv at test/e2e/python/.venv so it does not touch system Python.
test-e2e-python: build
	@set -e; \
	if [ ! -x $(E2E_PY_VENV)/bin/pytest ]; then \
		echo "+ creating venv at $(E2E_PY_VENV)"; \
		python3 -m venv $(E2E_PY_VENV); \
		$(E2E_PY_VENV)/bin/pip install --quiet --upgrade pip; \
		$(E2E_PY_VENV)/bin/pip install --quiet -r $(E2E_PY_DIR)/requirements.txt; \
	fi; \
	LOG=$$(mktemp -t edvabe-e2e.XXXXXX.log); \
	echo "+ starting edvabe serve --port $(E2E_PORT) (log: $$LOG)"; \
	$(BINARY) serve --port $(E2E_PORT) >"$$LOG" 2>&1 & \
	SERVE_PID=$$!; \
	trap 'echo "+ stopping edvabe serve ($$SERVE_PID)"; kill $$SERVE_PID 2>/dev/null || true; wait $$SERVE_PID 2>/dev/null || true' EXIT INT TERM; \
	for i in $$(seq 1 60); do \
		if curl -fsS -o /dev/null "http://localhost:$(E2E_PORT)/health"; then \
			echo "+ edvabe /health is up"; \
			break; \
		fi; \
		if ! kill -0 $$SERVE_PID 2>/dev/null; then \
			echo "edvabe serve exited early, log:"; cat "$$LOG"; exit 1; \
		fi; \
		sleep 0.5; \
	done; \
	if ! curl -fsS -o /dev/null "http://localhost:$(E2E_PORT)/health"; then \
		echo "edvabe /health never responded, log:"; cat "$$LOG"; exit 1; \
	fi; \
	E2B_API_URL=http://localhost:$(E2E_PORT) \
	E2B_DOMAIN=localhost:$(E2E_PORT) \
	E2B_API_KEY=edvabe_local \
	E2B_SANDBOX_URL=http://localhost:$(E2E_PORT) \
	$(E2E_PY_VENV)/bin/pytest -v $(E2E_PY_DIR)
