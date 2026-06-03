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

var maxRateValue int64 = 100
var durationControlIntervalMs int64 = 1000

// SmartBalancer реализует балансировку
type SmartBalancer2 struct {
	redisClient *redis.Client
	backends    []string
}

func NewSmartBalancer2(redisClient *redis.Client, excludedBackends []string) (*SmartBalancer2, error) {
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

	// Создаем map для быстрого поиска исключенных бэкендов
	excludedMap := make(map[string]bool)
	for _, backend := range excludedBackends {
		excludedMap[backend] = true
	}

	// Формируем полные URL для каждого адреса, исключая те, что в списке excludedBackends
	for _, addr := range addresses {
		addr = strings.TrimSpace(addr)
		if addr != "" && !excludedMap[addr] {
			backends = append(backends, addr)
		}
	}

	log.Printf("Length of backends: %d", len(backends))

	return &SmartBalancer2{
		redisClient: redisClient,
		backends:    backends,
	}, nil
}

func (lb *SmartBalancer2) Next() (string, bool) {
	if len(lb.backends) == 0 {
		log.Printf("List of backends is empty")
		return "", false
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
		remainingMs = max(durationControlIntervalMs-elapsedMs, 0)
	}

	hurstValues := make(map[string]int64)
	availableRateValues := make(map[string]int64)
	prevRatioMap := make(map[string]int64)
	for _, backend := range lb.backends {
		var preCount int64
		preCountStr, err := lb.redisClient.Get(lb.redisClient.Context(), "requests.prev."+backend).Result()
		if err != nil {
			preCount = 0
		} else {
			preCount, _ = strconv.ParseInt(preCountStr, 10, 64)
		}

		var curCount int64
		curCountStr, err := lb.redisClient.Get(lb.redisClient.Context(), "requests.cur."+backend).Result()
		if err != nil {
			curCount = 0
		} else {
			curCount, _ = strconv.ParseInt(curCountStr, 10, 64)
		}

		currentRate := preCount*remainingMs/durationControlIntervalMs + curCount
		// log.Printf("Current rate value is [%v * %v / 1000 + %v = %v] for %s", preCount, remainingMs, curCount, currentRate, backend)
		if currentRate >= maxRateValue {
			continue
		}

		availableRateValues[backend] = maxRateValue - currentRate

		prevRatioStr, err := lb.redisClient.Get(lb.redisClient.Context(), "ratio.prev."+backend).Result()
		if err != nil {
			prevRatioMap[backend] = 0 // Если ключ не найден, используем 0
		} else {
			prevRatio, _ := strconv.ParseInt(prevRatioStr, 10, 64)
			prevRatioMap[backend] = prevRatio
		}

		// Получаем значение hurst из Redis
		hurstStr, err := lb.redisClient.Get(lb.redisClient.Context(), "service.hurst."+backend).Result()
		if err != nil {
			// Если ключ не найден или ошибка, пропускаем этот бэкенд
			log.Printf("Failed to get service.hurst.%s from Redis: %v", backend, err)
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

		hurstValues[backend] = int64(math.Round(10 * hurst))
	}

	// Если нет бэкендов, которые могут обработать запрос
	if len(availableRateValues) == 0 {
		current := uint64(time.Now().UnixNano())
		return lb.backends[current%uint64(len(lb.backends))], false
	}

	// Если не нельзя выбрать бэкенд по hurst, используем рассчет
	if len(availableRateValues) != len(hurstValues) {
		bestBackends := make([]string, 0, len(availableRateValues))
		for backend := range availableRateValues {
			bestBackends = append(bestBackends, backend)
		}

		// Если не удалось выбрать бэкенд по hurst, используем дополнительный рассчет
		current := uint64(time.Now().UnixNano())
		bestBackend := bestBackends[current%uint64(len(bestBackends))]
		// log.Printf("Calculate backend %s from %d values", bestBackend, len(bestBackends))

		go lb.updateValuesInRedis(bestBackend, nil)
		return bestBackend, true
	}

	var bestBackend string
	var maxRatio int64 = math.MinInt64
	var ratioMap = make(map[int64]*list.List)
	for backend, count := range availableRateValues {
		hurst := hurstValues[backend]

		// var ratio int
		// if len(rateValues) == len(prevRatioMap) {
		// 	prevRatio := prevRatioMap[backend] // предыдущее значение нагрузки
		// 	// Считаем отношение количества запросов к показателю Херста
		// 	ratio = int(math.Round((prevRatio + (10 * float64(count) / hurst)) / 2))
		// } else {
		// }
		ratio := count / hurst
		// ratio := int(math.Round(1000 - 10*count/hurst))

		// Выбираем бэкенд с минимальным соотношением
		if ratio > maxRatio {
			maxRatio = ratio
			bestBackend = backend
		}

		if _, exists := ratioMap[ratio]; !exists {
			ratioMap[ratio] = list.New()
		}
		ratioMap[ratio].PushBack(backend)
	}

	if ratioMap[maxRatio].Len() > 1 {
		bestBackends := make([]string, 0, ratioMap[maxRatio].Len())
		for e := ratioMap[maxRatio].Front(); e != nil; e = e.Next() {
			if val, ok := e.Value.(string); ok {
				bestBackends = append(bestBackends, val)
			}
		}

		// Если не удалось выбрать бэкенд по hurst, используем дополнительный рассчет
		current := uint64(time.Now().UnixNano())
		bestBackend = bestBackends[current%uint64(len(bestBackends))]
	}

	go lb.updateValuesInRedis(bestBackend, ratioMap)

	return bestBackend, true
}

// Увеличиваем счётчик запросов по IP
func (lb *SmartBalancer2) updateValuesInRedis(backend string, ratioMap map[int64]*list.List) {
	if backend != "" {
		key := "requests.cur." + backend
		lb.redisClient.Incr(lb.redisClient.Context(), key).Err()
	}

	if len(ratioMap) > 0 {
		for ratio, backends := range ratioMap {
			for e := backends.Front(); e != nil; e = e.Next() {
				if val, ok := e.Value.(string); ok {
					if err := lb.redisClient.Set(lb.redisClient.Context(), "ratio.prev."+val, ratio, 0).Err(); err != nil {
						log.Printf("Failed to update ratio.prev.%s in Redis: %v", val, err)
					}
				}
			}
		}
	}
}
