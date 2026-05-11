package utils

import "math"

// Hurst рассчитывает упрощенный параметр Херста через R/S
func SimpleHurst(data []float64) (float64, error) {
	n := len(data)
	if n < 100 {
		return 0.5, nil
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
		return 0.5, nil
	}
	return math.Log(R/S) / math.Log(float64(n)), nil
}
