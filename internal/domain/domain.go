// Package domain содержит сущности предметной области.
//
// Сущности не знают ни про HTTP, ни про SQL: это чистые структуры Go,
// вокруг которых строятся сервисы и репозитории.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role — роль пользователя.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// User — зарегистрированный пользователь.
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Role         Role
	CreatedAt    time.Time
}

// AnalysisStatus — статус жизненного цикла анализа.
type AnalysisStatus string

const (
	StatusPending    AnalysisStatus = "pending"    // принят, ждёт воркера
	StatusProcessing AnalysisStatus = "processing" // воркер взял в работу
	StatusDone       AnalysisStatus = "done"       // результат готов
	StatusFailed     AnalysisStatus = "failed"     // исчерпаны попытки
)

// IsTerminal сообщает, завершён ли анализ.
func (s AnalysisStatus) IsTerminal() bool {
	return s == StatusDone || s == StatusFailed
}

// EmotionScore — вероятность одной эмоции.
type EmotionScore struct {
	Emotion string
	Score   float64
}

// Analysis — запрос на распознавание и его результат.
type Analysis struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	ImageKey     string
	Status       AnalysisStatus
	FaceDetected *bool
	Dominant     *string
	Scores       []EmotionScore
	ErrorMsg     *string
	Attempts     int
	CreatedAt    time.Time
	FinishedAt   *time.Time
}

// EmotionStat — агрегат для статистики: сколько раз эмоция была доминирующей.
type EmotionStat struct {
	Emotion string
	Count   int64
	Share   float64 // доля от общего числа, 0..1
}

// ImageUploadedEvent — событие Kafka: изображение загружено, нужно обработать.
type ImageUploadedEvent struct {
	AnalysisID uuid.UUID `json:"analysis_id"`
	UserID     uuid.UUID `json:"user_id"`
	ImageKey   string    `json:"image_key"`
	Attempt    int       `json:"attempt"`
	OccurredAt time.Time `json:"occurred_at"`
}

// ImageFailedEvent — событие DLQ: обработка не удалась окончательно.
type ImageFailedEvent struct {
	AnalysisID uuid.UUID `json:"analysis_id"`
	UserID     uuid.UUID `json:"user_id"`
	ImageKey   string    `json:"image_key"`
	Attempts   int       `json:"attempts"`
	Reason     string    `json:"reason"`
	FailedAt   time.Time `json:"failed_at"`
}
