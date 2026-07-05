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

func mockOSVersion(osName string) string {
	if osName == "windows" {
		return "11"
	}
	return "13"
}

func mockKernel(osName string) string {
	if osName == "windows" {
		return "10.0.26100"
	}
	return "6.12.0"
}

func mockVirtualization(osName string) string {
	if osName == "windows" {
		return "hyper-v"
	}
	return "kvm"
}

func mockCPUModel(osName string, cores float64) string {
	if osName == "windows" {
		return "AMD Ryzen 9 7945HX"
	}
	if cores >= 4 {
		return "AMD EPYC 7B13"
	}
	return "Intel Xeon Virtual CPU"
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
		OSVersion:            mockOSVersion(osName),
		Kernel:               mockKernel(osName),
		Arch:                 "x86_64",
		Virtualization:       mockVirtualization(osName),
		CPUModel:             mockCPUModel(osName, cores),
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
		BillingMode:          "both",
		MonthlyResetDay:      1,
		MonthlyPeriodStart:   "2026-07-01",
		MonthlyPeriodEnd:     "2026-07-31",
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

type latencyWindow struct {
	Name    string
	Samples int
	Step    time.Duration
}

func resolveLatencyWindow(rangeName string) (latencyWindow, bool) {
	switch rangeName {
	case "", "1h":
		return latencyWindow{Name: "1h", Samples: 20, Step: 3 * time.Minute}, true
	case "1d":
		return latencyWindow{Name: "1d", Samples: 48, Step: 30 * time.Minute}, true
	case "7d":
		return latencyWindow{Name: "7d", Samples: 56, Step: 3 * time.Hour}, true
	case "30d":
		return latencyWindow{Name: "30d", Samples: 60, Step: 12 * time.Hour}, true
	default:
		return latencyWindow{}, false
	}
}

func resolveKulinLatencyGridWindow(rangeName string) (latencyWindow, bool) {
	switch rangeName {
	case "1d":
		return latencyWindow{Name: "1d", Samples: 1440, Step: time.Minute}, true
	case "7d":
		return latencyWindow{Name: "7d", Samples: 336, Step: 30 * time.Minute}, true
	case "30d":
		return latencyWindow{Name: "30d", Samples: 360, Step: 2 * time.Hour}, true
	default:
		return latencyWindow{}, false
	}
}

func resolveStateWindow(rangeName string) (latencyWindow, bool) {
	switch rangeName {
	case "", "1h":
		return latencyWindow{Name: "1h", Samples: 30, Step: 2 * time.Minute}, true
	case "1d":
		return latencyWindow{Name: "1d", Samples: 2880, Step: 30 * time.Second}, true
	case "7d":
		return latencyWindow{Name: "7d", Samples: 336, Step: 30 * time.Minute}, true
	case "30d":
		return latencyWindow{Name: "30d", Samples: 360, Step: 2 * time.Hour}, true
	default:
		return latencyWindow{}, false
	}
}

func mockLatencyPoints(nodeID string, rangeNames ...string) []LatencyPoint {
	window, ok := resolveLatencyWindow("")
	if len(rangeNames) > 0 {
		if gridWindow, gridOK := resolveKulinLatencyGridWindow(rangeNames[0]); gridOK {
			window, ok = gridWindow, true
		} else {
			window, ok = resolveLatencyWindow(rangeNames[0])
		}
	}
	if !ok {
		window, _ = resolveLatencyWindow("")
	}
	end := time.Date(2026, 7, 2, 13, 30, 0, 0, time.UTC)
	targets := []struct {
		id       string
		name     string
		baseMS   float64
		jitterMS float64
		loss     float64
	}{
		{id: "cq-unicom", name: "重庆联通", baseMS: 34.8, jitterMS: 2.4, loss: 0.18},
		{id: "cq-mobile", name: "重庆移动", baseMS: 34.3, jitterMS: 2.1, loss: 0.19},
		{id: "cq-telecom", name: "重庆电信", baseMS: 43.5, jitterMS: 3.2, loss: 0.18},
		{id: "telegram-dc5", name: "DC5", baseMS: 31.8, jitterMS: 2.6, loss: 0.38},
		{id: "google", name: "Google", baseMS: 1.4, jitterMS: 0.4, loss: 0},
		{id: "telegram-dc2", name: "DC2", baseMS: 193.4, jitterMS: 9.2, loss: 0.12},
		{id: "telegram-dc1", name: "DC1", baseMS: 198.2, jitterMS: 10.5, loss: 1.71},
		{id: "akari-tw", name: "Akari TW", baseMS: 14.4, jitterMS: 1.4, loss: 0},
		{id: "akari-jp", name: "Akari JP", baseMS: 50.3, jitterMS: 4.1, loss: 1.02},
		{id: "akari-hk", name: "Akari HK", baseMS: 1.7, jitterMS: 0.5, loss: 1.22},
		{id: "hytron", name: "Hytron", baseMS: 1.8, jitterMS: 0.5, loss: 1.29},
		{id: "hostdzire", name: "HostDZire", baseMS: 152.2, jitterMS: 8.4, loss: 0.19},
		{id: "bage", name: "BAGE", baseMS: 144.8, jitterMS: 7.5, loss: 0.16},
	}

	points := make([]LatencyPoint, 0, window.Samples*len(targets))
	spikeIndex := window.Samples / 2
	partialLossIndex := window.Samples / 3
	fullLossIndex := window.Samples * 2 / 3
	for index := 0; index < window.Samples; index++ {
		ts := end.Add(-time.Duration(window.Samples-1-index) * window.Step).Format(time.RFC3339)
		for targetIndex, target := range targets {
			wave := float64((index+targetIndex)%6) / 5
			median := target.baseMS + wave*target.jitterMS
			if index == spikeIndex && (target.id == "cq-telecom" || target.id == "telegram-dc5") {
				median += 110
			}

			loss := target.loss
			var medianPtr *float64
			if target.id == "telegram-dc1" && index == fullLossIndex {
				loss = 100
			} else {
				if target.id == "telegram-dc1" && index == partialLossIndex {
					loss = 30
				}
				medianPtr = ptr(median)
			}

			points = append(points, LatencyPoint{TS: ts, TargetID: target.id, TargetName: target.name, MedianMS: medianPtr, AvgMS: medianPtr, LossPercent: loss})
		}
	}
	return points
}

func mockServiceTargets() []ServiceTarget {
	return []ServiceTarget{
		{ID: "cq-unicom", Name: "重庆联通", Type: "tcping", Address: "cq-unicom.example", Port: intValue(443), AssignedNodeCount: 11, ReportingNodeCount: 11, MedianMS: ptr(34.8), LossPercent: ptr(0.18), UpdatedAt: "2026-07-02T13:30:00Z"},
		{ID: "telegram-dc5", Name: "DC5", Type: "tcping", Address: "149.154.171.5", Port: intValue(443), AssignedNodeCount: 11, ReportingNodeCount: 10, MedianMS: ptr(31.8), LossPercent: ptr(0.38), UpdatedAt: "2026-07-02T13:30:00Z"},
		{ID: "google", Name: "Google", Type: "http_get", Address: "https://www.google.com/generate_204", AssignedNodeCount: 11, ReportingNodeCount: 11, MedianMS: ptr(1.4), LossPercent: ptr(0), UpdatedAt: "2026-07-02T13:30:00Z"},
	}
}

func mockServiceLatencyPoints(targetID string, rangeNames ...string) []ServiceLatencyPoint {
	window, ok := resolveLatencyWindow("")
	if len(rangeNames) > 0 {
		if gridWindow, gridOK := resolveKulinLatencyGridWindow(rangeNames[0]); gridOK {
			window, ok = gridWindow, true
		} else {
			window, ok = resolveLatencyWindow(rangeNames[0])
		}
	}
	if !ok {
		window, _ = resolveLatencyWindow("")
	}
	end := time.Date(2026, 7, 2, 13, 30, 0, 0, time.UTC)
	nodes := mockNodes()
	points := make([]ServiceLatencyPoint, 0, window.Samples*len(nodes))
	for index := 0; index < window.Samples; index++ {
		ts := end.Add(-time.Duration(window.Samples-1-index) * window.Step).Format(time.RFC3339)
		for nodeIndex, node := range nodes {
			base := 20 + float64(nodeIndex*8)
			if targetID == "telegram-dc5" {
				base += 15
			}
			median := base + float64((index+nodeIndex)%5)*1.7
			loss := float64((index+nodeIndex)%4) * 0.05
			points = append(points, ServiceLatencyPoint{TS: ts, NodeID: node.ID, NodeName: node.DisplayName, MedianMS: ptr(median), AvgMS: ptr(median), LossPercent: loss})
		}
	}
	return points
}

func intValue(value int) *int { return &value }

func mockStatePoints(window latencyWindow) []StatePoint {
	end := time.Date(2026, 7, 2, 13, 30, 0, 0, time.UTC)
	points := make([]StatePoint, 0, window.Samples)
	for index := 0; index < window.Samples; index++ {
		wave := float64(index%8) / 7
		points = append(points, StatePoint{
			TS:               end.Add(-time.Duration(window.Samples-1-index) * window.Step).Format(time.RFC3339),
			CPUPercent:       ptr(8 + wave*18),
			MemoryUsedBytes:  gb(2.1 + wave*0.4),
			MemoryTotalBytes: gb(3.83),
			DiskUsedBytes:    gb(18 + wave*2),
			DiskTotalBytes:   gb(97.87),
			NetInTotalBytes:  mb(1500 + float64(index)*5),
			NetOutTotalBytes: mb(900 + float64(index)*3),
			NetInSpeedBps:    kb(20 + wave*30),
			NetOutSpeedBps:   kb(10 + wave*20),
			UptimeSeconds:    ptr(86400 + float64(index)*float64(window.Step/time.Second)),
		})
	}
	return points
}
