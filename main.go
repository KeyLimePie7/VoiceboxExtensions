// Command vbrouter is a small GUI that generates speech via a local Voicebox
// backend and plays it to one or more selected Windows audio outputs.
package main

import (
	"context"
	"fmt"
	"os"

	"vbrouter/internal/audio"
	"vbrouter/internal/config"
	"vbrouter/internal/ui"
	"vbrouter/internal/voicebox"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vbrouter:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	devices, err := audio.EnumerateOutputDevices()
	if err != nil {
		return fmt.Errorf("enumerating audio devices: %w", err)
	}

	engine := audio.NewEngine(cfg.KeepOutputsOpen)
	client := voicebox.NewClient()

	// Voicebox may not be running yet; a failure here isn't fatal, the UI
	// just surfaces it so the user can restart once the backend is up.
	profiles, profilesErr := client.ListProfiles(context.Background(), cfg.BackendURL)

	app := ui.New(&cfg, client, engine, devices, profiles, profilesErr)
	return app.Run()
}
