#!/bin/bash

# Script monitoring chi tiết cho MQTT benchmark trên K8s local
set -e

NAMESPACE="mqtt-bench"
REFRESH_INTERVAL=${1:-5}  # seconds

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

clear_screen() {
    clear
}

print_header() {
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}   MQTT Request-Response Benchmark - K8s Local Monitoring${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "Namespace: ${GREEN}${NAMESPACE}${NC} | Refresh: ${REFRESH_INTERVAL}s | $(date '+%Y-%m-%d %H:%M:%S')"
    echo ""
}

print_pods_status() {
    echo -e "${YELLOW}▶ Pods Status${NC}"
    echo "─────────────────────────────────────────────────────────────"
    kubectl get pods -n "$NAMESPACE" -o wide 2>/dev/null | head -20 || echo "No pods found"
    echo ""
}

print_resource_usage() {
    echo -e "${YELLOW}▶ Resource Usage${NC}"
    echo "─────────────────────────────────────────────────────────────"
    kubectl top pods -n "$NAMESPACE" 2>/dev/null | head -20 || echo "Metrics server not available (install: kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml)"
    echo ""
}

print_node_usage() {
    echo -e "${YELLOW}▶ Node Resource Usage${NC}"
    echo "─────────────────────────────────────────────────────────────"
    kubectl top nodes 2>/dev/null || echo "Metrics server not available"
    echo ""
}

print_loadgen_stats() {
    echo -e "${YELLOW}▶ Loadgen Statistics (sample from first pod)${NC}"
    echo "─────────────────────────────────────────────────────────────"

    LOADGEN_POD=$(kubectl get pods -n "$NAMESPACE" -l app=loadgen -o name 2>/dev/null | head -1 | cut -d/ -f2)

    if [ -n "$LOADGEN_POD" ]; then
        STATS=$(kubectl exec -n "$NAMESPACE" "$LOADGEN_POD" -- wget -qO- http://localhost:8090/debug/vars 2>/dev/null)

        if [ $? -eq 0 ]; then
            VIRTUAL_CLIENTS=$(echo "$STATS" | grep -o '"virtual_clients":[0-9]*' | cut -d: -f2)
            SENT=$(echo "$STATS" | grep -o '"sent":[0-9]*' | cut -d: -f2)
            RECEIVED=$(echo "$STATS" | grep -o '"received":[0-9]*' | cut -d: -f2)
            MATCH_RATE=$(echo "$STATS" | grep -o '"match_rate":[0-9.]*' | cut -d: -f2)
            LATENCY_P50=$(echo "$STATS" | grep -o '"latency_p50_ms":[0-9.]*' | cut -d: -f2)
            LATENCY_P99=$(echo "$STATS" | grep -o '"latency_p99_ms":[0-9.]*' | cut -d: -f2)

            echo "  Virtual Clients: ${GREEN}${VIRTUAL_CLIENTS:-0}${NC}"
            echo "  Messages Sent: ${GREEN}${SENT:-0}${NC}"
            echo "  Messages Received: ${GREEN}${RECEIVED:-0}${NC}"
            echo "  Match Rate: ${GREEN}${MATCH_RATE:-0}%${NC}"
            echo "  Latency P50: ${GREEN}${LATENCY_P50:-0}ms${NC}"
            echo "  Latency P99: ${GREEN}${LATENCY_P99:-0}ms${NC}"
        else
            echo "  ⚠️  Loadgen not ready or metrics endpoint unavailable"
        fi
    else
        echo "  ⚠️  No loadgen pods found"
    fi
    echo ""
}

print_worker_stats() {
    echo -e "${YELLOW}▶ Worker Statistics (sample from first pod)${NC}"
    echo "─────────────────────────────────────────────────────────────"

    WORKER_POD=$(kubectl get pods -n "$NAMESPACE" -l app=worker -o name 2>/dev/null | head -1 | cut -d/ -f2)

    if [ -n "$WORKER_POD" ]; then
        STATS=$(kubectl exec -n "$NAMESPACE" "$WORKER_POD" -- wget -qO- http://localhost:8080/stats 2>/dev/null)

        if [ $? -eq 0 ]; then
            RECEIVED=$(echo "$STATS" | grep -o '"received":[0-9]*' | cut -d: -f2)
            PROCESSED=$(echo "$STATS" | grep -o '"processed":[0-9]*' | cut -d: -f2)
            SENT=$(echo "$STATS" | grep -o '"sent":[0-9]*' | cut -d: -f2)
            CURRENT_WORKERS=$(echo "$STATS" | grep -o '"current_workers":[0-9]*' | cut -d: -f2)
            QUEUE_SIZE=$(echo "$STATS" | grep -o '"queue_size":[0-9]*' | cut -d: -f2)

            echo "  Received: ${GREEN}${RECEIVED:-0}${NC}"
            echo "  Processed: ${GREEN}${PROCESSED:-0}${NC}"
            echo "  Sent: ${GREEN}${SENT:-0}${NC}"
            echo "  Current Workers: ${GREEN}${CURRENT_WORKERS:-0}${NC}"
            echo "  Queue Size: ${GREEN}${QUEUE_SIZE:-0}${NC}"
        else
            echo "  ⚠️  Worker not ready or stats endpoint unavailable"
        fi
    else
        echo "  ⚠️  No worker pods found"
    fi
    echo ""
}

print_emqx_stats() {
    echo -e "${YELLOW}▶ EMQX Broker Statistics${NC}"
    echo "─────────────────────────────────────────────────────────────"

    EMQX_POD=$(kubectl get pods -n "$NAMESPACE" -l app=emqx -o name 2>/dev/null | head -1 | cut -d/ -f2)

    if [ -n "$EMQX_POD" ]; then
        STATS=$(kubectl exec -n "$NAMESPACE" "$EMQX_POD" -- emqx ctl broker stats 2>/dev/null)

        if [ $? -eq 0 ]; then
            CONNECTIONS=$(echo "$STATS" | grep "connections.count" | awk '{print $2}')
            TOPICS=$(echo "$STATS" | grep "topics.count" | awk '{print $2}')
            SUBS=$(echo "$STATS" | grep "subscriptions.count" | awk '{print $2}')

            echo "  Connections: ${GREEN}${CONNECTIONS:-0}${NC}"
            echo "  Topics: ${GREEN}${TOPICS:-0}${NC}"
            echo "  Subscriptions: ${GREEN}${SUBS:-0}${NC}"
        else
            echo "  ⚠️  EMQX not ready"
        fi
    else
        echo "  ⚠️  No EMQX pods found"
    fi
    echo ""
}

print_deployment_summary() {
    echo -e "${YELLOW}▶ Deployment Summary${NC}"
    echo "─────────────────────────────────────────────────────────────"

    LOADGEN_REPLICAS=$(kubectl get deployment -n "$NAMESPACE" loadgen -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
    LOADGEN_READY=$(kubectl get deployment -n "$NAMESPACE" loadgen -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
    WORKER_REPLICAS=$(kubectl get deployment -n "$NAMESPACE" worker -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
    WORKER_READY=$(kubectl get deployment -n "$NAMESPACE" worker -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
    EMQX_REPLICAS=$(kubectl get deployment -n "$NAMESPACE" emqx -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
    EMQX_READY=$(kubectl get deployment -n "$NAMESPACE" emqx -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")

    TOTAL_CLIENTS=$((LOADGEN_REPLICAS * 100000))

    echo "  Loadgen: ${GREEN}${LOADGEN_READY}/${LOADGEN_REPLICAS}${NC} ready"
    echo "  Worker:  ${GREEN}${WORKER_READY}/${WORKER_REPLICAS}${NC} ready"
    echo "  EMQX:    ${GREEN}${EMQX_READY}/${EMQX_REPLICAS}${NC} ready"
    echo ""
    echo "  Total Virtual Clients: ${GREEN}${TOTAL_CLIENTS}${NC}"
    echo ""
}

print_help() {
    echo -e "${YELLOW}▶ Quick Actions${NC}"
    echo "─────────────────────────────────────────────────────────────"
    echo "  Logs:      kubectl logs -n $NAMESPACE -l app=loadgen --tail=50 -f"
    echo "  Dashboard: kubectl port-forward -n $NAMESPACE svc/emqx 18083:18083"
    echo "  Scale:     kubectl scale deployment/loadgen -n $NAMESPACE --replicas=N"
    echo "  Restart:   kubectl rollout restart deployment/loadgen -n $NAMESPACE"
    echo ""
}

# Main monitoring loop
if [ "$1" = "--once" ]; then
    print_header
    print_deployment_summary
    print_pods_status
    print_resource_usage
    print_node_usage
    print_loadgen_stats
    print_worker_stats
    print_emqx_stats
    print_help
else
    while true; do
        clear_screen
        print_header
        print_deployment_summary
        print_pods_status
        print_resource_usage
        print_node_usage
        print_loadgen_stats
        print_worker_stats
        print_emqx_stats
        print_help

        echo -e "${BLUE}Press Ctrl+C to exit${NC}"
        sleep "$REFRESH_INTERVAL"
    done
fi
