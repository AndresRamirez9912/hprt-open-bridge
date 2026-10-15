# photo-print-bridge: static Go binary on Alpine with the tools of the network diagnostics
#   docker build -t photo-print-bridge .
#   docker run --network host photo-print-bridge

FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bridge ./cmd/bridge

FROM alpine:3.20

# ping, ip and nmcli back /api/link, /api/gateway and /api/wifi/scan
RUN apk add --no-cache iputils iproute2 networkmanager-cli
WORKDIR /app
COPY --from=build /out/bridge /app/bridge
COPY config.yaml /app/config.yaml

# Inside the container the API must listen on every interface
ENV BRIDGE_APP_REST_ADDR=0.0.0.0:8787 \
    BRIDGE_LOGGER_NO_COLOR=true
EXPOSE 8787
ENTRYPOINT ["/app/bridge", "-config", "/app"]
