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
	previousCPU      cpuTimes
	hasCPU           bool
	previousNet      networkTotals
	previousNetAt    time.Time
	hasNet           bool
	networkAllowlist map[string]struct{}
	diskAllowlist    []string
}

type MetricsOptions struct {
	NetworkInterfaceAllowlist []string
	DiskMountAllowlist        []string
}

func NewMetricsCollector(options ...MetricsOptions) *MetricsCollector {
	var opts MetricsOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return &MetricsCollector{
		networkAllowlist: allowlistSet(opts.NetworkInterfaceAllowlist),
		diskAllowlist:    normalizeAllowlist(opts.DiskMountAllowlist),
	}
}

func (c *MetricsCollector) CollectHost(version string) HostInfo {
	memTotal, _ := readMemoryTotals()
	_, diskTotal := diskUsage(c.diskAllowlist)
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
	swapTotal, swapFree := readSwapTotals()
	load1, load5, load15 := readLoadAverages()
	diskUsed, diskTotal := diskUsage(c.diskAllowlist)
	netTotals := readNetworkTotals(c.networkAllowlist)
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
		TS:                 now.UTC().Unix(),
		CPUPercent:         cpu,
		Load1:              load1,
		Load5:              load5,
		Load15:             load15,
		MemoryUsedBytes:    nonNegativeInt64(memTotal - memAvailable),
		MemoryTotalBytes:   memTotal,
		SwapUsedBytes:      nonNegativeInt64(swapTotal - swapFree),
		SwapTotalBytes:     swapTotal,
		DiskUsedBytes:      diskUsed,
		DiskTotalBytes:     diskTotal,
		NetInTotalBytes:    netTotals.InBytes,
		NetOutTotalBytes:   netTotals.OutBytes,
		NetInSpeedBps:      inSpeed,
		NetOutSpeedBps:     outSpeed,
		ProcessCount:       processCount(),
		TCPConnectionCount: tcpConnectionCount(),
		UDPConnectionCount: udpConnectionCount(),
		UptimeSeconds:      uptimeSeconds(),
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
	stats := parseMemoryStats(string(content))
	return stats.memTotal, stats.memAvailable
}

type memoryStats struct {
	memTotal     int64
	memAvailable int64
	swapTotal    int64
	swapFree     int64
}

func readSwapTotals() (total int64, free int64) {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	stats := parseMemoryStats(string(content))
	return stats.swapTotal, stats.swapFree
}

func parseMemoryStats(content string) memoryStats {
	stats := memoryStats{}
	scanner := bufio.NewScanner(strings.NewReader(content))
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
			stats.memTotal = valueKB * 1024
		case "MemAvailable":
			stats.memAvailable = valueKB * 1024
		case "SwapTotal":
			stats.swapTotal = valueKB * 1024
		case "SwapFree":
			stats.swapFree = valueKB * 1024
		}
	}
	return stats
}

func readLoadAverages() (load1, load5, load15 float64) {
	content, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(content))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	load1, _ = strconv.ParseFloat(fields[0], 64)
	load5, _ = strconv.ParseFloat(fields[1], 64)
	load15, _ = strconv.ParseFloat(fields[2], 64)
	return load1, load5, load15
}

func processCount() int64 {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var count int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "" {
			continue
		}
		allDigits := true
		for _, r := range name {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			count++
		}
	}
	return count
}

func tcpConnectionCount() int64 {
	return tcpConnectionCountFromFile("/proc/net/tcp") + tcpConnectionCountFromFile("/proc/net/tcp6")
}

func udpConnectionCount() int64 {
	return tcpConnectionCountFromFile("/proc/net/udp") + tcpConnectionCountFromFile("/proc/net/udp6")
}

func tcpConnectionCountFromFile(path string) int64 {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) <= 1 {
		return 0
	}
	return int64(len(lines) - 1)
}

var defaultExcludedDiskFSTypes = map[string]struct{}{
	"autofs":      {},
	"binfmt_misc": {},
	"bpf":         {},
	"cgroup":      {},
	"cgroup2":     {},
	"configfs":    {},
	"debugfs":     {},
	"devpts":      {},
	"devtmpfs":    {},
	"fusectl":     {},
	"hugetlbfs":   {},
	"mqueue":      {},
	"nsfs":        {},
	"overlay":     {},
	"proc":        {},
	"pstore":      {},
	"ramfs":       {},
	"rpc_pipefs":  {},
	"securityfs":  {},
	"squashfs":    {},
	"sysfs":       {},
	"tmpfs":       {},
	"tracefs":     {},
}

var defaultExcludedDiskMountPrefixes = []string{
	"/dev",
	"/proc",
	"/run",
	"/sys",
	"/var/lib/docker",
	"/var/lib/containerd",
	"/var/lib/kubelet",
	"/var/lib/containers/storage",
	"/snap",
}

func diskUsage(allowlist []string) (used int64, total int64) {
	if len(allowlist) > 0 {
		return diskUsageForAllowlist(allowlist)
	}
	return diskUsageFromMountInfo("/proc/self/mountinfo")
}

func diskUsageForAllowlist(paths []string) (used int64, total int64) {
	seen := map[uint64]struct{}{}
	for _, path := range normalizeAllowlist(paths) {
		key, ok := statDeviceKey(path)
		if ok {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		pathUsed, pathTotal := diskUsageForPath(path)
		used += pathUsed
		total += pathTotal
	}
	return used, total
}

func diskUsageFromMountInfo(path string) (used int64, total int64) {
	file, err := os.Open(path)
	if err != nil {
		return diskUsageForPath("/")
	}
	defer file.Close()

	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		mount, ok := parseMountInfoLine(scanner.Text())
		if !ok || !includeDiskMount(mount.mountPoint, mount.fsType, mount.source) {
			continue
		}
		if _, exists := seen[mount.device]; exists {
			continue
		}
		seen[mount.device] = struct{}{}
		mountUsed, mountTotal := diskUsageForPath(mount.mountPoint)
		if mountTotal <= 0 {
			continue
		}
		used += mountUsed
		total += mountTotal
	}
	if total == 0 {
		return diskUsageForPath("/")
	}
	return used, total
}

type mountInfoEntry struct {
	device     string
	mountPoint string
	fsType     string
	source     string
}

func parseMountInfoLine(line string) (mountInfoEntry, bool) {
	left, right, ok := strings.Cut(line, " - ")
	if !ok {
		return mountInfoEntry{}, false
	}
	leftFields := strings.Fields(left)
	rightFields := strings.Fields(right)
	if len(leftFields) < 5 || len(rightFields) < 2 {
		return mountInfoEntry{}, false
	}
	return mountInfoEntry{
		device:     leftFields[2],
		mountPoint: decodeMountInfoField(leftFields[4]),
		fsType:     strings.ToLower(rightFields[0]),
		source:     decodeMountInfoField(rightFields[1]),
	}, true
}

func decodeMountInfoField(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func includeDiskMount(mountPoint, fsType, source string) bool {
	mountPoint = strings.TrimSpace(mountPoint)
	if mountPoint == "" {
		return false
	}
	if _, excluded := defaultExcludedDiskFSTypes[strings.ToLower(fsType)]; excluded {
		return false
	}
	lowerMount := strings.ToLower(mountPoint)
	for _, prefix := range defaultExcludedDiskMountPrefixes {
		if lowerMount == prefix || strings.HasPrefix(lowerMount, prefix+"/") {
			return false
		}
	}
	lowerSource := strings.ToLower(strings.TrimSpace(source))
	if lowerSource == "" || lowerSource == "none" || strings.HasPrefix(lowerSource, "overlay") {
		return false
	}
	return true
}

func diskUsageForPath(path string) (used int64, total int64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0
	}
	total = int64(stat.Blocks) * int64(stat.Bsize)
	free := int64(stat.Bfree) * int64(stat.Bsize)
	used = nonNegativeInt64(total - free)
	return used, total
}

func statDeviceKey(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(stat.Dev), true
}

type networkTotals struct {
	InBytes  int64
	OutBytes int64
}

var defaultExcludedInterfacePrefixes = []string{
	"lo",
	"docker",
	"veth",
	"br-",
	"tun",
	"tailscale",
	"kube",
	"vmbr",
	"tap",
	"cni",
	"flannel",
	"cali",
	"weave",
	"virbr",
	"vnet",
	"vethernet",
	"virtualbox",
	"vmware",
	"hyper-v",
	"loopback",
	"isatap",
	"teredo",
	"npcap",
	"bluetooth",
	"zt",
}

func readNetworkTotals(allowlist map[string]struct{}) networkTotals {
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
		if !includeNetworkInterface(iface, allowlist) {
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

func normalizeAllowlist(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func allowlistSet(values []string) map[string]struct{} {
	list := normalizeAllowlist(values)
	if len(list) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(list))
	for _, value := range list {
		set[value] = struct{}{}
	}
	return set
}

func includeNetworkInterface(name string, allowlist map[string]struct{}) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	if len(allowlist) > 0 {
		_, ok := allowlist[trimmed]
		return ok
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range defaultExcludedInterfacePrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
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
