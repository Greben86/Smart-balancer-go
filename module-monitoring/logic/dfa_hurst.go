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

	for _, v := range series {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("ряд содержит NaN или Inf")
		}
	}

	// 1. Центрирование и интегрирование ряда (Cumulative Sum)
	mean := stat.Mean(series, nil)
	y := make([]float64, n)
	currentSum := .0
	for i, val := range series {
		currentSum += val - mean
		y[i] = currentSum
	}

	// Определение размеров окон (scales)
	minScale, maxScale := 8, n/4
	numScales := 20
	scales := make([]int, 0, numScales)
	for i := 0; i < numScales; i++ {
		scale := int(math.Exp(math.Log(float64(minScale)) +
			float64(i)*(math.Log(float64(maxScale))-math.Log(float64(minScale)))/float64(numScales-1)))
		if scale > maxScale {
			break
		}
		scales = append(scales, scale)
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
	hurst, err := CalculateDFA(series)
	if err != nil {
		return 0, err
	}
	fmt.Printf("DFA Hurst: %.4f\n", hurst)

	// Интерпретация DFA:
	// alpha < 0.5: антикорреляция (возврат к среднему)
	// alpha ~= 0.5: белый шум (случайный процесс)
	// alpha > 0.5: корреляция (персистентность/тренд)
	// alpha ~= 1.0: розовый шум (1/f шум)
	// alpha ~= 1.5: броуновское движение
	// Интерпретация результатов
	switch {
	case hurst > 0.5:
		fmt.Println("Персистентный ряд (долгосрочная зависимость).")
	case hurst < 0.5:
		fmt.Println("Антиперсистентный ряд (возврат к среднему).")
	default:
		fmt.Println("Белый шум (без долгосрочной зависимости).")
	}

	roundedHurst := math.Round(hurst*10) / 10
	return roundedHurst, nil
}
