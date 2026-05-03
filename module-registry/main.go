package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp"
)

var (
	port   = flag.String("port", ":8888", "Port to listen on")
	config = flag.String("config", "application.yml", "Configuration file")
)

// Мапа для хранения IP-адресов сервисов и времени их последнего heartbeat
var (
	services      = make(map[string]time.Time)
	servicesMutex sync.RWMutex
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

	// Инициализация Redis
	initRedis()

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

	// Формируем строку с адресами через запятую
	var serviceList []string
	for ip := range services {
		serviceList = append(serviceList, ip)
	}
	servicesMutex.Unlock()

	// Отправляем список сервисов в Redis
	if err := redisClient.Set(redisClient.Context(), "services.list", strings.Join(serviceList, ","), 0).Err(); err != nil {
		log.Printf("Failed to update services.list in Redis: %v", err)
	}

	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString(fmt.Sprintf("{\"status\": \"heartbeat received\", \"client_ip\": \"%s\"}", clientIP))
}
