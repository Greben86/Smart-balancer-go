package main

import (
	"container/list"
	"flag"
	"log"
	"net/http"
	"smart-balancer-go/logic"
	"smart-balancer-go/utils"
	"strings"
	"sync"
	"sync/atomic"
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
			Name: "smart_balancer_requests_count",
			Help: "Количество запросов к микросервисам",
		},
		[]string{"node", "backend", "path"},
	)
	statusCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "smart_balancer_status_code_count",
			Help: "Количество запросов к микросервисам",
		},
		[]string{"node", "status_code", "backend", "path"},
	)
	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "smart_balancer_request_duration_seconds",
			Help:    "Request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"node", "backend", "path"},
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

// In-memory storage for values per backend with fixed size of 1000 entries (FIFO)
var (
	backendValues = make(map[string]*list.List)    // Store values per backend
	backendNews   = make(map[string]*atomic.Int64) // Store values per backend
	valuesMutex   sync.RWMutex                     // Mutex for thread-safe access
	maxValues     = 1024                           // Maximum number of values to store per backend
)

func initRedis() {
	// Получаем адрес Redis из переменной окружения
	redisAddr := utils.GetEnv("REDIS_ADDR", "localhost:6379")
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

	// Запускаем горутину для логирования количества записей каждую минуту
	go func() {
		ticker := time.NewTicker(time.Second)
		for {
			<-ticker.C
			// Получаем строку с адресами из Redis
			serviceStr, err := redisClient.Get(redisClient.Context(), "target.services").Result()
			if err != nil {
				log.Printf("Failed to get target.services from Redis: %v", err)
			}
			if serviceStr != "" {
				// Разделяем строку по запятым и формируем список адресов
				backends := strings.SplitSeq(serviceStr, ",")
				for backend := range backends {
					var curCount int
					curCountStr, err := redisClient.Get(redisClient.Context(), "requests.cur."+backend).Result()
					if err != nil {
						curCount = 0
					} else {
						curCount, _ = strconv.Atoi(curCountStr)
					}
					if err := redisClient.Set(redisClient.Context(), "requests.prev."+backend, curCount, 0).Err(); err != nil {
						log.Printf("Failed to update requests.prev.%s in Redis: %v", backend, err)
					}
					if err := redisClient.Set(redisClient.Context(), "requests.cur."+backend, 0, 0).Err(); err != nil {
						log.Printf("Failed to update requests.cur.%s in Redis: %v", backend, err)
					}
				}
				// Записываем текущее время для backend
				timestamp := time.Now().Local().UnixMilli()
				if err := redisClient.Set(redisClient.Context(), "timestamp.current", timestamp, 0).Err(); err != nil {
					log.Printf("Failed to update timestamp.current in Redis: %v", err)
				}
			} else {
				log.Printf("No backends found in Redis")
			}
		}
	}()

	// Инициализация метрик
	initMetrics()

	// Запускаем горутину для логирования количества записей каждую минуту
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		// defer ticker.Stop()
		for {
			<-ticker.C
			// Получаем строку с адресами из Redis
			serviceStr, err := redisClient.Get(redisClient.Context(), "target.services").Result()
			if err != nil {
				log.Printf("Failed to get target.services from Redis: %v", err)
			}
			if serviceStr != "" {
				log.Printf("Target services: %s", serviceStr)
				// Разделяем строку по запятым и формируем список адресов
				backends := strings.SplitSeq(serviceStr, ",")
				for backend := range backends {
					if value, exists := backendNews[backend]; !exists || value.Load() == 0 {
						continue
					}

					log.Printf("Backend: %s", backend)
					// Преобразуем list.List в []float64 при необходимости
					var values []float64
					if backendValues[backend] != nil {
						valuesMutex.RLock()
						values = make([]float64, 0, backendValues[backend].Len())
						for e := backendValues[backend].Front(); e != nil; e = e.Next() {
							if val, ok := e.Value.(float64); ok {
								values = append(values, val)
							}
						}
						valuesMutex.RUnlock()
					}

					// Подсчитываем Херст для текущего списка значений
					hurst, _ := logic.VGHurst(values)

					// Отправляем параметр Херста в Redis
					if err := redisClient.Set(redisClient.Context(), "service.hurst."+backend, hurst, 0).Err(); err != nil {
						log.Printf("Failed to update service.hurst.%s in Redis: %v", backend, err)
					} else {
						log.Printf("Updated service.hurst.%s in Redis with value %f", backend, hurst)
					}

					// Обновляем метрику Prometheus
					serviceHurst.With(prometheus.Labels{"backend": backend}).Observe(hurst)

					backendNews[backend].Store(0)
				}
			} else {
				log.Printf("No backends found in Redis")
			}
		}
	}()

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
		// Читаем из stream monitoring.events
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
				nodeName := entry.Values["node"].(string)
				durationStr := entry.Values["duration"].(string)
				duration, _ := strconv.ParseFloat(durationStr, 64)
				backend := entry.Values["backend"].(string)
				path := entry.Values["path"].(string)
				statusStr := entry.Values["status_code"].(string)
				// Обновляем метрику Prometheus
				requestsCounter.With(prometheus.Labels{"node": nodeName, "backend": backend, "path": path}).Inc()
				statusCounter.With(prometheus.Labels{"node": nodeName, "status_code": statusStr, "backend": backend, "path": path}).Inc()
				requestDuration.With(prometheus.Labels{"node": nodeName, "backend": backend, "path": path}).Observe(duration)

				// Сохраняем значение в памяти для этого backend
				go func() {
					valuesMutex.Lock()
					defer valuesMutex.Unlock()
					if _, exists := backendValues[backend]; !exists {
						backendValues[backend] = list.New()
					}
					// Добавляем новое значение
					backendValues[backend].PushBack(duration)
					// Проверяем размер и удаляем старые значения при необходимости (FIFO)
					for backendValues[backend].Len() > maxValues {
						backendValues[backend].Remove(backendValues[backend].Front())
					}

					if _, exists := backendNews[backend]; !exists {
						backendNews[backend] = &atomic.Int64{}
					}
					backendNews[backend].Add(1)
				}()
			}
		}
	}
}
