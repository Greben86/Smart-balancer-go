package main

import (
	"flag"
	"log"
	"math"
	"net/http"
	"os"
	"time"

	"strconv"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp"
)

var (
	port   = flag.String("port", ":8080", "Port to listen on")
	config = flag.String("config", "application.yml", "Configuration file")
)

// Redis клиент
var redisClient *redis.Client

// Metrics
var (
	requestsCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "smart_balancer_requests_total",
			Help: "Количество запросов к микросервисам",
		},
		[]string{"backend", "path"},
	)
	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "smart_balancer_request_duration_seconds",
			Help:    "Request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"backend", "path"},
	)
	serviceHurst = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "smart_balancer_service_hurst_value",
			Help:    "Request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"backend"},
	)
)

func initRedis() {
	// Получаем адрес Redis из переменной окружения
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		log.Fatal("REDIS_ADDR environment variable is not set")
	}

	// Создаем Redis клиент
	redisClient = redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	// Проверяем соединение с Redis
	if _, err := redisClient.Ping(redisClient.Context()).Result(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Printf("Connected to Redis at %s", redisAddr)
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

	// Инициализация Redis
	initRedis()

	// Инициализация метрик
	initMetrics()

	// Запускаем горутину для чтения из Redis stream
	go readRequestDurations()

	// Запускаем health check сервер
	log.Printf("Monitoring service started on %s", *port)
	if err := fasthttp.ListenAndServe(*port, healthHandler); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}

func healthHandler(ctx *fasthttp.RequestCtx) {
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString("{\"status\": \"healthy\"}")
}

// Функция для чтения данных из Redis stream и обновления метрик
func readRequestDurations() {
	// Используем последнюю доступную запись
	lastID := "$"

	for {
		// Читаем из stream request.durations
		streamEntries, err := redisClient.XRead(redisClient.Context(), &redis.XReadArgs{
			Streams: []string{"monitoring.events", lastID},
			Count:   10,              // читаем до 10 сообщений за раз
			Block:   5 * time.Second, // ждем новых сообщений до 5 секунд
		}).Result()

		if err != nil {
			// Пропускаем ошибку timeout, так как это ожидаемо при использовании Block
			if err != redis.Nil && err.Error() != "redis: timed out" {
				log.Printf("Error reading from Redis stream: %v", err)
			}
			continue
		}

		// Обрабатываем полученные сообщения
		for _, stream := range streamEntries {
			for _, entry := range stream.Messages {
				// Обновляем lastID для следующего чтения
				lastID = entry.ID

				// Извлекаем данные из сообщения
				durationStr := entry.Values["duration"].(string)
				backend := entry.Values["backend"].(string)
				path := entry.Values["path"].(string)
				duration, _ := strconv.ParseFloat(durationStr, 64)
				// Обновляем метрику Prometheus
				requestsCounter.With(prometheus.Labels{"backend": backend, "path": path}).Inc()
				requestDuration.With(prometheus.Labels{"backend": backend, "path": path}).Observe(duration)
				serviceHurst.With(prometheus.Labels{"backend": backend}).Observe(2.5)
				log.Printf("Recorded request duration for %s: %f seconds", backend, duration)

				// Отправляем параметр Херста в Redis
				if err := redisClient.Set(redisClient.Context(), "service.hurst."+backend, 2.5, 0).Err(); err != nil {
					log.Printf("Failed to update service.hurst.%s in Redis: %v", backend, err)
				}
			}
		}
	}
}

// Hurst рассчитывает упрощенный параметр Херста через R/S
func Hurst(data []float64) float64 {
	n := len(data)
	if n < 2 {
		return 0
	}

	// 1. Находим среднее
	var sum float64
	for _, v := range data {
		sum += v
	}
	mean := sum / float64(n)

	// 2. Рассчитываем отклонения и их накопленную сумму
	z := make([]float64, n)
	var cumulativeSum float64
	var maxZ, minZ float64

	for i, v := range data {
		cumulativeSum += v - mean
		z[i] = cumulativeSum
		if cumulativeSum > maxZ {
			maxZ = cumulativeSum
		}
		if cumulativeSum < minZ {
			minZ = cumulativeSum
		}
	}

	// 3. Размах (Range)
	R := maxZ - minZ

	// 4. Стандартное отклонение (S)
	var sqSum float64
	for _, v := range data {
		sqSum += math.Pow(v-mean, 2)
	}
	S := math.Sqrt(sqSum / float64(n))

	// 5. Итоговое значение H = log(R/S) / log(n)
	if S == 0 {
		return 0
	}
	return math.Log(R/S) / math.Log(float64(n))
}
