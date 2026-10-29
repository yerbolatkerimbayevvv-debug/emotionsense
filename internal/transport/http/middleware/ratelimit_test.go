package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/yerbolat/emotionsense/internal/apperror"
)

type countingLimiter struct {
	limit int
	n     map[string]int64
	err   error
}

func (l *countingLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, int64, error) {
	if l.err != nil {
		return false, 0, l.err
	}
	if l.n == nil {
		l.n = map[string]int64{}
	}
	l.n[key]++
	return l.n[key] <= int64(limit), l.n[key], nil
}

func TestRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lim := &countingLimiter{}
	r := gin.New()
	r.Use(RequestID(), RateLimit(lim, 2, "test"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	hit := func() int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		r.ServeHTTP(w, req)
		return w.Code
	}
	assert.Equal(t, http.StatusOK, hit())
	assert.Equal(t, http.StatusOK, hit())
	assert.Equal(t, http.StatusTooManyRequests, hit(), "третий запрос в минуту отклонён")
}

func TestRateLimit_FailOpenWhenRedisDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lim := &countingLimiter{err: errors.New("redis down")}
	r := gin.New()
	r.Use(RequestID(), RateLimit(lim, 1, "test"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, http.StatusOK, w.Code, "недоступный Redis не должен блокировать API")
	}
}

func TestStatusFor(t *testing.T) {
	cases := map[string]int{
		"INVALID_INPUT":     400,
		"UNAUTHORIZED":      401,
		"FORBIDDEN":         403,
		"NOT_FOUND":         404,
		"CONFLICT":          409,
		"TOO_MANY_REQUESTS": 429,
		"UNAVAILABLE":       503,
		"INTERNAL":          500,
		"SOMETHING_ELSE":    500,
	}
	for code, want := range cases {
		assert.Equal(t, want, StatusFor(apperror.Code(code)), code)
	}
}
