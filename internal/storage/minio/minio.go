// Package minio — объектное хранилище для загруженных изображений.
//
// Изображения не хранятся в Postgres (bytea раздувает БД и бэкапы) и не на
// локальном диске (воркер и API — разные контейнеры). MinIO даёт S3-совместимый
// API, так что в проде его можно заменить на AWS S3 без изменения кода.
package minio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	mc "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/yerbolat/emotionsense/internal/config"
)

// Storage — обёртка над minio-go для одного бакета.
type Storage struct {
	client *mc.Client
	bucket string
}

// New подключается к MinIO и создаёт бакет, если его нет.
func New(ctx context.Context, cfg config.MinIO) (*Storage, error) {
	client, err := mc.New(cfg.Endpoint, &mc.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}

	s := &Storage{client: client, bucket: cfg.Bucket}

	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	exists, err := client.BucketExists(initCtx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("bucket exists: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(initCtx, cfg.Bucket, mc.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("make bucket: %w", err)
		}
	}
	return s, nil
}

// Put загружает объект и возвращает его ключ.
func (s *Storage) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		mc.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	return nil
}

// Get скачивает объект целиком.
func (s *Storage) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, mc.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("read object %s: %w", key, err)
	}
	return data, nil
}

// Delete удаляет объект.
func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, mc.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove object %s: %w", key, err)
	}
	return nil
}

// Ping проверяет доступность хранилища для health-check.
func (s *Storage) Ping(ctx context.Context) error {
	_, err := s.client.BucketExists(ctx, s.bucket)
	return err
}
