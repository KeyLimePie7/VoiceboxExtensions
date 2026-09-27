// Package config loads and saves the application's persisted settings.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// fileName is the settings file, always kept beside the running executable
// so the app stays portable.
const fileName = "vbedb.json"

// OutputDevice is a persisted audio output selection.
type OutputDevice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// WindowState persists the main window's last position and size.
type WindowState struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Config is the full set of persisted application settings.
type Config struct {
	ProfileID       string         `json:"profile_id"`
	BackendURL      string         `json:"backend_url"`
	KeepOutputsOpen bool           `json:"keep_outputs_open"`
	Outputs         []OutputDevice `json:"outputs"`
	Window          WindowState    `json:"window"`
}

// Default returns a Config populated with sensible defaults for a first run.
func Default() Config {
	return Config{
		BackendURL:      "http://127.0.0.1:17493",
		KeepOutputsOpen: true,
		Window:          WindowState{X: 100, Y: 100, W: 480, H: 360},
	}
}

// path returns the absolute path to vbedb.json, kept next to the executable.
func path() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating executable path: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), fileName), nil
}

// Load reads the config file beside the executable. If it doesn't exist yet,
// it returns Default() without error so first runs work out of the box.
func Load() (Config, error) {
	p, err := path()
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", p, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", p, err)
	}
	return cfg, nil
}

// Save writes the config file atomically (temp file + rename) so a crash or
// power loss mid-write can't corrupt the settings file.
func Save(cfg Config) error {
	p, err := path()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing temp config: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("replacing config file: %w", err)
	}
	return nil
}
