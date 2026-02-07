package main

import (
	"context"
	"expvar"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ============================================================================
// Metrics - Production-grade observability
// ============================================================================

var (
	// Counters (expvar for backward compatibility)
	msgReceived    = expvar.NewInt("msg_received_total")
	msgProcessed   = expvar.NewInt("msg_processed_total")
	msgPublished   = expvar.NewInt("msg_published_total")
	msgDropped     = expvar.NewInt("msg_dropped_total")
	msgErrors      = expvar.NewInt("msg_errors_total")

	// Gauges
	queueSize      = expvar.NewInt("queue_size")
	activeWorkers  = expvar.NewInt("active_workers")
	currentWorkers = expvar.NewInt("current_workers")
	connCount      = expvar.NewInt("connection_count")

	// Latency
	procLatP50     = expvar.NewInt("proc_lat_p50_us")
	procLatP95     = expvar.NewInt("proc_lat_p95_us")
	procLatP99     = expvar.NewInt("proc_lat_p99_us")

	// Throughput
	throughputIn   = expvar.NewInt("throughput_in_per_sec")
	throughputOut  = expvar.NewInt("throughput_out_per_sec")

	// System
	upSince        = expvar.NewInt("up_since")
	lastErr        = expvar.NewString("last_error")
)

// Prometheus metrics
var (
	promMsgReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_worker_messages_received_total",
		Help: "Total messages received",
	})
	promMsgProcessed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_worker_messages_processed_total",
		Help: "Total messages processed",
	})
	promMsgPublished = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_worker_messages_published_total",
		Help: "Total messages published",
	})
	promMsgDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_worker_messages_dropped_total",
		Help: "Total messages dropped",
	})
	promMsgErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_worker_messages_errors_total",
		Help: "Total message errors",
	})
	promQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_worker_queue_size",
		Help: "Current queue size",
	})
	promActiveWorkers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_worker_active_workers",
		Help: "Number of active workers",
	})
	promCurrentWorkers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_worker_current_workers",
		Help: "Current number of workers",
	})
	promConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_worker_connections",
		Help: "Number of MQTT connections",
	})
	promLatency = prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "mqtt_worker_processing_latency_microseconds",
		Help:       "Processing latency in microseconds",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.95: 0.005, 0.99: 0.001},
	})
	promThroughputIn = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_worker_throughput_in_per_sec",
		Help: "Incoming messages per second",
	})
	promThroughputOut = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_worker_throughput_out_per_sec",
		Help: "Outgoing messages per second",
	})
)

func init() {
	prometheus.MustRegister(
		promMsgReceived, promMsgProcessed, promMsgPublished,
		promMsgDropped, promMsgErrors,
		promQueueSize, promActiveWorkers, promCurrentWorkers, promConnections,
		promLatency, promThroughputIn, promThroughputOut,
	)
}

// ============================================================================
// Configuration
// ============================================================================

type Config struct {
	// MQTT
	Broker           string
	SubTopic         string
	SubQoS           byte
	PubQoS           byte
	Connections      int           // Number of MQTT connections for publishing

	// Worker Pool
	MinWorkers       int           // Minimum worker count
	MaxWorkers       int           // Maximum worker count (auto-scale ceiling)
	QueueCap         int           // Message queue capacity

	// Auto-scaling
	ScaleUpThreshold   float64     // Queue fill % to trigger scale up
	ScaleDownThreshold float64     // Queue fill % to trigger scale down
	ScaleInterval      time.Duration
	WorkersPerScale    int         // Workers to add/remove per scale event

	// Processing
	ProcessMode      string        // health | sleep | cpu
	SleepMS          int
	CPUWork          int

	// Backpressure
	DropOnFull       bool          // Drop messages when queue full (vs block)

	// Observability
	MetricsPort      int
	PprofPort        int

	// Graceful shutdown
	ShutdownTimeout  time.Duration
}

func loadConfig() Config {
	return Config{
		Broker:             envStr("MQTT_BROKER", "tcp://localhost:1883"),
		SubTopic:           envStr("MQTT_SUB", "req/+"),
		SubQoS:             byte(envInt("MQTT_QOS", 0)),
		PubQoS:             byte(envInt("PUB_QOS", 0)),
		Connections:        envInt("CONNECTIONS", 10),

		MinWorkers:         envInt("MIN_WORKERS", 10),
		MaxWorkers:         envInt("MAX_WORKERS", 1000),
		QueueCap:           envInt("QUEUE_CAP", 100000),

		ScaleUpThreshold:   envFloat("SCALE_UP_THRESHOLD", 0.7),
		ScaleDownThreshold: envFloat("SCALE_DOWN_THRESHOLD", 0.2),
		ScaleInterval:      time.Duration(envInt("SCALE_INTERVAL_MS", 1000)) * time.Millisecond,
		WorkersPerScale:    envInt("WORKERS_PER_SCALE", 10),

		ProcessMode:        envStr("MODE", "health"),
		SleepMS:            envInt("SLEEP_MS", 0),
		CPUWork:            envInt("CPU_WORK", 0),

		DropOnFull:         envBool("DROP_ON_FULL", true),

		MetricsPort:        envInt("METRICS_PORT", 8080),
		PprofPort:          envInt("PPROF_PORT", 6060),

		ShutdownTimeout:    time.Duration(envInt("SHUTDOWN_TIMEOUT_SEC", 30)) * time.Second,
	}
}

// ============================================================================
// Message Types
// ============================================================================

type Message struct {
	Topic     string
	Payload   []byte
	RecvTime  time.Time
	Shard     int
}

// ============================================================================
// Connection Pool - Optimized for high-throughput publishing
// ============================================================================

type ConnectionPool struct {
	connections []mqtt.Client
	current     uint64
	cfg         Config
}

func NewConnectionPool(cfg Config) *ConnectionPool {
	return &ConnectionPool{
		connections: make([]mqtt.Client, 0, cfg.Connections),
		cfg:         cfg,
	}
}

func (p *ConnectionPool) Connect() error {
	hostname, _ := os.Hostname()

	for i := 0; i < p.cfg.Connections; i++ {
		clientID := fmt.Sprintf("%s-pub-%d", hostname, i)

		opts := mqtt.NewClientOptions().
			AddBroker(p.cfg.Broker).
			SetClientID(clientID).
			SetAutoReconnect(true).
			SetConnectTimeout(10 * time.Second).
			SetWriteTimeout(10 * time.Second).
			SetOrderMatters(false).
			SetCleanSession(true).
			SetKeepAlive(60 * time.Second).
			SetPingTimeout(30 * time.Second).
			SetMaxReconnectInterval(10 * time.Second).
			SetMessageChannelDepth(10000)

		client := mqtt.NewClient(opts)
		token := client.Connect()
		if !token.WaitTimeout(10 * time.Second) {
			return fmt.Errorf("connection timeout for %s", clientID)
		}
		if token.Error() != nil {
			return fmt.Errorf("connection error for %s: %w", clientID, token.Error())
		}

		p.connections = append(p.connections, client)
		connCount.Add(1)
		log.Printf("[pool] Connected: %s", clientID)
	}

	return nil
}

func (p *ConnectionPool) Publish(topic string, qos byte, payload []byte) error {
	if len(p.connections) == 0 {
		return fmt.Errorf("no connections available")
	}

	// Round-robin connection selection
	idx := atomic.AddUint64(&p.current, 1) % uint64(len(p.connections))
	client := p.connections[idx]

	token := client.Publish(topic, qos, false, payload)
	if qos > 0 {
		token.Wait()
		return token.Error()
	}
	return nil
}

func (p *ConnectionPool) Close() {
	for _, client := range p.connections {
		client.Disconnect(1000)
	}
}

// ============================================================================
// Worker Pool - Auto-scaling worker management
// ============================================================================

type WorkerPool struct {
	cfg         Config
	queue       chan *Message
	connPool    *ConnectionPool
	latWin      *LatencyWindow

	workerCount int32
	stopCh      chan struct{}
	workerWg    sync.WaitGroup
	scaleMu     sync.Mutex

	ctx         context.Context
	cancel      context.CancelFunc
}

func NewWorkerPool(cfg Config, connPool *ConnectionPool) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())

	return &WorkerPool{
		cfg:      cfg,
		queue:    make(chan *Message, cfg.QueueCap),
		connPool: connPool,
		latWin:   NewLatencyWindow(10000),
		stopCh:   make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (p *WorkerPool) Start() {
	// Start minimum workers
	for i := 0; i < p.cfg.MinWorkers; i++ {
		p.addWorker()
	}

	// Start auto-scaler
	go p.autoScaler()

	// Start metrics reporter
	go p.metricsReporter()

	log.Printf("[pool] Started with %d workers", p.cfg.MinWorkers)
}

func (p *WorkerPool) addWorker() {
	count := atomic.AddInt32(&p.workerCount, 1)
	currentWorkers.Set(int64(count))

	p.workerWg.Add(1)
	go p.worker(int(count))
}

func (p *WorkerPool) removeWorker() bool {
	count := atomic.LoadInt32(&p.workerCount)
	if count <= int32(p.cfg.MinWorkers) {
		return false
	}

	// Signal one worker to stop (via queue close check in worker loop)
	atomic.AddInt32(&p.workerCount, -1)
	currentWorkers.Add(-1)
	return true
}

func (p *WorkerPool) worker(id int) {
	defer p.workerWg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		case msg, ok := <-p.queue:
			if !ok {
				return
			}

			// Check if this worker should exit (scale down)
			if int32(id) > atomic.LoadInt32(&p.workerCount) {
				// Put message back and exit
				select {
				case p.queue <- msg:
				default:
				}
				return
			}

			activeWorkers.Add(1)
			p.processMessage(msg)
			activeWorkers.Add(-1)
		}
	}
}

func (p *WorkerPool) processMessage(msg *Message) {
	startTime := time.Now()

	// Simulate processing based on mode
	switch p.cfg.ProcessMode {
	case "sleep":
		time.Sleep(time.Duration(p.cfg.SleepMS) * time.Millisecond)
	case "cpu":
		doCPUWork(p.cfg.CPUWork)
	case "health":
		// No-op, just respond immediately
	}

	// Build response topic: req/N -> resp/N
	respTopic := strings.Replace(msg.Topic, "req/", "resp/", 1)

	// Publish response
	if err := p.connPool.Publish(respTopic, p.cfg.PubQoS, msg.Payload); err != nil {
		msgErrors.Add(1)
		promMsgErrors.Inc()
		lastErr.Set(err.Error())
		return
	}

	msgPublished.Add(1)
	msgProcessed.Add(1)
	promMsgPublished.Inc()
	promMsgProcessed.Inc()

	// Record latency
	latUs := time.Since(startTime).Microseconds()
	p.latWin.Add(latUs)
	promLatency.Observe(float64(latUs))
}

func (p *WorkerPool) autoScaler() {
	ticker := time.NewTicker(p.cfg.ScaleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.scaleMu.Lock()

			qLen := len(p.queue)
			qCap := cap(p.queue)
			fillRatio := float64(qLen) / float64(qCap)

			queueSize.Set(int64(qLen))

			currentCount := atomic.LoadInt32(&p.workerCount)

			if fillRatio > p.cfg.ScaleUpThreshold && int(currentCount) < p.cfg.MaxWorkers {
				// Scale up
				toAdd := min(p.cfg.WorkersPerScale, p.cfg.MaxWorkers-int(currentCount))
				for i := 0; i < toAdd; i++ {
					p.addWorker()
				}
				log.Printf("[autoscale] Scaled UP: %d -> %d workers (queue: %.1f%%)",
					currentCount, currentCount+int32(toAdd), fillRatio*100)
			} else if fillRatio < p.cfg.ScaleDownThreshold && int(currentCount) > p.cfg.MinWorkers {
				// Scale down
				toRemove := min(p.cfg.WorkersPerScale, int(currentCount)-p.cfg.MinWorkers)
				for i := 0; i < toRemove; i++ {
					p.removeWorker()
				}
				log.Printf("[autoscale] Scaled DOWN: %d -> %d workers (queue: %.1f%%)",
					currentCount, currentCount-int32(toRemove), fillRatio*100)
			}

			p.scaleMu.Unlock()
		}
	}
}

func (p *WorkerPool) metricsReporter() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastRecv, lastPub int64

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			recv := msgReceived.Value()
			pub := msgPublished.Value()

			inRate := (recv - lastRecv) / 2
			outRate := (pub - lastPub) / 2

			throughputIn.Set(inRate)
			throughputOut.Set(outRate)

			// Update Prometheus gauges
			promThroughputIn.Set(float64(inRate))
			promThroughputOut.Set(float64(outRate))
			promQueueSize.Set(float64(len(p.queue)))
			promCurrentWorkers.Set(float64(atomic.LoadInt32(&p.workerCount)))
			promActiveWorkers.Set(float64(activeWorkers.Value()))
			promConnections.Set(float64(connCount.Value()))

			lastRecv, lastPub = recv, pub

			// Update latency percentiles
			p50, p95, p99 := p.latWin.Percentiles()
			procLatP50.Set(p50)
			procLatP95.Set(p95)
			procLatP99.Set(p99)
		}
	}
}

func (p *WorkerPool) Enqueue(msg *Message) bool {
	if p.cfg.DropOnFull {
		select {
		case p.queue <- msg:
			return true
		default:
			msgDropped.Add(1)
			promMsgDropped.Inc()
			return false
		}
	} else {
		p.queue <- msg
		return true
	}
}

func (p *WorkerPool) Stop() {
	p.cancel()
	close(p.queue)

	// Wait for workers with timeout
	done := make(chan struct{})
	go func() {
		p.workerWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("[pool] All workers stopped gracefully")
	case <-time.After(p.cfg.ShutdownTimeout):
		log.Printf("[pool] Shutdown timeout, some workers may not have finished")
	}
}

// ============================================================================
// Subscriber Pool - Multiple subscriber connections with shared subscriptions
// ============================================================================

type SubscriberPool struct {
	clients []mqtt.Client
	pool    *WorkerPool
	cfg     Config
}

func NewSubscriberPool(cfg Config, pool *WorkerPool) *SubscriberPool {
	// Use multiple subscribers to distribute load
	numSubs := cfg.Connections // Match publish connections
	if numSubs < 5 {
		numSubs = 5
	}
	if numSubs > 20 {
		numSubs = 20
	}

	return &SubscriberPool{
		clients: make([]mqtt.Client, 0, numSubs),
		cfg:     cfg,
		pool:    pool,
	}
}

func (sp *SubscriberPool) Connect() error {
	hostname, _ := os.Hostname()
	numSubs := cap(sp.clients)

	log.Printf("[subscriber-pool] Creating %d subscriber connections with shared subscription", numSubs)

	var wg sync.WaitGroup
	errors := make(chan error, numSubs)

	for i := 0; i < numSubs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			clientID := fmt.Sprintf("%s-sub-%d", hostname, idx)
			subDone := make(chan error, 1)

			// Use shared subscription topic: $share/workers/req/+
			sharedTopic := fmt.Sprintf("$share/workers/%s", sp.cfg.SubTopic)

			opts := mqtt.NewClientOptions().
				AddBroker(sp.cfg.Broker).
				SetClientID(clientID).
				SetAutoReconnect(true).
				SetConnectTimeout(10 * time.Second).
				SetOrderMatters(false).
				SetCleanSession(true).
				SetKeepAlive(60 * time.Second).
				SetPingTimeout(30 * time.Second).
				SetWriteTimeout(10 * time.Second).
				SetMaxReconnectInterval(10 * time.Second).
				SetMessageChannelDepth(50000).
				SetOnConnectHandler(func(c mqtt.Client) {
					log.Printf("[subscriber-%d] Connected, subscribing to: %s", idx, sharedTopic)
					token := c.Subscribe(sharedTopic, 0, sp.messageHandler) // QoS 0 for high throughput
					if token.WaitTimeout(30 * time.Second) {
						if token.Error() != nil {
							log.Printf("[subscriber-%d] Subscribe error: %v", idx, token.Error())
							select {
							case subDone <- token.Error():
							default:
							}
						} else {
							log.Printf("[subscriber-%d] Subscribed successfully", idx)
							select {
							case subDone <- nil:
							default:
							}
						}
					} else {
						log.Printf("[subscriber-%d] Subscribe timeout", idx)
						select {
						case subDone <- fmt.Errorf("subscribe timeout"):
						default:
						}
					}
				}).
				SetConnectionLostHandler(func(c mqtt.Client, err error) {
					log.Printf("[subscriber-%d] Connection lost: %v", idx, err)
				}).
				SetReconnectingHandler(func(c mqtt.Client, opts *mqtt.ClientOptions) {
					log.Printf("[subscriber-%d] Reconnecting...", idx)
				})

			client := mqtt.NewClient(opts)

			token := client.Connect()
			if !token.WaitTimeout(10 * time.Second) {
				errors <- fmt.Errorf("subscriber-%d connection timeout", idx)
				return
			}
			if token.Error() != nil {
				errors <- fmt.Errorf("subscriber-%d error: %w", idx, token.Error())
				return
			}

			// Wait for subscription
			select {
			case err := <-subDone:
				if err != nil {
					errors <- fmt.Errorf("subscriber-%d subscription failed: %w", idx, err)
					return
				}
			case <-time.After(35 * time.Second):
				errors <- fmt.Errorf("subscriber-%d subscription timeout", idx)
				return
			}

			sp.clients = append(sp.clients, client)
		}(i)

		// Small delay between connections
		time.Sleep(50 * time.Millisecond)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		log.Printf("[subscriber-pool] Warning: %v", err)
	}

	if len(sp.clients) == 0 {
		return fmt.Errorf("no subscribers connected")
	}

	log.Printf("[subscriber-pool] %d/%d subscribers connected", len(sp.clients), numSubs)
	return nil
}

func (sp *SubscriberPool) messageHandler(_ mqtt.Client, msg mqtt.Message) {
	msgReceived.Add(1)
	promMsgReceived.Inc()

	// Extract shard from topic (req/N -> N)
	shard := 0
	parts := strings.Split(msg.Topic(), "/")
	if len(parts) >= 2 {
		shard, _ = strconv.Atoi(parts[1])
	}

	m := &Message{
		Topic:    msg.Topic(),
		Payload:  msg.Payload(),
		RecvTime: time.Now(),
		Shard:    shard,
	}

	sp.pool.Enqueue(m)
}

func (sp *SubscriberPool) Close() {
	for _, client := range sp.clients {
		client.Disconnect(1000)
	}
}

// ============================================================================
// Utilities
// ============================================================================

func doCPUWork(iterations int) {
	x := 1.0
	for i := 0; i < iterations; i++ {
		x = x * 1.0001
	}
}

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

func (w *LatencyWindow) Percentiles() (p50, p95, p99 int64) {
	head := atomic.LoadUint64(&w.head)
	if head == 0 {
		return 0, 0, 0
	}

	count := head
	if count > w.size {
		count = w.size
	}

	data := make([]int64, count)
	for i := uint64(0); i < count; i++ {
		idx := (head - count + 1 + i) % w.size
		data[i] = atomic.LoadInt64(&w.samples[idx])
	}

	sortInt64(data)

	n := len(data)
	if n == 0 {
		return 0, 0, 0
	}

	p50 = data[n*50/100]
	p95 = data[n*95/100]
	p99 = data[n*99/100]
	return
}

func sortInt64(data []int64) {
	for i := 1; i < len(data); i++ {
		for j := i; j > 0 && data[j] < data[j-1]; j-- {
			data[j], data[j-1] = data[j-1], data[j]
		}
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

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ============================================================================
// Main
// ============================================================================

func main() {
	cfg := loadConfig()

	log.Printf("=== WORKER-V2 - Production-Ready MQTT Worker ===")
	log.Printf("[config] Broker: %s", cfg.Broker)
	log.Printf("[config] Subscribe: %s (QoS %d)", cfg.SubTopic, cfg.SubQoS)
	log.Printf("[config] Workers: %d - %d (auto-scale)", cfg.MinWorkers, cfg.MaxWorkers)
	log.Printf("[config] Queue capacity: %d", cfg.QueueCap)
	log.Printf("[config] Connections: %d", cfg.Connections)
	log.Printf("[config] Process mode: %s", cfg.ProcessMode)
	log.Printf("[config] Scale thresholds: up=%.0f%% down=%.0f%%",
		cfg.ScaleUpThreshold*100, cfg.ScaleDownThreshold*100)

	upSince.Set(time.Now().Unix())

	// Start pprof server
	go func() {
		addr := fmt.Sprintf(":%d", cfg.PprofPort)
		log.Printf("[pprof] Starting on %s", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("[pprof] Error: %v", err)
		}
	}()

	// Start metrics server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/debug/vars", expvar.Handler())
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "OK\n")
		})
		mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
			if connCount.Value() > 0 {
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "Ready\n")
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprintf(w, "Not ready\n")
			}
		})
		mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"received": %d,
				"processed": %d,
				"published": %d,
				"dropped": %d,
				"errors": %d,
				"queue_size": %d,
				"workers": %d,
				"active_workers": %d,
				"connections": %d,
				"throughput_in": %d,
				"throughput_out": %d,
				"latency_p50_us": %d,
				"latency_p95_us": %d,
				"latency_p99_us": %d,
				"goroutines": %d
			}`,
				msgReceived.Value(),
				msgProcessed.Value(),
				msgPublished.Value(),
				msgDropped.Value(),
				msgErrors.Value(),
				queueSize.Value(),
				currentWorkers.Value(),
				activeWorkers.Value(),
				connCount.Value(),
				throughputIn.Value(),
				throughputOut.Value(),
				procLatP50.Value(),
				procLatP95.Value(),
				procLatP99.Value(),
				runtime.NumGoroutine(),
			)
		})

		addr := fmt.Sprintf(":%d", cfg.MetricsPort)
		log.Printf("[metrics] Starting on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[metrics] Error: %v", err)
		}
	}()

	// Create connection pool for publishing
	connPool := NewConnectionPool(cfg)
	if err := connPool.Connect(); err != nil {
		log.Fatalf("[fatal] Connection pool error: %v", err)
	}

	// Create worker pool
	workerPool := NewWorkerPool(cfg, connPool)
	workerPool.Start()

	// Create subscriber pool with shared subscriptions
	subscriberPool := NewSubscriberPool(cfg, workerPool)
	if err := subscriberPool.Connect(); err != nil {
		log.Fatalf("[fatal] Subscriber pool connect error: %v", err)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	log.Printf("[main] Received signal %v, starting graceful shutdown...", sig)

	// Graceful shutdown
	subscriberPool.Close()
	workerPool.Stop()
	connPool.Close()

	log.Printf("[main] Shutdown complete")
	log.Printf("[final] Received: %d, Processed: %d, Published: %d, Dropped: %d, Errors: %d",
		msgReceived.Value(),
		msgProcessed.Value(),
		msgPublished.Value(),
		msgDropped.Value(),
		msgErrors.Value(),
	)
}
