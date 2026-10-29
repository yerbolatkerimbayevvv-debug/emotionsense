package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yerbolat/emotionsense/internal/domain"
)

// AnalysisRepo — репозиторий анализов и их результатов.
type AnalysisRepo struct {
	pool *pgxpool.Pool
}

func NewAnalysisRepo(pool *pgxpool.Pool) *AnalysisRepo { return &AnalysisRepo{pool: pool} }

const analysisColumns = `id, user_id, image_key, status, face_detected, dominant, error_msg, attempts, created_at, finished_at`

func scanAnalysis(row pgx.Row) (*domain.Analysis, error) {
	var a domain.Analysis
	var status string
	var finished *time.Time
	if err := row.Scan(&a.ID, &a.UserID, &a.ImageKey, &status, &a.FaceDetected,
		&a.Dominant, &a.ErrorMsg, &a.Attempts, &a.CreatedAt, &finished); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.Status = domain.AnalysisStatus(status)
	a.FinishedAt = scanTime(finished)
	return &a, nil
}

// Create создаёт запись анализа со статусом pending.
func (r *AnalysisRepo) Create(ctx context.Context, a *domain.Analysis) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.Status == "" {
		a.Status = domain.StatusPending
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO analyses (id, user_id, image_key, status, attempts, created_at)
		VALUES ($1, $2, $3, $4, 0, $5)`,
		a.ID, a.UserID, a.ImageKey, string(a.Status), a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert analysis: %w", err)
	}
	return nil
}

// GetByID возвращает анализ вместе с распределением эмоций.
func (r *AnalysisRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Analysis, error) {
	a, err := scanAnalysis(r.pool.QueryRow(ctx,
		`SELECT `+analysisColumns+` FROM analyses WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	scores, err := r.loadScores(ctx, id)
	if err != nil {
		return nil, err
	}
	a.Scores = scores
	return a, nil
}

func (r *AnalysisRepo) loadScores(ctx context.Context, id uuid.UUID) ([]domain.EmotionScore, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT emotion, score FROM emotion_scores WHERE analysis_id = $1 ORDER BY score DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.EmotionScore
	for rows.Next() {
		var s domain.EmotionScore
		if err := rows.Scan(&s.Emotion, &s.Score); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListByUser возвращает анализы пользователя постранично (без scores — для списка).
func (r *AnalysisRepo) ListByUser(ctx context.Context, userID uuid.UUID, offset, limit int) ([]domain.Analysis, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM analyses WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+analysisColumns+` FROM analyses
		 WHERE user_id = $1 ORDER BY created_at DESC OFFSET $2 LIMIT $3`,
		userID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []domain.Analysis
	for rows.Next() {
		a, err := scanAnalysis(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, *a)
	}
	return list, total, rows.Err()
}

// Delete удаляет анализ пользователя. Возвращает ErrNotFound, если не нашёл.
func (r *AnalysisRepo) Delete(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM analyses WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TryMarkProcessing атомарно переводит pending -> processing и увеличивает attempts.
// Возвращает false, если анализ уже не в pending (идемпотентность воркера).
func (r *AnalysisRepo) TryMarkProcessing(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE analyses SET status = $2, attempts = attempts + 1
		WHERE id = $1 AND status IN ($3, $2)`,
		id, string(domain.StatusProcessing), string(domain.StatusPending))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MarkPending возвращает анализ в очередь для повторной попытки.
func (r *AnalysisRepo) MarkPending(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE analyses SET status = $2, error_msg = $3 WHERE id = $1`,
		id, string(domain.StatusPending), errMsg)
	return err
}

// SaveResult сохраняет результат в транзакции: статус + распределение эмоций.
func (r *AnalysisRepo) SaveResult(ctx context.Context, id uuid.UUID, faceDetected bool, dominant string, scores []domain.EmotionScore) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var dom *string
	if dominant != "" {
		dom = &dominant
	}
	if _, err := tx.Exec(ctx, `
		UPDATE analyses
		SET status = $2, face_detected = $3, dominant = $4, error_msg = NULL, finished_at = now()
		WHERE id = $1`,
		id, string(domain.StatusDone), faceDetected, dom); err != nil {
		return fmt.Errorf("update analysis: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM emotion_scores WHERE analysis_id = $1`, id); err != nil {
		return err
	}
	for _, s := range scores {
		if _, err := tx.Exec(ctx, `
			INSERT INTO emotion_scores (analysis_id, emotion, score) VALUES ($1, $2, $3)`,
			id, s.Emotion, s.Score); err != nil {
			return fmt.Errorf("insert score: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// MarkFailed помечает анализ как окончательно неуспешный.
func (r *AnalysisRepo) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE analyses SET status = $2, error_msg = $3, finished_at = now() WHERE id = $1`,
		id, string(domain.StatusFailed), errMsg)
	return err
}

// --- Статистика ---

// UserEmotionStats считает, сколько раз каждая эмоция была доминирующей за период.
func (r *AnalysisRepo) UserEmotionStats(ctx context.Context, userID uuid.UUID, since time.Time) ([]domain.EmotionStat, int64, error) {
	return r.emotionStats(ctx, `WHERE user_id = $1 AND status = 'done' AND dominant IS NOT NULL AND created_at >= $2`, userID, since)
}

// GlobalEmotionStats — то же по всем пользователям.
func (r *AnalysisRepo) GlobalEmotionStats(ctx context.Context, since time.Time) ([]domain.EmotionStat, int64, error) {
	return r.emotionStats(ctx, `WHERE status = 'done' AND dominant IS NOT NULL AND created_at >= $1`, since)
}

func (r *AnalysisRepo) emotionStats(ctx context.Context, where string, args ...any) ([]domain.EmotionStat, int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT dominant, count(*) FROM analyses `+where+`
		GROUP BY dominant ORDER BY count(*) DESC`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var stats []domain.EmotionStat
	var total int64
	for rows.Next() {
		var s domain.EmotionStat
		if err := rows.Scan(&s.Emotion, &s.Count); err != nil {
			return nil, 0, err
		}
		total += s.Count
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range stats {
		if total > 0 {
			stats[i].Share = float64(stats[i].Count) / float64(total)
		}
	}
	return stats, total, nil
}

// StatusCounts возвращает количество анализов по каждому статусу.
func (r *AnalysisRepo) StatusCounts(ctx context.Context) (map[domain.AnalysisStatus]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT status, count(*) FROM analyses GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[domain.AnalysisStatus]int64{}
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[domain.AnalysisStatus(st)] = n
	}
	return out, rows.Err()
}
