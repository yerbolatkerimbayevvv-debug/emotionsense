package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/apperror"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
)

type analysisFixture struct {
	svc       *AnalysisService
	analyses  *memAnalyses
	cache     *memCache
	storage   *memStorage
	publisher *memPublisher
}

func newAnalysisFixture() *analysisFixture {
	f := &analysisFixture{
		analyses:  newMemAnalyses(),
		cache:     newMemCache(),
		storage:   newMemStorage(),
		publisher: &memPublisher{},
	}
	f.svc = NewAnalysisService(f.analyses, f.cache, f.storage, f.publisher, discardLogger())
	return f
}

func TestAnalysisService_Submit(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("happy path: stored, created, cached, published", func(t *testing.T) {
		f := newAnalysisFixture()
		a, err := f.svc.Submit(ctx, userID, testJPEG(t, 100, 100))
		require.NoError(t, err)

		assert.Equal(t, domain.StatusPending, a.Status)
		assert.Equal(t, userID, a.UserID)
		assert.Contains(t, a.ImageKey, userID.String()+"/")
		assert.Contains(t, a.ImageKey, ".jpeg")

		_, ok := f.storage.files[a.ImageKey]
		assert.True(t, ok, "файл сохранён в хранилище")

		st, err := f.cache.GetStatus(ctx, a.ID.String())
		require.NoError(t, err)
		assert.Equal(t, domain.StatusPending, st)

		require.Len(t, f.publisher.events, 1)
		evt := f.publisher.events[0].(domain.ImageUploadedEvent)
		assert.Equal(t, a.ID, evt.AnalysisID)
		assert.Equal(t, a.ImageKey, evt.ImageKey)
	})

	t.Run("png accepted", func(t *testing.T) {
		f := newAnalysisFixture()
		a, err := f.svc.Submit(ctx, userID, testPNG(t, 64, 64))
		require.NoError(t, err)
		assert.Contains(t, a.ImageKey, ".png")
	})

	t.Run("empty body", func(t *testing.T) {
		f := newAnalysisFixture()
		_, err := f.svc.Submit(ctx, userID, nil)
		assert.True(t, apperror.Is(err, apperror.CodeInvalidInput))
	})

	t.Run("not an image", func(t *testing.T) {
		f := newAnalysisFixture()
		_, err := f.svc.Submit(ctx, userID, []byte("hello, this is not an image"))
		assert.True(t, apperror.Is(err, apperror.CodeInvalidInput))
		assert.Empty(t, f.storage.files, "ничего не должно быть сохранено")
		assert.Empty(t, f.publisher.events)
	})

	t.Run("too small", func(t *testing.T) {
		f := newAnalysisFixture()
		_, err := f.svc.Submit(ctx, userID, testJPEG(t, 10, 10))
		assert.True(t, apperror.Is(err, apperror.CodeInvalidInput))
	})

	t.Run("storage down -> 503", func(t *testing.T) {
		f := newAnalysisFixture()
		f.storage.putErr = errors.New("minio down")
		_, err := f.svc.Submit(ctx, userID, testJPEG(t, 100, 100))
		assert.True(t, apperror.Is(err, apperror.CodeUnavailable))
		assert.Empty(t, f.analyses.items)
	})

	t.Run("queue down -> 503 and analysis marked failed", func(t *testing.T) {
		f := newAnalysisFixture()
		f.publisher.err = errors.New("kafka down")
		_, err := f.svc.Submit(ctx, userID, testJPEG(t, 100, 100))
		assert.True(t, apperror.Is(err, apperror.CodeUnavailable))
		require.Len(t, f.analyses.items, 1)
		for _, a := range f.analyses.items {
			assert.Equal(t, domain.StatusFailed, a.Status)
		}
	})
}

func TestAnalysisService_GetAndOwnership(t *testing.T) {
	ctx := context.Background()
	f := newAnalysisFixture()
	owner, stranger := uuid.New(), uuid.New()

	a, err := f.svc.Submit(ctx, owner, testJPEG(t, 100, 100))
	require.NoError(t, err)

	got, err := f.svc.Get(ctx, owner, a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.ID, got.ID)

	_, err = f.svc.Get(ctx, stranger, a.ID)
	assert.True(t, apperror.Is(err, apperror.CodeNotFound), "чужой анализ выглядит как несуществующий")

	_, err = f.svc.Get(ctx, owner, uuid.New())
	assert.True(t, apperror.Is(err, apperror.CodeNotFound))
}

func TestAnalysisService_StatusUsesCacheFirst(t *testing.T) {
	ctx := context.Background()
	f := newAnalysisFixture()
	userID := uuid.New()

	a, err := f.svc.Submit(ctx, userID, testJPEG(t, 100, 100))
	require.NoError(t, err)

	// Эмулируем: воркер обновил кэш, БД ещё не читали.
	require.NoError(t, f.cache.SetStatus(ctx, a.ID.String(), domain.StatusProcessing))
	st, err := f.svc.Status(ctx, userID, a.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProcessing, st)

	// Кэш пуст -> fallback в БД и прогрев кэша.
	require.NoError(t, f.cache.DeleteStatus(ctx, a.ID.String()))
	st, err = f.svc.Status(ctx, userID, a.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPending, st)
	cached, err := f.cache.GetStatus(ctx, a.ID.String())
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPending, cached)
}

func TestAnalysisService_ListAndDelete(t *testing.T) {
	ctx := context.Background()
	f := newAnalysisFixture()
	userID := uuid.New()

	for i := 0; i < 3; i++ {
		_, err := f.svc.Submit(ctx, userID, testJPEG(t, 100, 100))
		require.NoError(t, err)
	}
	_, err := f.svc.Submit(ctx, uuid.New(), testJPEG(t, 100, 100)) // чужой
	require.NoError(t, err)

	items, total, err := f.svc.List(ctx, userID, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, items, 2)

	items, _, err = f.svc.List(ctx, userID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, items, 1)

	target := items[0]
	require.NoError(t, f.svc.Delete(ctx, userID, target.ID))
	_, ok := f.storage.files[target.ImageKey]
	assert.False(t, ok, "файл удалён вместе с записью")
	_, err = f.svc.Get(ctx, userID, target.ID)
	assert.True(t, apperror.Is(err, apperror.CodeNotFound))

	err = f.svc.Delete(ctx, userID, target.ID)
	assert.True(t, apperror.Is(err, apperror.CodeNotFound))
}
