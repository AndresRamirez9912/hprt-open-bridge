package main

import (
	"context"
	"flag"
	"log"

	"github.com/AndresRamirez9912/photo-print-bridge/internal/app"
)

// main loads the configuration, wires the bridge and serves until a stop signal
func main() {
	// Directory of the configuration files
	configDir := flag.String("config", ".", "directory that holds config.yaml and the optional .env")
	flag.Parse()

	// Load config.yaml, the .env file and the BRIDGE_ overrides
	cfg, err := app.LoadConfig(*configDir)
	if err != nil {
		log.Fatal(err)
	}

	// Wire everything and serve until a stop signal
	if err := app.New(cfg).Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
