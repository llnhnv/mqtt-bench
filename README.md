# MQTT Request-Response Benchmark

High-performance benchmark system for testing MQTT request-response patterns at scale (**1M+ virtual clients**).

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         VIRTUAL CLIENT MULTIPLEXING                         │
│                                                                             │
│   ┌─────────────┐         ┌─────────────┐         ┌─────────────┐           │
│   │  LOADGEN-V2 │         │    EMQX     │         │  WORKER-V2  │           │
│   │             │         │   BROKER    │         │             │           │
│   │ 1M Virtual  │ ──req──►│             │──req───►│ Subscriber  │           │
│   │  Clients    │         │   $share/   │         │    Pool     │           │
│   │             │◄─resp── │  workers/   │◄─resp── │  (10 subs)  │           │
│   │ 100 Conns   │         │             │         │ Auto-scale  │           │
│   └─────────────┘         └─────────────┘         └─────────────┘           │
│                                                                             │
│   Multiplexing Ratio: 1 connection : 10,000 virtual clients                 │
│   Total Connections: 120 (100 loadgen + 10 pub + 10 sub)                    │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Key Features

| Feature | Description |
|---------|-------------|
| **Virtual Client Multiplexing** | 1M+ clients using only 100 TCP connections |
| **Shared Subscription Pool** | 10 subscriber connections with `$share/workers/` for load distribution |
| **Auto-scaling Workers** | 10 → 1000 workers based on queue pressure |
| **Topic Sharding** | 256 shards for optimal message distribution |
| **Prometheus Metrics** | Full observability with `/metrics` endpoint |
| **Kubernetes Ready** | Production deployment with HPA |

## Performance Limits

| Environment | Max Virtual Clients | Match Rate | Latency p50 |
|-------------|---------------------|------------|-------------|
| **Single Docker Host (12GB)** | 100k-200k | 90%+ | 5-10ms |
| **Kind Cluster (local)** | 10k | 98%+ | 5ms |
| **K8s Production (3+ nodes)** | 1M+ | 95%+ | <10ms |

## Quick Start

### Docker Compose (Local Development)

```bash
# Start with 100k virtual clients
make up

# Monitor live dashboard
make dashboard

# View stats
make stats

# Stop services
make down
```

### Scaling Tests

```bash
# 100k virtual clients (optimal for single host)
make up-100k

# 200k with longer interval
make up VIRTUAL_CLIENTS=100000 LOADGEN_INSTANCES=2 INTERVAL_MS=10000

# Custom configuration
make up VIRTUAL_CLIENTS=50000 WORKER_INSTANCES=3 INTERVAL_MS=5000
```

### Kubernetes Deployment (Production)

```bash
# Build and load images to kind cluster
make k8s-build

# Deploy to kind (10k clients for testing)
make k8s-deploy-kind

# Deploy to production K8s (1M clients)
make k8s-deploy-1m

# Check status
make k8s-status

# View logs
make k8s-logs

# Cleanup
make k8s-cleanup
```

## Monitoring

### Terminal Dashboard
```bash
make dashboard
```

### Web Dashboard
```bash
make dashboard-web
# Opens http://localhost:8000/dashboard.html
```

### Prometheus + Grafana
```bash
make monitoring-up
# Prometheus: http://localhost:9090
# Grafana: http://localhost:3000 (admin/admin)
```

### Metrics Endpoints

| Service | Endpoint | Description |
|---------|----------|-------------|
| Loadgen | http://localhost:8090/metrics | Prometheus metrics |
| Loadgen | http://localhost:8090/debug/vars | JSON metrics |
| Worker | http://localhost:8080/metrics | Prometheus metrics |
| Worker | http://localhost:8080/stats | JSON stats |
| Worker | http://localhost:8080/health | Health check |
| EMQX | http://localhost:18083 | Dashboard (admin/public) |

## Configuration

### Loadgen-V2 Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `BROKER` | tcp://localhost:1883 | MQTT broker URL |
| `VIRTUAL_CLIENTS` | 100,000 | Number of virtual clients |
| `CONNECTIONS` | 100 | MQTT connections (multiplexing) |
| `SHARDS` | 256 | Topic shards for distribution |
| `INTERVAL_MS` | 2000 | Message interval per client (ms) |
| `MAX_INFLIGHT_PER_CLIENT` | 1 | Max pending requests per client |
| `PUBLISH_WORKERS` | 20 | Publisher goroutines |
| `BATCH_SIZE` | 500 | Messages per batch |

### Worker-V2 Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MQTT_BROKER` | tcp://localhost:1883 | MQTT broker URL |
| `MQTT_SUB` | req/+ | Subscribe topic pattern |
| `CONNECTIONS` | 10 | Subscriber/Publisher connections |
| `MIN_WORKERS` | 10 | Minimum worker count |
| `MAX_WORKERS` | 1000 | Maximum workers (auto-scale) |
| `QUEUE_CAP` | 100,000 | Message queue capacity |
| `SCALE_UP_THRESHOLD` | 0.7 | Scale up when queue > 70% |
| `SCALE_DOWN_THRESHOLD` | 0.2 | Scale down when queue < 20% |
| `MODE` | health | Processing mode: health/sleep/cpu |
| `DROP_ON_FULL` | true | Drop messages when queue full |

## Project Structure

```
mqtt-rr-bench/
├── loadgen-v2/              # Virtual client multiplexer
│   ├── main.go              # Connection pool, virtual clients
│   └── Dockerfile
├── worker-v2/               # Auto-scaling message processor
│   ├── main.go              # Subscriber pool, worker pool
│   └── Dockerfile
├── k8s/                     # Kubernetes manifests
│   ├── deploy.sh            # Deployment script
│   ├── emqx-cluster.yaml    # 3-node EMQX StatefulSet
│   ├── worker.yaml          # Worker Deployment + HPA
│   ├── loadgen.yaml         # Loadgen Deployment
│   └── overlays/kind/       # Lightweight config for kind
├── monitoring/              # Observability stack
│   ├── prometheus.yml       # Prometheus config
│   ├── dashboard.html       # Web dashboard
│   └── grafana/             # Grafana dashboards
├── scripts/
│   ├── benchmark.sh         # Benchmark suite
│   └── dashboard.sh         # Terminal dashboard
├── docker-compose.production.yml
├── Makefile
└── README.md
```

## Architecture Details

### Request-Response Flow

```
┌──────────────────┐                           ┌──────────────────┐
│   LOADGEN-V2     │                           │    WORKER-V2     │
│ (Virtual Clients)│                           │ (Service/Backend)│
└────────┬─────────┘                           └────────┬─────────┘
         │                                              │
         │  1. REQUEST                                  │
         │  Topic: req/{shard}                          │
         │  Payload: "client-123|timestamp"             │
         │  ─────────────────────────────────────────►  │
         │                                              │
         │                        2. PROCESS            │
         │                        (health/sleep/cpu)    │
         │                                              │
         │  3. RESPONSE                                 │
         │  Topic: resp/{shard}                         │
         │  ◄─────────────────────────────────────────  │
         │                                              │
         │  4. MEASURE LATENCY                          │
         │  latency = now() - timestamp                 │
```

### Why Shared Subscriptions?

MQTT 5.0 shared subscriptions (`$share/group/topic`) distribute messages across subscribers:

```
Without Shared Sub:                 With Shared Sub ($share/workers/req/+):

  req/1 ──► Sub1 (ALL messages)       req/1 ──► Sub1 (33%)
            Sub2 (ALL messages)               ──► Sub2 (33%)
            Sub3 (ALL messages)               ──► Sub3 (33%)

  Problem: Each sub gets 100%       Solution: Load balanced across subs
  = Message queue overflow          = No single point of bottleneck
```

### Resource Efficiency

| Metric | Direct Connections | Multiplexed (V2) | Savings |
|--------|-------------------|------------------|---------|
| **1M clients** | 1,000,000 TCP | 120 TCP | **99.99%** |
| **Goroutines** | ~2,000,000 | ~1,200 | **99.94%** |
| **Memory** | ~2TB | ~150MB | **99.99%** |

## Troubleshooting

### EOF Disconnections

If you see `Connection lost: EOF` errors:

1. **EMQX message queue limit**: Already increased to 100k in production config
2. **Subscriber count**: Uses 10 subscribers with shared subscription
3. **Message rate**: Reduce `VIRTUAL_CLIENTS` or increase `INTERVAL_MS`

### High Latency

High latency indicates queue backup:

1. **Increase workers**: Set higher `MAX_WORKERS`
2. **Scale horizontally**: Use `--scale worker-v2=5`
3. **Check processing mode**: `MODE=health` is fastest

### 1M+ Clients Not Working Locally

Single Docker host has resource limits:

1. **Use Kubernetes** for 1M+ clients
2. **Cloud K8s** (GKE/EKS/AKS) with 3+ nodes
3. **100k-200k** is optimal for single host

## Requirements

### Docker Compose (Local)
- Docker & Docker Compose
- 8GB+ RAM recommended
- 12GB+ RAM for 200k clients

### Kubernetes (Production)
- K8s cluster with 3+ nodes
- 4 CPU, 8GB RAM per node minimum
- kubectl & helm configured

## Development

```bash
# Build locally
cd loadgen-v2 && go build -o loadgen-v2 .
cd worker-v2 && go build -o worker-v2 .

# Run tests
make dev-test

# Rebuild Docker images
make build
```

## Benchmarking

```bash
# Quick validation
make benchmark-quick

# Full benchmark suite
make benchmark

# Specific tests
./scripts/benchmark.sh baseline     # 100k baseline
./scripts/benchmark.sh clients      # Scale 100k → 500k
./scripts/benchmark.sh autoscale    # Auto-scaling behavior
```

## License

MIT
