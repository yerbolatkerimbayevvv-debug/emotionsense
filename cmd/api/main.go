// Command api — HTTP API сервиса EmotionSense.
//
// Жизненный цикл: загрузить конфиг → подключить зависимости → применить
// миграции → собрать роутер → слушать → по SIGTERM/SIGINT дать активным
// запросам завершиться (graceful shutdown) и закрыть соединения.
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

	"github.com/yerbolat/emotionsense/internal/config"
	"github.com/yerbolat/emotionsense/internal/logger"
	pgrepo "github.com/yerbolat/emotionsense/internal/repository/postgres"
	redisrepo "github.com/yerbolat/emotionsense/internal/repository/redis"
	"github.com/yerbolat/emotionsense/internal/service"
	miniostore "github.com/yerbolat/emotionsense/internal/storage/minio"
	httptransport "github.com/yerbolat/emotionsense/internal/transport/http"
	"github.com/yerbolat/emotionsense/internal/transport/http/handler"
	"github.com/yerbolat/emotionsense/internal/transport/kafka"
	"github.com/yerbolat/emotionsense/migrations"
	"github.com/yerbolat/emotionsense/pkg/jwt"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logger.New(cfg.App.LogLevel, cfg.App.Env != config.EnvDev,
		slog.String("service", "api"), slog.String("env", string(cfg.App.Env)))
	log.Info("starting", "port", cfg.HTTP.Port)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- инфраструктура ---
	pool, err := pgrepo.New(ctx, cfg.Postgres)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	if cfg.Postgres.MigrateOnStart {
		n, err := pgrepo.Migrate(ctx, pool, migrations.FS)
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		log.Info("migrations applied", "count", n)
	}

	rdb, err := redisrepo.New(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer rdb.Close()

	store, err := miniostore.New(ctx, cfg.MinIO)
	if err != nil {
		return fmt.Errorf("minio: %w", err)
	}

	if err := kafka.EnsureTopics(ctx, cfg.Kafka.Brokers, cfg.Kafka.Topic, cfg.Kafka.DLQTopic); err != nil {
		// Не фатально: продюсер умеет автосоздание, но логируем.
		log.Warn("ensure kafka topics failed", "err", err)
	}
	producer := kafka.NewProducer(cfg.Kafka, cfg.Kafka.Topic)
	defer producer.Close()

	// --- слои приложения ---
	userRepo := pgrepo.NewUserRepo(pool)
	analysisRepo := pgrepo.NewAnalysisRepo(pool)
	tokens := jwt.NewManager(cfg.JWT.Secret, cfg.JWT.AccessTTL, cfg.App.Name)

	authSvc := service.NewAuthService(userRepo, tokens, cfg.JWT.RefreshTTL)
	analysisSvc := service.NewAnalysisService(analysisRepo, rdb, store, producer, log)
	statsSvc := service.NewStatsService(analysisRepo, userRepo, rdb, log)

	router := httptransport.NewRouter(httptransport.Deps{
		Cfg:      cfg,
		Log:      log,
		Auth:     handler.NewAuthHandler(authSvc),
		Analysis: handler.NewAnalysisHandler(analysisSvc, cfg.HTTP.MaxUploadBytes),
		Stats:    handler.NewStatsHandler(statsSvc),
		Health: handler.NewHealthHandler(map[string]handler.Pinger{
			"postgres": pingFunc(pool.Ping),
			"redis":    rdb,
			"minio":    store,
		}),
		Tokens:  tokens,
		Limiter: rdb,
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTP.Port),
		Handler:           router,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("server stopped gracefully")
	return nil
}

// pingFunc адаптирует функцию к интерфейсу handler.Pinger.
type pingFunc func(ctx context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }
