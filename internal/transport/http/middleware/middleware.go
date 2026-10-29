// Package middleware — сквозная логика HTTP: request id, логирование,
// recovery, аутентификация, авторизация по ролям, rate limiting.
package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/apperror"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/dto"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/logger"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/pkg/jwt"
)

const (
	HeaderRequestID = "X-Request-ID"

	ctxUserID = "auth.user_id"
	ctxRole   = "auth.role"
)

// RequestID проставляет X-Request-ID (берёт из запроса или генерирует).
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(HeaderRequestID)
		if rid == "" {
			rid = uuid.NewString()
		}
		c.Set(HeaderRequestID, rid)
		c.Header(HeaderRequestID, rid)
		c.Next()
	}
}

// Logger пишет структурированный access-лог и кладёт логгер с request_id в context.
func Logger(base *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		rid := c.GetString(HeaderRequestID)
		l := base.With("request_id", rid)
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context(), l))

		c.Next()

		attrs := []any{
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		}
		if uid, ok := c.Get(ctxUserID); ok {
			attrs = append(attrs, "user_id", uid)
		}
		switch {
		case c.Writer.Status() >= 500:
			l.Error("request", attrs...)
		case c.Writer.Status() >= 400:
			l.Warn("request", attrs...)
		default:
			l.Info("request", attrs...)
		}
	}
}

// Recovery ловит панику и отдаёт 500 в едином формате.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.FromContext(c.Request.Context()).Error("panic recovered", "panic", r)
				c.AbortWithStatusJSON(http.StatusInternalServerError, dto.ErrorResponse{
					Code: string(apperror.CodeInternal), Message: "internal error",
				})
			}
		}()
		c.Next()
	}
}

// ErrorHandler — централизованная обработка ошибок: хендлеры вызывают
// c.Error(err), а сюда приходит перевод в HTTP-статус и JSON.
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}
		err := c.Errors.Last().Err
		appErr := apperror.From(err)

		if appErr.Code == apperror.CodeInternal {
			logger.FromContext(c.Request.Context()).Error("internal error", "err", err)
		}
		c.JSON(StatusFor(appErr.Code), dto.ErrorResponse{
			Code:    string(appErr.Code),
			Message: appErr.Message,
			Details: appErr.Details,
		})
	}
}

// StatusFor переводит код ошибки в HTTP-статус.
func StatusFor(code apperror.Code) int {
	switch code {
	case apperror.CodeInvalidInput:
		return http.StatusBadRequest
	case apperror.CodeUnauthorized:
		return http.StatusUnauthorized
	case apperror.CodeForbidden:
		return http.StatusForbidden
	case apperror.CodeNotFound:
		return http.StatusNotFound
	case apperror.CodeConflict:
		return http.StatusConflict
	case apperror.CodeTooManyReq:
		return http.StatusTooManyRequests
	case apperror.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// TokenParser — то, что нужно middleware от JWT-менеджера.
type TokenParser interface {
	Parse(token string) (*jwt.Claims, error)
}

// Auth проверяет Bearer-токен и кладёт user_id и роль в контекст.
func Auth(parser TokenParser) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if h == "" || !strings.HasPrefix(strings.ToLower(h), "bearer ") {
			abort(c, apperror.Unauthorized("missing bearer token"))
			return
		}
		claims, err := parser.Parse(strings.TrimSpace(h[7:]))
		if err != nil {
			abort(c, apperror.Unauthorized("invalid or expired token"))
			return
		}
		uid, err := uuid.Parse(claims.UserID)
		if err != nil {
			abort(c, apperror.Unauthorized("invalid token subject"))
			return
		}
		c.Set(ctxUserID, uid)
		c.Set(ctxRole, domain.Role(claims.Role))
		c.Next()
	}
}

// RequireRole пропускает только пользователей с одной из ролей.
func RequireRole(roles ...domain.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := RoleFrom(c)
		if !ok {
			abort(c, apperror.Unauthorized("unauthenticated"))
			return
		}
		for _, r := range roles {
			if role == r {
				c.Next()
				return
			}
		}
		abort(c, apperror.Forbidden("insufficient permissions"))
	}
}

// Limiter — абстракция над Redis для rate limit.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int64, error)
}

// RateLimit ограничивает число запросов на пользователя (или IP) в минуту.
// При недоступности Redis пропускает запрос (fail-open), чтобы деградация
// кэша не ронила весь API.
func RateLimit(l Limiter, perMinute int, scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if perMinute <= 0 {
			c.Next()
			return
		}
		subject := c.ClientIP()
		if uid, ok := UserIDFrom(c); ok {
			subject = uid.String()
		}
		key := "ratelimit:" + scope + ":" + subject + ":" + time.Now().Format("200601021504")

		allowed, n, err := l.Allow(c.Request.Context(), key, perMinute, time.Minute)
		if err != nil {
			logger.FromContext(c.Request.Context()).Warn("rate limiter unavailable", "err", err)
			c.Next()
			return
		}
		c.Header("X-RateLimit-Limit", itoa(perMinute))
		remaining := int64(perMinute) - n
		if remaining < 0 {
			remaining = 0
		}
		c.Header("X-RateLimit-Remaining", itoa64(remaining))
		if !allowed {
			abort(c, apperror.TooMany("rate limit exceeded, try again later"))
			return
		}
		c.Next()
	}
}

// UserIDFrom достаёт id пользователя из контекста.
func UserIDFrom(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxUserID)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

// RoleFrom достаёт роль из контекста.
func RoleFrom(c *gin.Context) (domain.Role, bool) {
	v, ok := c.Get(ctxRole)
	if !ok {
		return "", false
	}
	r, ok := v.(domain.Role)
	return r, ok
}

// MustUserID — для хендлеров за Auth: id гарантированно есть.
func MustUserID(c *gin.Context) uuid.UUID {
	id, ok := UserIDFrom(c)
	if !ok {
		panic(errors.New("user id missing in context: Auth middleware not applied"))
	}
	return id
}

func abort(c *gin.Context, err *apperror.Error) {
	c.AbortWithStatusJSON(StatusFor(err.Code), dto.ErrorResponse{Code: string(err.Code), Message: err.Message})
}

func itoa(n int) string     { return strconv.Itoa(n) }
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
