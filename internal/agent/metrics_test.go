package agent

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseMemoryStatsIncludesSwapTotals(t *testing.T) {
	stats := parseMemoryStats(`MemTotal:        2097152 kB
MemAvailable:    1048576 kB
SwapTotal:       524288 kB
SwapFree:        393216 kB
`)

	if stats.memTotal != 2*1024*1024*1024 || stats.memAvailable != 1024*1024*1024 {
		t.Fatalf("memory stats = %+v, want mem total/available bytes", stats)
	}
	if stats.swapTotal != 512*1024*1024 || stats.swapFree != 384*1024*1024 {
		t.Fatalf("swap stats = %+v, want swap total/free bytes", stats)
	}
}

func TestTCPConnectionCountFromFileSkipsHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tcp")
	content := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1F91 0100007F:0050 01 00000000:00000000 00:00000000 00000000     0        0 2 1 0000000000000000 100 0 0 10 0
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write tcp fixture: %v", err)
	}

	if got := tcpConnectionCountFromFile(path); got != 2 {
		t.Fatalf("tcp connection count = %d, want 2 data rows", got)
	}
}

func TestIncludeNetworkInterfaceFiltersVirtualDefaults(t *testing.T) {
	for _, name := range []string{"lo", "docker0", "vethabc", "br-123", "tun0", "tailscale0", "kube-ipvs0", "vmbr0", "tap1", "cni0", "flannel.1", "cali123", "virbr0"} {
		if includeNetworkInterface(name, nil) {
			t.Fatalf("interface %s included by default, want excluded", name)
		}
	}
	for _, name := range []string{"eth0", "ens3", "enp1s0", "wlan0"} {
		if !includeNetworkInterface(name, nil) {
			t.Fatalf("interface %s excluded by default, want included", name)
		}
	}
}

func TestNetworkInterfaceAllowlistOverridesDefaultFilter(t *testing.T) {
	allowlist := allowlistSet([]string{"tailscale0", "eth0"})
	if !includeNetworkInterface("tailscale0", allowlist) {
		t.Fatal("allowlisted tailscale0 should be included")
	}
	if includeNetworkInterface("ens3", allowlist) {
		t.Fatal("non-allowlisted ens3 should be excluded when allowlist is set")
	}
}

func TestParseMountInfoFiltersPseudoAndContainerMounts(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "mountinfo")
	content := `26 23 8:1 / / rw,relatime - ext4 /dev/vda1 rw
27 26 0:24 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
28 26 0:25 / /run rw,nosuid,nodev - tmpfs tmpfs rw
29 26 0:26 / /var/lib/docker/overlay2/abc/merged rw,relatime - overlay overlay rw
30 26 8:2 / /data rw,relatime - xfs /dev/vdb1 rw
31 26 8:2 / /mnt/data-bind rw,relatime - xfs /dev/vdb1 rw
`
	if err := os.WriteFile(fixture, []byte(content), 0o600); err != nil {
		t.Fatalf("write mountinfo fixture: %v", err)
	}
	file, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open mountinfo fixture: %v", err)
	}
	defer file.Close()

	var included []string
	scanner := bufio.NewScanner(file)
	seen := map[string]struct{}{}
	for scanner.Scan() {
		mount, ok := parseMountInfoLine(scanner.Text())
		if !ok || !includeDiskMount(mount.mountPoint, mount.fsType, mount.source) {
			continue
		}
		if _, exists := seen[mount.device]; exists {
			continue
		}
		seen[mount.device] = struct{}{}
		included = append(included, mount.mountPoint)
	}
	if strings.Join(included, ",") != "/,/data" {
		t.Fatalf("included mounts = %v, want root and data only once", included)
	}
}

func TestCollectStateIncludesExtraMetrics(t *testing.T) {
	collector := NewMetricsCollector()
	sample := collector.CollectState(time.Now())

	if sample.TS <= 0 {
		t.Fatalf("state timestamp = %d, want unix timestamp", sample.TS)
	}
	if sample.Load1 < 0 || sample.Load5 < 0 || sample.Load15 < 0 {
		t.Fatalf("load averages should be non-negative: %+v", sample)
	}
	if sample.SwapUsedBytes < 0 || sample.SwapTotalBytes < 0 || sample.ProcessCount < 0 || sample.TCPConnectionCount < 0 {
		t.Fatalf("extra state metrics should be non-negative: %+v", sample)
	}
}
