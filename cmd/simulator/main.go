package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/simulator"
)

func main() {
	origin := flag.String("url", "http://127.0.0.1:8080", "service origin")
	items := flag.Int("items", 10000, "synthetic catalog size")
	batch := flag.Int("batch", 64, "maximum rows per upload")
	cache := flag.Int("cache", 256, "maximum cached short IDs")
	cycles := flag.Int("cycles", 0, "number of control cycles; zero runs until interrupted")
	interval := flag.Duration("interval", time.Second, "minimum interval between control cycles")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runner, err := simulator.New(simulator.Config{URL: *origin, Token: os.Getenv("AE_DEVICE_TOKEN"), Items: *items, BatchItems: *batch, CacheEntries: *cache})
	if err == nil {
		err = runner.Connect(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for i := 0; *cycles == 0 || i < *cycles; i++ {
		if err = runner.Step(ctx); err != nil {
			if ctx.Err() != nil {
				break
			}
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if i%10 == 0 {
			_ = json.NewEncoder(os.Stdout).Encode(runner.Stats())
		}
		if *cycles > 0 && i+1 >= *cycles {
			break
		}
		timer := time.NewTimer(max(*interval, runner.NextPoll()))
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = json.NewEncoder(os.Stdout).Encode(runner.Stats())
			return
		case <-timer.C:
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(runner.Stats())
}
