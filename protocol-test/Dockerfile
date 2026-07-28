ARG BUILD_FROM
FROM golang:1.26-alpine AS builder

ARG BUILD_ARCH

WORKDIR /build

COPY go.mod ./
COPY main.go ./

RUN case "${BUILD_ARCH}" in \
      "aarch64") export GOARCH="arm64" ;; \
      "amd64")   export GOARCH="amd64" ;; \
      "armv7")   export GOARCH="arm" GOARM="7" ;; \
      "armhf")   export GOARCH="arm" GOARM="6" ;; \
      "i386")    export GOARCH="386" ;; \
      *) echo "Unknown arch: ${BUILD_ARCH}" && exit 1 ;; \
    esac \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o protocol-test .

FROM $BUILD_FROM

COPY --from=builder /build/protocol-test /usr/bin/protocol-test
RUN chmod a+x /usr/bin/protocol-test

COPY rootfs /
