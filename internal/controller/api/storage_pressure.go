package api

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultTelemetryMinFreeBytes = uint64(512 << 20)
	telemetryStorageCheckFor     = 2 * time.Second
)

var errTelemetryStoragePressure = errors.New("telemetry storage high-water reached")

type telemetryStorageGuard struct {
	directory    string
	minFreeBytes uint64
	mu           sync.Mutex
	checkedAt    time.Time
	blocked      bool
	freeBytes    func(string) (uint64, error)
}

func newTelemetryStorageGuard(databasePath string) *telemetryStorageGuard {
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		absolute = databasePath
	}
	minimum := defaultTelemetryMinFreeBytes
	if raw := strings.TrimSpace(os.Getenv("ZENO_DATABASE_MIN_FREE_BYTES")); raw != "" {
		if parsed, parseErr := strconv.ParseUint(raw, 10, 64); parseErr == nil {
			minimum = parsed
		}
	}
	return &telemetryStorageGuard{
		directory:    filepath.Dir(absolute),
		minFreeBytes: minimum,
		freeBytes:    filesystemFreeBytes,
	}
}

func filesystemFreeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

func (guard *telemetryStorageGuard) check(now time.Time) error {
	if guard == nil || guard.minFreeBytes == 0 || guard.freeBytes == nil {
		return nil
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if !guard.checkedAt.IsZero() && now.Sub(guard.checkedAt) < telemetryStorageCheckFor {
		if guard.blocked {
			return errTelemetryStoragePressure
		}
		return nil
	}
	free, err := guard.freeBytes(guard.directory)
	if err != nil {
		// A transient statfs failure must not make every Agent look offline. The
		// database write remains the final source of truth for filesystem errors.
		return nil
	}
	guard.checkedAt = now
	guard.blocked = free < guard.minFreeBytes
	if guard.blocked {
		return errTelemetryStoragePressure
	}
	return nil
}

func (s *SQLiteStore) ensureTelemetryStorage() error {
	if s == nil {
		return errTelemetryStoragePressure
	}
	return s.telemetryStorage.check(time.Now().UTC())
}
