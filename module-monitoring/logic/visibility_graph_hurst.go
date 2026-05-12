package logic

import (
	"fmt"
	"math"

	"gonum.org/v1/gonum/stat"
)

// 1. Построение графа и расчет степеней узлов
func getDegrees(data []float64) []float64 {
	n := len(data)
	degrees := make([]float64, n)

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			visible := true
			// Условие видимости
			for k := i + 1; k < j; k++ {
				threshold := data[j] + (data[i]-data[j])*(float64(j-k)/float64(j-i))
				if data[k] >= threshold {
					visible = false
					break
				}
			}
			if visible {
				degrees[i]++
				degrees[j]++
			}
		}
	}
	return degrees
}

// 2. Оценка показателя Хёрста
func calculateHurstVG(degrees []float64, length int) float64 {

	// Подсчет частот P(k)
	counts := make(map[float64]float64)
	for _, k := range degrees {
		counts[k]++
	}

	var logK, logP []float64
	n := float64(length)

	for k, count := range counts {
		if k > 0 {
			logK = append(logK, math.Log(k))
			logP = append(logP, math.Log(count/n))
		}
	}

	// 3. Линейная регрессия через Gonum
	// LinearRegression возвращает (alpha, beta) для y = alpha + beta*x
	_, gammaRaw := stat.LinearRegression(logK, logP, nil, false)

	gamma := math.Abs(gammaRaw)

	// Формула H = (3 - gamma) / 2
	h := (3.0 - gamma) / 2.0
	return h
}

func VGHurst(series []float64) (float64, error) {
	length := len(series)
	if length < 100 {
		return .5, nil
	}

	degrees := getDegrees(series)
	hurst := calculateHurstVG(degrees, length)

	fmt.Printf("Оценка показателя Хёрста (VG): %.4f\n", hurst)

	return hurst, nil
}
