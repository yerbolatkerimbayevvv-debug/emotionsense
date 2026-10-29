package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("JWT_SECRET", "")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, EnvDev, cfg.App.Env)
	assert.Equal(t, 8080, cfg.HTTP.Port)
	assert.Equal(t, int64(10<<20), cfg.HTTP.MaxUploadBytes)
	assert.NotEmpty(t, cfg.JWT.Secret, "в dev подставляется небезопасный дефолт")
	assert.Equal(t, []string{"localhost:9092"}, cfg.Kafka.Brokers)
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("APP_ENV", "stage")
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("KAFKA_BROKERS", "k1:9092,k2:9092")
	t.Setenv("JWT_ACCESS_TTL", "30m")
	t.Setenv("WORKER_CONCURRENCY", "8")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9090, cfg.HTTP.Port)
	assert.Equal(t, []string{"k1:9092", "k2:9092"}, cfg.Kafka.Brokers)
	assert.Equal(t, 30*time.Minute, cfg.JWT.AccessTTL)
	assert.Equal(t, 8, cfg.Worker.Concurrency)
}

func TestLoad_ProdRequiresSecret(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("JWT_SECRET", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET")

	t.Setenv("JWT_SECRET", "short")
	_, err = Load()
	require.Error(t, err)

	t.Setenv("JWT_SECRET", "long-enough-secret-for-prod")
	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.IsProd())
}

func TestLoad_InvalidEnv(t *testing.T) {
	t.Setenv("APP_ENV", "weird")
	_, err := Load()
	require.Error(t, err)
}
