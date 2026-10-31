package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadFromUsesSafeDefaults(t *testing.T) {
	cfg, err := LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %s, want 5s", cfg.ReadHeaderTimeout)
	}
	if cfg.ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout = %s, want 10s", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 15*time.Second {
		t.Errorf("WriteTimeout = %s, want 15s", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 60*time.Second {
		t.Errorf("IdleTimeout = %s, want 60s", cfg.IdleTimeout)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
}

func TestLoadFromParsesOverrides(t *testing.T) {
	values := map[string]string{
		"HTTP_ADDR":             "127.0.0.1:9090",
		"HTTP_READ_TIMEOUT":     "20s",
		"HTTP_SHUTDOWN_TIMEOUT": "3s",
		"LOG_LEVEL":             "DEBUG",
	}
	cfg, err := LoadFrom(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.ReadTimeout != 20*time.Second {
		t.Errorf("ReadTimeout = %s, want 20s", cfg.ReadTimeout)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 3s", cfg.ShutdownTimeout)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
}

func TestLoadFromRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "invalid address", values: map[string]string{"HTTP_ADDR": "8080"}},
		{name: "invalid port", values: map[string]string{"HTTP_ADDR": ":70000"}},
		{name: "invalid duration", values: map[string]string{"HTTP_READ_TIMEOUT": "later"}},
		{name: "zero duration", values: map[string]string{"HTTP_IDLE_TIMEOUT": "0s"}},
		{name: "negative duration", values: map[string]string{"HTTP_WRITE_TIMEOUT": "-1s"}},
		{name: "invalid log level", values: map[string]string{"LOG_LEVEL": "verbose"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(func(key string) string { return tt.values[key] })
			if err == nil {
				t.Fatal("LoadFrom() expected an error")
			}
		})
	}
}

func TestLoadFromRejectsNilLookup(t *testing.T) {
	if _, err := LoadFrom(nil); err == nil {
		t.Fatal("LoadFrom(nil) expected an error")
	}
}
