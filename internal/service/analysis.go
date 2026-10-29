package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // регистрация декодеров для проверки формата
	_ "image/png"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/apperror"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/repository/postgres"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/repository/redis"
)

// AnalysisService — приём изображений и чтение результатов (сторона API).
type AnalysisService struct {
	analyses  AnalysisRepository
	cache     StatusCache
	storage   ObjectStorage
	publisher EventPublisher
	log       *slog.Logger
}

// NewAnalysisService создаёт сервис.
func NewAnalysisService(analyses AnalysisRepository, cache StatusCache, storage ObjectStorage, publisher EventPublisher, log *slog.Logger) *AnalysisService {
	return &AnalysisService{analyses: analyses, cache: cache, storage: storage, publisher: publisher, log: log}
}

// allowedFormats — что принимаем от клиента.
var allowedFormats = map[string]string{
	"jpeg": "image/jpeg",
	"png":  "image/png",
}

// Submit принимает изображение: валидирует, кладёт в хранилище, создаёт запись
// и публикует событие. Возвращает анализ в статусе pending.
func (s *AnalysisService) Submit(ctx context.Context, userID uuid.UUID, data []byte) (*domain.Analysis, error) {
	if len(data) == 0 {
		return nil, apperror.InvalidInput("image is empty")
	}
	// Проверяем, что это действительно картинка, а не произвольный файл.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, apperror.InvalidInput("file is not a valid JPEG or PNG image")
	}
	contentType, ok := allowedFormats[format]
	if !ok {
		return nil, apperror.InvalidInput("unsupported image format: " + format)
	}
	if cfg.Width < 48 || cfg.Height < 48 {
		return nil, apperror.InvalidInput("image is too small, minimum 48x48")
	}

	id := uuid.New()
	key := fmt.Sprintf("%s/%s.%s", userID, id, format)

	if err := s.storage.Put(ctx, key, data, contentType); err != nil {
		return nil, apperror.Unavailable("storage is unavailable", err)
	}

	a := &domain.Analysis{ID: id, UserID: userID, ImageKey: key, Status: domain.StatusPending}
	if err := s.analyses.Create(ctx, a); err != nil {
		// Компенсируем: файл без записи в БД никому не нужен.
		_ = s.storage.Delete(ctx, key)
		return nil, apperror.Internal(err)
	}

	if err := s.cache.SetStatus(ctx, id.String(), domain.StatusPending); err != nil {
		// Кэш не критичен: статус всегда можно прочитать из БД.
		s.log.Warn("set status in cache failed", "err", err, "analysis_id", id)
	}

	evt := domain.ImageUploadedEvent{
		AnalysisID: id, UserID: userID, ImageKey: key, Attempt: 1, OccurredAt: time.Now().UTC(),
	}
	if err := s.publisher.Publish(ctx, userID.String(), evt); err != nil {
		// Без события воркер никогда не обработает задачу — это ошибка.
		_ = s.analyses.MarkFailed(ctx, id, "failed to enqueue")
		_ = s.cache.SetStatus(ctx, id.String(), domain.StatusFailed)
		return nil, apperror.Unavailable("queue is unavailable", err)
	}

	return a, nil
}

// Get возвращает анализ пользователя. Статус сначала проверяется в кэше:
// пока задача не завершена, нет смысла ходить в БД за результатом.
func (s *AnalysisService) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Analysis, error) {
	a, err := s.analyses.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return nil, apperror.NotFound("analysis not found")
		}
		return nil, apperror.Internal(err)
	}
	if a.UserID != userID {
		// Не раскрываем существование чужих ресурсов.
		return nil, apperror.NotFound("analysis not found")
	}
	return a, nil
}

// Status — лёгкая проверка статуса: сначала Redis, потом БД.
func (s *AnalysisService) Status(ctx context.Context, userID, id uuid.UUID) (domain.AnalysisStatus, error) {
	if st, err := s.cache.GetStatus(ctx, id.String()); err == nil {
		return st, nil
	} else if !errors.Is(err, redis.ErrCacheMiss) {
		s.log.Warn("cache get status failed", "err", err)
	}
	a, err := s.Get(ctx, userID, id)
	if err != nil {
		return "", err
	}
	_ = s.cache.SetStatus(ctx, id.String(), a.Status)
	return a.Status, nil
}

// List возвращает историю анализов пользователя.
func (s *AnalysisService) List(ctx context.Context, userID uuid.UUID, page, limit int) ([]domain.Analysis, int64, error) {
	offset := (page - 1) * limit
	items, total, err := s.analyses.ListByUser(ctx, userID, offset, limit)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	return items, total, nil
}

// Delete удаляет анализ и его файл.
func (s *AnalysisService) Delete(ctx context.Context, userID, id uuid.UUID) error {
	a, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := s.analyses.Delete(ctx, id, userID); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return apperror.NotFound("analysis not found")
		}
		return apperror.Internal(err)
	}
	_ = s.cache.DeleteStatus(ctx, id.String())
	if err := s.storage.Delete(ctx, a.ImageKey); err != nil {
		s.log.Warn("delete image failed", "err", err, "key", a.ImageKey)
	}
	return nil
}
