package logic

import (
	"fmt"
	"math"

	"gonum.org/v1/gonum/stat"
)

// CalculateDFA реализует Detrended Fluctuation Analysis
func CalculateDFA(series []float64) (float64, error) {
	n := len(series)
	if n < 512 {
		return .5, fmt.Errorf("ряд слишком короткий")
	}

	// 1. Центрирование и интегрирование ряда (Cumulative Sum)
	mean := stat.Mean(series, nil)
	y := make([]float64, n)
	currentSum := 0.0
	for i, val := range series {
		currentSum += val - mean
		y[i] = currentSum
	}

	// Определение размеров окон (scales)
	var scales []int
	for s := 8; s <= n/4; s = int(float64(s) * 1.5) { // Геометрический шаг
		scales = append(scales, s)
	}

	var xLog, yLog []float64

	// 2. Расчет флуктуаций F(s) для каждого масштаба s
	for _, s := range scales {
		numWindows := n / s
		var rmsSum float64

		for i := 0; i < numWindows; i++ {
			start := i * s
			end := start + s
			windowY := y[start:end]

			// Создаем временную шкалу для регрессии (0, 1, 2...s-1)
			timeX := make([]float64, s)
			for j := range timeX {
				timeX[j] = float64(j)
			}

			// Линейная регрессия внутри окна (поиск локального тренда)
			intercept, slope := stat.LinearRegression(timeX, windowY, nil, false)

			// Вычисляем сумму квадратов отклонений от тренда (RMS)
			var squareDiff float64
			for j := 0; j < s; j++ {
				trendVal := intercept + slope*float64(j)
				diff := windowY[j] - trendVal
				squareDiff += diff * diff
			}
			rmsSum += squareDiff / float64(s)
		}

		// Среднеквадратичное отклонение для данного масштаба s
		fs := math.Sqrt(rmsSum / float64(numWindows))

		if fs > 0 {
			xLog = append(xLog, math.Log(float64(s)))
			yLog = append(yLog, math.Log(fs))
		}
	}

	// 3. Итоговый показатель (наклон линии в логарифмических координатах)
	_, beta := stat.LinearRegression(xLog, yLog, nil, false)

	return beta, nil
}

func DFAHurst(series []float64) (float64, error) {
	hurst, _ := CalculateDFA(series)
	fmt.Printf("DFA Hurst: %.4f\n", hurst)

	// Интерпретация DFA:
	// alpha < 0.5: антикорреляция (возврат к среднему)
	// alpha ~= 0.5: белый шум (случайный процесс)
	// alpha > 0.5: корреляция (персистентность/тренд)
	// alpha ~= 1.0: розовый шум (1/f шум)
	// alpha ~= 1.5: броуновское движение
	// Интерпретация результатов
	switch {
	case hurst > 0.6:
		fmt.Println("Интерпретация: Персистентный ряд (выраженный тренд).")
	case hurst < 0.5:
		fmt.Println("Интерпретация: Антиперсистентный ряд (возврат к среднему).")
	default:
		fmt.Println("Интерпретация: Случайное блуждание (белый шум).")
	}

	roundedHurst := math.Round(hurst*10) / 10
	return roundedHurst, nil
}
