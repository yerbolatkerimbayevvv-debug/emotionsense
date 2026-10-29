// Package http собирает Gin-роутер из хендлеров и middleware.
package http

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/docs"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/config"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/transport/http/handler"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/transport/http/middleware"
)

// Deps — всё, что нужно роутеру.
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	Auth     *handler.AuthHandler
	Analysis *handler.AnalysisHandler
	Stats    *handler.StatsHandler
	Health   *handler.HealthHandler
	Tokens   middleware.TokenParser
	Limiter  middleware.Limiter
}

// NewRouter собирает маршруты.
func NewRouter(d Deps) *gin.Engine {
	if d.Cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.MaxMultipartMemory = d.Cfg.HTTP.MaxUploadBytes

	r.Use(
		middleware.RequestID(),
		middleware.Logger(d.Log),
		middleware.Recovery(),
		middleware.ErrorHandler(),
	)

	// Служебные
	r.GET("/health", d.Health.Live)
	r.GET("/ready", d.Health.Ready)

	// Swagger UI + спецификация
	r.GET("/swagger", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/swagger/") })
	r.GET("/swagger/", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", docs.SwaggerHTML) })
	r.GET("/swagger/openapi.yaml", func(c *gin.Context) { c.Data(http.StatusOK, "application/yaml", docs.OpenAPI) })

	v1 := r.Group("/api/v1")

	// Публичные
	auth := v1.Group("/auth")
	auth.Use(middleware.RateLimit(d.Limiter, d.Cfg.HTTP.RateLimitPerMin, "auth"))
	{
		auth.POST("/register", d.Auth.Register)
		auth.POST("/login", d.Auth.Login)
		auth.POST("/refresh", d.Auth.Refresh)
	}

	// Требуют access-токен
	protected := v1.Group("")
	protected.Use(middleware.Auth(d.Tokens))
	{
		protected.POST("/auth/logout", d.Auth.Logout)
		protected.GET("/auth/me", d.Auth.Me)

		an := protected.Group("/analyses")
		{
			an.POST("", middleware.RateLimit(d.Limiter, d.Cfg.HTTP.RateLimitPerMin, "upload"), d.Analysis.Submit)
			an.GET("", d.Analysis.List)
			an.GET("/:id", d.Analysis.Get)
			an.DELETE("/:id", d.Analysis.Delete)
		}

		protected.GET("/stats/me", d.Stats.Me)

		admin := protected.Group("/admin")
		admin.Use(middleware.RequireRole(domain.RoleAdmin))
		{
			admin.GET("/stats", d.Stats.AdminStats)
			admin.GET("/users", d.Stats.AdminUsers)
		}
	}

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"code": "NOT_FOUND", "message": "route not found"})
	})
	return r
}
