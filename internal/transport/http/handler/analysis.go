package handler

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/apperror"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/dto"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/transport/http/middleware"
)

// AnalysisUsecase — что хендлеру нужно от сервиса анализов.
type AnalysisUsecase interface {
	Submit(ctx context.Context, userID uuid.UUID, data []byte) (*domain.Analysis, error)
	Get(ctx context.Context, userID, id uuid.UUID) (*domain.Analysis, error)
	Status(ctx context.Context, userID, id uuid.UUID) (domain.AnalysisStatus, error)
	List(ctx context.Context, userID uuid.UUID, page, limit int) ([]domain.Analysis, int64, error)
	Delete(ctx context.Context, userID, id uuid.UUID) error
}

// AnalysisHandler обслуживает /analyses/*.
type AnalysisHandler struct {
	svc       AnalysisUsecase
	maxUpload int64
}

func NewAnalysisHandler(svc AnalysisUsecase, maxUpload int64) *AnalysisHandler {
	return &AnalysisHandler{svc: svc, maxUpload: maxUpload}
}

// Submit — POST /analyses (multipart/form-data, поле image). Возвращает 202.
func (h *AnalysisHandler) Submit(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxUpload)

	fh, err := c.FormFile("image")
	if err != nil {
		_ = c.Error(apperror.InvalidInput("field 'image' is required (multipart/form-data)"))
		return
	}
	if fh.Size > h.maxUpload {
		_ = c.Error(apperror.InvalidInput("image is too large"))
		return
	}
	f, err := fh.Open()
	if err != nil {
		_ = c.Error(apperror.InvalidInput("cannot read uploaded file"))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, h.maxUpload+1))
	if err != nil {
		_ = c.Error(apperror.InvalidInput("cannot read uploaded file"))
		return
	}
	if int64(len(data)) > h.maxUpload {
		_ = c.Error(apperror.InvalidInput("image is too large"))
		return
	}

	a, err := h.svc.Submit(c.Request.Context(), middleware.MustUserID(c), data)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.Header("Location", "/api/v1/analyses/"+a.ID.String())
	c.JSON(http.StatusAccepted, dto.AnalysisAccepted{ID: a.ID.String(), Status: string(a.Status)})
}

// Get — GET /analyses/:id
func (h *AnalysisHandler) Get(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	userID := middleware.MustUserID(c)

	// Быстрый путь: если задача ещё в работе, отвечаем из кэша, не трогая БД.
	st, err := h.svc.Status(c.Request.Context(), userID, id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if !st.IsTerminal() {
		c.JSON(http.StatusOK, dto.AnalysisResponse{ID: id.String(), Status: string(st)})
		return
	}

	a, err := h.svc.Get(c.Request.Context(), userID, id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.AnalysisFromDomain(a))
}

// List — GET /analyses?page=&limit=
func (h *AnalysisHandler) List(c *gin.Context) {
	var q dto.ListQuery
	if !bindQuery(c, &q) {
		return
	}
	items, total, err := h.svc.List(c.Request.Context(), middleware.MustUserID(c), q.Page, q.Limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]dto.AnalysisResponse, 0, len(items))
	for i := range items {
		out = append(out, dto.AnalysisFromDomain(&items[i]))
	}
	c.JSON(http.StatusOK, dto.Page[dto.AnalysisResponse]{Items: out, Page: q.Page, Limit: q.Limit, Total: total})
}

// Delete — DELETE /analyses/:id
func (h *AnalysisHandler) Delete(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), middleware.MustUserID(c), id); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
