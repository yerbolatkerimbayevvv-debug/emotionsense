// Package dto описывает структуры запросов и ответов HTTP API.
//
// DTO отделены от domain: клиентский контракт может меняться независимо
// от внутренней модели, а теги binding задают правила валидации.
package dto

import (
	"time"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
)

// --- Auth ---

type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email,max=255"`
	Password string `json:"password" binding:"required,min=8,max=72"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // секунды до истечения access-токена
	TokenType    string `json:"token_type"`
}

type UserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func UserFromDomain(u *domain.User) UserResponse {
	return UserResponse{
		ID:        u.ID.String(),
		Email:     u.Email,
		Role:      string(u.Role),
		CreatedAt: u.CreatedAt,
	}
}

// --- Analyses ---

type EmotionScore struct {
	Emotion string  `json:"emotion"`
	Score   float64 `json:"score"`
}

type AnalysisResponse struct {
	ID           string         `json:"id"`
	Status       string         `json:"status"`
	FaceDetected *bool          `json:"face_detected,omitempty"`
	Dominant     *string        `json:"dominant,omitempty"`
	Scores       []EmotionScore `json:"scores,omitempty"`
	Error        *string        `json:"error,omitempty"`
	Attempts     int            `json:"attempts"`
	CreatedAt    time.Time      `json:"created_at"`
	FinishedAt   *time.Time     `json:"finished_at,omitempty"`
}

func AnalysisFromDomain(a *domain.Analysis) AnalysisResponse {
	resp := AnalysisResponse{
		ID:           a.ID.String(),
		Status:       string(a.Status),
		FaceDetected: a.FaceDetected,
		Dominant:     a.Dominant,
		Error:        a.ErrorMsg,
		Attempts:     a.Attempts,
		CreatedAt:    a.CreatedAt,
		FinishedAt:   a.FinishedAt,
	}
	for _, s := range a.Scores {
		resp.Scores = append(resp.Scores, EmotionScore{Emotion: s.Emotion, Score: s.Score})
	}
	return resp
}

type AnalysisAccepted struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type ListQuery struct {
	Page  int `form:"page,default=1" binding:"min=1"`
	Limit int `form:"limit,default=20" binding:"min=1,max=100"`
}

type Page[T any] struct {
	Items []T   `json:"items"`
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// --- Stats ---

type StatsQuery struct {
	// Период в днях: 1..365. По умолчанию 7.
	Days int `form:"days,default=7" binding:"min=1,max=365"`
}

type EmotionStat struct {
	Emotion string  `json:"emotion"`
	Count   int64   `json:"count"`
	Share   float64 `json:"share"`
}

type StatsResponse struct {
	Days     int           `json:"days"`
	Total    int64         `json:"total"`
	Emotions []EmotionStat `json:"emotions"`
}

type AdminStatsResponse struct {
	Users       int64         `json:"users"`
	Analyses    int64         `json:"analyses"`
	Done        int64         `json:"done"`
	Failed      int64         `json:"failed"`
	Pending     int64         `json:"pending"`
	TopEmotions []EmotionStat `json:"top_emotions"`
	GeneratedAt time.Time     `json:"generated_at"`
}

// --- Common ---

type ErrorResponse struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type HealthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}
