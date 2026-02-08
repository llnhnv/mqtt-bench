#!/bin/bash

# MQTT Request-Response Benchmark - Kubernetes Deployment
# Supports 1M+ virtual clients with horizontal scaling

set -e

NAMESPACE="mqtt-bench"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# Check prerequisites
check_prereqs() {
    log "Checking prerequisites..."
    command -v kubectl >/dev/null 2>&1 || error "kubectl not found"
    command -v docker >/dev/null 2>&1 || error "docker not found"
    kubectl cluster-info >/dev/null 2>&1 || error "Cannot connect to Kubernetes cluster"
    log "Prerequisites OK"
}

# Build and push images
build_images() {
    log "Building Docker images..."

    cd "$ROOT_DIR"

    # Build loadgen-v2
    docker build -t mqtt-rr-bench-loadgen-v2:latest -f loadgen-v2/Dockerfile loadgen-v2/

    # Build worker-v2
    docker build -t mqtt-rr-bench-worker-v2:latest -f worker-v2/Dockerfile worker-v2/

    # For minikube: load images directly
    if command -v minikube >/dev/null 2>&1 && minikube status >/dev/null 2>&1; then
        log "Loading images into minikube..."
        minikube image load mqtt-rr-bench-loadgen-v2:latest
        minikube image load mqtt-rr-bench-worker-v2:latest
    fi

    # For kind: load images
    if command -v kind >/dev/null 2>&1; then
        CLUSTER=$(kind get clusters 2>/dev/null | head -1)
        if [ -n "$CLUSTER" ]; then
            log "Loading images into kind cluster: $CLUSTER"
            kind load docker-image mqtt-rr-bench-loadgen-v2:latest --name "$CLUSTER"
            kind load docker-image mqtt-rr-bench-worker-v2:latest --name "$CLUSTER"
        fi
    fi

    log "Images built successfully"
}

# Deploy to Kubernetes
deploy() {
    local SCALE=${1:-1m}  # kind, 100k, 500k, 1m, 5m

    log "Deploying MQTT Benchmark ($SCALE scale)..."

    # Use lightweight overlay for kind
    if [ "$SCALE" = "kind" ]; then
        log "Using lightweight deployment for kind cluster..."
        kubectl apply -f "$SCRIPT_DIR/namespace.yaml"
        kubectl apply -f "$SCRIPT_DIR/overlays/kind/emqx-single.yaml"
        kubectl apply -f "$SCRIPT_DIR/overlays/kind/worker-light.yaml"
        kubectl apply -f "$SCRIPT_DIR/overlays/kind/loadgen-light.yaml"

        log "Waiting for EMQX to be ready..."
        kubectl wait --for=condition=available deployment/emqx -n "$NAMESPACE" --timeout=120s

        log "Waiting for workers to be ready..."
        kubectl rollout restart deployment/worker -n "$NAMESPACE"
        kubectl wait --for=condition=available deployment/worker -n "$NAMESPACE" --timeout=120s

        log "Deployment complete! (10k virtual clients for kind testing)"
        log ""
        log "=== Access Points ==="
        log "EMQX Dashboard: kubectl port-forward svc/emqx 18083:18083 -n $NAMESPACE"
        log "Logs: kubectl logs -f deployment/loadgen -n $NAMESPACE"
        return
    fi

    # Apply base configuration for production
    kubectl apply -k "$SCRIPT_DIR"

    # Wait for EMQX to be ready
    log "Waiting for EMQX cluster to be ready..."
    kubectl rollout status statefulset/emqx -n "$NAMESPACE" --timeout=300s

    # Wait for workers to be ready
    log "Waiting for workers to be ready..."
    kubectl rollout status deployment/worker -n "$NAMESPACE" --timeout=120s

    # Scale loadgen based on target
    case $SCALE in
        "1m"|"1M")
            REPLICAS=10  # 10 × 100k = 1M
            ;;
        "5m"|"5M")
            REPLICAS=50  # 50 × 100k = 5M
            ;;
        "100k")
            REPLICAS=1
            ;;
        "500k")
            REPLICAS=5
            ;;
        *)
            REPLICAS=10
            ;;
    esac

    log "Scaling loadgen to $REPLICAS replicas ($SCALE virtual clients)..."
    kubectl scale deployment/loadgen -n "$NAMESPACE" --replicas=$REPLICAS

    # Wait for loadgen to be ready
    kubectl rollout status deployment/loadgen -n "$NAMESPACE" --timeout=300s

    log "Deployment complete!"
    log ""
    log "=== Access Points ==="
    log "EMQX Dashboard: kubectl port-forward svc/emqx 18083:18083 -n $NAMESPACE"
    log "Worker Metrics: kubectl port-forward svc/worker 8080:8080 -n $NAMESPACE"
    log "Loadgen Metrics: kubectl port-forward svc/loadgen 8090:8090 -n $NAMESPACE"
}

# Status check
status() {
    log "Checking deployment status..."
    echo ""

    echo "=== Pods ==="
    kubectl get pods -n "$NAMESPACE" -o wide
    echo ""

    echo "=== EMQX Cluster ==="
    kubectl exec -n "$NAMESPACE" emqx-0 -- emqx ctl cluster status 2>/dev/null || warn "EMQX not ready"
    echo ""

    echo "=== HPA Status ==="
    kubectl get hpa -n "$NAMESPACE"
    echo ""

    echo "=== Resource Usage ==="
    kubectl top pods -n "$NAMESPACE" 2>/dev/null || warn "Metrics not available"
}

# Cleanup
cleanup() {
    log "Cleaning up..."
    kubectl delete namespace "$NAMESPACE" --ignore-not-found
    log "Cleanup complete"
}

# Show logs
logs() {
    local COMPONENT=${1:-"all"}

    case $COMPONENT in
        "emqx")
            kubectl logs -n "$NAMESPACE" -l app=emqx --tail=100 -f
            ;;
        "worker")
            kubectl logs -n "$NAMESPACE" -l app=worker --tail=100 -f
            ;;
        "loadgen")
            kubectl logs -n "$NAMESPACE" -l app=loadgen --tail=100 -f
            ;;
        *)
            log "Showing all logs (Ctrl+C to stop)..."
            kubectl logs -n "$NAMESPACE" -l app=loadgen --tail=50 &
            kubectl logs -n "$NAMESPACE" -l app=worker --tail=50 &
            kubectl logs -n "$NAMESPACE" -l app=emqx --tail=50 &
            wait
            ;;
    esac
}

# Dashboard
dashboard() {
    log "Opening port forwards..."

    # Kill existing port-forwards
    pkill -f "kubectl port-forward.*mqtt-bench" 2>/dev/null || true

    # Start port forwards
    kubectl port-forward svc/emqx 18083:18083 -n "$NAMESPACE" &
    kubectl port-forward svc/worker 8080:8080 -n "$NAMESPACE" &
    kubectl port-forward svc/loadgen 8090:8090 -n "$NAMESPACE" &

    sleep 2

    log "Dashboard URLs:"
    log "  EMQX: http://localhost:18083 (admin/public)"
    log "  Worker: http://localhost:8080/stats"
    log "  Loadgen: http://localhost:8090/debug/vars"

    wait
}

# Help
usage() {
    echo "Usage: $0 <command> [args]"
    echo ""
    echo "Commands:"
    echo "  build           Build Docker images"
    echo "  deploy [scale]  Deploy to Kubernetes (scale: kind, 100k, 500k, 1m, 5m)"
    echo "  status          Check deployment status"
    echo "  logs [comp]     Show logs (comp: emqx, worker, loadgen, all)"
    echo "  dashboard       Open monitoring dashboards"
    echo "  cleanup         Remove all resources"
    echo ""
    echo "Examples:"
    echo "  $0 build"
    echo "  $0 deploy kind  # Deploy lightweight for kind cluster (10k clients)"
    echo "  $0 deploy 1m    # Deploy with 1M virtual clients (production)"
    echo "  $0 deploy 5m    # Deploy with 5M virtual clients (production)"
    echo "  $0 status"
    echo "  $0 logs worker"
    echo "  $0 cleanup"
}

# Main
case ${1:-help} in
    build)
        check_prereqs
        build_images
        ;;
    deploy)
        check_prereqs
        deploy "${2:-1m}"
        ;;
    status)
        status
        ;;
    logs)
        logs "${2:-all}"
        ;;
    dashboard)
        dashboard
        ;;
    cleanup)
        cleanup
        ;;
    *)
        usage
        ;;
esac
