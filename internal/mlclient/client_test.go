package mlclient

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	emotionv1 "github.com/yerbolatkerimbayevvv-debug/emotionsense/gen/emotion/v1"
)

// fakeServer — gRPC-сервер в памяти, имитирующий Python ML-сервис.
type fakeServer struct {
	emotionv1.UnimplementedEmotionServiceServer
}

func (fakeServer) Analyze(_ context.Context, req *emotionv1.AnalyzeRequest) (*emotionv1.AnalyzeResponse, error) {
	if len(req.Image) == 0 {
		return nil, status.Error(codes.InvalidArgument, "image is empty")
	}
	if string(req.Image) == "noface" {
		return &emotionv1.AnalyzeResponse{FaceDetected: false, InferenceMs: 5}, nil
	}
	return &emotionv1.AnalyzeResponse{
		FaceDetected: true, Dominant: "happy", InferenceMs: 42,
		Scores: []*emotionv1.EmotionScore{{Emotion: "happy", Score: 0.9}, {Emotion: "sad", Score: 0.1}},
	}, nil
}

func (fakeServer) Health(context.Context, *emotionv1.HealthRequest) (*emotionv1.HealthResponse, error) {
	return &emotionv1.HealthResponse{Ready: true, Model: "fake"}, nil
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	emotionv1.RegisterEmotionServiceServer(srv, fakeServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return &Client{conn: conn, api: emotionv1.NewEmotionServiceClient(conn), timeout: 2 * time.Second}
}

func TestClient_Analyze(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	res, err := c.Analyze(ctx, []byte("jpeg-bytes"), "rid-1")
	require.NoError(t, err)
	assert.True(t, res.FaceDetected)
	assert.Equal(t, "happy", res.Dominant)
	require.Len(t, res.Scores, 2)
	assert.InDelta(t, 0.9, res.Scores[0].Score, 1e-6)
	assert.Equal(t, int64(42), res.InferenceMS)
}

func TestClient_NoFace(t *testing.T) {
	c := newTestClient(t)
	res, err := c.Analyze(context.Background(), []byte("noface"), "rid")
	require.NoError(t, err)
	assert.False(t, res.FaceDetected)
	assert.Empty(t, res.Dominant)
}

func TestClient_InvalidArgumentMapsToErrInvalidImage(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Analyze(context.Background(), nil, "rid")
	assert.ErrorIs(t, err, ErrInvalidImage)
}

func TestClient_Ready(t *testing.T) {
	c := newTestClient(t)
	assert.NoError(t, c.Ready(context.Background()))
}
