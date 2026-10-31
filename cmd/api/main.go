package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nitinvedi/url-shortener/internal/auth"
	rediscache "github.com/nitinvedi/url-shortener/internal/cache/redis"
	"github.com/nitinvedi/url-shortener/internal/config"
	"github.com/nitinvedi/url-shortener/internal/httpapi"
	"github.com/nitinvedi/url-shortener/internal/link"
	"github.com/nitinvedi/url-shortener/internal/storage/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startupCancel()

	db, err := postgres.Open(startupCtx, cfg.DatabaseURL, postgres.PoolConfig{
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: cfg.DBConnMaxLifetime,
		ConnMaxIdleTime: cfg.DBConnMaxIdleTime,
		ConnectTimeout:  cfg.DBConnectTimeout,
	})
	if err != nil {
		return fmt.Errorf("initialize PostgreSQL: %w", err)
	}
	defer db.Close()

	if err := postgres.Migrate(startupCtx, db); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	logger.Info("database migrations are up to date")

	tokenManager, err := auth.NewTokenManager(cfg.JWTSigningKey, cfg.JWTIssuer, cfg.JWTAudience, auth.AccessTokenTTL)
	if err != nil {
		return fmt.Errorf("initialize access token manager: %w", err)
	}
	authService, err := auth.NewService(
		postgres.NewUserRepository(db),
		auth.NewArgon2idHasher(),
		tokenManager,
	)
	if err != nil {
		return fmt.Errorf("initialize authentication service: %w", err)
	}

	redisStore, err := rediscache.New(
		cfg.RedisURL,
		cfg.RedisDialTimeout,
		cfg.RedisReadTimeout,
		cfg.RedisWriteTimeout,
	)
	if err != nil {
		return fmt.Errorf("initialize Redis cache client: %w", err)
	}
	defer func() {
		if err := redisStore.Close(); err != nil {
			logger.Warn("close Redis client failed", "error", err)
		}
	}()

	redirectService, err := link.NewRedirectServiceWithClock(
		postgres.NewLinkRepository(db),
		redisStore,
		link.NoopClickRecorder{},
		logger,
		nil,
		cfg.RedisCacheTimeout,
	)
	if err != nil {
		return fmt.Errorf("initialize redirect service: %w", err)
	}
	linkService, err := link.NewService(postgres.NewLinkRepository(db))
	if err != nil {
		return fmt.Errorf("initialize URL service: %w", err)
	}

	handler := httpapi.NewHandlerWithRedirects(
		logger,
		authService,
		tokenManager,
		linkService,
		redirectService,
		cfg.PublicBaseURL,
		httpapi.DependencyCheck{
			Name:     "postgres",
			Required: true,
			Check:    db.PingContext,
		},
		httpapi.DependencyCheck{
			Name:     "redis",
			Required: false,
			Check:    redisStore.Ping,
		},
	)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "address", cfg.HTTPAddr)
		serverErrors <- server.ListenAndServe()
	}()

	shutdownSignals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server failed: %w", err)
	case <-shutdownSignals.Done():
		logger.Info("shutdown signal received")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}

		if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server stopped unexpectedly: %w", err)
		}

		logger.Info("http server stopped")
		return nil
	}
}
