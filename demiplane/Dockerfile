ARG BUILD_FROM
ARG BUILD_ARCH

# ---------------------------------------------------------------------------
# Stage 1: Compile the Go binary
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS builder

ARG BUILD_ARCH

WORKDIR /build

# Download dependencies first (layer cache friendly)
COPY go.mod go.sum ./
RUN go mod download

# Build the static binary.
# BUILD_ARCH uses HA naming (aarch64/amd64); translate to GOARCH naming.
COPY main.go ./
RUN set -eu; \
    case "${BUILD_ARCH}" in \
        aarch64) export GOARCH=arm64 ;; \
        amd64)   export GOARCH=amd64 ;; \
        armv7)   export GOARCH=arm GOARM=7 ;; \
        armhf)   export GOARCH=arm GOARM=6 ;; \
        i386)    export GOARCH=386 ;; \
        *)       export GOARCH="${BUILD_ARCH}" ;; \
    esac; \
    CGO_ENABLED=0 go build \
        -ldflags="-s -w" \
        -trimpath \
        -o demiplane \
        .

# ---------------------------------------------------------------------------
# Stage 2: Runtime image based on the HA base image
# ---------------------------------------------------------------------------
FROM $BUILD_FROM

COPY --from=builder /build/demiplane /usr/bin/demiplane
RUN chmod a+x /usr/bin/demiplane

# Overlay the rootfs (s6 service definitions, etc.)
COPY rootfs /
