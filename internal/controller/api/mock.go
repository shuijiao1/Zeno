package api

import (
	"time"
)

const gib = 1024 * 1024 * 1024
const tib = 1024 * gib

func ptr(v float64) *float64 { return &v }

func mockNodes() []Node {
	return []Node{
		{
			ID: "hytron", DisplayName: "Hytron", Status: "online", OS: "debian", CountryCode: "HK", Subtitle: "Hong Kong · Controller", CPUCores: ptr(2), ExpiryLabel: "永 久",
			CPUPercent: ptr(55.06), MemoryUsedBytes: ptr(2.59 * gib), MemoryTotalBytes: ptr(3.83 * gib), DiskUsedBytes: ptr(17.8 * gib), DiskTotalBytes: ptr(97.87 * gib),
			NetInSpeedBps: ptr(18.62 * 1024), NetOutSpeedBps: ptr(197.69 * 1024), NetInTotalBytes: ptr(1.29 * tib), NetOutTotalBytes: ptr(1.01 * tib), MonthlyBillableBytes: ptr(1.30 * 1024 * 1024), MonthlyQuotaBytes: ptr(10 * tib),
			LatencySummary: &LatencySummary{TargetID: "telegram-dc5", TargetName: "Telegram DC5", MedianMS: ptr(33.1), AvgMS: ptr(33.1), LossPercent: ptr(0.40), UpdatedAt: "2026-07-02T12:10:00Z"},
		},
		{
			ID: "sharon", DisplayName: "Sharon", Status: "online", OS: "debian", CountryCode: "HK", Subtitle: "Hong Kong", CPUCores: ptr(2), ExpiryLabel: "余 7 天",
			CPUPercent: ptr(8.9), MemoryUsedBytes: ptr(512 * 1024 * 1024), MemoryTotalBytes: ptr(1536 * 1024 * 1024), DiskUsedBytes: ptr(8 * gib), DiskTotalBytes: ptr(30 * gib),
			NetInSpeedBps: ptr(14 * 1024), NetOutSpeedBps: ptr(27 * 1024), NetInTotalBytes: ptr(812 * gib), NetOutTotalBytes: ptr(2.1 * tib), MonthlyBillableBytes: ptr(641 * gib), MonthlyQuotaBytes: ptr(2 * tib),
			LatencySummary: &LatencySummary{TargetID: "google", TargetName: "Google", MedianMS: ptr(0.8), AvgMS: ptr(1.1), LossPercent: ptr(0), UpdatedAt: "2026-07-02T12:10:00Z"},
		},
		{
			ID: "alibaba", DisplayName: "Alibaba", Status: "online", OS: "debian", CountryCode: "HK", Subtitle: "Hong Kong", CPUCores: ptr(2), ExpiryLabel: "余 1755 天",
			CPUPercent: ptr(62.1), MemoryUsedBytes: ptr(1.45 * gib), MemoryTotalBytes: ptr(2 * gib), DiskUsedBytes: ptr(33 * gib), DiskTotalBytes: ptr(40 * gib),
			NetInSpeedBps: ptr(4 * 1024), NetOutSpeedBps: ptr(7 * 1024), NetInTotalBytes: ptr(512 * gib), NetOutTotalBytes: ptr(420 * gib), MonthlyBillableBytes: ptr(788 * gib), MonthlyQuotaBytes: ptr(1 * tib),
			LatencySummary: &LatencySummary{TargetID: "telegram-dc1", TargetName: "Telegram DC1", MedianMS: ptr(188.2), AvgMS: ptr(196.4), LossPercent: ptr(2.5), UpdatedAt: "2026-07-02T12:10:00Z"},
		},
		{
			ID: "hostdzire", DisplayName: "HostDZire", Status: "offline", OS: "debian", CountryCode: "US", Subtitle: "United States", CPUCores: ptr(4), ExpiryLabel: "余 30 天",
			MonthlyQuotaBytes: ptr(1 * tib),
			LatencySummary:    &LatencySummary{TargetID: "google", TargetName: "Google", MedianMS: nil, AvgMS: nil, LossPercent: ptr(100), UpdatedAt: "2026-07-02T12:10:00Z"},
		},
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
