# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25.12
ARG BUILDPLATFORM=linux/amd64

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath \
    -ldflags "-s -w -X github.com/pantalk/pantalk/internal/version.Version=${VERSION}" \
    -o /out/pantalk ./cmd/pantalk && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath \
    -ldflags "-s -w -X github.com/pantalk/pantalk/internal/version.Version=${VERSION}" \
    -o /out/pantalkd ./cmd/pantalkd

FROM alpine:3.22

RUN apk add --no-cache ca-certificates git tzdata && \
    addgroup -S -g 10001 pantalk && \
    adduser -S -D -H -u 10001 -G pantalk pantalk && \
    mkdir -p \
        /home/pantalk/.cache \
        /home/pantalk/.config/pantalk \
        /home/pantalk/.local/share/pantalk \
        /home/pantalk/.run && \
    chown -R pantalk:pantalk /home/pantalk

COPY --from=build /out/pantalk /usr/local/bin/pantalk
COPY --from=build /out/pantalkd /usr/local/bin/pantalkd
COPY --chown=pantalk:pantalk configs/pantalk.docker.yaml \
    /home/pantalk/.config/pantalk/config.yaml
COPY configs/pantalk.example.yaml \
    /usr/local/share/pantalk/pantalk.example.yaml

ENV HOME=/home/pantalk \
    XDG_CACHE_HOME=/home/pantalk/.cache \
    XDG_CONFIG_HOME=/home/pantalk/.config \
    XDG_DATA_HOME=/home/pantalk/.local/share \
    XDG_RUNTIME_DIR=/home/pantalk/.run \
    PANTALK_CONFIG=/home/pantalk/.config/pantalk/config.yaml

USER pantalk
WORKDIR /home/pantalk

VOLUME ["/home/pantalk/.config/pantalk", "/home/pantalk/.local/share/pantalk"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD pantalk bots >/dev/null || exit 1

ENTRYPOINT ["pantalkd"]
