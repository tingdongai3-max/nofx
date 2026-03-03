package market

import "math"

// CalculatePOC 计算简化版筹码分布的 POC（Point of Control，成交量最密集的价格区）。
// buckets 为价格分桶数量，建议 30–100 之间；返回值为该桶的代表价格（区间中点）。
func CalculatePOC(klines []Kline, buckets int) float64 {
	if len(klines) == 0 || buckets <= 0 {
		return 0
	}

	// 找到整体价格区间
	pMin := klines[0].Low
	pMax := klines[0].High
	for _, k := range klines[1:] {
		if k.Low < pMin {
			pMin = k.Low
		}
		if k.High > pMax {
			pMax = k.High
		}
	}
	if !isFinite(pMin) || !isFinite(pMax) || pMax <= pMin {
		return 0
	}

	step := (pMax - pMin) / float64(buckets)
	if step <= 0 || math.IsNaN(step) || math.IsInf(step, 0) {
		return 0
	}

	volumes := make([]float64, buckets)

	for _, k := range klines {
		if k.Volume <= 0 || !isFinite(k.Volume) {
			continue
		}
		// 采用区间中点近似该根 K 线的主要成交价格
		price := (k.High + k.Low) / 2
		if !isFinite(price) {
			continue
		}
		idx := int((price - pMin) / step)
		if idx < 0 {
			idx = 0
		}
		if idx >= buckets {
			idx = buckets - 1
		}
		volumes[idx] += k.Volume
	}

	// 找到成交量最大的桶
	maxIdx := -1
	maxVol := 0.0
	for i, v := range volumes {
		if v > maxVol {
			maxVol = v
			maxIdx = i
		}
	}
	if maxIdx < 0 || maxVol <= 0 {
		return 0
	}

	// 返回该桶的价格中点
	return pMin + (float64(maxIdx)+0.5)*step
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

