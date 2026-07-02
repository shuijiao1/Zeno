package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/shuijiao1/jiaoprobe/internal/controller/api"
)

type handlerConfig struct {
	DBPath      string
	WebDir      string
	SeedPreview bool
	NodeID      string
}

type controllerRuntime struct {
	Handler http.Handler
	Store   *api.SQLiteStore
	Cleanup func() error
}

func buildController(config handlerConfig) (*controllerRuntime, error) {
	cleanup := func() error { return nil }
	options := api.HandlerOptions{StaticDir: config.WebDir}
	var store *api.SQLiteStore
	if config.DBPath != "" {
		opened, err := api.OpenSQLiteStore(config.DBPath)
		if err != nil {
			return nil, err
		}
		store = opened
		options.Store = store
		cleanup = store.Close
		if config.SeedPreview {
			if err := store.SeedPreviewData(context.Background(), api.PreviewSeedOptions{NodeID: config.NodeID, DisplayName: "Hytron", CountryCode: "HK"}); err != nil {
				_ = cleanup()
				return nil, err
			}
		}
	}
	return &controllerRuntime{Handler: api.NewHandler(options), Store: store, Cleanup: cleanup}, nil
}

func buildHandler(config handlerConfig) (http.Handler, func() error, error) {
	runtime, err := buildController(config)
	if err != nil {
		return nil, func() error { return nil }, err
	}
	return runtime.Handler, runtime.Cleanup, nil
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18980", "controller listen address")
	webDir := flag.String("web-dir", "", "optional built web dashboard directory")
	dbPath := flag.String("db", "", "optional SQLite database path for real controller data")
	seedPreview := flag.Bool("seed-preview", false, "seed the Hytron preview node and TCP probe targets into SQLite; requires -db")
	collectLocal := flag.Bool("collect-local", false, "run a controller-local TCP probe collector for preview real latency data; requires -db")
	nodeID := flag.String("node-id", "hytron", "node id for seeded preview data and controller-local collection")
	probeInterval := flag.Duration("probe-interval", time.Minute, "controller-local probe collection interval")
	flag.Parse()

	runtime, err := buildController(handlerConfig{DBPath: *dbPath, WebDir: *webDir, SeedPreview: *seedPreview, NodeID: *nodeID})
	if err != nil {
		log.Fatal(err)
	}
	defer runtime.Cleanup()

	if *collectLocal {
		if runtime.Store == nil {
			log.Fatal("-collect-local requires -db")
		}
		collector := api.NewLocalProbeCollector(runtime.Store, api.LocalProbeCollectorOptions{NodeID: *nodeID})
		go func() {
			ticker := time.NewTicker(*probeInterval)
			defer ticker.Stop()
			for {
				if err := collector.CollectOnce(context.Background()); err != nil {
					log.Printf("local probe collection failed: %v", err)
				}
				<-ticker.C
			}
		}()
		log.Printf("controller-local probe collector enabled for node %s every %s", *nodeID, probeInterval.String())
	}

	log.Printf("jiaoprobe controller listening on %s", *addr)
	if *webDir != "" {
		log.Printf("serving dashboard from %s", *webDir)
	}
	if *dbPath != "" {
		log.Printf("using SQLite data store %s", *dbPath)
	}
	if *seedPreview {
		log.Printf("seeded preview data for node %s", *nodeID)
	}
	if err := http.ListenAndServe(*addr, runtime.Handler); err != nil {
		log.Fatal(err)
	}
}
