# MQTT Request-Response Benchmark

High-performance benchmark system for testing MQTT request-response patterns at scale (**1M-3M virtual clients**).

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
| **Docker Compose (12GB RAM)** | 100k-200k | 90%+ | 5-10ms |
| **K8s Local - 1M (24GB RAM)** | 1M | 90-95% | 10-20ms |
| **K8s Local - 3M (48GB RAM)** | 3M | 85-90% | 20-50ms |
| **K8s Production (3+ nodes)** | 5M+ | 95%+ | <10ms |

## Deployment Options

### 1. Docker Compose (100k-500k clients)

**Best for**: Local development, testing, small-scale benchmarks

```powershell
# Start with 100k virtual clients
make up

# Monitor live dashboard
make dashboard

# View stats
make stats

# Stop services
make down
```

See [Docker Compose Section](#docker-compose-deployment) for details.

### 2. Kubernetes Local (1M-3M clients)

**Best for**: Large-scale testing on Windows with Kind cluster

```powershell
# Quick start (PowerShell)
.\k8s-local.ps1 build
.\k8s-local.ps1 deploy-1m
.\k8s-local.ps1 monitor
```

See [Kubernetes Local Section](#kubernetes-local-deployment-windows) for full guide.

### 3. Kubernetes Production (5M+ clients)

**Best for**: Production deployments on cloud K8s

```bash
make k8s-deploy-1m
make k8s-status
```

See [Kubernetes Production Section](#kubernetes-production-deployment) for details.

---

## Docker Compose Deployment

### Quick Start

```bash
# Start with 100k virtual clients
make up-100k

# Monitor
make dashboard

# Stop
make down
```

### Scaling Tests

```bash
# 200k with longer interval
make up VIRTUAL_CLIENTS=100000 LOADGEN_INSTANCES=2 INTERVAL_MS=10000

# Custom configuration
make up VIRTUAL_CLIENTS=50000 WORKER_INSTANCES=3 INTERVAL_MS=5000
```

### Monitoring

#### Terminal Dashboard
```bash
make dashboard
```

#### Web Dashboard
```bash
make dashboard-web
# Opens http://localhost:8000/dashboard.html
```

#### Prometheus + Grafana
```bash
make monitoring-up
# Prometheus: http://localhost:9090
# Grafana: http://localhost:3000 (admin/admin)
```

### Requirements

- Docker & Docker Compose
- 8GB+ RAM for 100k clients
- 12GB+ RAM for 200k clients

---

## Kubernetes Local Deployment (Windows)

Complete guide for running 1M-3M virtual MQTT clients on Kind cluster locally.

### System Requirements

#### For 1M Clients
- **RAM**: 24GB+ (Docker Desktop)
- **CPU**: 8+ cores
- **Disk**: 50GB+ free
- **WSL2**: 32GB memory allocation

#### For 3M Clients
- **RAM**: 48GB+ (Docker Desktop)
- **CPU**: 12+ cores
- **Disk**: 100GB+ free
- **WSL2**: 48GB memory allocation

### Architecture

#### 1M Virtual Clients (~20GB RAM)
```
┌─────────────────────────────────────────────┐
│ 10 Loadgen Pods × 100k = 1M clients         │
│ 10 Worker Pods (auto-scale: 50-2000)        │
│ 1 EMQX Broker (2GB RAM)                     │
│ = ~20GB RAM total                            │
└─────────────────────────────────────────────┘
```

#### 3M Virtual Clients (~44GB RAM)
```
┌─────────────────────────────────────────────┐
│ 30 Loadgen Pods × 100k = 3M clients         │
│ 20 Worker Pods (auto-scale: 100-3000)       │
│ 1 EMQX Broker (4GB RAM)                     │
│ = ~44GB RAM total                            │
└─────────────────────────────────────────────┘
```

### Prerequisites

#### 1. Install Kind

**Option A: Quick Install Script (Recommended)**

```powershell
# PowerShell as Administrator
cd c:\Users\Administrator\Downloads\Coding\mqtt-bench
.\scripts\install-kind-windows.ps1

# Restart PowerShell
kind version
```

**Option B: Manual Install**

```powershell
# Download Kind
New-Item -ItemType Directory -Path "$env:ProgramFiles\Kind" -Force
curl.exe -Lo kind-windows-amd64.exe https://kind.sigs.k8s.io/dl/v0.20.0/kind-windows-amd64.exe
Move-Item .\kind-windows-amd64.exe "$env:ProgramFiles\Kind\kind.exe"

# Add to PATH
$oldPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
$newPath = "$oldPath;$env:ProgramFiles\Kind"
[Environment]::SetEnvironmentVariable("Path", $newPath, "Machine")

# Restart PowerShell and verify
kind version
```

**Option C: Chocolatey**

```powershell
choco install kind
```

#### 2. Install kubectl

```powershell
New-Item -ItemType Directory -Path "$env:ProgramFiles\kubectl" -Force
curl.exe -Lo kubectl.exe https://dl.k8s.io/release/v1.28.0/bin/windows/amd64/kubectl.exe
Move-Item .\kubectl.exe "$env:ProgramFiles\kubectl\kubectl.exe"

# Add to PATH
$oldPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
$newPath = "$oldPath;$env:ProgramFiles\kubectl"
[Environment]::SetEnvironmentVariable("Path", $newPath, "Machine")

# Restart PowerShell and verify
kubectl version --client
```

#### 3. Configure WSL2

Edit `C:\Users\Administrator\.wslconfig`:

```ini
[wsl2]
memory=32GB        # For 1M, or 48GB for 3M
processors=8       # Adjust based on your CPU
swap=8GB
```

Restart WSL: `wsl --shutdown`

#### 4. Configure Docker Desktop

```
Settings > Resources:
- CPUs: 8-12 cores
- Memory: 24GB (1M) or 48GB (3M)
- Swap: 8GB
- Disk: 100GB
```

### Quick Start Guide

#### Step 1: Setup Kind Cluster

```powershell
cd c:\Users\Administrator\Downloads\Coding\mqtt-bench

# For 1M clients
.\scripts\setup-kind-local.ps1 1m

# OR for 3M clients
.\scripts\setup-kind-local.ps1 3m
```

#### Step 2: Build and Load Images

```powershell
.\k8s-local.ps1 build
```

This will:
- Build loadgen-v2 and worker-v2 Docker images
- Load them into Kind cluster

#### Step 3: Deploy

```powershell
# Deploy 1M virtual clients
.\k8s-local.ps1 deploy-1m

# OR deploy 3M clients
.\k8s-local.ps1 deploy-3m
```

#### Step 4: Monitor

```powershell
# View status
.\k8s-local.ps1 monitor

# Watch logs
kubectl logs -n mqtt-bench -l app=loadgen --tail=100 -f
kubectl logs -n mqtt-bench -l app=worker --tail=100 -f
```

#### Step 5: Access Dashboards

```powershell
# EMQX Dashboard
kubectl port-forward -n mqtt-bench svc/emqx 18083:18083
# Open: http://localhost:18083 (admin/public)

# Worker Stats
kubectl port-forward -n mqtt-bench svc/worker 8080:8080
# API: http://localhost:8080/stats
```

#### Step 6: Setup Grafana (Optional)

```powershell
.\k8s-local.ps1 monitoring

# Port forward Grafana
kubectl port-forward -n mqtt-bench svc/grafana 3000:3000

# Open http://localhost:3000 (admin/admin)
```

Import dashboards from:
- `k8s/monitoring/emqx-production-dashboard.json`
- `k8s/monitoring/simple-dashboard.json`

### Available Commands

```powershell
.\k8s-local.ps1 help          # Show all commands
.\k8s-local.ps1 build         # Build and load images
.\k8s-local.ps1 deploy-1m     # Deploy 1M clients
.\k8s-local.ps1 deploy-3m     # Deploy 3M clients
.\k8s-local.ps1 monitor       # Show deployment status
.\k8s-local.ps1 monitoring    # Deploy Prometheus + Grafana
.\k8s-local.ps1 cleanup       # Delete everything
```

### Kubectl Commands

```powershell
# View pods
kubectl get pods -n mqtt-bench

# Scale loadgen
kubectl scale deployment/loadgen -n mqtt-bench --replicas=5

# Adjust message interval
kubectl set env deployment/loadgen -n mqtt-bench INTERVAL_MS=10000

# Scale workers
kubectl scale deployment/worker -n mqtt-bench --replicas=15

# Port forward EMQX
kubectl port-forward -n mqtt-bench svc/emqx 18083:18083

# Port forward Grafana
kubectl port-forward -n mqtt-bench svc/grafana 3000:3000
```

### Expected Performance

#### 1M Clients
- **Match Rate**: 90-95%
- **Latency P50**: 10-20ms
- **Latency P99**: 50-100ms
- **RAM Usage**: ~20GB
- **CPU Usage**: 60-80%

#### 3M Clients
- **Match Rate**: 85-90%
- **Latency P50**: 20-50ms
- **Latency P99**: 100-200ms
- **RAM Usage**: ~44GB
- **CPU Usage**: 80-95%

### Troubleshooting

#### 1. Pods Pending or OOM

**Cause**: Insufficient resources

**Solution**:
```powershell
# Check resources
kubectl top nodes

# Scale down
kubectl scale deployment/loadgen -n mqtt-bench --replicas=3

# Increase Docker Desktop memory (Settings > Resources)
```

#### 2. High Latency (>100ms)

**Cause**: Queue backup, insufficient workers

**Solution**:
```powershell
# Increase workers
kubectl scale deployment/worker -n mqtt-bench --replicas=15

# Increase interval
kubectl set env deployment/loadgen -n mqtt-bench INTERVAL_MS=10000
```

#### 3. EMQX Connection Refused

**Cause**: EMQX not ready

**Solution**:
```powershell
# Check status
kubectl get pods -n mqtt-bench -l app=emqx

# View logs
kubectl logs -n mqtt-bench -l app=emqx --tail=50

# Restart
kubectl rollout restart deployment/emqx -n mqtt-bench
```

#### 4. Grafana No Data

**Cause**: Prometheus not scraping

**Solution**:
```powershell
# Check Prometheus targets
kubectl port-forward -n mqtt-bench svc/prometheus 9090:9090
# Open: http://localhost:9090/targets

# Restart Prometheus
kubectl rollout restart deployment/prometheus -n mqtt-bench
```

#### 5. Low Match Rate (<80%)

**Cause**: Message loss, worker overload

**Solution**:
```powershell
# Increase workers
kubectl scale deployment/worker -n mqtt-bench --replicas=20

# Increase interval
kubectl set env deployment/loadgen -n mqtt-bench INTERVAL_MS=8000
```

#### 6. "kind: command not found"

**Cause**: PATH not refreshed

**Solution**:
```powershell
# Refresh PATH
$env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine")

# Or restart PowerShell
```

#### 7. Script Execution Policy Error

**Cause**: PowerShell security policy

**Solution**:
```powershell
# PowerShell as Admin
Set-ExecutionPolicy RemoteSigned -Scope CurrentUser -Force
```

### Performance Tips

#### 1. Gradual Load Increase

Don't start with full load. Scale gradually:

```powershell
# Start with 100k
kubectl scale deployment/loadgen -n mqtt-bench --replicas=1
# Wait 2-3 minutes

# Scale to 500k
kubectl scale deployment/loadgen -n mqtt-bench --replicas=5
# Wait 2-3 minutes

# Scale to 1M
kubectl scale deployment/loadgen -n mqtt-bench --replicas=10
```

#### 2. Warmup Period

Wait 2-5 minutes after deployment for system warmup and stabilization.

#### 3. Monitor Resources

```powershell
# Continuous monitoring
kubectl top pods -n mqtt-bench

# Watch in real-time
kubectl get pods -n mqtt-bench -w
```

#### 4. Optimize Intervals

- **1M clients**: 5s interval (default)
- **3M clients**: 10s interval recommended

```powershell
kubectl set env deployment/loadgen -n mqtt-bench INTERVAL_MS=10000
```

### Cleanup

#### Delete Deployment (Keep Cluster)

```powershell
kubectl delete namespace mqtt-bench
```

#### Delete Entire Cluster

```powershell
kind delete cluster --name mqtt-bench
```

#### Delete Images

```powershell
docker rmi mqtt-rr-bench-loadgen-v2:latest
docker rmi mqtt-rr-bench-worker-v2:latest
```

---

## Kubernetes Production Deployment

For production deployments on cloud K8s (GKE, EKS, AKS) with 3+ nodes.

### Build and Deploy

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

### Requirements

- K8s cluster with 3+ nodes
- 4 CPU, 8GB RAM per node minimum
- kubectl & helm configured

---

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

---

## Monitoring and Metrics

### Metrics Endpoints

| Service | Endpoint | Description |
|---------|----------|-------------|
| Loadgen | http://localhost:8090/metrics | Prometheus metrics |
| Loadgen | http://localhost:8090/debug/vars | JSON metrics |
| Worker | http://localhost:8080/metrics | Prometheus metrics |
| Worker | http://localhost:8080/stats | JSON stats |
| Worker | http://localhost:8080/health | Health check |
| EMQX | http://localhost:18083 | Dashboard (admin/public) |

### Prometheus Queries

```promql
# Active virtual clients
sum(loadgen_virtual_clients)

# Message rate
rate(loadgen_messages_sent[1m])

# Latency P50
histogram_quantile(0.50, rate(loadgen_latency_bucket[5m]))

# Worker pool size
sum(worker_pool_size)

# Queue usage
worker_queue_length / worker_queue_capacity
```

---

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

---

## Project Structure

```
mqtt-bench/
├── README.md                      # This file
├── Makefile                       # Build and deployment commands
├── k8s-local.ps1                  # Main script for K8s local (Windows)
├── loadgen-v2/                    # Virtual client multiplexer
│   ├── main.go                    # Connection pool, virtual clients
│   └── Dockerfile
├── worker-v2/                     # Auto-scaling message processor
│   ├── main.go                    # Subscriber pool, worker pool
│   └── Dockerfile
├── scripts/
│   ├── install-kind-windows.ps1   # Install Kind on Windows
│   ├── setup-kind-local.ps1       # Setup Kind cluster
│   ├── setup-monitoring.ps1       # Deploy monitoring stack
│   ├── monitor-k8s-local.sh       # Monitoring dashboard (bash)
│   ├── benchmark.sh               # Benchmark suite
│   └── dashboard.sh               # Terminal dashboard
├── k8s/
│   ├── overlays/
│   │   ├── 1m-local/              # Config for 1M clients
│   │   ├── 3m-local/              # Config for 3M clients
│   │   └── kind/                  # Config for Kind testing
│   └── monitoring/
│       ├── prometheus-config.yaml
│       ├── prometheus-rbac.yaml
│       ├── prometheus.yaml
│       ├── grafana.yaml
│       ├── emqx-production-dashboard.json
│       └── simple-dashboard.json
├── monitoring/                    # Docker Compose monitoring
│   ├── prometheus.yml
│   ├── dashboard.html
│   └── grafana/
├── docker-compose.production.yml
└── .gitignore
```

---

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

---

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

---

## Useful Commands

### General Kubernetes

```bash
# View all pods
kubectl get pods -n mqtt-bench -o wide

# Describe pod for debugging
kubectl describe pod -n mqtt-bench <pod-name>

# Exec into pod
kubectl exec -it -n mqtt-bench <pod-name> -- /bin/sh

# View events
kubectl get events -n mqtt-bench --sort-by='.lastTimestamp'

# Export metrics
kubectl port-forward -n mqtt-bench svc/loadgen 8090:8090 &
curl http://localhost:8090/metrics > metrics.txt

# Restart deployments
kubectl rollout restart deployment/loadgen -n mqtt-bench
kubectl rollout restart deployment/worker -n mqtt-bench

# View resource allocation
kubectl describe nodes | grep -A 5 "Allocated resources"

# Resource usage
kubectl top nodes
kubectl top pods -n mqtt-bench
```

---

## License

MIT