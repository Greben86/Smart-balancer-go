package utils

import (
	"fmt"
	"math"
)

// VisibilityGraph строит степени узлов для временного ряда
func calculateDegrees(data []float64) []int {
	n := len(data)
	degrees := make([]int, n)

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			visible := true
			// Проверяем условие видимости для всех промежуточных точек k
			for k := i + 1; k < j; k++ {
				valK := data[k]
				// Формула прямой линии между i и j
				threshold := data[j] + (data[i]-data[j])*(float64(j-k)/float64(j-i))
				if valK >= threshold {
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

// Простая оценка экспоненты распределения gamma через МНК (логарифмические координаты)
func estimateHurst(degrees []int) float64 {
	// 1. Считаем частоты степеней P(k)
	counts := make(map[int]int)
	for _, k := range degrees {
		counts[k]++
	}

	var logK, logP []float64
	for k, count := range counts {
		if k > 0 {
			logK = append(logK, math.Log(float64(k)))
			logP = append(logP, math.Log(float64(count)/float64(len(degrees))))
		}
	}

	// 2. Линейная регрессия для поиска наклона -gamma
	// В продакшене лучше использовать gonum/stat для регрессии
	gamma := simpleLinearSlope(logK, logP)

	// 3. H = (3 - gamma) / 2. Т.к. наклон отрицательный, берем по модулю.
	return (3.0 - math.Abs(gamma)) / 2.0
}

func simpleLinearSlope(x, y []float64) float64 {
	var sumX, sumY, sumXY, sumXX float64
	n := float64(len(x))
	for i := 0; i < len(x); i++ {
		sumX += x[i]
		sumY += y[i]
		sumXY += x[i] * y[i]
		sumXX += x[i] * x[i]
	}
	return (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
}

func VGHurst(series []float64) (float64, error) {
	degrees := calculateDegrees(series)
	h := estimateHurst(degrees)

	fmt.Printf("Оценка показателя Хёрста (VG): %.4f\n", h)

	return h, nil
}
