package agent

import (
	"os"
	"path/filepath"
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
