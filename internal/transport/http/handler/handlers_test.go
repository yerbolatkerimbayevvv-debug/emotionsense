package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/apperror"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/config"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/dto"
	httptransport "github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/transport/http"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/transport/http/handler"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/pkg/jwt"
)

// --- стабы сервисов ---

type stubAuth struct {
	registerFn func(ctx context.Context, req dto.RegisterRequest) (*dto.TokenPair, *domain.User, error)
	loginFn    func(ctx context.Context, req dto.LoginRequest) (*dto.TokenPair, error)
}

func (s *stubAuth) Register(ctx context.Context, req dto.RegisterRequest) (*dto.TokenPair, *domain.User, error) {
	return s.registerFn(ctx, req)
}
func (s *stubAuth) Login(ctx context.Context, req dto.LoginRequest) (*dto.TokenPair, error) {
	return s.loginFn(ctx, req)
}
func (s *stubAuth) Refresh(context.Context, string) (*dto.TokenPair, error) {
	return &dto.TokenPair{AccessToken: "new"}, nil
}
func (s *stubAuth) Logout(context.Context, uuid.UUID) error { return nil }
func (s *stubAuth) Me(_ context.Context, id uuid.UUID) (*domain.User, error) {
	return &domain.User{ID: id, Email: "me@x", Role: domain.RoleUser}, nil
}

type stubAnalysis struct {
	submitFn func(ctx context.Context, userID uuid.UUID, data []byte) (*domain.Analysis, error)
	statusFn func(ctx context.Context, userID, id uuid.UUID) (domain.AnalysisStatus, error)
	getFn    func(ctx context.Context, userID, id uuid.UUID) (*domain.Analysis, error)
}

func (s *stubAnalysis) Submit(ctx context.Context, u uuid.UUID, d []byte) (*domain.Analysis, error) {
	return s.submitFn(ctx, u, d)
}
func (s *stubAnalysis) Get(ctx context.Context, u, id uuid.UUID) (*domain.Analysis, error) {
	return s.getFn(ctx, u, id)
}
func (s *stubAnalysis) Status(ctx context.Context, u, id uuid.UUID) (domain.AnalysisStatus, error) {
	return s.statusFn(ctx, u, id)
}
func (s *stubAnalysis) List(context.Context, uuid.UUID, int, int) ([]domain.Analysis, int64, error) {
	return nil, 0, nil
}
func (s *stubAnalysis) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }

type stubStats struct{}

func (stubStats) UserStats(_ context.Context, _ uuid.UUID, days int) (*dto.StatsResponse, error) {
	return &dto.StatsResponse{Days: days, Total: 0}, nil
}
func (stubStats) AdminStats(context.Context) (*dto.AdminStatsResponse, error) {
	return &dto.AdminStatsResponse{Users: 42}, nil
}
func (stubStats) ListUsers(context.Context, int, int) ([]domain.User, int64, error) {
	return nil, 0, nil
}

type noLimit struct{}

func (noLimit) Allow(context.Context, string, int, time.Duration) (bool, int64, error) {
	return true, 1, nil
}

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

// --- сборка тестового приложения ---

type app struct {
	router *gin.Engine
	tokens *jwt.Manager
	auth   *stubAuth
	an     *stubAnalysis
}

func newApp(t *testing.T) *app {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tokens := jwt.NewManager("test-secret-test-secret", time.Minute, "test")
	a := &app{
		tokens: tokens,
		auth: &stubAuth{
			registerFn: func(_ context.Context, req dto.RegisterRequest) (*dto.TokenPair, *domain.User, error) {
				return &dto.TokenPair{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer"},
					&domain.User{ID: uuid.New(), Email: req.Email, Role: domain.RoleUser}, nil
			},
			loginFn: func(context.Context, dto.LoginRequest) (*dto.TokenPair, error) {
				return nil, apperror.Unauthorized("invalid email or password")
			},
		},
		an: &stubAnalysis{
			submitFn: func(_ context.Context, u uuid.UUID, _ []byte) (*domain.Analysis, error) {
				return &domain.Analysis{ID: uuid.New(), UserID: u, Status: domain.StatusPending}, nil
			},
			statusFn: func(context.Context, uuid.UUID, uuid.UUID) (domain.AnalysisStatus, error) {
				return domain.StatusProcessing, nil
			},
			getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Analysis, error) {
				return nil, apperror.NotFound("analysis not found")
			},
		},
	}

	cfg := &config.Config{App: config.App{Env: config.EnvDev}, HTTP: config.HTTP{MaxUploadBytes: 1 << 20, RateLimitPerMin: 100}}
	a.router = httptransport.NewRouter(httptransport.Deps{
		Cfg:      cfg,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:     handler.NewAuthHandler(a.auth),
		Analysis: handler.NewAnalysisHandler(a.an, cfg.HTTP.MaxUploadBytes),
		Stats:    handler.NewStatsHandler(stubStats{}),
		Health:   handler.NewHealthHandler(map[string]handler.Pinger{"db": okPinger{}}),
		Tokens:   tokens,
		Limiter:  noLimit{},
	})
	return a
}

func (a *app) do(t *testing.T, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, req)
	return w
}

func (a *app) tokenFor(t *testing.T, role domain.Role) string {
	t.Helper()
	tok, err := a.tokens.Issue(uuid.NewString(), string(role))
	require.NoError(t, err)
	return tok
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) dto.ErrorResponse {
	t.Helper()
	var e dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e), w.Body.String())
	return e
}

// --- тесты ---

func TestHealth(t *testing.T) {
	a := newApp(t)
	w := a.do(t, http.MethodGet, "/health", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	w = a.do(t, http.MethodGet, "/ready", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"db":"ok"`)
}

func TestRegister_Validation(t *testing.T) {
	a := newApp(t)

	w := a.do(t, http.MethodPost, "/api/v1/auth/register", dto.RegisterRequest{Email: "not-an-email", Password: "short"}, "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	e := decodeErr(t, w)
	assert.Equal(t, "INVALID_INPUT", e.Code)
	assert.Equal(t, "email", e.Details["email"])
	assert.Equal(t, "min", e.Details["password"])

	w = a.do(t, http.MethodPost, "/api/v1/auth/register", dto.RegisterRequest{Email: "ok@example.com", Password: "longenough"}, "")
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"access_token":"a"`)
	assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
}

func TestRegister_ConflictMapsTo409(t *testing.T) {
	a := newApp(t)
	a.auth.registerFn = func(context.Context, dto.RegisterRequest) (*dto.TokenPair, *domain.User, error) {
		return nil, nil, apperror.Conflict("email already registered")
	}
	w := a.do(t, http.MethodPost, "/api/v1/auth/register", dto.RegisterRequest{Email: "ok@example.com", Password: "longenough"}, "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "CONFLICT", decodeErr(t, w).Code)
}

func TestLogin_Unauthorized(t *testing.T) {
	a := newApp(t)
	w := a.do(t, http.MethodPost, "/api/v1/auth/login", dto.LoginRequest{Email: "x@example.com", Password: "p"}, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestInternalErrorIsMasked(t *testing.T) {
	a := newApp(t)
	a.auth.loginFn = func(context.Context, dto.LoginRequest) (*dto.TokenPair, error) {
		return nil, errors.New("pq: connection refused with secret dsn")
	}
	w := a.do(t, http.MethodPost, "/api/v1/auth/login", dto.LoginRequest{Email: "x@example.com", Password: "p"}, "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	e := decodeErr(t, w)
	assert.Equal(t, "INTERNAL", e.Code)
	assert.NotContains(t, w.Body.String(), "secret dsn", "детали внутренней ошибки не утекают клиенту")
}

func TestProtectedRoutesRequireToken(t *testing.T) {
	a := newApp(t)
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/analyses", "/api/v1/stats/me"} {
		w := a.do(t, http.MethodGet, path, nil, "")
		assert.Equal(t, http.StatusUnauthorized, w.Code, path)
	}
	w := a.do(t, http.MethodGet, "/api/v1/auth/me", nil, "garbage.token.here")
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	w = a.do(t, http.MethodGet, "/api/v1/auth/me", nil, a.tokenFor(t, domain.RoleUser))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAdminRequiresRole(t *testing.T) {
	a := newApp(t)
	w := a.do(t, http.MethodGet, "/api/v1/admin/stats", nil, a.tokenFor(t, domain.RoleUser))
	assert.Equal(t, http.StatusForbidden, w.Code)

	w = a.do(t, http.MethodGet, "/api/v1/admin/stats", nil, a.tokenFor(t, domain.RoleAdmin))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"users":42`)
}

func TestSubmitAnalysis_Multipart(t *testing.T) {
	a := newApp(t)
	token := a.tokenFor(t, domain.RoleUser)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("image", "face.jpg")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("fake-bytes"))
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyses", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "/api/v1/analyses/")
	var resp dto.AnalysisAccepted
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "pending", resp.Status)

	t.Run("missing file field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/analyses", bytes.NewReader(nil))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestGetAnalysis_FastPathWhileProcessing(t *testing.T) {
	a := newApp(t)
	id := uuid.New()
	getCalled := false
	a.an.getFn = func(context.Context, uuid.UUID, uuid.UUID) (*domain.Analysis, error) {
		getCalled = true
		return nil, nil
	}

	w := a.do(t, http.MethodGet, "/api/v1/analyses/"+id.String(), nil, a.tokenFor(t, domain.RoleUser))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"processing"`)
	assert.False(t, getCalled, "пока задача в работе, в БД за результатом не ходим")

	t.Run("done -> full result", func(t *testing.T) {
		dom := "happy"
		a.an.statusFn = func(context.Context, uuid.UUID, uuid.UUID) (domain.AnalysisStatus, error) {
			return domain.StatusDone, nil
		}
		a.an.getFn = func(_ context.Context, u, id uuid.UUID) (*domain.Analysis, error) {
			return &domain.Analysis{ID: id, UserID: u, Status: domain.StatusDone, Dominant: &dom,
				Scores: []domain.EmotionScore{{Emotion: "happy", Score: 0.9}}}, nil
		}
		w := a.do(t, http.MethodGet, "/api/v1/analyses/"+id.String(), nil, a.tokenFor(t, domain.RoleUser))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"dominant":"happy"`)
	})

	t.Run("bad uuid", func(t *testing.T) {
		w := a.do(t, http.MethodGet, "/api/v1/analyses/not-a-uuid", nil, a.tokenFor(t, domain.RoleUser))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestStatsQueryValidation(t *testing.T) {
	a := newApp(t)
	tok := a.tokenFor(t, domain.RoleUser)

	w := a.do(t, http.MethodGet, "/api/v1/stats/me?days=0", nil, tok)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = a.do(t, http.MethodGet, "/api/v1/stats/me", nil, tok)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"days":7`)
}

func TestSwaggerServed(t *testing.T) {
	a := newApp(t)
	w := a.do(t, http.MethodGet, "/swagger/openapi.yaml", nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "openapi: 3.0.3")
}
