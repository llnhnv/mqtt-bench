#!/bin/bash

# Script để setup Kind cluster cho MQTT benchmark 1M/3M clients
set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log() { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

CLUSTER_NAME="mqtt-bench"
SCALE="${1:-1m}"  # 1m or 3m

# Check prerequisites
log "Checking prerequisites..."
command -v kind >/dev/null 2>&1 || error "kind not found. Install: https://kind.sigs.k8s.io/docs/user/quick-start/"
command -v kubectl >/dev/null 2>&1 || error "kubectl not found"
command -v docker >/dev/null 2>&1 || error "docker not found"

# Check if cluster exists
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    warn "Cluster '${CLUSTER_NAME}' already exists"
    read -p "Do you want to delete and recreate it? (y/N): " -r
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        log "Deleting existing cluster..."
        kind delete cluster --name "$CLUSTER_NAME"
    else
        log "Using existing cluster"
        kubectl cluster-info --context "kind-${CLUSTER_NAME}"
        exit 0
    fi
fi

# Create Kind config based on scale
log "Creating Kind cluster configuration for $SCALE..."

if [ "$SCALE" = "3m" ]; then
    MEMORY_HINT="48GB+"
    CPU_HINT="12+ cores"
else
    MEMORY_HINT="24GB+"
    CPU_HINT="8+ cores"
fi

log "⚠️  Make sure Docker Desktop has at least:"
log "   - Memory: $MEMORY_HINT"
log "   - CPUs: $CPU_HINT"
log "   - Swap: 8GB"

cat > /tmp/kind-config-mqtt-bench.yaml <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: ${CLUSTER_NAME}
nodes:
  - role: control-plane
    extraPortMappings:
      # EMQX Dashboard
      - containerPort: 18083
        hostPort: 18083
        protocol: TCP
      # EMQX MQTT
      - containerPort: 1883
        hostPort: 1883
        protocol: TCP
      # Worker metrics
      - containerPort: 30080
        hostPort: 30080
        protocol: TCP
      # Loadgen metrics
      - containerPort: 30090
        hostPort: 30090
        protocol: TCP
    kubeadmConfigPatches:
      - |
        kind: InitConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            node-labels: "mqtt-bench=enabled"
            system-reserved: memory=2Gi,cpu=1000m
            kube-reserved: memory=2Gi,cpu=1000m
            eviction-hard: memory.available<1Gi
            max-pods: "200"
    extraMounts:
      - hostPath: /dev/null
        containerPath: /dev/null
EOF

# Create cluster
log "Creating Kind cluster '$CLUSTER_NAME'..."
kind create cluster --config /tmp/kind-config-mqtt-bench.yaml

# Verify cluster
log "Verifying cluster..."
kubectl cluster-info --context "kind-${CLUSTER_NAME}"

# Label the node
log "Labeling nodes..."
kubectl label nodes --all mqtt-bench=enabled --overwrite

# Show cluster info
log ""
log "✅ Kind cluster created successfully!"
log ""
log "Next steps:"
log "  1. Build and load images:"
log "     make k8s-local-build"
log ""
if [ "$SCALE" = "3m" ]; then
    log "  2. Deploy 3M virtual clients:"
    log "     make k8s-local-3m"
else
    log "  2. Deploy 1M virtual clients:"
    log "     make k8s-local-1m"
fi
log ""
log "  3. Monitor deployment:"
log "     make k8s-local-monitor"
log ""
log "  4. Access dashboards:"
log "     kubectl port-forward -n mqtt-bench svc/emqx 18083:18083"
log "     Open: http://localhost:18083 (admin/public)"
log ""
log "  5. Cleanup when done:"
log "     kind delete cluster --name ${CLUSTER_NAME}"

# Show resource info
log ""
log "📊 Estimated resource usage for $SCALE:"
if [ "$SCALE" = "3m" ]; then
    log "   - RAM: ~44GB (30 loadgen + 20 worker + 1 EMQX + overhead)"
    log "   - CPU: ~80-95% utilization"
    log "   - Pods: ~51 total"
else
    log "   - RAM: ~20GB (10 loadgen + 10 worker + 1 EMQX + overhead)"
    log "   - CPU: ~60-80% utilization"
    log "   - Pods: ~21 total"
fi
