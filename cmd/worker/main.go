// Command worker — консьюмер Kafka, который выполняет распознавание.
//
// Воркер не имеет HTTP API (кроме /health для оркестратора). Масштабируется
// горизонтально: несколько реплик в одной consumer group делят партиции.
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

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/config"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/logger"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/mlclient"
	pgrepo "github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/repository/postgres"
	redisrepo "github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/repository/redis"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/service"
	miniostore "github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/storage/minio"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/transport/kafka"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logger.New(cfg.App.LogLevel, cfg.App.Env != config.EnvDev,
		slog.String("service", "worker"), slog.String("env", string(cfg.App.Env)))
	log.Info("starting", "concurrency", cfg.Worker.Concurrency, "topic", cfg.Kafka.Topic)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- инфраструктура ---
	pool, err := pgrepo.New(ctx, cfg.Postgres)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	if cfg.Postgres.MigrateOnStart {
		// Воркер тоже умеет мигрировать — на случай, если стартует первым.
		if _, err := pgrepo.Migrate(ctx, pool, migrations.FS); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
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

	ml, err := mlclient.New(cfg.ML)
	if err != nil {
		return fmt.Errorf("ml client: %w", err)
	}
	defer ml.Close()
	waitForML(ctx, ml, log)

	if err := kafka.EnsureTopics(ctx, cfg.Kafka.Brokers, cfg.Kafka.Topic, cfg.Kafka.DLQTopic); err != nil {
		log.Warn("ensure kafka topics failed", "err", err)
	}
	dlq := kafka.NewProducer(cfg.Kafka, cfg.Kafka.DLQTopic)
	defer dlq.Close()

	consumer := kafka.NewConsumer(cfg.Kafka, log)
	defer consumer.Close()

	// --- приложение ---
	processor := service.NewProcessor(
		pgrepo.NewAnalysisRepo(pool), rdb, store, ml, dlq,
		cfg.Worker.MaxAttempts, cfg.Worker.RetryBase, log,
	)

	// Минимальный health-эндпоинт для Docker/K8s.
	healthSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTP.Port),
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}),
	}
	go func() {
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server failed", "err", err)
		}
	}()

	log.Info("consuming", "group", cfg.Kafka.ConsumerGroup)
	runErr := consumer.Run(ctx, cfg.Worker.Concurrency, processor.Handle)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	_ = healthSrv.Shutdown(shutdownCtx)

	if runErr != nil {
		return fmt.Errorf("consumer: %w", runErr)
	}
	log.Info("worker stopped gracefully")
	return nil
}

// waitForML ждёт готовности модели: ML-контейнер стартует дольше остальных,
// и нет смысла брать задачи, пока он грузит веса.
func waitForML(ctx context.Context, ml *mlclient.Client, log *slog.Logger) {
	backoff := time.Second
	for {
		if err := ml.Ready(ctx); err == nil {
			log.Info("ml service is ready")
			return
		} else {
			log.Warn("ml service not ready, waiting", "err", err, "retry_in", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}
