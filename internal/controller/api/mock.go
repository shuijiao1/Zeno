package api

import (
	"time"
)

const kib = 1024
const mib = 1024 * kib
const gib = 1024 * mib
const tib = 1024 * gib

func ptr(v float64) *float64 { return &v }
func kb(v float64) *float64  { return ptr(v * kib) }
func mb(v float64) *float64  { return ptr(v * mib) }
func gb(v float64) *float64  { return ptr(v * gib) }
func tb(v float64) *float64  { return ptr(v * tib) }

func used(total *float64, percent float64) *float64 {
	if total == nil {
		return nil
	}
	return ptr(*total * percent / 100)
}

func mockNode(id, name, osName, countryCode string, cores float64, expiry string, cpuPercent float64, memoryTotal *float64, memoryPercent float64, diskTotal *float64, diskPercent float64, monthlyBillable, monthlyQuota, netOutSpeed, netInSpeed *float64, latency, loss *float64) Node {
	var latencySummary *LatencySummary
	if latency != nil || loss != nil {
		latencySummary = &LatencySummary{TargetID: "default", TargetName: "Default", MedianMS: latency, AvgMS: latency, LossPercent: loss, UpdatedAt: "2026-07-02T12:10:00Z"}
	}
	return Node{
		ID:                   id,
		DisplayName:          name,
		Status:               "online",
		OS:                   osName,
		CountryCode:          countryCode,
		CPUCores:             ptr(cores),
		ExpiryLabel:          expiry,
		CPUPercent:           ptr(cpuPercent),
		MemoryUsedBytes:      used(memoryTotal, memoryPercent),
		MemoryTotalBytes:     memoryTotal,
		DiskUsedBytes:        used(diskTotal, diskPercent),
		DiskTotalBytes:       diskTotal,
		NetInSpeedBps:        netInSpeed,
		NetOutSpeedBps:       netOutSpeed,
		NetInTotalBytes:      monthlyBillable,
		NetOutTotalBytes:     monthlyBillable,
		MonthlyBillableBytes: monthlyBillable,
		MonthlyQuotaBytes:    monthlyQuota,
		LatencySummary:       latencySummary,
	}
}

func mockNodes() []Node {
	return []Node{
		mockNode("mechrevo", "Mechrevo", "windows", "HK", 16, "永 久", 19.73, gb(23.29), 58.73, tb(1.37), 30.62, kb(104.32), tb(10), kb(27.22), kb(967.98), nil, nil),
		mockNode("sharon", "Sharon", "debian", "HK", 2, "余 7 天", 6.72, gb(1.89), 28.08, gb(29.36), 5.92, gb(212.90), gb(512), kb(7.00), kb(10.75), ptr(55.9), ptr(0.18)),
		mockNode("alibaba", "Alibaba", "debian", "HK", 2, "余 1755 天", 5.04, mb(431.25), 47.63, gb(4.84), 34.00, gb(2.75), gb(200), kb(4.63), kb(7.98), ptr(35.1), ptr(0)),
		mockNode("hytron", "Hytron", "debian", "HK", 2, "永 久", 61.43, gb(3.83), 68.52, gb(97.87), 18.21, mb(1.50), tb(10), kb(419.02), kb(20.00), ptr(34.6), ptr(0.40)),
		mockNode("datawave-hk", "DataWave HK", "debian", "HK", 1, "余 30 天", 3.02, mb(464.69), 44.13, gb(9.74), 15.31, kb(19.80), gb(1000), kb(3.87), kb(4.01), ptr(31.6), ptr(0)),
		mockNode("zouter", "Zouter", "debian", "JP", 1, "余 15 天", 7.88, mb(967.94), 42.87, gb(19.52), 8.03, kb(14.18), tb(2), kb(4.50), kb(8.41), ptr(108), ptr(1.22)),
		mockNode("datawave-jp", "DataWave JP", "debian", "JP", 1, "余 30 天", 0.49, mb(464.69), 49.53, gb(9.74), 16.12, kb(4.92), gb(1000), kb(0.40), kb(11.96), ptr(86.1), ptr(0.66)),
		mockNode("datawave-tw", "DataWave TW", "debian", "CN", 1, "余 30 天", 6.97, mb(464.69), 45.27, gb(9.74), 16.07, kb(4.48), gb(1000), kb(1.13), kb(2.11), ptr(62.7), ptr(0.16)),
		mockNode("hostdzire", "HostDZire", "debian", "US", 4, "余 1017 天", 50.31, gb(5.79), 76.18, gb(97.87), 53.88, gb(28.34), tb(25), kb(281.90), kb(512.42), ptr(172), ptr(0.04)),
		mockNode("bage", "BAGE", "debian", "US", 1, "余 91 天", 4.85, mb(967.94), 39.16, gb(19.52), 5.31, kb(68.65), tb(4), kb(22.88), kb(25.78), ptr(202), ptr(1.48)),
		mockNode("hostishere", "Hostishere", "debian", "DE", 1, "余 331 天", 1.38, gb(1.84), 23.82, gb(19.52), 7.52, kb(2.63), tb(1), kb(1.31), kb(1.69), ptr(156), ptr(0.09)),
	}
}

func mockLatencyPoints(nodeID string) []LatencyPoint {
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	points := make([]LatencyPoint, 0, 36*3)
	for index := 0; index < 36; index++ {
		ts := base.Add(time.Duration(index*2) * time.Minute).Format(time.RFC3339)
		spike := 0.0
		if index == 18 {
			spike = 110
		}
		loss := 0.0
		if index == 24 {
			loss = 100
		} else if index == 12 {
			loss = 30
		}
		points = append(points,
			LatencyPoint{TS: ts, TargetID: "google", TargetName: "Google", MedianMS: ptr(0.7 + float64(index%5)*0.08), LossPercent: 0},
			LatencyPoint{TS: ts, TargetID: "telegram-dc5", TargetName: "Telegram DC5", MedianMS: ptr(31 + float64(index%6)*0.7 + spike), LossPercent: 0},
		)
		var dc1Median *float64
		if loss < 100 {
			dc1Median = ptr(185 + float64(index%4)*7)
		}
		points = append(points, LatencyPoint{TS: ts, TargetID: "telegram-dc1", TargetName: "Telegram DC1", MedianMS: dc1Median, LossPercent: loss})
	}
	return points
}
