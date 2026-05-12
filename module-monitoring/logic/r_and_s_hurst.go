package logic

import (
	"fmt"
	"math"

	"gonum.org/v1/gonum/stat"
)

// CalculateHurst находит показатель Хёрста методом R/S анализа.
func CalculateHurst(series []float64) (float64, error) {
	n := len(series)
	if n < 32 {
		return 0, fmt.Errorf("длина временного ряда слишком мала для R/S-анализа (минимум 32)")
	}

	// Определение размеров окон (разбиение по степеням двойки)
	var chunkSizes []int
	for size := 8; size <= n/2; size *= 2 {
		chunkSizes = append(chunkSizes, size)
	}

	if len(chunkSizes) < 2 {
		return 0, fmt.Errorf("недостаточно точек для линейной регрессии")
	}

	var xReg, yReg []float64

	// Расчет R/S для каждого размера окна
	for _, size := range chunkSizes {
		numChunks := n / size
		var rsValues []float64

		for i := 0; i < numChunks; i++ {
			start := i * size
			end := start + size
			chunk := series[start:end]

			// 1. Поиск среднего и стандартного отклонения (Gonum)
			mean, std := stat.MeanStdDev(chunk, nil)
			if std == 0 {
				rsValues = append(rsValues, .5)
				continue // Пропускаем стационарные участки во избежание деления на ноль
			}

			// 2. Расчет накопленного отклонения и его размаха (R)
			minDev := 0.0
			maxDev := 0.0
			cumDev := 0.0

			for _, val := range chunk {
				cumDev += val - mean
				if cumDev < minDev {
					minDev = cumDev
				}
				if cumDev > maxDev {
					maxDev = cumDev
				}
			}
			r := maxDev - minDev

			// 3. Значение R/S для конкретного блока
			rsValues = append(rsValues, r/std)
		}

		if len(rsValues) > 0 {
			// Среднее R/S для текущего размера окна (Gonum)
			meanRS := stat.Mean(rsValues, nil)

			// Логарифмирование для линейной регрессии
			xReg = append(xReg, math.Log(float64(size)))
			yReg = append(yReg, math.Log(meanRS))
		}
	}

	// 4. Поиск наклона линии (показателя Хёрста) методом наименьших квадратов
	_, beta := stat.LinearRegression(xReg, yReg, nil, false)

	return beta, nil
}

func RSHurst(series []float64) (float64, error) {
	hurst, err := CalculateHurst(series)
	if err != nil {
		fmt.Printf("Ошибка: %v\n", err)
		return .5, err
	}
	roundedHurst := math.Round(hurst*10) / 10

	fmt.Printf("Показатель Хёрста (H): %.1f\n", roundedHurst)

	// Интерпретация результатов
	switch {
	case hurst > 0.7:
		fmt.Println("Интерпретация: Персистентный ряд (выраженный тренд).")
	case hurst < 0.5:
		fmt.Println("Интерпретация: Антиперсистентный ряд (возврат к среднему).")
	default:
		fmt.Println("Интерпретация: Случайное блуждание (белый шум).")
	}

	return roundedHurst, nil
}
