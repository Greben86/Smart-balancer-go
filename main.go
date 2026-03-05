package main

import (
	"flag"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"strings"
)

var (
	backendURLs     = flag.String("backends", "http://backend1:8080,http://backend2:8080", "Comma-separated list of backend service URLs")
	port            = flag.String("port", ":8080", "Port to listen on")
	requestCounters = make(map[string]*uint64)
	counterMutex    sync.RWMutex
)

// Metrics
var (
	requestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "smart_balancer_requests_total",
			Help: "Total number of requests sent to backends",
		},
		[]string{"backend"},
	)

	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "smart_balancer_request_duration_seconds",
			Help:    "Request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"backend"},
	)
)

// RoundRobinBalancer реализует балансировку методом round-robin
type RoundRobinBalancer struct {
	backends []string
	current  uint64
}

func NewRoundRobinBalancer(backends []string) *RoundRobinBalancer {
	return &RoundRobinBalancer{
		backends: backends,
	}
}

func (r *RoundRobinBalancer) Next() string {
	current := atomic.AddUint64(&r.current, 1)
	return r.backends[(current-1)%uint64(len(r.backends))]
}

func initMetrics() {
	// Регистрация метрик Prometheus
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		log.Println("Metrics server listening on :9090")
		log.Fatal(http.ListenAndServe(":9090", nil))
	}()
}

func main() {
	flag.Parse()

	// Инициализация метрик
	initMetrics()

	backends := []string{}
	for _, b := range strings.Split(*backendURLs, ",") {
		trimmed := strings.TrimSpace(b)
		if trimmed != "" {
			backends = append(backends, trimmed)
			// Инициализация счётчиков
			counter := uint64(0)
			counterMutex.Lock()
			requestCounters[trimmed] = &counter
			counterMutex.Unlock()
	}
	}

	if len(backends) == 0 {
		log.Fatal("No backends specified")
	}

	balancer := NewRoundRobinBalancer(backends)
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Настройка Gin-роутера
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy", "backends": backends})
	})

	r.POST("/heartbeat", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "heartbeat received"})
	})

	r.NoRoute(func(c *gin.Context) {
		start := time.Now()

		backendURL := balancer.Next()

		// Формирование запроса к бэкенду
		req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, backendURL+c.Request.URL.String(), c.Request.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request to backend"})
			return
		}

		// Копирование заголовков
		for key, values := range c.Request.Header {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}

		// Выполнение запроса
		resp, err := httpClient.Do(req)
		if err != nil {
			log.Printf("Error forwarding request to %s: %v", backendURL, err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Backend service unavailable"})
			return
		}
		defer resp.Body.Close()

		// Копирование заголовков ответа
		for key, values := range resp.Header {
			for _, value := range values {
				c.Header(key, value)
			}
		}

		// Копирование статуса и тела
		c.Status(resp.StatusCode)
		c.DataFromReader(resp.StatusCode, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)

		// Обновление счётчиков
		counterMutex.Lock()
		counter := requestCounters[backendURL]
		if counter != nil {
			atomic.AddUint64(counter, 1)
		}
		counterMutex.Unlock()

		// Обновление метрик Prometheus
		requestsTotal.WithLabelValues(backendURL).Inc()
		duration := time.Since(start).Seconds()
		requestDuration.WithLabelValues(backendURL).Observe(duration)
	})

	log.Printf("Smart balancer started on %s, forwarding to %v", *port, backends)
	log.Fatal(r.Run(*port))
}

