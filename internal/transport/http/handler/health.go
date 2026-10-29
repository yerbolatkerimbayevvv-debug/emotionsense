package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yerbolat/emotionsense/internal/dto"
)

// Pinger — зависимость, умеющая проверять своё здоровье.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthHandler — liveness и readiness.
type HealthHandler struct {
	checks map[string]Pinger
}

// NewHealthHandler принимает именованные зависимости для readiness.
func NewHealthHandler(checks map[string]Pinger) *HealthHandler {
	return &HealthHandler{checks: checks}
}

// Live — GET /health: процесс жив.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, dto.HealthResponse{Status: "ok"})
}

// Ready — GET /ready: все зависимости отвечают.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	result := make(map[string]string, len(h.checks))
	healthy := true
	for name, p := range h.checks {
		if err := p.Ping(ctx); err != nil {
			result[name] = "down: " + err.Error()
			healthy = false
		} else {
			result[name] = "ok"
		}
	}
	status := http.StatusOK
	resp := dto.HealthResponse{Status: "ready", Checks: result}
	if !healthy {
		status = http.StatusServiceUnavailable
		resp.Status = "degraded"
	}
	c.JSON(status, resp)
}
