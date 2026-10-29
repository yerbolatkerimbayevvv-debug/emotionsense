package service

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/mlclient"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/repository/postgres"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/repository/redis"
)

// Ручные in-memory моки. Они проще генерируемых и показывают,
// что именно сервис ожидает от каждого порта.

// --- UserRepository ---

type memUsers struct {
	mu      sync.Mutex
	byID    map[uuid.UUID]*domain.User
	byEmail map[string]*domain.User
	refresh map[string]struct {
		userID  uuid.UUID
		expires time.Time
	}
	createErr error
}

func newMemUsers() *memUsers {
	return &memUsers{
		byID:    map[uuid.UUID]*domain.User{},
		byEmail: map[string]*domain.User{},
		refresh: map[string]struct {
			userID  uuid.UUID
			expires time.Time
		}{},
	}
}

func (m *memUsers) Create(_ context.Context, u *domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	if _, ok := m.byEmail[u.Email]; ok {
		return postgres.ErrConflict
	}
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	u.CreatedAt = time.Now()
	cp := *u
	m.byID[u.ID] = &cp
	m.byEmail[u.Email] = &cp
	return nil
}

func (m *memUsers) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.byEmail[email]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, postgres.ErrNotFound
}

func (m *memUsers) GetByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.byID[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, postgres.ErrNotFound
}

func (m *memUsers) List(_ context.Context, offset, limit int) ([]domain.User, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := make([]domain.User, 0, len(m.byID))
	for _, u := range m.byID {
		all = append(all, *u)
	}
	total := int64(len(all))
	if offset > len(all) {
		return nil, total, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], total, nil
}

func (m *memUsers) Count(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.byID)), nil
}

func (m *memUsers) SaveRefreshToken(_ context.Context, h string, userID uuid.UUID, exp time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refresh[h] = struct {
		userID  uuid.UUID
		expires time.Time
	}{userID, exp}
	return nil
}

func (m *memUsers) ConsumeRefreshToken(_ context.Context, h string) (uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.refresh[h]
	if !ok {
		return uuid.Nil, postgres.ErrNotFound
	}
	delete(m.refresh, h)
	if time.Now().After(r.expires) {
		return uuid.Nil, postgres.ErrNotFound
	}
	return r.userID, nil
}

func (m *memUsers) DeleteUserRefreshTokens(_ context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.refresh {
		if r.userID == userID {
			delete(m.refresh, k)
		}
	}
	return nil
}

// --- AnalysisRepository ---

type memAnalyses struct {
	mu    sync.Mutex
	items map[uuid.UUID]*domain.Analysis
}

func newMemAnalyses() *memAnalyses { return &memAnalyses{items: map[uuid.UUID]*domain.Analysis{}} }

func (m *memAnalyses) Create(_ context.Context, a *domain.Analysis) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	a.CreatedAt = time.Now()
	cp := *a
	m.items[a.ID] = &cp
	return nil
}

func (m *memAnalyses) GetByID(_ context.Context, id uuid.UUID) (*domain.Analysis, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.items[id]
	if !ok {
		return nil, postgres.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (m *memAnalyses) ListByUser(_ context.Context, userID uuid.UUID, offset, limit int) ([]domain.Analysis, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Analysis
	for _, a := range m.items {
		if a.UserID == userID {
			out = append(out, *a)
		}
	}
	total := int64(len(out))
	if offset >= len(out) {
		return nil, total, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], total, nil
}

func (m *memAnalyses) Delete(_ context.Context, id, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.items[id]
	if !ok || a.UserID != userID {
		return postgres.ErrNotFound
	}
	delete(m.items, id)
	return nil
}

func (m *memAnalyses) TryMarkProcessing(_ context.Context, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.items[id]
	if !ok || (a.Status != domain.StatusPending && a.Status != domain.StatusProcessing) {
		return false, nil
	}
	a.Status = domain.StatusProcessing
	a.Attempts++
	return true, nil
}

func (m *memAnalyses) MarkPending(_ context.Context, id uuid.UUID, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.items[id]; ok {
		a.Status = domain.StatusPending
		a.ErrorMsg = &msg
	}
	return nil
}

func (m *memAnalyses) SaveResult(_ context.Context, id uuid.UUID, face bool, dominant string, scores []domain.EmotionScore) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.items[id]
	if !ok {
		return postgres.ErrNotFound
	}
	now := time.Now()
	a.Status = domain.StatusDone
	a.FaceDetected = &face
	if dominant != "" {
		a.Dominant = &dominant
	}
	a.Scores = scores
	a.FinishedAt = &now
	a.ErrorMsg = nil
	return nil
}

func (m *memAnalyses) MarkFailed(_ context.Context, id uuid.UUID, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.items[id]; ok {
		now := time.Now()
		a.Status = domain.StatusFailed
		a.ErrorMsg = &msg
		a.FinishedAt = &now
	}
	return nil
}

func (m *memAnalyses) UserEmotionStats(_ context.Context, userID uuid.UUID, since time.Time) ([]domain.EmotionStat, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := map[string]int64{}
	var total int64
	for _, a := range m.items {
		if a.UserID == userID && a.Status == domain.StatusDone && a.Dominant != nil && a.CreatedAt.After(since) {
			counts[*a.Dominant]++
			total++
		}
	}
	out := make([]domain.EmotionStat, 0, len(counts))
	for e, n := range counts {
		out = append(out, domain.EmotionStat{Emotion: e, Count: n, Share: float64(n) / float64(total)})
	}
	return out, total, nil
}

func (m *memAnalyses) GlobalEmotionStats(ctx context.Context, since time.Time) ([]domain.EmotionStat, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := map[string]int64{}
	var total int64
	for _, a := range m.items {
		if a.Status == domain.StatusDone && a.Dominant != nil {
			counts[*a.Dominant]++
			total++
		}
	}
	out := make([]domain.EmotionStat, 0, len(counts))
	for e, n := range counts {
		out = append(out, domain.EmotionStat{Emotion: e, Count: n, Share: float64(n) / float64(total)})
	}
	return out, total, nil
}

func (m *memAnalyses) StatusCounts(_ context.Context) (map[domain.AnalysisStatus]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[domain.AnalysisStatus]int64{}
	for _, a := range m.items {
		out[a.Status]++
	}
	return out, nil
}

// --- StatusCache ---

type memCache struct {
	mu     sync.Mutex
	status map[string]domain.AnalysisStatus
	json   map[string][]byte
}

func newMemCache() *memCache {
	return &memCache{status: map[string]domain.AnalysisStatus{}, json: map[string][]byte{}}
}

func (c *memCache) SetStatus(_ context.Context, id string, s domain.AnalysisStatus) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status[id] = s
	return nil
}

func (c *memCache) GetStatus(_ context.Context, id string) (domain.AnalysisStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.status[id]; ok {
		return s, nil
	}
	return "", redis.ErrCacheMiss
}

func (c *memCache) DeleteStatus(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.status, id)
	return nil
}

func (c *memCache) SetJSON(_ context.Context, key string, v any, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := jsonMarshal(v)
	if err != nil {
		return err
	}
	c.json[key] = b
	return nil
}

func (c *memCache) GetJSON(_ context.Context, key string, dst any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.json[key]
	if !ok {
		return redis.ErrCacheMiss
	}
	return jsonUnmarshal(b, dst)
}

func (c *memCache) Delete(_ context.Context, keys ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		delete(c.json, k)
		delete(c.status, k)
	}
	return nil
}

// --- ObjectStorage ---

type memStorage struct {
	mu     sync.Mutex
	files  map[string][]byte
	putErr error
	getErr error
}

func newMemStorage() *memStorage { return &memStorage{files: map[string][]byte{}} }

func (s *memStorage) Put(_ context.Context, key string, data []byte, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.files[key] = data
	return nil
}

func (s *memStorage) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	b, ok := s.files[key]
	if !ok {
		return nil, postgres.ErrNotFound
	}
	return b, nil
}

func (s *memStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, key)
	return nil
}

// --- EventPublisher ---

type memPublisher struct {
	mu     sync.Mutex
	events []any
	err    error
}

func (p *memPublisher) Publish(_ context.Context, _ string, v any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.events = append(p.events, v)
	return nil
}

// --- Analyzer ---

type fakeAnalyzer struct {
	mu      sync.Mutex
	calls   int
	results []func() (*mlclient.Result, error) // сценарий по вызовам
}

func (f *fakeAnalyzer) Analyze(_ context.Context, _ []byte, _ string) (*mlclient.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.calls
	f.calls++
	if idx < len(f.results) {
		return f.results[idx]()
	}
	return f.results[len(f.results)-1]()
}

func (f *fakeAnalyzer) Ready(context.Context) error { return nil }

// --- TokenIssuer ---

type fakeTokens struct{}

func (fakeTokens) Issue(userID, role string) (string, error) {
	return "access-" + userID + "-" + role, nil
}
func (fakeTokens) TTL() time.Duration { return 15 * time.Minute }
