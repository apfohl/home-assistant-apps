ARG BUILD_FROM
ARG BUILD_ARCH

FROM golang:1.26-alpine AS builder

ARG BUILD_ARCH

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY main.go mock.go client.go client_test.go dashboard.html create.html edit.html settings.html ./
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
        -o mailcow \
        .

FROM $BUILD_FROM

COPY --from=builder /build/mailcow /usr/bin/mailcow
RUN chmod a+x /usr/bin/mailcow

COPY rootfs /
