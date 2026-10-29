package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/yerbolat/emotionsense/internal/domain"
	"github.com/yerbolat/emotionsense/internal/dto"
	"github.com/yerbolat/emotionsense/internal/transport/http/middleware"
)

// StatsUsecase — что хендлеру нужно от сервиса статистики.
type StatsUsecase interface {
	UserStats(ctx context.Context, userID uuid.UUID, days int) (*dto.StatsResponse, error)
	AdminStats(ctx context.Context) (*dto.AdminStatsResponse, error)
	ListUsers(ctx context.Context, page, limit int) ([]domain.User, int64, error)
}

// StatsHandler обслуживает /stats/* и /admin/*.
type StatsHandler struct {
	svc StatsUsecase
}

func NewStatsHandler(svc StatsUsecase) *StatsHandler { return &StatsHandler{svc: svc} }

// Me — GET /stats/me?days=7
func (h *StatsHandler) Me(c *gin.Context) {
	var q dto.StatsQuery
	if !bindQuery(c, &q) {
		return
	}
	resp, err := h.svc.UserStats(c.Request.Context(), middleware.MustUserID(c), q.Days)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// AdminStats — GET /admin/stats
func (h *StatsHandler) AdminStats(c *gin.Context) {
	resp, err := h.svc.AdminStats(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// AdminUsers — GET /admin/users?page=&limit=
func (h *StatsHandler) AdminUsers(c *gin.Context) {
	var q dto.ListQuery
	if !bindQuery(c, &q) {
		return
	}
	users, total, err := h.svc.ListUsers(c.Request.Context(), q.Page, q.Limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]dto.UserResponse, 0, len(users))
	for i := range users {
		out = append(out, dto.UserFromDomain(&users[i]))
	}
	c.JSON(http.StatusOK, dto.Page[dto.UserResponse]{Items: out, Page: q.Page, Limit: q.Limit, Total: total})
}
