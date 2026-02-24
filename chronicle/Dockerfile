ARG BUILD_FROM
ARG BUILD_ARCH

FROM golang:1.26-alpine AS builder

ARG BUILD_ARCH

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

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
        -o chronicle \
        .

FROM $BUILD_FROM

RUN apk add --no-cache ruby ruby-dev build-base libffi-dev && \
    gem install imap-backup --no-document && \
    apk del ruby-dev build-base libffi-dev && \
    apk add --no-cache ruby

COPY --from=builder /build/chronicle /usr/bin/chronicle
RUN chmod a+x /usr/bin/chronicle

COPY rootfs /
