package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains validated process-level application settings.
type Config struct {
	HTTPAddr          string
	PublicBaseURL     string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	LogLevel          slog.Level

	DatabaseURL       string
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
	DBConnMaxIdleTime time.Duration
	DBConnectTimeout  time.Duration

	JWTSigningKey string
	JWTIssuer     string
	JWTAudience   string
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom parses configuration using getenv. It is exported to make
// configuration behavior deterministic in tests and small command-line tools.
func LoadFrom(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, fmt.Errorf("environment lookup function is nil")
	}

	cfg := Config{
		HTTPAddr:          valueOrDefault(getenv, "HTTP_ADDR", ":8080"),
		PublicBaseURL:     valueOrDefault(getenv, "PUBLIC_BASE_URL", "http://localhost:8080"),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   10 * time.Second,
		LogLevel:          slog.LevelInfo,

		DatabaseURL:       valueOrDefault(getenv, "DATABASE_URL", "postgres://shortener:local_dev_password@localhost:5432/shortener?sslmode=disable"),
		DBMaxOpenConns:    25,
		DBMaxIdleConns:    5,
		DBConnMaxLifetime: 30 * time.Minute,
		DBConnMaxIdleTime: 5 * time.Minute,
		DBConnectTimeout:  5 * time.Second,

		JWTSigningKey: valueOrDefault(getenv, "JWT_SIGNING_KEY", "local-development-only-signing-key-change-before-deploy"),
		JWTIssuer:     valueOrDefault(getenv, "JWT_ISSUER", "url-shortener"),
		JWTAudience:   valueOrDefault(getenv, "JWT_AUDIENCE", "url-shortener-api"),
	}

	if err := validateListenAddress(cfg.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("invalid HTTP_ADDR: %w", err)
	}
	if err := validatePublicBaseURL(cfg.PublicBaseURL); err != nil {
		return Config{}, fmt.Errorf("invalid PUBLIC_BASE_URL: %w", err)
	}
	if err := validateDatabaseURL(cfg.DatabaseURL); err != nil {
		return Config{}, fmt.Errorf("invalid DATABASE_URL: %w", err)
	}
	if len(cfg.JWTSigningKey) < 32 {
		return Config{}, fmt.Errorf("JWT_SIGNING_KEY must contain at least 32 bytes")
	}
	if cfg.JWTIssuer == "" || cfg.JWTAudience == "" {
		return Config{}, fmt.Errorf("JWT_ISSUER and JWT_AUDIENCE are required")
	}

	var err error
	if cfg.ReadHeaderTimeout, err = durationOrDefault(getenv, "HTTP_READ_HEADER_TIMEOUT", cfg.ReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ReadTimeout, err = durationOrDefault(getenv, "HTTP_READ_TIMEOUT", cfg.ReadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.WriteTimeout, err = durationOrDefault(getenv, "HTTP_WRITE_TIMEOUT", cfg.WriteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.IdleTimeout, err = durationOrDefault(getenv, "HTTP_IDLE_TIMEOUT", cfg.IdleTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = durationOrDefault(getenv, "HTTP_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if cfg.DBConnMaxLifetime, err = durationOrDefault(getenv, "DB_CONN_MAX_LIFETIME", cfg.DBConnMaxLifetime); err != nil {
		return Config{}, err
	}
	if cfg.DBConnMaxIdleTime, err = durationOrDefault(getenv, "DB_CONN_MAX_IDLE_TIME", cfg.DBConnMaxIdleTime); err != nil {
		return Config{}, err
	}
	if cfg.DBConnectTimeout, err = durationOrDefault(getenv, "DB_CONNECT_TIMEOUT", cfg.DBConnectTimeout); err != nil {
		return Config{}, err
	}

	if cfg.DBMaxOpenConns, err = intOrDefault(getenv, "DB_MAX_OPEN_CONNS", cfg.DBMaxOpenConns, 1); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxIdleConns, err = intOrDefault(getenv, "DB_MAX_IDLE_CONNS", cfg.DBMaxIdleConns, 0); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxIdleConns > cfg.DBMaxOpenConns {
		return Config{}, fmt.Errorf("DB_MAX_IDLE_CONNS cannot exceed DB_MAX_OPEN_CONNS")
	}

	if raw := strings.TrimSpace(getenv("LOG_LEVEL")); raw != "" {
		switch strings.ToLower(raw) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn", "warning":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			return Config{}, fmt.Errorf("invalid LOG_LEVEL %q: expected debug, info, warn, or error", raw)
		}
	}

	return cfg, nil
}

func valueOrDefault(getenv func(string) string, key, fallback string) string {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value
	}
	return fallback
}

func durationOrDefault(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}

	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, raw, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("invalid %s %q: duration must be greater than zero", key, raw)
	}
	return value, nil
}

func intOrDefault(getenv func(string) string, key string, fallback, minimum int) (int, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum {
		return 0, fmt.Errorf("invalid %s %q: expected an integer greater than or equal to %d", key, raw, minimum)
	}
	return value, nil
}

func validateListenAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("must use host:port format: %w", err)
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("port must be an integer between 1 and 65535")
	}
	return nil
}

func validatePublicBaseURL(raw string) error {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return fmt.Errorf("must be an absolute HTTP or HTTPS origin")
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" {
		return fmt.Errorf("must be an absolute HTTP or HTTPS origin")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") ||
		parsed.Hostname() == "" ||
		parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" {
		return fmt.Errorf("must be an HTTP or HTTPS origin without credentials, path, query, or fragment")
	}
	return nil
}

func validateDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return fmt.Errorf("scheme must be postgres or postgresql")
	}
	if parsed.Hostname() == "" || strings.Trim(parsed.Path, "/") == "" {
		return fmt.Errorf("host and database name are required")
	}
	if parsed.User == nil || strings.TrimSpace(parsed.User.Username()) == "" {
		return fmt.Errorf("database username is required")
	}
	return nil
}
