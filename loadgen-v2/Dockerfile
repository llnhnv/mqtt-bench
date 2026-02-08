FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o loadgen-v2 .

FROM alpine:3.18

# Install ca-certificates for TLS connections
RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /app/loadgen-v2 .

# Default environment variables for 1M virtual clients
ENV VIRTUAL_CLIENTS=1000000
ENV CONNECTIONS=100
ENV SHARDS=256
ENV INTERVAL_MS=1000
ENV MAX_INFLIGHT_PER_CLIENT=1
ENV REQ_QOS=0
ENV RESP_QOS=0
ENV WARMUP_SEC=10
ENV DURATION_SEC=0
ENV PUBLISH_WORKERS=20
ENV BATCH_SIZE=500
ENV USE_SHARED_SUB=true
ENV METRICS_PORT=8090

EXPOSE 8090

ENTRYPOINT ["./loadgen-v2"]
