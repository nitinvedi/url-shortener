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
	if cfg.PublicBaseURL != "http://localhost:8080" {
		t.Errorf("PublicBaseURL = %q, want http://localhost:8080", cfg.PublicBaseURL)
	}
	if cfg.ReadHeaderTimeout != 5*time.Second || cfg.ReadTimeout != 10*time.Second ||
		cfg.WriteTimeout != 15*time.Second || cfg.IdleTimeout != 60*time.Second ||
		cfg.ShutdownTimeout != 10*time.Second {
		t.Error("unexpected HTTP timeout defaults")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.DatabaseURL != "postgres://shortener:local_dev_password@localhost:5432/shortener?sslmode=disable" {
		t.Errorf("unexpected default DatabaseURL: %q", cfg.DatabaseURL)
	}
	if cfg.DBMaxOpenConns != 25 || cfg.DBMaxIdleConns != 5 {
		t.Errorf("unexpected default pool sizes: max open %d, max idle %d", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	}
	if len(cfg.JWTSigningKey) < 32 || cfg.JWTIssuer != "url-shortener" || cfg.JWTAudience != "url-shortener-api" {
		t.Error("unexpected JWT defaults")
	}
	if cfg.RedisAddr != "localhost:6379" || cfg.RedisDB != 0 || cfg.RedisOperationTimeout != 250*time.Millisecond {
		t.Errorf("unexpected Redis defaults: addr=%q db=%d timeout=%s", cfg.RedisAddr, cfg.RedisDB, cfg.RedisOperationTimeout)
	}
}

func TestLoadFromParsesOverrides(t *testing.T) {
	values := map[string]string{
		"HTTP_ADDR":               "127.0.0.1:9090",
		"PUBLIC_BASE_URL":         "https://sho.rt",
		"HTTP_READ_TIMEOUT":       "20s",
		"HTTP_SHUTDOWN_TIMEOUT":   "3s",
		"LOG_LEVEL":               "DEBUG",
		"DATABASE_URL":            "postgresql://app:secret@db.internal:5432/shortener?sslmode=require",
		"DB_MAX_OPEN_CONNS":       "40",
		"DB_MAX_IDLE_CONNS":       "10",
		"DB_CONN_MAX_LIFETIME":    "1h",
		"DB_CONN_MAX_IDLE_TIME":   "2m",
		"DB_CONNECT_TIMEOUT":      "8s",
		"JWT_SIGNING_KEY":         "0123456789abcdef0123456789abcdef",
		"JWT_ISSUER":              "shortener-service",
		"JWT_AUDIENCE":            "shortener-clients",
		"REDIS_ADDR":              "redis.internal:6380",
		"REDIS_PASSWORD":          "redis-secret",
		"REDIS_DB":                "2",
		"REDIS_OPERATION_TIMEOUT": "400ms",
	}
	cfg, err := LoadFrom(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9090" || cfg.PublicBaseURL != "https://sho.rt" {
		t.Errorf("unexpected endpoint configuration: addr=%q base=%q", cfg.HTTPAddr, cfg.PublicBaseURL)
	}
	if cfg.ReadTimeout != 20*time.Second || cfg.ShutdownTimeout != 3*time.Second {
		t.Error("unexpected HTTP timeout overrides")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.DatabaseURL != values["DATABASE_URL"] {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.DBMaxOpenConns != 40 || cfg.DBMaxIdleConns != 10 {
		t.Errorf("unexpected pool sizes: max open %d, max idle %d", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime != time.Hour || cfg.DBConnMaxIdleTime != 2*time.Minute || cfg.DBConnectTimeout != 8*time.Second {
		t.Error("unexpected database duration configuration")
	}
	if cfg.JWTSigningKey != values["JWT_SIGNING_KEY"] || cfg.JWTIssuer != values["JWT_ISSUER"] || cfg.JWTAudience != values["JWT_AUDIENCE"] {
		t.Error("unexpected JWT configuration")
	}
	if cfg.RedisAddr != values["REDIS_ADDR"] || cfg.RedisPassword != values["REDIS_PASSWORD"] ||
		cfg.RedisDB != 2 || cfg.RedisOperationTimeout != 400*time.Millisecond {
		t.Error("unexpected Redis configuration")
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
		{name: "wrong database scheme", values: map[string]string{"DATABASE_URL": "mysql://app:secret@localhost:3306/shortener"}},
		{name: "database name missing", values: map[string]string{"DATABASE_URL": "postgres://app:secret@localhost:5432"}},
		{name: "database username missing", values: map[string]string{"DATABASE_URL": "postgres://localhost:5432/shortener"}},
		{name: "invalid max connections", values: map[string]string{"DB_MAX_OPEN_CONNS": "0"}},
		{name: "idle exceeds open", values: map[string]string{"DB_MAX_OPEN_CONNS": "4", "DB_MAX_IDLE_CONNS": "5"}},
		{name: "invalid connection lifetime", values: map[string]string{"DB_CONN_MAX_LIFETIME": "-1m"}},
		{name: "short JWT signing key", values: map[string]string{"JWT_SIGNING_KEY": "short"}},
		{name: "unsupported public URL scheme", values: map[string]string{"PUBLIC_BASE_URL": "ftp://sho.rt"}},
		{name: "public URL with credentials", values: map[string]string{"PUBLIC_BASE_URL": "https://user:pass@sho.rt"}},
		{name: "public URL with path", values: map[string]string{"PUBLIC_BASE_URL": "https://sho.rt/short"}},
		{name: "public URL with query", values: map[string]string{"PUBLIC_BASE_URL": "https://sho.rt?x=1"}},
		{name: "invalid Redis address", values: map[string]string{"REDIS_ADDR": "not-a-host-port"}},
		{name: "invalid Redis port", values: map[string]string{"REDIS_ADDR": "localhost:70000"}},
		{name: "invalid Redis database", values: map[string]string{"REDIS_DB": "-1"}},
		{name: "invalid Redis timeout", values: map[string]string{"REDIS_OPERATION_TIMEOUT": "0s"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadFrom(func(key string) string { return test.values[key] })
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
