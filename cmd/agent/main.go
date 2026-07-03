package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"github.com/shuijiao1/jiaoprobe/internal/agent"
)

const defaultVersion = "zeno-agent-dev"

type config struct {
	ControllerURL string
	NodeID        string
	Token         string
	TokenFile     string
	Interval      time.Duration
	Once          bool
	Version       string
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.ControllerURL, "controller-url", "http://127.0.0.1:18980", "Zeno controller base URL")
	flag.StringVar(&cfg.NodeID, "node-id", "hytron", "agent node id")
	flag.StringVar(&cfg.Token, "token", "", "agent bearer token; prefer -token-file")
	flag.StringVar(&cfg.TokenFile, "token-file", "", "file containing the agent bearer token")
	flag.DurationVar(&cfg.Interval, "interval", time.Minute, "host/state/probe report interval")
	flag.BoolVar(&cfg.Once, "once", false, "collect and report once, then exit")
	flag.StringVar(&cfg.Version, "version", defaultVersion, "agent version string reported to controller")
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
	client := agent.NewClient(cfg.ControllerURL, cfg.NodeID, cfg.Token)
	collector := agent.NewMetricsCollector()
	scheduler := agent.NewProbeScheduler()
	if err := reportOnce(ctx, client, collector, cfg.Version, true, scheduler); err != nil {
		return err
	}
	if cfg.Once {
		return nil
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := reportOnce(ctx, client, collector, cfg.Version, true, scheduler); err != nil {
				log.Printf("report failed: %v", err)
			}
		}
	}
}

func reportOnce(ctx context.Context, client *agent.Client, collector *agent.MetricsCollector, version string, includeHost bool, scheduler *agent.ProbeScheduler) error {
	now := time.Now().UTC()
	if err := client.PostHeartbeat(ctx, "online", version, now); err != nil {
		return err
	}
	if includeHost {
		if err := client.PostHost(ctx, collector.CollectHost(version)); err != nil {
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
