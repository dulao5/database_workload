package main

import (
	"context"
	"database_workload/config"
	"database_workload/worker"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to the configuration file")
	flag.Parse()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Starting workload with concurrency %d", cfg.Concurrency)

	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	for i := 0; i < cfg.Concurrency; i++ {
		w, err := worker.New(i+1, cfg)
		if err != nil {
			log.Fatalf("Failed to create worker %d: %v", i+1, err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Run(ctx)
		}()
	}

	<-sigChan
	log.Println("Shutdown signal received, stopping workers...")
	cancel()

	wg.Wait()
	log.Println("All workers have stopped. Exiting.")
}
