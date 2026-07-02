package agent

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type MetricsCollector struct {
	previousCPU   cpuTimes
	hasCPU        bool
	previousNet   networkTotals
	previousNetAt time.Time
	hasNet        bool
}

func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

func (c *MetricsCollector) CollectHost(version string) HostInfo {
	memTotal, _ := readMemoryTotals()
	_, diskTotal := diskUsage("/")
	hostname, _ := os.Hostname()
	osName, osVersion := osRelease()
	return HostInfo{
		Hostname:         hostname,
		OSName:           osName,
		OSVersion:        osVersion,
		Kernel:           kernelRelease(),
		Arch:             normalizedArch(runtime.GOARCH),
		Virtualization:   virtualizationName(),
		CPUModel:         cpuModel(),
		CPUCores:         runtime.NumCPU(),
		MemoryTotalBytes: memTotal,
		DiskTotalBytes:   diskTotal,
		BootTime:         bootTime(),
		AgentVersion:     version,
	}
}

func (c *MetricsCollector) CollectState(now time.Time) StateSample {
	cpu := c.cpuPercent()
	memTotal, memAvailable := readMemoryTotals()
	diskUsed, diskTotal := diskUsage("/")
	netTotals := readNetworkTotals()
	var inSpeed, outSpeed float64
	if c.hasNet {
		elapsed := now.Sub(c.previousNetAt).Seconds()
		if elapsed > 0 {
			inSpeed = float64(nonNegativeInt64(netTotals.InBytes-c.previousNet.InBytes)) / elapsed
			outSpeed = float64(nonNegativeInt64(netTotals.OutBytes-c.previousNet.OutBytes)) / elapsed
		}
	}
	c.previousNet = netTotals
	c.previousNetAt = now
	c.hasNet = true

	return StateSample{
		TS:               now.UTC().Unix(),
		CPUPercent:       cpu,
		MemoryUsedBytes:  nonNegativeInt64(memTotal - memAvailable),
		MemoryTotalBytes: memTotal,
		DiskUsedBytes:    diskUsed,
		DiskTotalBytes:   diskTotal,
		NetInTotalBytes:  netTotals.InBytes,
		NetOutTotalBytes: netTotals.OutBytes,
		NetInSpeedBps:    inSpeed,
		NetOutSpeedBps:   outSpeed,
		UptimeSeconds:    uptimeSeconds(),
	}
}

func (c *MetricsCollector) cpuPercent() float64 {
	current, ok := readCPUTimes()
	if !ok {
		return 0
	}
	defer func() {
		c.previousCPU = current
		c.hasCPU = true
	}()
	if !c.hasCPU {
		return 0
	}
	totalDelta := current.Total - c.previousCPU.Total
	idleDelta := current.Idle - c.previousCPU.Idle
	if totalDelta <= 0 {
		return 0
	}
	value := (1 - float64(idleDelta)/float64(totalDelta)) * 100
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

type cpuTimes struct {
	Total uint64
	Idle  uint64
}

func readCPUTimes() (cpuTimes, bool) {
	content, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	if !scanner.Scan() {
		return cpuTimes{}, false
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuTimes{}, false
	}
	var total uint64
	var idle uint64
	for index, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuTimes{}, false
		}
		total += value
		if index == 3 || index == 4 {
			idle += value
		}
	}
	return cpuTimes{Total: total, Idle: idle}, true
}

func readMemoryTotals() (total int64, available int64) {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		valueKB, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = valueKB * 1024
		case "MemAvailable":
			available = valueKB * 1024
		}
	}
	return total, available
}

func diskUsage(path string) (used int64, total int64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0
	}
	total = int64(stat.Blocks) * int64(stat.Bsize)
	free := int64(stat.Bfree) * int64(stat.Bsize)
	used = nonNegativeInt64(total - free)
	return used, total
}

type networkTotals struct {
	InBytes  int64
	OutBytes int64
}

func readNetworkTotals() networkTotals {
	content, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return networkTotals{}
	}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	var totals networkTotals
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, ":") {
			continue
		}
		iface, rest, _ := strings.Cut(line, ":")
		iface = strings.TrimSpace(iface)
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 16 {
			continue
		}
		inBytes, _ := strconv.ParseInt(fields[0], 10, 64)
		outBytes, _ := strconv.ParseInt(fields[8], 10, 64)
		totals.InBytes += inBytes
		totals.OutBytes += outBytes
	}
	return totals
}

func osRelease() (string, string) {
	content, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "linux", ""
	}
	values := parseKeyValueLines(string(content))
	id := values["ID"]
	if id == "" {
		id = "linux"
	}
	return id, values["VERSION_ID"]
}

func kernelRelease() string {
	content, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

func virtualizationName() string {
	for _, path := range []string{"/sys/class/dmi/id/product_name", "/sys/class/dmi/id/sys_vendor"} {
		content, err := os.ReadFile(path)
		if err == nil {
			value := strings.TrimSpace(string(content))
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func cpuModel() string {
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "model name" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func bootTime() int64 {
	content, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "btime" {
			value, _ := strconv.ParseInt(fields[1], 10, 64)
			return value
		}
	}
	return 0
}

func uptimeSeconds() int64 {
	content, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return 0
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return int64(value)
}

func normalizedArch(arch string) string {
	switch arch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return arch
	}
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
