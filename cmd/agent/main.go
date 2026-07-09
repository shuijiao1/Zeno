package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/shuijiao1/zeno/internal/agent"
)

const defaultVersion = "zeno-agent-dev"
const defaultStateInterval = 3 * time.Second
const defaultHeartbeatInterval = 15 * time.Second
const defaultHostInterval = 30 * time.Minute
const defaultIdentityRefreshInterval = 12 * time.Hour

const defaultReportInterval = defaultStateInterval
const defaultFullReportInterval = defaultHeartbeatInterval

type config struct {
	ControllerURL           string
	NodeID                  string
	Token                   string
	TokenFile               string
	StateInterval           time.Duration
	HeartbeatInterval       time.Duration
	HostInterval            time.Duration
	Once                    bool
	Version                 string
	IdentityRefreshInterval time.Duration
	AllowInsecureHTTP       bool
	NetworkInterfaces       string
	DiskMounts              string
}

func main() {
	cfg := config{}
	legacyInterval := time.Duration(0)
	flag.StringVar(&cfg.ControllerURL, "controller-url", "http://127.0.0.1:18980", "Zeno controller base URL")
	flag.StringVar(&cfg.NodeID, "node-id", "hytron", "agent node id")
	flag.StringVar(&cfg.Token, "token", "", "agent bearer token; prefer -token-file")
	flag.StringVar(&cfg.TokenFile, "token-file", "", "file containing the agent bearer token")
	flag.DurationVar(&legacyInterval, "interval", 0, "deprecated alias for -state-interval")
	flag.DurationVar(&cfg.StateInterval, "state-interval", defaultStateInterval, "state metrics report interval")
	flag.DurationVar(&cfg.HeartbeatInterval, "heartbeat-interval", defaultHeartbeatInterval, "heartbeat report interval")
	flag.DurationVar(&cfg.HostInterval, "host-interval", defaultHostInterval, "static host information report interval")
	flag.BoolVar(&cfg.Once, "once", false, "collect and report once, then exit")
	flag.StringVar(&cfg.Version, "version", defaultVersion, "agent version string reported to controller")
	flag.DurationVar(&cfg.IdentityRefreshInterval, "identity-refresh-interval", defaultIdentityRefreshInterval, "public IPv4/IPv6 and GeoIP refresh interval; best-effort and cached")
	flag.BoolVar(&cfg.AllowInsecureHTTP, "allow-insecure-http", false, "allow bearer-token reporting to a non-local plain HTTP controller URL")
	flag.StringVar(&cfg.NetworkInterfaces, "network-interfaces", "", "comma-separated network interface allowlist; default excludes virtual/container interfaces")
	flag.StringVar(&cfg.DiskMounts, "disk-mounts", "", "comma-separated disk mount/path allowlist; default sums real filesystems")
	flag.Parse()
	if legacyInterval > 0 {
		cfg.StateInterval = legacyInterval
	}

	token, err := readToken(cfg.Token, cfg.TokenFile)
	if err != nil {
		log.Fatal(err)
	}
	cfg.Token = token
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

func normalizeConfigIntervals(cfg *config) {
	if cfg.StateInterval <= 0 {
		cfg.StateInterval = defaultStateInterval
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = defaultHeartbeatInterval
	}
	if cfg.HostInterval <= 0 {
		cfg.HostInterval = defaultHostInterval
	}
	if cfg.IdentityRefreshInterval <= 0 {
		cfg.IdentityRefreshInterval = defaultIdentityRefreshInterval
	}
}

func run(ctx context.Context, cfg config) error {
	if err := validateControllerURL(cfg.ControllerURL, cfg.AllowInsecureHTTP); err != nil {
		return err
	}
	normalizeConfigIntervals(&cfg)
	client := agent.NewClient(cfg.ControllerURL, cfg.NodeID, cfg.Token)
	collector := agent.NewMetricsCollector(agent.MetricsOptions{NetworkInterfaceAllowlist: splitCommaList(cfg.NetworkInterfaces), DiskMountAllowlist: splitCommaList(cfg.DiskMounts)})
	probeManager := newProbeTargetManager()
	identityDiscoverer := agent.NewCachedNetworkIdentityDiscoverer(agent.NewNetworkIdentityDiscoverer(), cfg.IdentityRefreshInterval)

	refreshProbeConfig := func(ctx context.Context, requestedVersion int64) (int64, error) {
		return refreshProbeTargets(ctx, client, probeManager, requestedVersion)
	}

	if _, err := refreshProbeConfig(ctx, 0); err != nil {
		log.Printf("initial probe config fetch failed: %v", err)
	}
	if err := reportHeartbeat(ctx, client, cfg.Version); err != nil {
		log.Printf("initial heartbeat failed: %v", err)
	}
	if err := reportHost(ctx, client, collector, cfg.Version, identityDiscoverer); err != nil {
		log.Printf("initial host report failed: %v", err)
	}
	if err := reportState(ctx, client, collector); err != nil {
		log.Printf("initial state report failed: %v", err)
	}
	if err := runDueProbes(ctx, client, probeManager); err != nil {
		log.Printf("initial probe report failed: %v", err)
	}
	if cfg.Once {
		return nil
	}

	go client.RunPresence(ctx, refreshProbeConfig)
	go runPeriodic(ctx, cfg.StateInterval, func(ctx context.Context) error { return reportState(ctx, client, collector) }, "state report")
	go runPeriodic(ctx, cfg.HeartbeatInterval, func(ctx context.Context) error { return reportHeartbeat(ctx, client, cfg.Version) }, "heartbeat report")
	go runPeriodic(ctx, cfg.HostInterval, func(ctx context.Context) error {
		return reportHost(ctx, client, collector, cfg.Version, identityDiscoverer)
	}, "host report")
	go runPeriodicWithTimeout(ctx, time.Second, 30*time.Second, func(ctx context.Context) error { return runDueProbes(ctx, client, probeManager) }, "probe report")

	<-ctx.Done()
	return ctx.Err()
}

func runPeriodic(ctx context.Context, interval time.Duration, fn func(context.Context) error, label string) {
	runPeriodicWithTimeout(ctx, interval, perCallTimeout(interval), fn, label)
}

func runPeriodicWithTimeout(ctx context.Context, interval time.Duration, timeout time.Duration, fn func(context.Context) error, label string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			callCtx, cancel := context.WithTimeout(ctx, timeout)
			if err := fn(callCtx); err != nil {
				log.Printf("%s failed: %v", label, err)
			}
			cancel()
		}
	}
}

func perCallTimeout(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 20 * time.Second
	}
	if interval < 10*time.Second {
		return 10 * time.Second
	}
	if interval < 30*time.Second {
		return interval
	}
	return 30 * time.Second
}

func validateControllerURL(rawURL string, allowInsecure bool) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("controller URL %q must be absolute", rawURL)
	}
	if parsed.Scheme != "http" {
		return nil
	}
	host := parsed.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	if allowInsecure {
		log.Printf("warning: reporting bearer token over insecure HTTP to %s", parsed.Host)
		return nil
	}
	return fmt.Errorf("controller URL %q uses insecure HTTP; use HTTPS or pass -allow-insecure-http for trusted private testing", rawURL)
}

type networkIdentityDiscoverer interface {
	Discover(context.Context) agent.NetworkIdentity
}

func reportHeartbeat(ctx context.Context, client *agent.Client, version string) error {
	now := time.Now().UTC()
	return client.PostHeartbeat(ctx, "online", version, now)
}

func reportHost(ctx context.Context, client *agent.Client, collector *agent.MetricsCollector, version string, identityDiscoverer networkIdentityDiscoverer) error {
	host := collector.CollectHost(version)
	if identityDiscoverer != nil {
		identity := identityDiscoverer.Discover(ctx)
		host.PublicIPv4 = identity.PublicIPv4
		host.PublicIPv6 = identity.PublicIPv6
		host.CountryCode = identity.CountryCode
	}
	return client.PostHost(ctx, host)
}

func reportState(ctx context.Context, client *agent.Client, collector *agent.MetricsCollector) error {
	now := time.Now().UTC()
	return client.PostState(ctx, collector.CollectState(now))
}

func reportOnce(ctx context.Context, client *agent.Client, collector *agent.MetricsCollector, version string, includeHost bool, scheduler *agent.ProbeScheduler, identityDiscoverer networkIdentityDiscoverer) error {
	if err := reportHeartbeat(ctx, client, version); err != nil {
		return err
	}
	if includeHost {
		if err := reportHost(ctx, client, collector, version, identityDiscoverer); err != nil {
			return err
		}
	}
	if err := reportState(ctx, client, collector); err != nil {
		return err
	}
	targets, err := client.FetchProbeTargets(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	dueTargets := targets
	if scheduler != nil {
		dueTargets = scheduler.Due(targets, now)
	}
	if len(dueTargets) > 0 {
		rounds := agent.ProbeTargets(ctx, dueTargets, now)
		if err := client.PostProbeResults(ctx, rounds); err != nil {
			return err
		}
		if scheduler != nil {
			scheduler.MarkCompleted(dueTargets, now)
		}
	}
	log.Printf("reported host/state and %d probe target(s)", len(dueTargets))
	return nil
}

func reportStateOnly(ctx context.Context, client *agent.Client, collector *agent.MetricsCollector) error {
	return reportState(ctx, client, collector)
}

type probeTargetManager struct {
	mu        sync.Mutex
	targets   []agent.ProbeTarget
	version   int64
	scheduler *agent.ProbeScheduler
}

func newProbeTargetManager() *probeTargetManager {
	return &probeTargetManager{scheduler: agent.NewProbeScheduler()}
}

func (m *probeTargetManager) update(targets []agent.ProbeTarget, version int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets = append(m.targets[:0], targets...)
	if version != 0 && version != m.version {
		m.scheduler = agent.NewProbeScheduler()
		m.version = version
	}
}

func (m *probeTargetManager) due(now time.Time) []agent.ProbeTarget {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.targets) == 0 {
		return nil
	}
	due := m.scheduler.Due(m.targets, now)
	return append([]agent.ProbeTarget(nil), due...)
}

func (m *probeTargetManager) markCompleted(targets []agent.ProbeTarget, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduler.MarkCompleted(targets, now)
}

func refreshProbeTargets(ctx context.Context, client *agent.Client, manager *probeTargetManager, _ int64) (int64, error) {
	response, err := client.FetchProbeConfig(ctx)
	if err != nil {
		return 0, err
	}
	manager.update(response.Targets, response.Version)
	return response.Version, nil
}

func runDueProbes(ctx context.Context, client *agent.Client, manager *probeTargetManager) error {
	now := time.Now().UTC()
	dueTargets := manager.due(now)
	if len(dueTargets) == 0 {
		return nil
	}
	rounds := agent.ProbeTargets(ctx, dueTargets, now)
	if err := client.PostProbeResults(ctx, rounds); err != nil {
		return err
	}
	manager.markCompleted(dueTargets, now)
	return nil
}

func splitCommaList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func readToken(token, tokenFile string) (string, error) {
	if tokenFile != "" {
		content, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(content)), nil
	}
	return strings.TrimSpace(token), nil
}
