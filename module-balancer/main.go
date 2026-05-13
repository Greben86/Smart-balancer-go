package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"smart-balancer-go/logic"
	"smart-balancer-go/utils"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/valyala/fasthttp"
)

var (
	port         = flag.String("port", ":8080", "Port to listen on")
	config       = flag.String("config", "application.yml", "Configuration file")
	counterMutex sync.RWMutex
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
func logRequestDuration(backend string, path string, duration time.Duration, status int) {
	// Создаем map с данными для записи в stream
	values := map[string]any{
		"event_type":  "request_duration",
		"backend":     backend,
		"path":        path,
		"status_code": status,
		"duration":    duration.Microseconds(), // сохраняем в микросекундах
		"timestamp":   time.Now().Unix(),
	}

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

	// Запускаем горутину для логирования количества записей каждую минуту
	go func() {
		ticker := time.NewTicker(10 * time.Second)
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
					} else {
						log.Printf("Updated requests.prev.%s in Redis with value %v", backend, curCount)
					}
					if err := redisClient.Set(redisClient.Context(), "requests.cur."+backend, 0, 0).Err(); err != nil {
						log.Printf("Failed to update requests.cur.%s in Redis: %v", backend, err)
					} else {
						log.Printf("Updated requests.cur.%s in Redis with value 0", backend)
					}
				}
				// Записываем текущее время для backend
				timestamp := time.Now().Local().UnixMilli()
				if err := redisClient.Set(redisClient.Context(), "timestamp.current", timestamp, 0).Err(); err != nil {
					log.Printf("Failed to update timestamp.current in Redis: %v", err)
				} else {
					log.Printf("Updated timestamp.current in Redis with value %d", timestamp)
				}
			} else {
				log.Printf("No backends found in Redis")
			}
		}
	}()

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
	var statusCode int
	// Начало точного измерения времени
	startTime := time.Now()
	balancer, err := logic.NewSmartBalancer(redisClient)
	if err != nil {
		log.Printf("Error creating balancer instance: %v", err)
		ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
		ctx.SetBodyString("{\"error\": \"Backend list is empty\"}")
		return
	}
	backend := balancer.Next()
	if backend == "" {
		log.Printf("Too many requests")
		statusCode = fasthttp.StatusTooManyRequests
		ctx.SetStatusCode(statusCode)
		ctx.SetBodyString("{\"error\": \"Too many requests\"}")
	} else {
		fullPath := "http://" + backend + ":5000" + string(ctx.Path())

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
			statusCode = fasthttp.StatusServiceUnavailable
			ctx.SetStatusCode(statusCode)
			ctx.SetBodyString("{\"error\": \"Backend service unavailable\"}")
		} else {
			// Копирование заголовков ответа
			resp.Header.VisitAll(func(key, value []byte) {
				ctx.Response.Header.SetBytesKV(key, value)
			})

			// Копирование статуса и тела
			statusCode = resp.StatusCode()
			ctx.SetStatusCode(statusCode)
			ctx.SetBody(resp.Body())
		}
	}

	// Точное измерение времени выполнения
	duration := time.Since(startTime)

	// Запись времени выполнения в Redis stream
	go logRequestDuration(backend, string(ctx.Path()), duration, statusCode)
}
