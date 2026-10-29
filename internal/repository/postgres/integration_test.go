package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerbolat/emotionsense/internal/config"
	"github.com/yerbolat/emotionsense/internal/domain"
	"github.com/yerbolat/emotionsense/migrations"
)

// Интеграционные тесты требуют живой Postgres:
//
//	TEST_POSTGRES_DSN=postgres://emotion:emotion@localhost:5432/emotionsense_test?sslmode=disable go test ./internal/repository/postgres/...
//
// Без переменной тесты пропускаются (unit-прогон остаётся быстрым).
func setupDB(t *testing.T) (*UserRepo, *AnalysisRepo) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set; skipping integration test")
	}
	ctx := context.Background()
	pool, err := New(ctx, config.Postgres{DSN: dsn, MaxConns: 4, ConnectTimeout: 5 * time.Second})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(ctx, `DROP TABLE IF EXISTS emotion_scores, analyses, refresh_tokens, users, schema_migrations CASCADE`)
	require.NoError(t, err)
	_, err = Migrate(ctx, pool, migrations.FS)
	require.NoError(t, err)

	return NewUserRepo(pool), NewAnalysisRepo(pool)
}

func TestIntegration_MigrateIsIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	pool, err := New(ctx, config.Postgres{DSN: dsn, MaxConns: 2, ConnectTimeout: 5 * time.Second})
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Exec(ctx, `DROP TABLE IF EXISTS emotion_scores, analyses, refresh_tokens, users, schema_migrations CASCADE`)
	require.NoError(t, err)

	n1, err := Migrate(ctx, pool, migrations.FS)
	require.NoError(t, err)
	assert.Equal(t, 2, n1)
	n2, err := Migrate(ctx, pool, migrations.FS)
	require.NoError(t, err)
	assert.Equal(t, 0, n2, "повторный запуск ничего не применяет")
}

func TestIntegration_Users(t *testing.T) {
	users, _ := setupDB(t)
	ctx := context.Background()

	u := &domain.User{Email: "a@example.com", PasswordHash: "h", Role: domain.RoleUser}
	require.NoError(t, users.Create(ctx, u))
	assert.NotEqual(t, uuid.Nil, u.ID)

	dup := &domain.User{Email: "a@example.com", PasswordHash: "h", Role: domain.RoleUser}
	assert.ErrorIs(t, users.Create(ctx, dup), ErrConflict)

	got, err := users.GetByEmail(ctx, "a@example.com")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)

	_, err = users.GetByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)

	// refresh-токены
	require.NoError(t, users.SaveRefreshToken(ctx, "hash1", u.ID, time.Now().Add(time.Hour)))
	owner, err := users.ConsumeRefreshToken(ctx, "hash1")
	require.NoError(t, err)
	assert.Equal(t, u.ID, owner)
	_, err = users.ConsumeRefreshToken(ctx, "hash1")
	assert.ErrorIs(t, err, ErrNotFound, "токен одноразовый")

	require.NoError(t, users.SaveRefreshToken(ctx, "expired", u.ID, time.Now().Add(-time.Hour)))
	_, err = users.ConsumeRefreshToken(ctx, "expired")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestIntegration_AnalysisLifecycle(t *testing.T) {
	users, analyses := setupDB(t)
	ctx := context.Background()

	u := &domain.User{Email: "b@example.com", PasswordHash: "h", Role: domain.RoleUser}
	require.NoError(t, users.Create(ctx, u))

	a := &domain.Analysis{UserID: u.ID, ImageKey: "k.jpeg"}
	require.NoError(t, analyses.Create(ctx, a))

	ok, err := analyses.TryMarkProcessing(ctx, a.ID)
	require.NoError(t, err)
	assert.True(t, ok)

	require.NoError(t, analyses.SaveResult(ctx, a.ID, true, "happy", []domain.EmotionScore{
		{Emotion: "happy", Score: 0.8}, {Emotion: "neutral", Score: 0.2},
	}))

	ok, err = analyses.TryMarkProcessing(ctx, a.ID)
	require.NoError(t, err)
	assert.False(t, ok, "done нельзя взять в работу повторно")

	got, err := analyses.GetByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDone, got.Status)
	assert.Equal(t, 1, got.Attempts)
	require.NotNil(t, got.Dominant)
	assert.Equal(t, "happy", *got.Dominant)
	require.Len(t, got.Scores, 2)
	assert.Equal(t, "happy", got.Scores[0].Emotion, "отсортировано по score")
	assert.NotNil(t, got.FinishedAt)

	stats, total, err := analyses.UserEmotionStats(ctx, u.ID, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, stats, 1)
	assert.InDelta(t, 1.0, stats[0].Share, 1e-9)

	list, n, err := analyses.ListByUser(ctx, u.ID, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.Len(t, list, 1)

	require.NoError(t, analyses.Delete(ctx, a.ID, u.ID))
	_, err = analyses.GetByID(ctx, a.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}
