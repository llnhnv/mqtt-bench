FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o worker-v2 .

FROM alpine:3.18

RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /app/worker-v2 .

# Default environment variables
ENV MQTT_BROKER=tcp://emqx:1883
ENV MQTT_SUB=req/+
ENV MQTT_QOS=0
ENV PUB_QOS=0
ENV CONNECTIONS=10

ENV MIN_WORKERS=10
ENV MAX_WORKERS=1000
ENV QUEUE_CAP=100000

ENV SCALE_UP_THRESHOLD=0.7
ENV SCALE_DOWN_THRESHOLD=0.2
ENV SCALE_INTERVAL_MS=1000
ENV WORKERS_PER_SCALE=10

ENV MODE=health
ENV SLEEP_MS=0
ENV CPU_WORK=0

ENV DROP_ON_FULL=true

ENV METRICS_PORT=8080
ENV PPROF_PORT=6060

EXPOSE 8080 6060

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["./worker-v2"]
