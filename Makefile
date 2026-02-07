# =============================================================================
# MQTT HIGH-PERFORMANCE ARCHITECTURE - Makefile
# =============================================================================

.PHONY: help build up down logs stats dashboard benchmark clean

# Default values
VIRTUAL_CLIENTS ?= 100000
LOADGEN_INSTANCES ?= 1
WORKER_INSTANCES ?= 1
INTERVAL_MS ?= 2000
COMPOSE_FILE ?= docker-compose.production.yml

help:
	@echo "MQTT High-Performance Architecture"
	@echo ""
	@echo "Usage: make <target> [VARIABLE=value]"
	@echo ""
	@echo "Quick Start:"
	@echo "  make up                    Start with default config (100k clients)"
	@echo "  make dashboard             Show live monitoring dashboard"
	@echo "  make down                  Stop all services"
	@echo ""
	@echo "Scaling:"
	@echo "  make up-100k               Start with 100k virtual clients"
	@echo "  make up-500k               Start with 500k virtual clients"
	@echo "  make up-1m                 Start with 1M virtual clients"
	@echo "  make up-5m                 Start with 5M clients (5 instances)"
	@echo ""
	@echo "Custom:"
	@echo "  make up VIRTUAL_CLIENTS=200000 LOADGEN_INSTANCES=2"
	@echo ""
	@echo "Benchmarking:"
	@echo "  make benchmark             Run full benchmark suite"
	@echo "  make benchmark-quick       Run quick benchmark"
	@echo ""
	@echo "Other:"
	@echo "  make build                 Build all containers"
	@echo "  make logs                  Follow container logs"
	@echo "  make stats                 Show current statistics"
	@echo "  make clean                 Remove containers and volumes"

# Build
build:
	docker-compose -f $(COMPOSE_FILE) build

# Start services
up:
	VIRTUAL_CLIENTS=$(VIRTUAL_CLIENTS) \
	INTERVAL_MS=$(INTERVAL_MS) \
	docker-compose -f $(COMPOSE_FILE) up -d \
		--scale loadgen-v2=$(LOADGEN_INSTANCES) \
		--scale worker-v2=$(WORKER_INSTANCES)
	@echo ""
	@echo "Services started! Run 'make dashboard' to monitor."

up-100k:
	$(MAKE) up VIRTUAL_CLIENTS=100000

up-500k:
	$(MAKE) up VIRTUAL_CLIENTS=500000

up-1m:
	$(MAKE) up VIRTUAL_CLIENTS=1000000

up-5m:
	$(MAKE) up VIRTUAL_CLIENTS=1000000 LOADGEN_INSTANCES=5 WORKER_INSTANCES=3

# Stop services
down:
	docker-compose -f $(COMPOSE_FILE) down

# Logs
logs:
	docker-compose -f $(COMPOSE_FILE) logs -f

logs-loadgen:
	docker-compose -f $(COMPOSE_FILE) logs -f loadgen-v2

logs-worker:
	docker-compose -f $(COMPOSE_FILE) logs -f worker-v2

# Monitoring
dashboard:
	@./scripts/dashboard.sh

dashboard-web:
	@echo "Opening web dashboard at http://localhost:8000"
	@cd monitoring && python3 -m http.server 8000 &
	@sleep 1 && open http://localhost:8000/dashboard.html 2>/dev/null || xdg-open http://localhost:8000/dashboard.html 2>/dev/null || echo "Open http://localhost:8000/dashboard.html"

monitoring-up:
	@echo "Starting Prometheus + Grafana..."
	docker-compose -f monitoring/docker-compose.monitoring.yml up -d
	@echo ""
	@echo "Prometheus: http://localhost:9090"
	@echo "Grafana: http://localhost:3000 (admin/admin)"

monitoring-down:
	docker-compose -f monitoring/docker-compose.monitoring.yml down

stats:
	@echo "=== LOADGEN ==="
	@curl -s http://localhost:8090/debug/vars 2>/dev/null | jq '.' || echo "Not available"
	@echo ""
	@echo "=== WORKER ==="
	@curl -s http://localhost:8080/stats 2>/dev/null | jq '.' || echo "Not available"
	@echo ""
	@echo "=== EMQX ==="
	@curl -s -u admin:public http://localhost:18083/api/v5/stats 2>/dev/null | jq '{connections: .["connections.count"], topics: .["topics.count"], subscriptions: .["subscriptions.count"]}' || echo "Not available"

# Benchmarking
benchmark:
	@./scripts/benchmark.sh all

benchmark-quick:
	@./scripts/benchmark.sh quick

benchmark-clients:
	@./scripts/benchmark.sh clients

benchmark-instances:
	@./scripts/benchmark.sh instances

# Scale
scale-loadgen:
	docker-compose -f $(COMPOSE_FILE) up -d --scale loadgen-v2=$(LOADGEN_INSTANCES) --no-recreate

scale-worker:
	docker-compose -f $(COMPOSE_FILE) up -d --scale worker-v2=$(WORKER_INSTANCES) --no-recreate

# Cleanup
clean:
	docker-compose -f $(COMPOSE_FILE) down -v --remove-orphans
	docker system prune -f

# Development
dev-build-loadgen:
	cd loadgen-v2 && go build -o loadgen-v2 .

dev-build-worker:
	cd worker-v2 && go build -o worker-v2 .

dev-test:
	cd loadgen-v2 && go test -v ./...
	cd worker-v2 && go test -v ./...

# =============================================================================
# KUBERNETES DEPLOYMENT (for 1M+ clients)
# =============================================================================

k8s-build:
	@./k8s/deploy.sh build

k8s-deploy-kind:
	@./k8s/deploy.sh deploy kind

k8s-deploy-1m:
	@./k8s/deploy.sh deploy 1m

k8s-deploy-5m:
	@./k8s/deploy.sh deploy 5m

k8s-status:
	@./k8s/deploy.sh status

k8s-logs:
	@./k8s/deploy.sh logs

k8s-dashboard:
	@./k8s/deploy.sh dashboard

k8s-cleanup:
	@./k8s/deploy.sh cleanup
