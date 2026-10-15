package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadConfig checks config.yaml, the .env file and the env var overrides
func TestLoadConfig(t *testing.T) {
	// A minimal config.yaml with the values the cases override
	yaml := "app:\n  name: bridge\n  rest_addr: 127.0.0.1:8787\n  shutdown_timeout: 10s\nlogger:\n  level: info\ncors:\n  enabled: true\n  allowed_origins: [\"*\"]\nprinter:\n  wake_window: 45s\n"
	testCases := []struct {
		name     string
		yaml     string
		dotenv   string
		env      map[string]string
		wantErr  bool
		wantAddr string
		wantWake time.Duration
		wantOrig []string
	}{
		{name: "yaml only", yaml: yaml, wantAddr: "127.0.0.1:8787", wantWake: 45 * time.Second, wantOrig: []string{"*"}},
		{name: "env var overrides yaml", yaml: yaml, env: map[string]string{"BRIDGE_APP_REST_ADDR": "0.0.0.0:9000"}, wantAddr: "0.0.0.0:9000", wantWake: 45 * time.Second, wantOrig: []string{"*"}},
		{name: ".env file overrides yaml", yaml: yaml, dotenv: "BRIDGE_PRINTER_WAKE_WINDOW=8s\nBRIDGE_CORS_ALLOWED_ORIGINS=http://a.local,http://b.local\n", wantAddr: "127.0.0.1:8787", wantWake: 8 * time.Second, wantOrig: []string{"http://a.local", "http://b.local"}},
		{name: "missing config.yaml", wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Write the files of the case in a fresh directory
			dir := t.TempDir()
			if tc.yaml != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(tc.yaml), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.dotenv != "" {
				if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(tc.dotenv), 0o600); err != nil {
					t.Fatal(err)
				}
				// godotenv sets real env vars: remove them after the case
				t.Cleanup(func() {
					os.Unsetenv("BRIDGE_PRINTER_WAKE_WINDOW")
					os.Unsetenv("BRIDGE_CORS_ALLOWED_ORIGINS")
				})
			}

			// Real env vars, restored by t.Setenv when the case ends
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			// Load the configuration
			cfg, err := LoadConfig(dir)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}

			// Values from the yaml, overridden by .env and by the env vars
			if cfg.App.RestAddr != tc.wantAddr || cfg.Printer.WakeWindow != tc.wantWake {
				t.Errorf("addr %q wake %v, want %q %v", cfg.App.RestAddr, cfg.Printer.WakeWindow, tc.wantAddr, tc.wantWake)
			}

			// A comma separated env var becomes a list
			if len(cfg.CORS.AllowedOrigins) != len(tc.wantOrig) {
				t.Fatalf("origins = %v, want %v", cfg.CORS.AllowedOrigins, tc.wantOrig)
			}
			for i := range tc.wantOrig {
				if cfg.CORS.AllowedOrigins[i] != tc.wantOrig[i] {
					t.Errorf("origins = %v, want %v", cfg.CORS.AllowedOrigins, tc.wantOrig)
				}
			}
		})
	}
}
