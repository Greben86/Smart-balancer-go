package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"smart-balancer-go/logic"
	"smart-balancer-go/utils"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/valyala/fasthttp"
)

var (
	port            = flag.String("port", ":8080", "Port to listen on")
	config          = flag.String("config", "application.yml", "Configuration file")
	requestCounters = make(map[string]*uint64)
	counterMutex    sync.RWMutex
)

// Redis клиент
var redisClient *redis.Client

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

// Запись метрики времени выполнения в Redis stream
func logRequestDuration(backend string, path string, duration time.Duration) {
	// Создаем map с данными для записи в stream
	values := map[string]any{
		"event_type": "request_duration",
		"backend":    backend,
		"path":       path,
		"duration":   duration.Microseconds(), // сохраняем в микросекундах
		"timestamp":  time.Now().Unix(),
	}

	// log.Printf("Writing to Redis stream: %v", values)

	// Добавляем запись в stream
	if err := redisClient.XAdd(redisClient.Context(), &redis.XAddArgs{
		Stream: "monitoring.events", // имя stream
		Values: values,
	}).Err(); err != nil {
		log.Printf("Failed to write request duration to Redis stream: %v", err)
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
	httpClient := &fasthttp.Client{
		MaxIdleConnDuration: 30 * time.Second,
	}

	// Настройка обработчика запросов
	requestHandler := func(ctx *fasthttp.RequestCtx) {
		switch string(ctx.Path()) {
		case "/health":
			healthHandler(ctx)
		default:
			proxyHandler(ctx, httpClient)
		}
	}

	// Запуск HTTP-сервера
	log.Printf("Smart balancer started on %s", *port)
	if err := fasthttp.ListenAndServe(*port, requestHandler); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}

func healthHandler(ctx *fasthttp.RequestCtx) {
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString("{\"status\": \"healthy\", \"backends\": ...}")
}

func backendsJSON(backends []string) string {
	json, _ := json.Marshal(backends)
	return string(json)
}

func proxyHandler(ctx *fasthttp.RequestCtx, client *fasthttp.Client) {
	balancer, err := logic.NewSmartBalancer(redisClient)
	if err != nil {
		log.Printf("Error creating balancer instance: %v", err)
		ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
		ctx.SetBodyString("{\"error\": \"Backend list is empty\"}")
		return
	}
	backend := balancer.Next()
	fullPath := "http://" + backend + ":5000" + string(ctx.Path())

	// Начало точного измерения времени
	startTime := time.Now()
	// Создание запроса к бэкенду
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.SetRequestURI(fullPath)
	req.SetTimeout(time.Duration(time.Duration.Seconds(5)))
	req.Header.SetMethodBytes(ctx.Method())
	req.SetBody(ctx.PostBody())

	// Копирование заголовков
	ctx.Request.Header.VisitAll(func(key, value []byte) {
		req.Header.SetBytesKV(key, value)
	})

	// Выполнение запроса
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	err = client.Do(req, resp)
	if err != nil {
		log.Printf("Error forwarding request to %s ==>> %v", fullPath, err)
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
	counter := requestCounters[backend]
	if counter != nil {
		atomic.AddUint64(counter, 1)
	}
	counterMutex.Unlock()

	// Точное измерение времени выполнения
	duration := time.Since(startTime)

	// Запись времени выполнения в Redis stream
	logRequestDuration(backend, string(ctx.Path()), duration)

	// Логирование результата
	// log.Printf("Request to %s:5000%s completed in %v", backend, string(ctx.Path()), duration)
}
