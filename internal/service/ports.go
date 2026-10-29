// Package service — бизнес-логика приложения.
//
// Сервисы зависят только от интерфейсов (портов), объявленных здесь.
// Конкретные реализации (Postgres, Redis, MinIO, Kafka, gRPC) подключаются
// в main. Это позволяет тестировать логику без инфраструктуры.
package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/mlclient"
)

// UserRepository — хранилище пользователей и refresh-токенов.
type UserRepository interface {
	Create(ctx context.Context, u *domain.User) error
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	List(ctx context.Context, offset, limit int) ([]domain.User, int64, error)
	Count(ctx context.Context) (int64, error)

	SaveRefreshToken(ctx context.Context, tokenHash string, userID uuid.UUID, expiresAt time.Time) error
	ConsumeRefreshToken(ctx context.Context, tokenHash string) (uuid.UUID, error)
	DeleteUserRefreshTokens(ctx context.Context, userID uuid.UUID) error
}

// AnalysisRepository — хранилище анализов.
type AnalysisRepository interface {
	Create(ctx context.Context, a *domain.Analysis) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Analysis, error)
	ListByUser(ctx context.Context, userID uuid.UUID, offset, limit int) ([]domain.Analysis, int64, error)
	Delete(ctx context.Context, id, userID uuid.UUID) error

	TryMarkProcessing(ctx context.Context, id uuid.UUID) (bool, error)
	MarkPending(ctx context.Context, id uuid.UUID, errMsg string) error
	SaveResult(ctx context.Context, id uuid.UUID, faceDetected bool, dominant string, scores []domain.EmotionScore) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error

	UserEmotionStats(ctx context.Context, userID uuid.UUID, since time.Time) ([]domain.EmotionStat, int64, error)
	GlobalEmotionStats(ctx context.Context, since time.Time) ([]domain.EmotionStat, int64, error)
	StatusCounts(ctx context.Context) (map[domain.AnalysisStatus]int64, error)
}

// StatusCache — быстрый доступ к статусам и кэш.
type StatusCache interface {
	SetStatus(ctx context.Context, id string, status domain.AnalysisStatus) error
	GetStatus(ctx context.Context, id string) (domain.AnalysisStatus, error)
	DeleteStatus(ctx context.Context, id string) error
	SetJSON(ctx context.Context, key string, v any, ttl time.Duration) error
	GetJSON(ctx context.Context, key string, dst any) error
	Delete(ctx context.Context, keys ...string) error
}

// ObjectStorage — хранилище файлов.
type ObjectStorage interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// EventPublisher — публикация событий в очередь.
type EventPublisher interface {
	Publish(ctx context.Context, key string, value any) error
}

// Analyzer — модель распознавания (алиас для удобства импорта).
type Analyzer = mlclient.Analyzer

// TokenIssuer — выпуск access-токенов.
type TokenIssuer interface {
	Issue(userID, role string) (string, error)
	TTL() time.Duration
}
