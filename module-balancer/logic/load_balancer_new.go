package logic

import (
	"container/list"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// SmartBalancer реализует балансировку
type SmartBalancer2 struct {
	redisClient *redis.Client
	backends    []string
}

func NewSmartBalancer2(redisClient *redis.Client) (*SmartBalancer2, error) {
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

	return &SmartBalancer2{
		redisClient: redisClient,
		backends:    backends,
	}, nil
}

func (lb *SmartBalancer2) Next() string {
	if len(lb.backends) == 0 {
		log.Printf("List of backends is empty")
		return ""
	}
	// Получаем время начала текущего периода
	var remainingMs int64
	timestampKey := "timestamp.current"
	timestampStr, err := lb.redisClient.Get(lb.redisClient.Context(), timestampKey).Result()
	if err != nil {
		remainingMs = 0
		log.Printf("Failed to get %s from Redis: %v", timestampKey, err)
	} else {
		timestamp, _ := strconv.ParseInt(timestampStr, 10, 64)
		now := time.Now().Local().UnixMilli()
		elapsedMs := now - timestamp
		remainingMs = max(1000-elapsedMs, 0)
	}

	hurstValues := make(map[string]float64)
	rateValues := make(map[string]int64)
	for _, backend := range lb.backends {
		var curCount int
		curCountStr, err := lb.redisClient.Get(lb.redisClient.Context(), "requests.cur."+backend).Result()
		if err != nil {
			curCount = 0
		} else {
			curCount, _ = strconv.Atoi(curCountStr)
		}

		var preCount int
		preCountStr, err := lb.redisClient.Get(lb.redisClient.Context(), "requests.prev."+backend).Result()
		if err != nil {
			preCount = 0
		} else {
			preCount, _ = strconv.Atoi(preCountStr)
		}

		currentRate := int64(math.Round(float64(preCount)*(float64(remainingMs)/1000)) + float64(curCount))
		if currentRate >= 100 {
			continue
		}

		rateValues[backend] = currentRate

		// Получаем значение hurst из Redis
		hurstKey := "service.hurst." + backend
		hurstStr, err := lb.redisClient.Get(lb.redisClient.Context(), hurstKey).Result()
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
	if len(rateValues) != len(hurstValues) {
		bestBackends := make([]string, 0, len(rateValues))
		for backend := range rateValues {
			bestBackends = append(bestBackends, backend)
		}

		// Если не удалось выбрать бэкенд по hurst, используем дополнительный рассчет
		current := uint64(time.Now().UnixNano())
		bestBackend := bestBackends[current%uint64(len(bestBackends))]
		log.Printf("Calculate backend %s from %d values", bestBackend, len(bestBackends))
		// Увеличиваем счётчик запросов по IP
		key := "requests.cur." + bestBackend
		lb.redisClient.Incr(lb.redisClient.Context(), key).Err() // игнорируем ошибку, как в logRequestDuration
		return bestBackend
	}

	var bestBackend string
	var minRatio = math.MaxInt64
	var ratioMap = make(map[int]*list.List)
	for backend, count := range rateValues {
		hurst := hurstValues[backend]

		// Считаем отношение количества запросов к показателю Херста
		ratio := int(math.Round(10 * float64(count) / hurst))

		// Выбираем бэкенд с максимальным отношением
		if ratio < minRatio {
			minRatio = ratio
			bestBackend = backend
		}

		if _, exists := ratioMap[ratio]; !exists {
			ratioMap[ratio] = list.New()
		}
		ratioMap[ratio].PushBack(backend)
	}

	if ratioMap[minRatio].Len() == 1 {
		log.Printf("The best backend from %d is %s ratio = %v", len(lb.backends), bestBackend, minRatio)
		return bestBackend
	}

	bestBackends := make([]string, 0, ratioMap[minRatio].Len())
	for e := ratioMap[minRatio].Front(); e != nil; e = e.Next() {
		if val, ok := e.Value.(string); ok {
			bestBackends = append(bestBackends, val)
		}
	}

	// Если не удалось выбрать бэкенд по hurst, используем дополнительный рассчет
	current := uint64(time.Now().UnixNano())
	bestBackend = bestBackends[current%uint64(len(bestBackends))]
	log.Printf("Calculate backend %s from %d values", bestBackend, len(bestBackends))

	// Увеличиваем счётчик запросов по IP
	key := "requests.cur." + bestBackend
	lb.redisClient.Incr(lb.redisClient.Context(), key).Err() // игнорируем ошибку, как в logRequestDuration

	return bestBackend
}
