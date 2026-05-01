package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp"
)

var (
	port = flag.String("port", ":8080", "Port to listen on")
)

// Мапа для хранения IP-адресов сервисов и времени их последнего heartbeat
var (
	services      = make(map[string]time.Time)
	servicesMutex sync.RWMutex
)

func initMetrics() {
	// Запуск сервера метрик Prometheus на порту 9090
	go func() {
		log.Println("Metrics server listening on :9090")
		if err := fasthttp.ListenAndServe(":9090", prometheusHandler); err != nil {
			log.Fatalf("cannot start metrics server: %v", err)
		}
	}()
}

func prometheusHandler(ctx *fasthttp.RequestCtx) {
	if string(ctx.Path()) == "/metrics" {
		// Адаптер для совместимости с promhttp.Handler()
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

	// Настройка обработчика запросов
	requestHandler := func(ctx *fasthttp.RequestCtx) {
		switch string(ctx.Path()) {
		case "/health":
			healthHandler(ctx)
		case "/heartbeat":
			heartbeatHandler(ctx)
		default:
			ctx.Error("not found", fasthttp.StatusNotFound)
		}
	}

	// Запуск HTTP-сервера
	log.Printf("Registry service started on %s", *port)
	if err := fasthttp.ListenAndServe(*port, requestHandler); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}

func healthHandler(ctx *fasthttp.RequestCtx) {
	// Обновляем счётчик зарегистрированных сервисов
	servicesMutex.RLock()
	count := len(services)
	servicesMutex.RUnlock()

	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString(fmt.Sprintf("{\"status\": \"healthy\", \"registered_services\": %d}", count))
}

func heartbeatHandler(ctx *fasthttp.RequestCtx) {
	clientIP := ctx.RemoteIP().String()
	log.Printf("Heartbeat received from IP: %s", clientIP)

	// Сохраняем или обновляем запись о сервисе
	servicesMutex.Lock()
	services[clientIP] = time.Now()
	servicesMutex.Unlock()

	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString(fmt.Sprintf("{\"status\": \"heartbeat received\", \"client_ip\": \"%s\"}", clientIP))
}
