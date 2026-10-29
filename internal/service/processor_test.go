package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/mlclient"
)

type processorFixture struct {
	analyses *memAnalyses
	cache    *memCache
	storage  *memStorage
	dlq      *memPublisher
	analyzer *fakeAnalyzer
	evt      domain.ImageUploadedEvent
}

func newProcessorFixture(t *testing.T, analyzer *fakeAnalyzer, maxAttempts int) (*Processor, *processorFixture) {
	t.Helper()
	f := &processorFixture{
		analyses: newMemAnalyses(),
		cache:    newMemCache(),
		storage:  newMemStorage(),
		dlq:      &memPublisher{},
		analyzer: analyzer,
	}
	userID := uuid.New()
	a := &domain.Analysis{UserID: userID, ImageKey: "k.jpeg", Status: domain.StatusPending}
	require.NoError(t, f.analyses.Create(context.Background(), a))
	f.storage.files["k.jpeg"] = []byte("img")
	f.evt = domain.ImageUploadedEvent{AnalysisID: a.ID, UserID: userID, ImageKey: "k.jpeg", Attempt: 1}

	p := NewProcessor(f.analyses, f.cache, f.storage, analyzer, f.dlq, maxAttempts, time.Millisecond, discardLogger())
	return p, f
}

func okResult() (*mlclient.Result, error) {
	return &mlclient.Result{
		FaceDetected: true, Dominant: "happy",
		Scores: []domain.EmotionScore{{Emotion: "happy", Score: 0.9}, {Emotion: "neutral", Score: 0.1}},
	}, nil
}

func TestProcessor_Success(t *testing.T) {
	ctx := context.Background()
	p, f := newProcessorFixture(t, &fakeAnalyzer{results: []func() (*mlclient.Result, error){okResult}}, 3)

	require.NoError(t, p.Handle(ctx, f.evt))

	a, err := f.analyses.GetByID(ctx, f.evt.AnalysisID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDone, a.Status)
	require.NotNil(t, a.Dominant)
	assert.Equal(t, "happy", *a.Dominant)
	assert.Len(t, a.Scores, 2)
	assert.Equal(t, 1, a.Attempts)
	assert.NotNil(t, a.FinishedAt)

	st, _ := f.cache.GetStatus(ctx, a.ID.String())
	assert.Equal(t, domain.StatusDone, st)
	assert.Empty(t, f.dlq.events)
	assert.Equal(t, 1, f.analyzer.calls)
}

func TestProcessor_RetriesThenSucceeds(t *testing.T) {
	ctx := context.Background()
	flaky := &fakeAnalyzer{results: []func() (*mlclient.Result, error){
		func() (*mlclient.Result, error) { return nil, errors.New("ml timeout") },
		func() (*mlclient.Result, error) { return nil, errors.New("ml timeout") },
		okResult,
	}}
	p, f := newProcessorFixture(t, flaky, 3)

	require.NoError(t, p.Handle(ctx, f.evt))

	a, _ := f.analyses.GetByID(ctx, f.evt.AnalysisID)
	assert.Equal(t, domain.StatusDone, a.Status)
	assert.Equal(t, 3, flaky.calls, "две неудачи и успех")
	assert.Empty(t, f.dlq.events)
}

func TestProcessor_ExhaustsAttemptsAndGoesToDLQ(t *testing.T) {
	ctx := context.Background()
	broken := &fakeAnalyzer{results: []func() (*mlclient.Result, error){
		func() (*mlclient.Result, error) { return nil, errors.New("ml down") },
	}}
	p, f := newProcessorFixture(t, broken, 3)

	// Handle не возвращает ошибку: сообщение должно закоммититься, а не крутиться вечно.
	require.NoError(t, p.Handle(ctx, f.evt))

	a, _ := f.analyses.GetByID(ctx, f.evt.AnalysisID)
	assert.Equal(t, domain.StatusFailed, a.Status)
	require.NotNil(t, a.ErrorMsg)
	assert.Contains(t, *a.ErrorMsg, "ml down")
	assert.Equal(t, 3, broken.calls)

	require.Len(t, f.dlq.events, 1)
	dlq := f.dlq.events[0].(domain.ImageFailedEvent)
	assert.Equal(t, f.evt.AnalysisID, dlq.AnalysisID)
	assert.Equal(t, 3, dlq.Attempts)
}

func TestProcessor_InvalidImageNotRetried(t *testing.T) {
	ctx := context.Background()
	bad := &fakeAnalyzer{results: []func() (*mlclient.Result, error){
		func() (*mlclient.Result, error) { return nil, mlclient.ErrInvalidImage },
	}}
	p, f := newProcessorFixture(t, bad, 5)

	require.NoError(t, p.Handle(ctx, f.evt))
	a, _ := f.analyses.GetByID(ctx, f.evt.AnalysisID)
	assert.Equal(t, domain.StatusFailed, a.Status)
	assert.Equal(t, 1, bad.calls, "битое изображение не ретраим")
	assert.Len(t, f.dlq.events, 1)
}

func TestProcessor_IdempotentOnRedelivery(t *testing.T) {
	ctx := context.Background()
	p, f := newProcessorFixture(t, &fakeAnalyzer{results: []func() (*mlclient.Result, error){okResult}}, 3)

	require.NoError(t, p.Handle(ctx, f.evt))
	require.NoError(t, p.Handle(ctx, f.evt), "повторная доставка — не ошибка")

	assert.Equal(t, 1, f.analyzer.calls, "модель вызвана один раз")
	a, _ := f.analyses.GetByID(ctx, f.evt.AnalysisID)
	assert.Equal(t, 1, a.Attempts)
}

func TestProcessor_StorageDownReturnsErrorForRedelivery(t *testing.T) {
	ctx := context.Background()
	p, f := newProcessorFixture(t, &fakeAnalyzer{results: []func() (*mlclient.Result, error){okResult}}, 3)
	f.storage.getErr = errors.New("minio down")

	err := p.Handle(ctx, f.evt)
	require.Error(t, err, "инфраструктурная ошибка -> Kafka должна перечитать сообщение")

	a, _ := f.analyses.GetByID(ctx, f.evt.AnalysisID)
	assert.Equal(t, domain.StatusPending, a.Status, "возвращён в pending")
	assert.Equal(t, 0, f.analyzer.calls)
}

func TestProcessor_NoFaceIsStillDone(t *testing.T) {
	ctx := context.Background()
	noFace := &fakeAnalyzer{results: []func() (*mlclient.Result, error){
		func() (*mlclient.Result, error) { return &mlclient.Result{FaceDetected: false}, nil },
	}}
	p, f := newProcessorFixture(t, noFace, 3)

	require.NoError(t, p.Handle(ctx, f.evt))
	a, _ := f.analyses.GetByID(ctx, f.evt.AnalysisID)
	assert.Equal(t, domain.StatusDone, a.Status)
	require.NotNil(t, a.FaceDetected)
	assert.False(t, *a.FaceDetected)
	assert.Nil(t, a.Dominant)
}

func TestProcessor_InvalidatesStatsCache(t *testing.T) {
	ctx := context.Background()
	p, f := newProcessorFixture(t, &fakeAnalyzer{results: []func() (*mlclient.Result, error){okResult}}, 3)

	key := StatsCacheKey(f.evt.UserID.String(), 7)
	require.NoError(t, f.cache.SetJSON(ctx, key, map[string]int{"x": 1}, time.Minute))
	require.NoError(t, f.cache.SetJSON(ctx, AdminStatsCacheKey, map[string]int{"x": 1}, time.Minute))

	require.NoError(t, p.Handle(ctx, f.evt))

	var dst map[string]int
	assert.Error(t, f.cache.GetJSON(ctx, key, &dst), "кэш пользователя сброшен")
	assert.Error(t, f.cache.GetJSON(ctx, AdminStatsCacheKey, &dst), "кэш админки сброшен")
}
