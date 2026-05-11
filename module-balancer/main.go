package main

import (
	"container/list"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
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

// Запись метрики времени выполнения в Redis stream
func logRequestDuration(backend string, path string, duration time.Duration) {
	// Создаем map с данными для записи в stream
	values := map[string]any{
		"backend":   backend,
		"path":      path,
		"duration":  duration.Microseconds(), // сохраняем в микросекундах
		"timestamp": time.Now().Unix(),
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

// SmartBalancer реализует балансировку
type SmartBalancer struct {
	backends []string
	current  uint64
}

func NewSmartBalancer() (*SmartBalancer, error) {
	// Получаем строку с адресами из Redis
	result, err := redisClient.Get(redisClient.Context(), "target.services").Result()
	if err != nil {
		log.Printf("Failed to get services.list from Redis: %v", err)
		return nil, err
	}

	// Если строка пустая, возвращаем пустой список
	if result == "" {
		return nil, fmt.Errorf("No backends found in Redis, waiting for services to register")
	}

	// Разделяем строку по запятым и формируем список адресов
	addresses := strings.Split(result, ",")
	backends := make([]string, 0, len(addresses))

	// Формируем полные URL для каждого адреса
	for _, addr := range addresses {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			backends = append(backends, addr)
		}
	}

	return &SmartBalancer{
		backends: backends,
	}, nil
}

func (r *SmartBalancer) Next() string {
	if len(r.backends) == 0 {
		log.Printf("List of backends is empty")
		return ""
	}

	hurstValues := make(map[string]float64)
	for _, backend := range r.backends {
		// Получаем значение hurst из Redis
		hurstKey := "service.hurst." + backend
		hurstStr, err := redisClient.Get(redisClient.Context(), hurstKey).Result()
		if err != nil {
			// Если ключ не найден или ошибка, пропускаем этот бэкенд
			log.Printf("Failed to get %s from Redis: %v", hurstKey, err)
			continue
		}
		hurst, err := strconv.ParseFloat(hurstStr, 64)
		if err != nil {
			log.Printf("Failed to parse hurst value %s for %s: %v", hurstStr, backend, err)
			continue
		}

		// Избегаем деления на ноль
		if hurst == .0 {
			log.Printf("Hurst value is 0 for %s", backend)
			continue
		}

		hurstValues[backend] = hurst
	}

	// Если не нельзя выбрать бэкенд по hurst, используем рассчет
	if len(r.backends) != len(hurstValues) {
		current := uint64(time.Now().UnixNano())
		bestBackend := r.backends[(current-1)%uint64(len(r.backends))]
		log.Printf("Calculate backend -> %s", bestBackend)
		return bestBackend
	}

	var bestBackend string
	var maxRatio = float64(0)
	var maxRationStr string
	var ratiolist strings.Builder
	var delimiter = ""
	var ratioMap = make(map[string]*list.List)
	for _, backend := range r.backends {
		// Преобразуем значение в float64
		hurst := hurstValues[backend]

		// Считаем отношение 10000/hurst
		ratio := 10_000.0 / hurst
		var ratioStr = fmt.Sprintf("%.3f", ratio)

		// Выбираем бэкенд с максимальным отношением
		if ratio > maxRatio {
			maxRatio = ratio
			maxRationStr = ratioStr
			bestBackend = backend
		}

		if _, exists := ratioMap[ratioStr]; !exists {
			ratioMap[ratioStr] = list.New()
		}
		ratioMap[ratioStr].PushBack(backend)
		ratiolist.WriteString(delimiter + ratioStr)
		delimiter = ", "
	}

	if ratioMap[maxRationStr].Len() == 1 {
		log.Printf("The best backend from %d is %s (ratio list = %s)", len(r.backends), bestBackend, ratiolist.String())
		return bestBackend
	}

	bestBackends := make([]string, 0, ratioMap[maxRationStr].Len())
	for e := ratioMap[maxRationStr].Front(); e != nil; e = e.Next() {
		if val, ok := e.Value.(string); ok {
			bestBackends = append(bestBackends, val)
		}
	}

	// Если не удалось выбрать бэкенд по hurst, используем дополнительный рассчет
	current := uint64(time.Now().UnixNano())
	bestBackend = bestBackends[(current-1)%uint64(len(bestBackends))]
	log.Printf("Calculate backend %s from %d values", bestBackend, len(bestBackends))

	return bestBackend
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
	balancer, err := NewSmartBalancer()
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
