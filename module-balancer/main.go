package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
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
	redisAddr := GetEnv("REDIS_ADDR", "localhost:6379")
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

// Чтение переменных окружения
func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value != "" {
		return value
	}
	return defaultValue
}

// Получение списка бэкендов из Redis
func getBackendsFromRedis() []string {
	// Получаем строку с адресами из Redis
	result, err := redisClient.Get(redisClient.Context(), "services.list").Result()
	if err != nil {
		log.Printf("Failed to get services.list from Redis: %v", err)
		return []string{}
	}

	// Если строка пустая, возвращаем пустой список
	if result == "" {
		return []string{}
	}

	// Разделяем строку по запятым и формируем список адресов
	addresses := strings.Split(result, ",")
	backends := make([]string, 0, len(addresses))

	// Формируем полные URL для каждого адреса
	for _, addr := range addresses {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			// Добавляем схему и порт, если их нет
			if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
				addr = "http://" + addr
			}
			// Если порт не указан, используем 8080
			if !strings.Contains(addr, ":") {
				addr = addr + ":8080"
			}
			backends = append(backends, addr)
		}
	}

	return backends
}

// Запись метрики времени выполнения в Redis stream
func logRequestDuration(backendURL string, duration time.Duration) {
	// Создаем map с данными для записи в stream
	values := map[string]interface{}{
		"backend":   backendURL,
		"duration":  duration.Microseconds(), // сохраняем в микросекундах
		"timestamp": time.Now().Unix(),
	}

	// Добавляем запись в stream
	if err := redisClient.XAdd(redisClient.Context(), &redis.XAddArgs{
		Stream: "request.durations", // имя stream
		Values: values,
	}).Err(); err != nil {
		log.Printf("Failed to write request duration to Redis stream: %v", err)
	}
}

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
	ctx.SetBodyString(fmt.Sprintf("{\"status\": \"healthy\", \"backends\": ...}"))
}

func backendsJSON(backends []string) string {
	json, _ := json.Marshal(backends)
	return string(json)
}

func proxyHandler(ctx *fasthttp.RequestCtx, client *fasthttp.Client) {
	// Получаем список бэкендов из Redis
	backends := getBackendsFromRedis()
	if len(backends) == 0 {
		log.Println("No backends found in Redis, waiting for services to register")
	}

	balancer := NewRoundRobinBalancer(backends)

	backendURL := balancer.Next() + ":5000"

	// Начало точного измерения времени
	startTime := time.Now()
	// Создание запроса к бэкенду
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.SetRequestURI(backendURL + string(ctx.Path()))
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

	// Точное измерение времени выполнения
	duration := time.Since(startTime)

	// Запись времени выполнения в Redis stream
	logRequestDuration(backendURL, duration)

	// Логирование результата
	log.Printf("Request to %s completed in %v", backendURL, duration)
}
