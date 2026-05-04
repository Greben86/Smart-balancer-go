package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp"
)

var (
	port            = flag.String("port", ":8080", "Port to listen on")
	config          = flag.String("config", "application.yml", "Configuration file")
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
	go func() {
		log.Println("Metrics server listening on :9090")
		if err := fasthttp.ListenAndServe(":9090", prometheusHandler); err != nil {
			log.Fatalf("cannot start metrics server: %v", err)
		}
	}()
}

func prometheusHandler(ctx *fasthttp.RequestCtx) {
	if string(ctx.Path()) == "/metrics" {
		// Создаём net/http-совместимый запрос и ответ
		req := &http.Request{Method: "GET"}
		rw := newResponseWriter(ctx)
		promhttp.Handler().ServeHTTP(rw, req)
	} else {
		ctx.Error("not found", fasthttp.StatusNotFound)
	}
}

// Адаптер для преобразования *fasthttp.RequestCtx в http.ResponseWriter
type responseWriter struct {
	ctx *fasthttp.RequestCtx
}

func newResponseWriter(ctx *fasthttp.RequestCtx) *responseWriter {
	return &responseWriter{ctx: ctx}
}

func (rw *responseWriter) Header() http.Header {
	h := make(http.Header)
	rw.ctx.Response.Header.VisitAll(func(key, value []byte) {
		h.Set(string(key), string(value))
	})
	return h
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	rw.ctx.Write(data)
	return len(data), nil
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.ctx.SetStatusCode(statusCode)
}

func main() {
	flag.Parse()

	// Инициализация метрик
	initMetrics()

	backends := []string{}

	balancer := NewRoundRobinBalancer(backends)
	httpClient := &fasthttp.Client{
		MaxIdleConnDuration: 30 * time.Second,
	}

	// Настройка обработчика запросов
	requestHandler := func(ctx *fasthttp.RequestCtx) {
		switch string(ctx.Path()) {
		case "/health":
			healthHandler(ctx)
		case "/heartbeat":
			heartbeatHandler(ctx)
		default:
			proxyHandler(ctx, balancer, httpClient, backends)
		}
	}

	// Запуск HTTP-сервера
	log.Printf("Smart balancer started on %s, forwarding to %v", *port, backends)
	if err := fasthttp.ListenAndServe(*port, requestHandler); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}

func healthHandler(ctx *fasthttp.RequestCtx) {
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString(fmt.Sprintf("{\"status\": \"healthy\", \"backends\": ...}"))
}

func heartbeatHandler(ctx *fasthttp.RequestCtx) {
	clientIP := ctx.RemoteIP().String()
	log.Printf("Heartbeat received from IP: %s", clientIP)
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString(fmt.Sprintf("{\"status\": \"heartbeat received\", \"client_ip\": \"%s\"}", clientIP))
}

func backendsJSON(backends []string) string {
	json, _ := json.Marshal(backends)
	return string(json)
}

func proxyHandler(ctx *fasthttp.RequestCtx, balancer *RoundRobinBalancer, client *fasthttp.Client, backends []string) {
	start := time.Now()

	backendURL := balancer.Next()

	// Создание запроса к бэкенду
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.SetRequestURI(backendURL + string(ctx.Path()))
	req.Header.SetMethodBytes(ctx.Method())
	req.SetBody(ctx.PostBody())

	// Копирование заголовков
	ctx.Request.Header.VisitAll(func(key, value []byte) {
		req.Header.SetBytesKV(key, value)
	})

	// Выполнение запроса
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	err := client.Do(req, resp)
	if err != nil {
		log.Printf("Error forwarding request to %s: %v", backendURL, err)
		ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
		ctx.SetBodyString("{\"error\": \"Backend service unavailable\"}")
		return
	}

	// Копирование заголовков ответа
	resp.Header.VisitAll(func(key, value []byte) {
		ctx.Response.Header.SetBytesKV(key, value)
	})

	// Копирование статуса и тела
	ctx.SetStatusCode(resp.StatusCode())
	ctx.SetBody(resp.Body())

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
}
