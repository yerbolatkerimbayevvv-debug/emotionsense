// Package handler — HTTP-хендлеры. Они только парсят вход, вызывают сервис
// и формируют ответ; бизнес-правил здесь нет.
package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/apperror"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/dto"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/transport/http/middleware"
)

// AuthUsecase — что хендлеру нужно от сервиса аутентификации.
type AuthUsecase interface {
	Register(ctx context.Context, req dto.RegisterRequest) (*dto.TokenPair, *domain.User, error)
	Login(ctx context.Context, req dto.LoginRequest) (*dto.TokenPair, error)
	Refresh(ctx context.Context, refreshToken string) (*dto.TokenPair, error)
	Logout(ctx context.Context, userID uuid.UUID) error
	Me(ctx context.Context, userID uuid.UUID) (*domain.User, error)
}

// AuthHandler обслуживает /auth/*.
type AuthHandler struct {
	svc AuthUsecase
}

func NewAuthHandler(svc AuthUsecase) *AuthHandler { return &AuthHandler{svc: svc} }

// Register godoc
// POST /auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var req dto.RegisterRequest
	if !bindJSON(c, &req) {
		return
	}
	pair, user, err := h.svc.Register(c.Request.Context(), req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user": dto.UserFromDomain(user), "tokens": pair})
}

// Login — POST /auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if !bindJSON(c, &req) {
		return
	}
	pair, err := h.svc.Login(c.Request.Context(), req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, pair)
}

// Refresh — POST /auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req dto.RefreshRequest
	if !bindJSON(c, &req) {
		return
	}
	pair, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, pair)
}

// Logout — POST /auth/logout (требует access-токен)
func (h *AuthHandler) Logout(c *gin.Context) {
	if err := h.svc.Logout(c.Request.Context(), middleware.MustUserID(c)); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Me — GET /auth/me
func (h *AuthHandler) Me(c *gin.Context) {
	u, err := h.svc.Me(c.Request.Context(), middleware.MustUserID(c))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.UserFromDomain(u))
}

// --- helpers ---

// bindJSON парсит и валидирует тело; при ошибке регистрирует apperror с деталями.
func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		_ = c.Error(validationError(err))
		return false
	}
	return true
}

// bindQuery парсит и валидирует query-параметры.
func bindQuery(c *gin.Context, dst any) bool {
	if err := c.ShouldBindWith(dst, binding.Query); err != nil {
		_ = c.Error(validationError(err))
		return false
	}
	return true
}

// validationError превращает ошибки validator в понятный клиенту формат:
// {"details": {"email": "email", "password": "min"}}.
func validationError(err error) error {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		details := make(map[string]any, len(verrs))
		for _, fe := range verrs {
			details[jsonFieldName(fe)] = fe.Tag()
		}
		return apperror.InvalidInput("validation failed").WithDetails(details)
	}
	return apperror.InvalidInput("malformed request body")
}

func jsonFieldName(fe validator.FieldError) string {
	// fe.Field() — имя Go-поля; namespace содержит путь. Для плоских DTO
	// достаточно привести к snake_case-подобному виду через нижний регистр.
	name := fe.Field()
	out := make([]byte, 0, len(name)+4)
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if ch >= 'A' && ch <= 'Z' {
			if i > 0 {
				out = append(out, '_')
			}
			ch += 'a' - 'A'
		}
		out = append(out, ch)
	}
	return string(out)
}

func parseUUIDParam(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		_ = c.Error(apperror.InvalidInput("invalid " + name))
		return uuid.Nil, false
	}
	return id, true
}
