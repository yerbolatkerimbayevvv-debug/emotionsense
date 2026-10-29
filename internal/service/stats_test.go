package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerbolat/emotionsense/internal/domain"
)

func seedDone(t *testing.T, repo *memAnalyses, userID uuid.UUID, emotion string) {
	t.Helper()
	a := &domain.Analysis{UserID: userID, ImageKey: "k", Status: domain.StatusPending}
	require.NoError(t, repo.Create(context.Background(), a))
	require.NoError(t, repo.SaveResult(context.Background(), a.ID, true, emotion, nil))
}

func TestStatsService_UserStatsAndCache(t *testing.T) {
	ctx := context.Background()
	analyses, users, cache := newMemAnalyses(), newMemUsers(), newMemCache()
	svc := NewStatsService(analyses, users, cache, discardLogger())
	userID := uuid.New()

	seedDone(t, analyses, userID, "happy")
	seedDone(t, analyses, userID, "happy")
	seedDone(t, analyses, userID, "sad")
	seedDone(t, analyses, uuid.New(), "angry") // чужой

	resp, err := svc.UserStats(ctx, userID, 7)
	require.NoError(t, err)
	assert.Equal(t, 7, resp.Days)
	assert.Equal(t, int64(3), resp.Total)

	byEmotion := map[string]float64{}
	for _, e := range resp.Emotions {
		byEmotion[e.Emotion] = e.Share
	}
	assert.InDelta(t, 2.0/3.0, byEmotion["happy"], 1e-9)
	assert.InDelta(t, 1.0/3.0, byEmotion["sad"], 1e-9)
	assert.NotContains(t, byEmotion, "angry")

	// Второй вызов идёт из кэша: добавим данные в репозиторий и убедимся,
	// что ответ не изменился.
	seedDone(t, analyses, userID, "sad")
	cached, err := svc.UserStats(ctx, userID, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(3), cached.Total, "ответ из кэша")

	// После инвалидации — свежие данные.
	svc.InvalidateUser(ctx, userID)
	fresh, err := svc.UserStats(ctx, userID, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(4), fresh.Total)
}

func TestStatsService_AdminStats(t *testing.T) {
	ctx := context.Background()
	analyses, users, cache := newMemAnalyses(), newMemUsers(), newMemCache()
	svc := NewStatsService(analyses, users, cache, discardLogger())

	require.NoError(t, users.Create(ctx, &domain.User{Email: "a@x", PasswordHash: "h", Role: domain.RoleUser}))
	require.NoError(t, users.Create(ctx, &domain.User{Email: "b@x", PasswordHash: "h", Role: domain.RoleAdmin}))

	u := uuid.New()
	seedDone(t, analyses, u, "happy")
	pending := &domain.Analysis{UserID: u, ImageKey: "k", Status: domain.StatusPending}
	require.NoError(t, analyses.Create(ctx, pending))
	failed := &domain.Analysis{UserID: u, ImageKey: "k", Status: domain.StatusPending}
	require.NoError(t, analyses.Create(ctx, failed))
	require.NoError(t, analyses.MarkFailed(ctx, failed.ID, "boom"))

	resp, err := svc.AdminStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Users)
	assert.Equal(t, int64(3), resp.Analyses)
	assert.Equal(t, int64(1), resp.Done)
	assert.Equal(t, int64(1), resp.Failed)
	assert.Equal(t, int64(1), resp.Pending)
	require.Len(t, resp.TopEmotions, 1)
	assert.Equal(t, "happy", resp.TopEmotions[0].Emotion)
}
