// Package config загружает настройки приложения из переменных окружения.
//
// Поддерживаются профили окружений (APP_ENV=dev|stage|prod): для dev
// автоматически подхватывается файл .env из корня проекта, для stage/prod
// ожидается, что переменные заданы оркестратором (Docker Compose, Kubernetes).
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env — профиль окружения.
type Env string

const (
	EnvDev   Env = "dev"
	EnvStage Env = "stage"
	EnvProd  Env = "prod"
)

// Config — полная конфигурация для api и worker.
type Config struct {
	App      App
	HTTP     HTTP
	Postgres Postgres
	Redis    Redis
	MinIO    MinIO
	Kafka    Kafka
	ML       ML
	JWT      JWT
	Worker   Worker
}

type App struct {
	Name     string
	Env      Env
	LogLevel string // debug | info | warn | error
}

type HTTP struct {
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	MaxUploadBytes  int64
	RateLimitPerMin int
}

type Postgres struct {
	DSN            string
	MaxConns       int32
	MigrateOnStart bool
	ConnectTimeout time.Duration
}

type Redis struct {
	Addr     string
	Password string
	DB       int
}

type MinIO struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

type Kafka struct {
	Brokers       []string
	Topic         string
	DLQTopic      string
	ConsumerGroup string
}

type ML struct {
	Addr    string
	Timeout time.Duration
}

type JWT struct {
	Secret     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type Worker struct {
	Concurrency int
	MaxAttempts int
	RetryBase   time.Duration
}

// Load читает конфигурацию. Для APP_ENV=dev дополнительно читает .env.
func Load() (*Config, error) {
	env := Env(getenv("APP_ENV", string(EnvDev)))
	if env == EnvDev {
		_ = loadDotEnv(".env") // отсутствие файла — не ошибка
	}

	cfg := &Config{
		App: App{
			Name:     getenv("APP_NAME", "emotionsense"),
			Env:      env,
			LogLevel: getenv("LOG_LEVEL", "info"),
		},
		HTTP: HTTP{
			Port:            getint("HTTP_PORT", 8080),
			ReadTimeout:     getdur("HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:    getdur("HTTP_WRITE_TIMEOUT", 30*time.Second),
			ShutdownTimeout: getdur("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),
			MaxUploadBytes:  int64(getint("HTTP_MAX_UPLOAD_MB", 10)) << 20,
			RateLimitPerMin: getint("HTTP_RATE_LIMIT_PER_MIN", 30),
		},
		Postgres: Postgres{
			DSN:            getenv("POSTGRES_DSN", "postgres://emotion:emotion@localhost:5432/emotionsense?sslmode=disable"),
			MaxConns:       int32(min(getint("POSTGRES_MAX_CONNS", 10), 1000)), //nolint:gosec // ограничено сверху
			MigrateOnStart: getbool("POSTGRES_MIGRATE_ON_START", true),
			ConnectTimeout: getdur("POSTGRES_CONNECT_TIMEOUT", 10*time.Second),
		},
		Redis: Redis{
			Addr:     getenv("REDIS_ADDR", "localhost:6379"),
			Password: getenv("REDIS_PASSWORD", ""),
			DB:       getint("REDIS_DB", 0),
		},
		MinIO: MinIO{
			Endpoint:  getenv("MINIO_ENDPOINT", "localhost:9000"),
			AccessKey: getenv("MINIO_ACCESS_KEY", "minioadmin"),
			SecretKey: getenv("MINIO_SECRET_KEY", "minioadmin"),
			Bucket:    getenv("MINIO_BUCKET", "faces"),
			UseSSL:    getbool("MINIO_USE_SSL", false),
		},
		Kafka: Kafka{
			Brokers:       strings.Split(getenv("KAFKA_BROKERS", "localhost:9092"), ","),
			Topic:         getenv("KAFKA_TOPIC", "image.uploaded"),
			DLQTopic:      getenv("KAFKA_DLQ_TOPIC", "image.failed"),
			ConsumerGroup: getenv("KAFKA_CONSUMER_GROUP", "emotion-workers"),
		},
		ML: ML{
			Addr:    getenv("ML_ADDR", "localhost:50051"),
			Timeout: getdur("ML_TIMEOUT", 20*time.Second),
		},
		JWT: JWT{
			Secret:     getenv("JWT_SECRET", ""),
			AccessTTL:  getdur("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTTL: getdur("JWT_REFRESH_TTL", 7*24*time.Hour),
		},
		Worker: Worker{
			Concurrency: getint("WORKER_CONCURRENCY", 4),
			MaxAttempts: getint("WORKER_MAX_ATTEMPTS", 3),
			RetryBase:   getdur("WORKER_RETRY_BASE", 2*time.Second),
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var errs []error
	if c.JWT.Secret == "" {
		if c.App.Env == EnvProd {
			errs = append(errs, errors.New("JWT_SECRET is required in prod"))
		} else {
			c.JWT.Secret = "dev-insecure-secret-change-me"
		}
	}
	if len(c.JWT.Secret) < 16 && c.App.Env == EnvProd {
		errs = append(errs, errors.New("JWT_SECRET must be at least 16 characters"))
	}
	if c.HTTP.Port <= 0 || c.HTTP.Port > 65535 {
		errs = append(errs, fmt.Errorf("HTTP_PORT out of range: %d", c.HTTP.Port))
	}
	if c.Worker.Concurrency <= 0 {
		errs = append(errs, errors.New("WORKER_CONCURRENCY must be > 0"))
	}
	switch c.App.Env {
	case EnvDev, EnvStage, EnvProd:
	default:
		errs = append(errs, fmt.Errorf("unknown APP_ENV %q", c.App.Env))
	}
	return errors.Join(errs...)
}

// IsProd — удобный предикат для включения строгих настроек.
func (c *Config) IsProd() bool { return c.App.Env == EnvProd }

// --- helpers ---

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getint(key string, def int) int {
	v := getenv(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getbool(key string, def bool) bool {
	v := getenv(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getdur(key string, def time.Duration) time.Duration {
	v := getenv(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// loadDotEnv читает файл формата KEY=VALUE и выставляет переменные,
// которые ещё не заданы в окружении (окружение имеет приоритет).
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
	return sc.Err()
}
