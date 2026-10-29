// Package mlclient — gRPC-клиент к ML-сервису распознавания эмоций.
//
// Сервисный слой зависит от интерфейса Analyzer, а не от gRPC напрямую:
// в тестах подставляется фейк, а при смене провайдера модели (например,
// на облачный Vision API) меняется только этот пакет.
package mlclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	emotionv1 "github.com/yerbolatkerimbayevvv-debug/emotionsense/gen/emotion/v1"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/config"
	"github.com/yerbolatkerimbayevvv-debug/emotionsense/internal/domain"
)

// Result — результат инференса в терминах домена.
type Result struct {
	FaceDetected bool
	Dominant     string
	Scores       []domain.EmotionScore
	InferenceMS  int64
}

// ErrInvalidImage — изображение не удалось декодировать (повторять бессмысленно).
var ErrInvalidImage = errors.New("invalid image")

// Analyzer — абстракция над моделью.
type Analyzer interface {
	Analyze(ctx context.Context, image []byte, requestID string) (*Result, error)
	Ready(ctx context.Context) error
}

// Client — gRPC-реализация Analyzer.
type Client struct {
	conn    *grpc.ClientConn
	api     emotionv1.EmotionServiceClient
	timeout time.Duration
}

// New устанавливает соединение (ленивое: реальный коннект при первом вызове).
func New(cfg config.ML) (*Client, error) {
	conn, err := grpc.NewClient(cfg.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(20<<20),
			grpc.MaxCallSendMsgSize(20<<20),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", cfg.Addr, err)
	}
	return &Client{conn: conn, api: emotionv1.NewEmotionServiceClient(conn), timeout: cfg.Timeout}, nil
}

// Analyze вызывает модель.
func (c *Client) Analyze(ctx context.Context, image []byte, requestID string) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.api.Analyze(ctx, &emotionv1.AnalyzeRequest{Image: image, RequestId: requestID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			return nil, fmt.Errorf("%w: %s", ErrInvalidImage, st.Message())
		}
		return nil, fmt.Errorf("ml analyze: %w", err)
	}

	res := &Result{
		FaceDetected: resp.GetFaceDetected(),
		Dominant:     resp.GetDominant(),
		InferenceMS:  resp.GetInferenceMs(),
	}
	for _, s := range resp.GetScores() {
		res.Scores = append(res.Scores, domain.EmotionScore{Emotion: s.GetEmotion(), Score: float64(s.GetScore())})
	}
	return res, nil
}

// Ready проверяет готовность модели.
func (c *Client) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := c.api.Health(ctx, &emotionv1.HealthRequest{})
	if err != nil {
		return err
	}
	if !resp.GetReady() {
		return errors.New("model not ready")
	}
	return nil
}

// Close закрывает соединение.
func (c *Client) Close() error { return c.conn.Close() }
