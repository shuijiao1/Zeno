package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shuijiao1/zeno/internal/controller/api"
)

type handlerConfig struct {
	DBPath          string
	WebDir          string
	SeedPreview     bool
	NodeID          string
	AgentToken      string
	AdminToken      string
	AgentBinaryPath string
	AgentVersion    string
}

type controllerRuntime struct {
	Handler http.Handler
	Store   *api.SQLiteStore
	Cleanup func(context.Context) error
}

func buildController(config handlerConfig) (*controllerRuntime, error) {
	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	cleanupHandlers := []func(context.Context) error{func(context.Context) error {
		backgroundCancel()
		return nil
	}}
	options := api.HandlerOptions{StaticDir: config.WebDir, AgentBinaryPath: config.AgentBinaryPath, AgentVersion: config.AgentVersion, BackgroundContext: backgroundCtx}
	if strings.TrimSpace(config.AdminToken) != "" {
		options.AdminTokenHash = api.HashAdminToken(config.AdminToken)
	}
	var store *api.SQLiteStore
	if config.DBPath != "" {
		opened, err := api.OpenSQLiteStore(config.DBPath)
		if err != nil {
			backgroundCancel()
			return nil, err
		}
		store = opened
		options.Store = store
		options.StaleOfflineScanInterval = 5 * time.Second
		cleanupHandlers = append(cleanupHandlers, func(context.Context) error { return store.Close() })
		if config.SeedPreview {
			if err := store.SeedPreviewData(context.Background(), api.PreviewSeedOptions{NodeID: config.NodeID, DisplayName: "Hytron", CountryCode: "HK", AgentToken: config.AgentToken}); err != nil {
				_ = store.Close()
				backgroundCancel()
				return nil, err
			}
		}
	}
	handler := api.NewHandler(options)
	if cleanupHandler, ok := handler.(interface{ Cleanup(context.Context) error }); ok {
		cleanupHandlers = append([]func(context.Context) error{cleanupHandler.Cleanup}, cleanupHandlers...)
	}
	cleanup := func(ctx context.Context) error {
		var firstErr error
		for _, cleanupHandler := range cleanupHandlers {
			if err := cleanupHandler(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	return &controllerRuntime{Handler: handler, Store: store, Cleanup: cleanup}, nil
}

func checkSQLiteDatabase(dbPath string) error {
	store, err := api.OpenSQLiteStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return store.QuickCheck(ctx)
}

func readSecret(secretValue, secretFile string) (string, error) {
	if secretFile != "" {
		content, err := os.ReadFile(secretFile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(content)), nil
	}
	return strings.TrimSpace(secretValue), nil
}

func readAgentToken(tokenValue, tokenFile string) (string, error) {
	return readSecret(tokenValue, tokenFile)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18980", "controller listen address")
	webDir := flag.String("web-dir", "", "optional built web dashboard directory")
	dbPath := flag.String("db", "", "optional SQLite database path for real controller data")
	seedPreview := flag.Bool("seed-preview", false, "seed the Hytron preview node and TCP probe targets into SQLite; requires -db")
	collectLocal := flag.Bool("collect-local", false, "run a controller-local TCP probe collector for preview real latency data; requires -db")
	nodeID := flag.String("node-id", "hytron", "node id for seeded preview data and controller-local collection")
	agentToken := flag.String("agent-token", "", "agent API bearer token for seeded preview node; prefer -agent-token-file in deployments")
	agentTokenFile := flag.String("agent-token-file", "", "file containing the agent API bearer token for seeded preview node")
	adminToken := flag.String("admin-token", "", "admin API token; prefer -admin-token-file in deployments")
	adminTokenFile := flag.String("admin-token-file", "", "file containing the admin API token")
	agentBinaryPath := flag.String("agent-binary", "", "optional Zeno agent linux/amd64 binary path served for dashboard install commands")
	agentVersion := flag.String("agent-version", "", "optional version string inserted into generated agent install commands")
	probeInterval := flag.Duration("probe-interval", time.Minute, "controller-local probe collection interval")
	checkDB := flag.Bool("check-db", false, "run SQLite schema setup and PRAGMA quick_check, then exit")
	flag.Parse()

	if *checkDB {
		if *dbPath == "" {
			log.Fatal("-check-db requires -db")
		}
		if err := checkSQLiteDatabase(*dbPath); err != nil {
			log.Fatal(err)
		}
		return
	}

	token, err := readAgentToken(*agentToken, *agentTokenFile)
	if err != nil {
		log.Fatal(err)
	}
	adminSecret, err := readSecret(*adminToken, *adminTokenFile)
	if err != nil {
		log.Fatal(err)
	}

	runtime, err := buildController(handlerConfig{DBPath: *dbPath, WebDir: *webDir, SeedPreview: *seedPreview, NodeID: *nodeID, AgentToken: token, AdminToken: adminSecret, AgentBinaryPath: *agentBinaryPath, AgentVersion: *agentVersion})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runtime.Cleanup(ctx); err != nil {
			log.Printf("controller cleanup timed out or failed: %v", err)
		}
	}()

	if *collectLocal {
		if runtime.Store == nil {
			log.Fatal("-collect-local requires -db")
		}
		collector := api.NewLocalProbeCollector(runtime.Store, api.LocalProbeCollectorOptions{NodeID: *nodeID})
		var collectorWG sync.WaitGroup
		collectorCtx, stopCollector := context.WithCancel(context.Background())
		defer func() {
			stopCollector()
			done := make(chan struct{})
			go func() {
				collectorWG.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				log.Printf("controller-local probe collector cleanup timed out")
			}
		}()
		collectorWG.Add(1)
		go func() {
			defer collectorWG.Done()
			ticker := time.NewTicker(*probeInterval)
			defer ticker.Stop()
			for {
				if err := collector.CollectOnce(collectorCtx); err != nil {
					log.Printf("local probe collection failed: %v", err)
				}
				select {
				case <-collectorCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		log.Printf("controller-local probe collector enabled for node %s every %s", *nodeID, probeInterval.String())
	}
	log.Printf("zeno controller listening on %s", *addr)
	if *webDir != "" {
		log.Printf("serving dashboard from %s", *webDir)
	}
	if *dbPath != "" {
		log.Printf("using SQLite data store %s", *dbPath)
	}
	if *agentBinaryPath != "" {
		log.Printf("serving agent binary from %s", *agentBinaryPath)
	}
	if *seedPreview {
		log.Printf("seeded preview data for node %s", *nodeID)
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           runtime.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	shutdownCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case <-shutdownCtx.Done():
		log.Printf("shutdown signal received; draining HTTP requests")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown timed out: %v; closing server", err)
			_ = server.Close()
		}
		if err := <-serverErr; err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}
}
