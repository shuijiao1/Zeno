package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/shuijiao1/zeno/internal/agent"
)

const defaultVersion = "zeno-agent-dev"
const defaultReportInterval = 2 * time.Second
const defaultFullReportInterval = 15 * time.Second

type config struct {
	ControllerURL           string
	NodeID                  string
	Token                   string
	TokenFile               string
	Interval                time.Duration
	Once                    bool
	Version                 string
	IdentityRefreshInterval time.Duration
	AllowInsecureHTTP       bool
	NetworkInterfaces       string
	DiskMounts              string
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.ControllerURL, "controller-url", "http://127.0.0.1:18980", "Zeno controller base URL")
	flag.StringVar(&cfg.NodeID, "node-id", "hytron", "agent node id")
	flag.StringVar(&cfg.Token, "token", "", "agent bearer token; prefer -token-file")
	flag.StringVar(&cfg.TokenFile, "token-file", "", "file containing the agent bearer token")
	flag.DurationVar(&cfg.Interval, "interval", defaultReportInterval, "state refresh interval; host heartbeat and probe target refresh run every 15s")
	flag.BoolVar(&cfg.Once, "once", false, "collect and report once, then exit")
	flag.StringVar(&cfg.Version, "version", defaultVersion, "agent version string reported to controller")
	flag.DurationVar(&cfg.IdentityRefreshInterval, "identity-refresh-interval", 6*time.Hour, "public IPv4/IPv6 and GeoIP refresh interval; best-effort and cached")
	flag.BoolVar(&cfg.AllowInsecureHTTP, "allow-insecure-http", false, "allow bearer-token reporting to a non-local plain HTTP controller URL")
	flag.StringVar(&cfg.NetworkInterfaces, "network-interfaces", "", "comma-separated network interface allowlist; default excludes virtual/container interfaces")
	flag.StringVar(&cfg.DiskMounts, "disk-mounts", "", "comma-separated disk mount/path allowlist; default sums real filesystems")
	flag.Parse()

	token, err := readToken(cfg.Token, cfg.TokenFile)
	if err != nil {
		log.Fatal(err)
	}
	cfg.Token = token
	if err := run(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cfg config) error {
	if err := validateControllerURL(cfg.ControllerURL, cfg.AllowInsecureHTTP); err != nil {
		return err
	}
	client := agent.NewClient(cfg.ControllerURL, cfg.NodeID, cfg.Token)
	collector := agent.NewMetricsCollector(agent.MetricsOptions{NetworkInterfaceAllowlist: splitCommaList(cfg.NetworkInterfaces), DiskMountAllowlist: splitCommaList(cfg.DiskMounts)})
	scheduler := agent.NewProbeScheduler()
	identityDiscoverer := agent.NewCachedNetworkIdentityDiscoverer(agent.NewNetworkIdentityDiscoverer(), cfg.IdentityRefreshInterval)
	if err := reportOnce(ctx, client, collector, cfg.Version, true, scheduler, identityDiscoverer); err != nil {
		return err
	}
	if cfg.Once {
		return nil
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultReportInterval
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	lastFullReport := time.Now().UTC()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case tick := <-ticker.C:
			if !tick.UTC().Before(lastFullReport.Add(defaultFullReportInterval)) {
				if err := reportOnce(ctx, client, collector, cfg.Version, true, scheduler, identityDiscoverer); err != nil {
					log.Printf("report failed: %v", err)
				} else {
					lastFullReport = tick.UTC()
				}
				continue
			}
			if err := reportStateOnly(ctx, client, collector); err != nil {
				log.Printf("report failed: %v", err)
			}
		}
	}
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

func reportOnce(ctx context.Context, client *agent.Client, collector *agent.MetricsCollector, version string, includeHost bool, scheduler *agent.ProbeScheduler, identityDiscoverer networkIdentityDiscoverer) error {
	now := time.Now().UTC()
	if err := client.PostHeartbeat(ctx, "online", version, now); err != nil {
		return err
	}
	if includeHost {
		host := collector.CollectHost(version)
		if identityDiscoverer != nil {
			identity := identityDiscoverer.Discover(ctx)
			host.PublicIPv4 = identity.PublicIPv4
			host.PublicIPv6 = identity.PublicIPv6
			host.CountryCode = identity.CountryCode
		}
		if err := client.PostHost(ctx, host); err != nil {
			return err
		}
	}
	if err := client.PostState(ctx, collector.CollectState(now)); err != nil {
		return err
	}
	targets, err := client.FetchProbeTargets(ctx)
	if err != nil {
		return err
	}
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
	now := time.Now().UTC()
	return client.PostState(ctx, collector.CollectState(now))
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
