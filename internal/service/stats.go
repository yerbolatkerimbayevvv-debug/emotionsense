package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/yerbolat/emotionsense/internal/apperror"
	"github.com/yerbolat/emotionsense/internal/domain"
	"github.com/yerbolat/emotionsense/internal/dto"
	"github.com/yerbolat/emotionsense/internal/repository/redis"
)

const (
	statsCacheTTL = 5 * time.Minute
	// AdminStatsCacheKey — ключ кэша глобальной статистики.
	AdminStatsCacheKey = "stats:admin"
)

// statsDays — периоды, которые кэшируются; их же инвалидируем при изменении.
var statsDays = []int{1, 7, 30, 365}

// StatsCacheKey — ключ кэша статистики пользователя за days дней.
func StatsCacheKey(userID string, days int) string {
	return "stats:user:" + userID + ":" + strconv.Itoa(days)
}

// UserStatsCacheKeys — все ключи кэша статистики пользователя (для инвалидации).
func UserStatsCacheKeys(userID string) []string {
	keys := make([]string, 0, len(statsDays))
	for _, d := range statsDays {
		keys = append(keys, StatsCacheKey(userID, d))
	}
	return keys
}

// StatsService — агрегаты по эмоциям с кэшированием.
type StatsService struct {
	analyses AnalysisRepository
	users    UserRepository
	cache    StatusCache
	log      *slog.Logger
}

// NewStatsService создаёт сервис статистики.
func NewStatsService(analyses AnalysisRepository, users UserRepository, cache StatusCache, log *slog.Logger) *StatsService {
	return &StatsService{analyses: analyses, users: users, cache: cache, log: log}
}

// UserStats — распределение доминирующих эмоций пользователя за N дней.
func (s *StatsService) UserStats(ctx context.Context, userID uuid.UUID, days int) (*dto.StatsResponse, error) {
	key := StatsCacheKey(userID.String(), days)

	var cached dto.StatsResponse
	if err := s.cache.GetJSON(ctx, key, &cached); err == nil {
		return &cached, nil
	} else if !errors.Is(err, redis.ErrCacheMiss) {
		s.log.Warn("stats cache read failed", "err", err)
	}

	since := time.Now().AddDate(0, 0, -days)
	stats, total, err := s.analyses.UserEmotionStats(ctx, userID, since)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	resp := &dto.StatsResponse{Days: days, Total: total, Emotions: toDTOStats(stats)}
	if err := s.cache.SetJSON(ctx, key, resp, statsCacheTTL); err != nil {
		s.log.Warn("stats cache write failed", "err", err)
	}
	return resp, nil
}

// InvalidateUser сбрасывает кэш пользователя (вызывается после завершения анализа).
func (s *StatsService) InvalidateUser(ctx context.Context, userID uuid.UUID) {
	_ = s.cache.Delete(ctx, UserStatsCacheKeys(userID.String())...)
}

// AdminStats — глобальная статистика для админа.
func (s *StatsService) AdminStats(ctx context.Context) (*dto.AdminStatsResponse, error) {
	var cached dto.AdminStatsResponse
	if err := s.cache.GetJSON(ctx, AdminStatsCacheKey, &cached); err == nil {
		return &cached, nil
	}

	users, err := s.users.Count(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	counts, err := s.analyses.StatusCounts(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	top, _, err := s.analyses.GlobalEmotionStats(ctx, time.Now().AddDate(0, 0, -30))
	if err != nil {
		return nil, apperror.Internal(err)
	}

	var total int64
	for _, n := range counts {
		total += n
	}
	resp := &dto.AdminStatsResponse{
		Users:       users,
		Analyses:    total,
		Done:        counts[domain.StatusDone],
		Failed:      counts[domain.StatusFailed],
		Pending:     counts[domain.StatusPending] + counts[domain.StatusProcessing],
		TopEmotions: toDTOStats(top),
		GeneratedAt: time.Now().UTC(),
	}
	_ = s.cache.SetJSON(ctx, AdminStatsCacheKey, resp, time.Minute)
	return resp, nil
}

// ListUsers — список пользователей для админа.
func (s *StatsService) ListUsers(ctx context.Context, page, limit int) ([]domain.User, int64, error) {
	users, total, err := s.users.List(ctx, (page-1)*limit, limit)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	return users, total, nil
}

func toDTOStats(in []domain.EmotionStat) []dto.EmotionStat {
	out := make([]dto.EmotionStat, 0, len(in))
	for _, s := range in {
		out = append(out, dto.EmotionStat{Emotion: s.Emotion, Count: s.Count, Share: s.Share})
	}
	return out
}
