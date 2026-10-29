package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/yerbolat/emotionsense/internal/domain"
	"github.com/yerbolat/emotionsense/internal/mlclient"
)

// Processor — обработка события из очереди (сторона worker).
//
// Гарантии:
//   - идемпотентность: повторная доставка того же события не приводит к
//     двойной обработке (TryMarkProcessing атомарно меняет статус);
//   - ретраи с экспоненциальной задержкой при временных сбоях ML;
//   - после MaxAttempts — статус failed и событие в DLQ.
type Processor struct {
	analyses    AnalysisRepository
	cache       StatusCache
	storage     ObjectStorage
	analyzer    Analyzer
	dlq         EventPublisher
	maxAttempts int
	retryBase   time.Duration
	log         *slog.Logger
}

// NewProcessor создаёт обработчик.
func NewProcessor(analyses AnalysisRepository, cache StatusCache, storage ObjectStorage, analyzer Analyzer, dlq EventPublisher, maxAttempts int, retryBase time.Duration, log *slog.Logger) *Processor {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if retryBase <= 0 {
		retryBase = time.Second
	}
	return &Processor{
		analyses: analyses, cache: cache, storage: storage, analyzer: analyzer, dlq: dlq,
		maxAttempts: maxAttempts, retryBase: retryBase, log: log,
	}
}

// Handle обрабатывает одно событие. Возвращает ошибку только если сообщение
// нужно перечитать из Kafka (например, недоступна БД). Ошибки инференса
// обрабатываются внутри через ретраи и не поднимаются наверх.
func (p *Processor) Handle(ctx context.Context, evt domain.ImageUploadedEvent) error {
	log := p.log.With("analysis_id", evt.AnalysisID, "user_id", evt.UserID)

	ok, err := p.analyses.TryMarkProcessing(ctx, evt.AnalysisID)
	if err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}
	if !ok {
		log.Info("analysis is not pending, skipping (duplicate or deleted)")
		return nil
	}
	_ = p.cache.SetStatus(ctx, evt.AnalysisID.String(), domain.StatusProcessing)

	img, err := p.storage.Get(ctx, evt.ImageKey)
	if err != nil {
		// Хранилище недоступно — вернём в pending и дадим Kafka перечитать.
		_ = p.analyses.MarkPending(ctx, evt.AnalysisID, "storage unavailable")
		_ = p.cache.SetStatus(ctx, evt.AnalysisID.String(), domain.StatusPending)
		return fmt.Errorf("get image: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		res, err := p.analyzer.Analyze(ctx, img, evt.AnalysisID.String())
		if err == nil {
			return p.finish(ctx, evt, res, log)
		}
		lastErr = err

		if errors.Is(err, mlclient.ErrInvalidImage) {
			// Повторять бессмысленно: файл битый.
			break
		}
		if ctx.Err() != nil {
			// Остановка воркера: вернуть в pending, сообщение будет перечитано.
			_ = p.analyses.MarkPending(ctx, evt.AnalysisID, "worker shutdown")
			_ = p.cache.SetStatus(ctx, evt.AnalysisID.String(), domain.StatusPending)
			return ctx.Err()
		}

		delay := p.retryBase * time.Duration(1<<(attempt-1)) // 1x, 2x, 4x ...
		log.Warn("inference failed, retrying", "attempt", attempt, "delay", delay, "err", err)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			_ = p.analyses.MarkPending(ctx, evt.AnalysisID, "worker shutdown")
			return ctx.Err()
		}
	}

	return p.fail(ctx, evt, lastErr, log)
}

func (p *Processor) finish(ctx context.Context, evt domain.ImageUploadedEvent, res *mlclient.Result, log *slog.Logger) error {
	if err := p.analyses.SaveResult(ctx, evt.AnalysisID, res.FaceDetected, res.Dominant, res.Scores); err != nil {
		return fmt.Errorf("save result: %w", err)
	}
	_ = p.cache.SetStatus(ctx, evt.AnalysisID.String(), domain.StatusDone)
	// Инвалидируем кэш статистики пользователя.
	_ = p.cache.Delete(ctx, append(UserStatsCacheKeys(evt.UserID.String()), AdminStatsCacheKey)...)
	log.Info("analysis done", "dominant", res.Dominant, "face_detected", res.FaceDetected, "inference_ms", res.InferenceMS)
	return nil
}

func (p *Processor) fail(ctx context.Context, evt domain.ImageUploadedEvent, cause error, log *slog.Logger) error {
	reason := "inference failed"
	if cause != nil {
		reason = cause.Error()
	}
	if err := p.analyses.MarkFailed(ctx, evt.AnalysisID, reason); err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	_ = p.cache.SetStatus(ctx, evt.AnalysisID.String(), domain.StatusFailed)
	_ = p.cache.Delete(ctx, AdminStatsCacheKey)

	if p.dlq != nil {
		dlqEvt := domain.ImageFailedEvent{
			AnalysisID: evt.AnalysisID, UserID: evt.UserID, ImageKey: evt.ImageKey,
			Attempts: p.maxAttempts, Reason: reason, FailedAt: time.Now().UTC(),
		}
		if err := p.dlq.Publish(ctx, evt.UserID.String(), dlqEvt); err != nil {
			log.Error("publish to DLQ failed", "err", err)
		}
	}
	log.Error("analysis failed permanently", "reason", reason)
	return nil
}
