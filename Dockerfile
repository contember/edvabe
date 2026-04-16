# syntax=docker/dockerfile:1.5
#
# edvabe server image — single static binary + embedded assets.
# The container needs access to a Docker socket to create sandboxes.
#
# Usage in docker-compose:
#   edvabe:
#     image: ghcr.io/contember/edvabe:latest
#     ports: ["3000:3000"]
#     volumes:
#       - /var/run/docker.sock:/var/run/docker.sock
#       - edvabe-data:/data
#     environment:
#       EDVABE_STATE_DIR: /data
#       EDVABE_CACHE_DIR: /data/cache

FROM golang:1.24-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /edvabe ./cmd/edvabe

FROM debian:bookworm-slim

# Docker CLI is needed for building base/envd-source/code-interpreter
# images inside the container (EnsureBaseImage shells out to `docker build`).
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        docker.io \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /edvabe /usr/local/bin/edvabe

# Persistent state (templates.json) and cache (file cache, builds).
ENV EDVABE_STATE_DIR=/data
ENV EDVABE_CACHE_DIR=/data/cache
VOLUME /data

EXPOSE 3000

ENTRYPOINT ["edvabe"]
CMD ["serve", "--port", "3000"]
