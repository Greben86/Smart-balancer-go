package logic

import (
	"container/list"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// SmartBalancer реализует балансировку
type SmartBalancer struct {
	redisClient *redis.Client
	backends    []string
}

func NewSmartBalancer(redisClient *redis.Client) (*SmartBalancer, error) {
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
		redisClient: redisClient,
		backends:    backends,
	}, nil
}

func (lb *SmartBalancer) Next() string {
	if len(lb.backends) == 0 {
		log.Printf("List of backends is empty")
		return ""
	}

	hurstValues := make(map[string]float64)
	for _, backend := range lb.backends {
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
	if len(lb.backends) != len(hurstValues) {
		current := uint64(time.Now().UnixNano())
		bestBackend := lb.backends[(current-1)%uint64(len(lb.backends))]
		log.Printf("Calculate backend -> %s", bestBackend)
		return bestBackend
	}

	var bestBackend string
	var maxRatio = float64(0)
	var maxRationStr string
	var ratiolist strings.Builder
	var delimiter = ""
	var ratioMap = make(map[string]*list.List)
	for _, backend := range lb.backends {
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
		log.Printf("The best backend from %d is %s (ratio list = %s)", len(lb.backends), bestBackend, ratiolist.String())
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
