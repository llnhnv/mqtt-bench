package main

import (
	"encoding/binary"
	"expvar"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ============================================================================
// Metrics
// ============================================================================

var (
	sentTotal      = expvar.NewInt("sent_total")
	recvMatchTotal = expvar.NewInt("recv_match_total")
	recvIgnoreTotal = expvar.NewInt("recv_ignore_total")
	pubErrTotal    = expvar.NewInt("pub_err_total")
	connRetryTotal = expvar.NewInt("conn_retry_total")
	inflightTotal  = expvar.NewInt("inflight_total")
	activeClients  = expvar.NewInt("active_clients")
	activeConns    = expvar.NewInt("active_connections")
	upSince        = expvar.NewInt("up_since")
	lastErr        = expvar.NewString("last_err")

	// Latency percentiles (microseconds)
	latP50  = expvar.NewInt("lat_p50_us")
	latP95  = expvar.NewInt("lat_p95_us")
	latP99  = expvar.NewInt("lat_p99_us")
	latP999 = expvar.NewInt("lat_p999_us")
)

// Prometheus metrics
var (
	promSentTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_loadgen_messages_sent_total",
		Help: "Total messages sent",
	})
	promRecvMatchTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_loadgen_messages_matched_total",
		Help: "Total response messages matched",
	})
	promPubErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_loadgen_publish_errors_total",
		Help: "Total publish errors",
	})
	promActiveClients = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_loadgen_active_clients",
		Help: "Number of active virtual clients",
	})
	promActiveConns = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_loadgen_active_connections",
		Help: "Number of active MQTT connections",
	})
	promInflight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_loadgen_inflight_messages",
		Help: "Number of inflight messages",
	})
	promLatency = prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "mqtt_loadgen_latency_microseconds",
		Help:       "Request-response latency in microseconds",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.95: 0.005, 0.99: 0.001, 0.999: 0.0001},
	})
	promThroughputSent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_loadgen_throughput_sent_per_sec",
		Help: "Messages sent per second",
	})
	promThroughputRecv = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_loadgen_throughput_recv_per_sec",
		Help: "Messages received per second",
	})
)

func init() {
	prometheus.MustRegister(
		promSentTotal, promRecvMatchTotal, promPubErrors,
		promActiveClients, promActiveConns, promInflight,
		promLatency, promThroughputSent, promThroughputRecv,
	)
}

// ============================================================================
// Configuration
// ============================================================================

type Config struct {
	Broker               string
	VirtualClients       int           // Number of virtual clients (target: 1M+)
	Connections          int           // Number of actual MQTT connections (50-200)
	Shards               int           // Number of topic shards
	IntervalMS           int           // Base interval between sends per client
	MaxInflightPerClient int           // Max pending requests per virtual client
	ReqQoS               byte          // QoS for request messages
	RespQoS              byte          // QoS for response subscription
	WarmupSec            int           // Warmup period before measuring latency
	DurationSec          int           // Total test duration (0 = infinite)
	ConnectRetries       int           // Connection retry attempts
	ConnectBackoffMS     int           // Base backoff between retries
	MetricsPort          int           // HTTP metrics port
	InstanceID           string        // Unique instance identifier
	UseSharedSub         bool          // Use MQTT 5.0 shared subscriptions
	BatchSize            int           // Messages per batch publish
	PublishWorkers       int           // Number of publisher goroutines
}

func loadConfig() Config {
	hostname, _ := os.Hostname()

	return Config{
		Broker:               envStr("BROKER", "tcp://localhost:1883"),
		VirtualClients:       envInt("VIRTUAL_CLIENTS", 100000),      // 100k default, can go to 1M+
		Connections:          envInt("CONNECTIONS", 50),              // Only 50 real connections!
		Shards:               envInt("SHARDS", 128),
		IntervalMS:           envInt("INTERVAL_MS", 1000),
		MaxInflightPerClient: envInt("MAX_INFLIGHT_PER_CLIENT", 1),
		ReqQoS:               byte(envInt("REQ_QOS", 0)),
		RespQoS:              byte(envInt("RESP_QOS", 0)),
		WarmupSec:            envInt("WARMUP_SEC", 5),
		DurationSec:          envInt("DURATION_SEC", 0),              // 0 = run forever
		ConnectRetries:       envInt("CONNECT_RETRIES", 10),
		ConnectBackoffMS:     envInt("CONNECT_BACKOFF_MS", 200),
		MetricsPort:          envInt("METRICS_PORT", 8090),
		InstanceID:           envStr("INSTANCE_ID", hostname),
		UseSharedSub:         envBool("USE_SHARED_SUB", true),
		BatchSize:            envInt("BATCH_SIZE", 100),
		PublishWorkers:       envInt("PUBLISH_WORKERS", 10),
	}
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		return v == "true" || v == "1" || v == "yes"
	}
	return def
}

// ============================================================================
// Virtual Client - Minimal memory footprint (~40 bytes per client)
// ============================================================================

type VirtualClient struct {
	id          uint32    // Client index (0 to N-1)
	shard       uint16    // Assigned shard
	inflight    int32     // Current inflight requests (atomic)
	lastSendNs  int64     // Last send timestamp (atomic)
}

// Response timeout in nanoseconds (5 seconds)
const ResponseTimeoutNs = 5 * int64(time.Second)

// VirtualClientPool manages millions of virtual clients efficiently
type VirtualClientPool struct {
	clients    []VirtualClient
	clientByID map[string]uint32  // Fast lookup: "instance-c-123" -> index
	instanceID string
	mu         sync.RWMutex
}

func NewVirtualClientPool(instanceID string, count int, shards int) *VirtualClientPool {
	pool := &VirtualClientPool{
		clients:    make([]VirtualClient, count),
		clientByID: make(map[string]uint32, count),
		instanceID: instanceID,
	}

	for i := 0; i < count; i++ {
		clientID := fmt.Sprintf("%s-c-%d", instanceID, i)
		shard := shardFor(clientID, shards)

		pool.clients[i] = VirtualClient{
			id:    uint32(i),
			shard: uint16(shard),
		}
		pool.clientByID[clientID] = uint32(i)
	}

	return pool
}

func (p *VirtualClientPool) GetClient(idx int) *VirtualClient {
	if idx < 0 || idx >= len(p.clients) {
		return nil
	}
	return &p.clients[idx]
}

func (p *VirtualClientPool) GetClientByID(clientID string) *VirtualClient {
	p.mu.RLock()
	idx, ok := p.clientByID[clientID]
	p.mu.RUnlock()
	if !ok {
		return nil
	}
	return &p.clients[idx]
}

func (p *VirtualClientPool) ClientID(idx int) string {
	return fmt.Sprintf("%s-c-%d", p.instanceID, idx)
}

func (p *VirtualClientPool) Size() int {
	return len(p.clients)
}

func shardFor(clientID string, shards int) int {
	h := fnv.New32a()
	h.Write([]byte(clientID))
	return int(h.Sum32() % uint32(shards))
}

// ============================================================================
// Connection Pool - Manages actual MQTT connections
// ============================================================================

type ConnectionPool struct {
	connections []mqtt.Client
	cfg         Config
	pool        *VirtualClientPool
	latWin      *LatencyWindow
	warmupEnd   time.Time
}

func NewConnectionPool(cfg Config, pool *VirtualClientPool, latWin *LatencyWindow, warmupEnd time.Time) *ConnectionPool {
	return &ConnectionPool{
		connections: make([]mqtt.Client, 0, cfg.Connections),
		cfg:         cfg,
		pool:        pool,
		latWin:      latWin,
		warmupEnd:   warmupEnd,
	}
}

func (cp *ConnectionPool) Connect() error {
	log.Printf("[pool] Creating %d connections for %d virtual clients (ratio 1:%d)",
		cp.cfg.Connections, cp.cfg.VirtualClients, cp.cfg.VirtualClients/cp.cfg.Connections)

	for i := 0; i < cp.cfg.Connections; i++ {
		connID := fmt.Sprintf("%s-conn-%d", cp.cfg.InstanceID, i)

		opts := mqtt.NewClientOptions().
			AddBroker(cp.cfg.Broker).
			SetClientID(connID).
			SetAutoReconnect(true).
			SetOrderMatters(false).
			SetConnectTimeout(10 * time.Second).
			SetWriteTimeout(10 * time.Second).
			SetMaxReconnectInterval(10 * time.Second).
			SetCleanSession(true).
			SetKeepAlive(60 * time.Second).
			SetPingTimeout(30 * time.Second).
			SetMessageChannelDepth(100000).
			SetConnectionLostHandler(func(c mqtt.Client, err error) {
				log.Printf("[conn-%d] Connection lost: %v", i, err)
			}).
			SetReconnectingHandler(func(c mqtt.Client, opts *mqtt.ClientOptions) {
				log.Printf("[conn-%d] Reconnecting...", i)
				connRetryTotal.Add(1)
			})

		client := mqtt.NewClient(opts)

		if err := cp.connectWithRetry(client, connID); err != nil {
			log.Printf("[pool] Failed to connect %s after retries: %v", connID, err)
			continue
		}

		cp.connections = append(cp.connections, client)
		activeConns.Add(1)

		// Small delay between connections to avoid thundering herd
		if i > 0 && i%10 == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}

	if len(cp.connections) == 0 {
		return fmt.Errorf("no connections established")
	}

	log.Printf("[pool] Established %d/%d connections", len(cp.connections), cp.cfg.Connections)
	return nil
}

func (cp *ConnectionPool) connectWithRetry(client mqtt.Client, connID string) error {
	backoff := time.Duration(cp.cfg.ConnectBackoffMS) * time.Millisecond

	for attempt := 0; attempt <= cp.cfg.ConnectRetries; attempt++ {
		token := client.Connect()
		if token.WaitTimeout(10*time.Second) && token.Error() == nil {
			return nil
		}

		connRetryTotal.Add(1)
		if attempt < cp.cfg.ConnectRetries {
			sleep := backoff * time.Duration(1+attempt)
			time.Sleep(sleep)
		}
	}

	return fmt.Errorf("connection failed after %d retries", cp.cfg.ConnectRetries)
}

func (cp *ConnectionPool) Subscribe() error {
	// Calculate which shards this instance handles
	shardsPerConn := (cp.cfg.Shards + len(cp.connections) - 1) / len(cp.connections)

	for i, client := range cp.connections {
		startShard := i * shardsPerConn
		endShard := min(startShard+shardsPerConn, cp.cfg.Shards)

		for shard := startShard; shard < endShard; shard++ {
			topic := cp.makeRespTopic(shard)

			token := client.Subscribe(topic, cp.cfg.RespQoS, cp.makeMessageHandler())
			if !token.WaitTimeout(10 * time.Second) {
				log.Printf("[pool] Subscribe timeout for %s", topic)
				continue
			}
			if token.Error() != nil {
				log.Printf("[pool] Subscribe error for %s: %v", topic, token.Error())
				continue
			}
		}
	}

	log.Printf("[pool] Subscribed to %d shards using %s subscriptions",
		cp.cfg.Shards, map[bool]string{true: "shared", false: "individual"}[cp.cfg.UseSharedSub])
	return nil
}

func (cp *ConnectionPool) makeRespTopic(shard int) string {
	if cp.cfg.UseSharedSub {
		// MQTT 5.0 shared subscription - all instances share the load
		return fmt.Sprintf("$share/loadgen/resp/%d", shard)
	}
	return fmt.Sprintf("resp/%d", shard)
}

func (cp *ConnectionPool) makeMessageHandler() mqtt.MessageHandler {
	return func(_ mqtt.Client, msg mqtt.Message) {
		clientID, sentNs, ok := parsePayload(msg.Payload())
		if !ok {
			return
		}

		// Find virtual client
		vc := cp.pool.GetClientByID(clientID)
		if vc == nil {
			recvIgnoreTotal.Add(1)
			return
		}

		// Decrement inflight
		atomic.AddInt32(&vc.inflight, -1)
		recvMatchTotal.Add(1)
		promRecvMatchTotal.Inc()

		// Record latency if past warmup
		if time.Now().After(cp.warmupEnd) {
			latUs := (time.Now().UnixNano() - sentNs) / 1000
			cp.latWin.Add(latUs)
			promLatency.Observe(float64(latUs))
		}
	}
}

func (cp *ConnectionPool) GetConnection(idx int) mqtt.Client {
	if len(cp.connections) == 0 {
		return nil
	}
	return cp.connections[idx%len(cp.connections)]
}

func (cp *ConnectionPool) Size() int {
	return len(cp.connections)
}

func (cp *ConnectionPool) Close() {
	for _, client := range cp.connections {
		client.Disconnect(1000)
	}
}

// ============================================================================
// Publisher - High-throughput message publishing
// ============================================================================

type Publisher struct {
	pool    *VirtualClientPool
	conns   *ConnectionPool
	cfg     Config
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func NewPublisher(pool *VirtualClientPool, conns *ConnectionPool, cfg Config) *Publisher {
	return &Publisher{
		pool:   pool,
		conns:  conns,
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

func (p *Publisher) Start() {
	log.Printf("[publisher] Starting %d workers for %d virtual clients",
		p.cfg.PublishWorkers, p.cfg.VirtualClients)

	clientsPerWorker := p.cfg.VirtualClients / p.cfg.PublishWorkers

	for w := 0; w < p.cfg.PublishWorkers; w++ {
		startIdx := w * clientsPerWorker
		endIdx := startIdx + clientsPerWorker
		if w == p.cfg.PublishWorkers-1 {
			endIdx = p.cfg.VirtualClients // Last worker takes remainder
		}

		p.wg.Add(1)
		go p.worker(w, startIdx, endIdx)
	}
}

func (p *Publisher) worker(workerID, startIdx, endIdx int) {
	defer p.wg.Done()

	interval := time.Duration(p.cfg.IntervalMS) * time.Millisecond
	ticker := time.NewTicker(interval / time.Duration(p.cfg.BatchSize))
	defer ticker.Stop()

	// Each worker manages a subset of virtual clients
	clientCount := endIdx - startIdx
	batchSize := min(p.cfg.BatchSize, clientCount)
	currentOffset := 0

	log.Printf("[worker-%d] Managing clients %d-%d (%d clients)",
		workerID, startIdx, endIdx-1, clientCount)

	// Pre-allocate payload buffer
	payloadBuf := make([]byte, 0, 128)

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			// Process a batch of clients
			for i := 0; i < batchSize; i++ {
				clientIdx := startIdx + ((currentOffset + i) % clientCount)
				vc := p.pool.GetClient(clientIdx)
				if vc == nil {
					continue
				}

				// Check inflight limit with timeout
				inflight := atomic.LoadInt32(&vc.inflight)
				if inflight >= int32(p.cfg.MaxInflightPerClient) {
					// Check if request timed out (no response received)
					lastSend := atomic.LoadInt64(&vc.lastSendNs)
					if lastSend > 0 && time.Now().UnixNano()-lastSend > ResponseTimeoutNs {
						// Timeout - reset inflight to allow new request
						atomic.StoreInt32(&vc.inflight, 0)
					} else {
						inflightTotal.Add(1)
						continue
					}
				}

				// Get connection for this client (round-robin)
				conn := p.conns.GetConnection(clientIdx)
				if conn == nil || !conn.IsConnected() {
					continue
				}

				// Build payload: "clientID|timestampNs"
				clientID := p.pool.ClientID(clientIdx)
				nowNs := time.Now().UnixNano()
				payloadBuf = payloadBuf[:0]
				payloadBuf = append(payloadBuf, clientID...)
				payloadBuf = append(payloadBuf, '|')
				payloadBuf = strconv.AppendInt(payloadBuf, nowNs, 10)

				// Publish
				topic := fmt.Sprintf("req/%d", vc.shard)
				token := conn.Publish(topic, p.cfg.ReqQoS, false, payloadBuf)

				// Fire and forget for QoS 0, wait briefly for QoS 1+
				if p.cfg.ReqQoS > 0 {
					go func(t mqtt.Token, client *VirtualClient) {
						if !t.WaitTimeout(5 * time.Second) || t.Error() != nil {
							pubErrTotal.Add(1)
							promPubErrors.Inc()
							atomic.AddInt32(&client.inflight, -1)
							if t.Error() != nil {
								lastErr.Set(t.Error().Error())
							}
						}
					}(token, vc)
				}

				atomic.AddInt32(&vc.inflight, 1)
				atomic.StoreInt64(&vc.lastSendNs, nowNs)
				sentTotal.Add(1)
				promSentTotal.Inc()
			}

			currentOffset = (currentOffset + batchSize) % clientCount
		}
	}
}

func (p *Publisher) Stop() {
	close(p.stopCh)
	p.wg.Wait()
}

// ============================================================================
// Latency Window - Lock-free circular buffer for latency tracking
// ============================================================================

type LatencyWindow struct {
	samples []int64
	head    uint64
	size    uint64
}

func NewLatencyWindow(size int) *LatencyWindow {
	return &LatencyWindow{
		samples: make([]int64, size),
		size:    uint64(size),
	}
}

func (w *LatencyWindow) Add(latencyUs int64) {
	idx := atomic.AddUint64(&w.head, 1) % w.size
	atomic.StoreInt64(&w.samples[idx], latencyUs)
}

func (w *LatencyWindow) Percentiles() (p50, p95, p99, p999 int64) {
	head := atomic.LoadUint64(&w.head)
	if head == 0 {
		return 0, 0, 0, 0
	}

	count := minU64(head, w.size)
	data := make([]int64, count)

	for i := uint64(0); i < count; i++ {
		idx := (head - count + 1 + i) % w.size
		data[i] = atomic.LoadInt64(&w.samples[idx])
	}

	// Sort for percentile calculation
	sortInt64(data)

	n := len(data)
	p50 = data[n*50/100]
	p95 = data[n*95/100]
	p99 = data[n*99/100]
	p999 = data[min(n*999/1000, n-1)]

	return
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func sortInt64(data []int64) {
	// Simple insertion sort for small arrays, quicksort for larger
	if len(data) < 50 {
		for i := 1; i < len(data); i++ {
			for j := i; j > 0 && data[j] < data[j-1]; j-- {
				data[j], data[j-1] = data[j-1], data[j]
			}
		}
		return
	}
	quickSortInt64(data, 0, len(data)-1)
}

func quickSortInt64(data []int64, low, high int) {
	if low < high {
		p := partitionInt64(data, low, high)
		quickSortInt64(data, low, p-1)
		quickSortInt64(data, p+1, high)
	}
}

func partitionInt64(data []int64, low, high int) int {
	pivot := data[high]
	i := low - 1
	for j := low; j < high; j++ {
		if data[j] <= pivot {
			i++
			data[i], data[j] = data[j], data[i]
		}
	}
	data[i+1], data[high] = data[high], data[i+1]
	return i + 1
}

// ============================================================================
// Payload parsing
// ============================================================================

func parsePayload(payload []byte) (clientID string, sentNs int64, ok bool) {
	for i := len(payload) - 1; i >= 0; i-- {
		if payload[i] == '|' {
			clientID = string(payload[:i])
			sentNs, _ = strconv.ParseInt(string(payload[i+1:]), 10, 64)
			return clientID, sentNs, true
		}
	}
	return "", 0, false
}

// Binary payload for even lower overhead (optional)
func makeBinaryPayload(clientIdx uint32, timestampNs int64) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], clientIdx)
	binary.BigEndian.PutUint64(buf[4:12], uint64(timestampNs))
	return buf
}

func parseBinaryPayload(payload []byte) (clientIdx uint32, sentNs int64, ok bool) {
	if len(payload) < 12 {
		return 0, 0, false
	}
	clientIdx = binary.BigEndian.Uint32(payload[0:4])
	sentNs = int64(binary.BigEndian.Uint64(payload[4:12]))
	return clientIdx, sentNs, true
}

// ============================================================================
// Stats Reporter
// ============================================================================

func startStatsReporter(latWin *LatencyWindow, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var lastSent, lastRecv int64

		for range ticker.C {
			sent := sentTotal.Value()
			recv := recvMatchTotal.Value()

			sentRate := sent - lastSent
			recvRate := recv - lastRecv
			lastSent, lastRecv = sent, recv

			p50, p95, p99, p999 := latWin.Percentiles()
			latP50.Set(p50)
			latP95.Set(p95)
			latP99.Set(p99)
			latP999.Set(p999)

			// Update Prometheus gauges
			intervalSec := int64(interval.Seconds())
			promThroughputSent.Set(float64(sentRate / intervalSec))
			promThroughputRecv.Set(float64(recvRate / intervalSec))
			promActiveClients.Set(float64(activeClients.Value()))
			promActiveConns.Set(float64(activeConns.Value()))
			promInflight.Set(float64(inflightTotal.Value()))

			var memStats runtime.MemStats
			runtime.ReadMemStats(&memStats)

			log.Printf("[stats] sent=%d/s recv=%d/s total_sent=%d total_recv=%d p50=%dμs p95=%dμs p99=%dμs goroutines=%d mem=%.1fMB",
				sentRate/intervalSec,
				recvRate/intervalSec,
				sent,
				recv,
				p50, p95, p99,
				runtime.NumGoroutine(),
				float64(memStats.Alloc)/1024/1024,
			)
		}
	}()
}

// ============================================================================
// Main
// ============================================================================

func main() {
	cfg := loadConfig()

	log.Printf("=== LOADGEN-V2 - High-Performance MQTT Load Generator ===")
	log.Printf("[config] Virtual clients: %d", cfg.VirtualClients)
	log.Printf("[config] Actual connections: %d (ratio 1:%d)", cfg.Connections, cfg.VirtualClients/cfg.Connections)
	log.Printf("[config] Shards: %d", cfg.Shards)
	log.Printf("[config] Interval: %dms", cfg.IntervalMS)
	log.Printf("[config] Shared subscriptions: %v", cfg.UseSharedSub)
	log.Printf("[config] Publish workers: %d", cfg.PublishWorkers)
	log.Printf("[config] Batch size: %d", cfg.BatchSize)

	// Print estimated resource usage
	estimatedMemMB := float64(cfg.VirtualClients*40) / 1024 / 1024 // ~40 bytes per virtual client
	log.Printf("[estimate] Memory for %d virtual clients: ~%.1f MB", cfg.VirtualClients, estimatedMemMB)
	log.Printf("[estimate] Goroutines: ~%d (workers) + ~%d (connections) = ~%d total",
		cfg.PublishWorkers, cfg.Connections*2, cfg.PublishWorkers+cfg.Connections*2)

	// Start metrics server
	upSince.Set(time.Now().Unix())
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.Handle("/debug/vars", expvar.Handler())
		addr := fmt.Sprintf(":%d", cfg.MetricsPort)
		log.Printf("[metrics] Starting HTTP server on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[metrics] HTTP server error: %v", err)
		}
	}()

	// Create virtual client pool
	log.Printf("[init] Creating %d virtual clients...", cfg.VirtualClients)
	startTime := time.Now()
	pool := NewVirtualClientPool(cfg.InstanceID, cfg.VirtualClients, cfg.Shards)
	activeClients.Set(int64(cfg.VirtualClients))
	log.Printf("[init] Created %d virtual clients in %v", cfg.VirtualClients, time.Since(startTime))

	// Create latency window
	latWin := NewLatencyWindow(100000) // 100k samples
	warmupEnd := time.Now().Add(time.Duration(cfg.WarmupSec) * time.Second)

	// Create connection pool
	connPool := NewConnectionPool(cfg, pool, latWin, warmupEnd)
	if err := connPool.Connect(); err != nil {
		log.Fatalf("[fatal] Failed to establish connections: %v", err)
	}

	// Subscribe to response topics
	if err := connPool.Subscribe(); err != nil {
		log.Fatalf("[fatal] Failed to subscribe: %v", err)
	}

	// Wait for other services (e.g., worker) to be ready
	startupDelay := time.Duration(cfg.WarmupSec) * time.Second
	log.Printf("[main] Waiting %v before starting publisher (warmup)...", startupDelay)
	time.Sleep(startupDelay)

	// Start publisher
	publisher := NewPublisher(pool, connPool, cfg)
	publisher.Start()

	// Start stats reporter
	startStatsReporter(latWin, 2*time.Second)

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if cfg.DurationSec > 0 {
		// Run for specified duration
		select {
		case <-time.After(time.Duration(cfg.DurationSec) * time.Second):
			log.Printf("[main] Duration reached, shutting down...")
		case sig := <-sigCh:
			log.Printf("[main] Received signal %v, shutting down...", sig)
		}
	} else {
		// Run until signal
		sig := <-sigCh
		log.Printf("[main] Received signal %v, shutting down...", sig)
	}

	// Graceful shutdown
	publisher.Stop()
	connPool.Close()

	// Final stats
	log.Printf("[final] Total sent: %d, Total received: %d, Match rate: %.2f%%",
		sentTotal.Value(),
		recvMatchTotal.Value(),
		float64(recvMatchTotal.Value())/float64(sentTotal.Value())*100,
	)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
