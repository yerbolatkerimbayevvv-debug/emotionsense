// Package kafka — продюсер и консьюмер событий обработки изображений.
//
// Почему Kafka, а не прямой HTTP-вызов воркера: инференс занимает секунды,
// очередь буферизует всплески нагрузки, позволяет масштабировать воркеры
// независимо от API и переживает перезапуск любого из сервисов без потери
// задач (сообщения коммитятся только после успешной обработки).
package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	kgo "github.com/segmentio/kafka-go"

	"github.com/yerbolat/emotionsense/internal/config"
	"github.com/yerbolat/emotionsense/internal/domain"
)

// EnsureTopics создаёт топики, если их ещё нет (удобно для dev-окружения;
// в проде топики обычно создаёт инфраструктурный код).
func EnsureTopics(ctx context.Context, brokers []string, topics ...string) error {
	if len(brokers) == 0 {
		return errors.New("no brokers")
	}
	conn, err := kgo.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return fmt.Errorf("dial kafka: %w", err)
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		return fmt.Errorf("get controller: %w", err)
	}
	cconn, err := kgo.DialContext(ctx, "tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		return fmt.Errorf("dial controller: %w", err)
	}
	defer cconn.Close()

	cfgs := make([]kgo.TopicConfig, 0, len(topics))
	for _, t := range topics {
		cfgs = append(cfgs, kgo.TopicConfig{Topic: t, NumPartitions: 3, ReplicationFactor: 1})
	}
	if err := cconn.CreateTopics(cfgs...); err != nil {
		// Топик уже существует — не ошибка.
		var kerr kgo.Error
		if errors.As(err, &kerr) && kerr == kgo.TopicAlreadyExists {
			return nil
		}
		return fmt.Errorf("create topics: %w", err)
	}
	return nil
}

// --- Producer ---

// Producer публикует события в топик.
type Producer struct {
	w *kgo.Writer
}

// NewProducer создаёт продюсер для топика.
func NewProducer(cfg config.Kafka, topic string) *Producer {
	return &Producer{w: &kgo.Writer{
		Addr:                   kgo.TCP(cfg.Brokers...),
		Topic:                  topic,
		Balancer:               &kgo.Hash{}, // один ключ -> одна партиция -> порядок для пользователя
		RequiredAcks:           kgo.RequireAll,
		AllowAutoTopicCreation: true,
		BatchTimeout:           10 * time.Millisecond,
	}}
}

// Publish сериализует value в JSON и отправляет с заданным ключом.
func (p *Producer) Publish(ctx context.Context, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.w.WriteMessages(ctx, kgo.Message{Key: []byte(key), Value: b, Time: time.Now()})
}

// Close завершает продюсер, дожидаясь отправки буфера.
func (p *Producer) Close() error { return p.w.Close() }

// --- Consumer ---

// Handler обрабатывает событие. Если возвращает ошибку, оффсет не коммитится
// и сообщение будет перечитано (at-least-once).
type Handler func(ctx context.Context, evt domain.ImageUploadedEvent) error

// Consumer читает события из топика в рамках consumer group.
type Consumer struct {
	r   *kgo.Reader
	log *slog.Logger
}

// NewConsumer создаёт консьюмер.
func NewConsumer(cfg config.Kafka, log *slog.Logger) *Consumer {
	return &Consumer{
		r: kgo.NewReader(kgo.ReaderConfig{
			Brokers:        cfg.Brokers,
			GroupID:        cfg.ConsumerGroup,
			Topic:          cfg.Topic,
			MinBytes:       1,
			MaxBytes:       10 << 20,
			CommitInterval: 0, // коммитим вручную после обработки
			StartOffset:    kgo.FirstOffset,
			MaxWait:        500 * time.Millisecond,
		}),
		log: log,
	}
}

// Run читает сообщения до отмены контекста. Обработка — через пул из
// concurrency горутин: FetchMessage блокирует, пока есть свободный слот.
func (c *Consumer) Run(ctx context.Context, concurrency int, h Handler) error {
	if concurrency <= 0 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	done := make(chan struct{})
	defer func() {
		// дожидаемся горутин перед выходом
		for i := 0; i < concurrency; i++ {
			sem <- struct{}{}
		}
		close(done)
	}()

	for {
		msg, err := c.r.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return fmt.Errorf("fetch message: %w", err)
		}

		sem <- struct{}{}
		go func(m kgo.Message) {
			defer func() { <-sem }()

			var evt domain.ImageUploadedEvent
			if err := json.Unmarshal(m.Value, &evt); err != nil {
				// Битое сообщение — логируем и коммитим, иначе оно заблокирует партицию.
				c.log.Error("malformed message, skipping", "err", err, "offset", m.Offset)
				_ = c.r.CommitMessages(ctx, m)
				return
			}

			if err := h(ctx, evt); err != nil {
				c.log.Error("handle message failed, will be redelivered",
					"err", err, "analysis_id", evt.AnalysisID, "offset", m.Offset)
				return
			}
			if err := c.r.CommitMessages(ctx, m); err != nil && !errors.Is(err, context.Canceled) {
				c.log.Error("commit failed", "err", err, "offset", m.Offset)
			}
		}(msg)
	}
}

// Close закрывает ридер.
func (c *Consumer) Close() error { return c.r.Close() }
