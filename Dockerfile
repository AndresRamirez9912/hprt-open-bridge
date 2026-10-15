# photo-print-bridge: static Go binary on Alpine with network diagnostics
#
# docker build -t photo-print-bridge .
# docker run --network host photo-print-bridge

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH \
    go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/bridge \
    ./cmd/bridge

FROM alpine:3.20

RUN apk add --no-cache \
    iputils \
    iproute2 \
    networkmanager-cli

WORKDIR /app

COPY --from=build /out/bridge /app/bridge
COPY config.yaml /app/config.yaml

ENV BRIDGE_APP_REST_ADDR=0.0.0.0:8787 \
    BRIDGE_LOGGER_NO_COLOR=true

EXPOSE 8787

ENTRYPOINT ["/app/bridge", "-config", "/app"]
