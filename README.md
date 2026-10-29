# EmotionSense

Асинхронный бэкенд на Go для распознавания эмоций по фотографии лица.

Пользователь загружает фото → API кладёт его в объектное хранилище и ставит
задачу в Kafka → воркер забирает задачу, вызывает ML-модель по gRPC и сохраняет
результат → клиент получает распределение эмоций (`happy 0.87, neutral 0.09, …`),
историю и статистику.

Выпускной проект курса **Golang Developer** (BITLAB Academy).

[![CI](https://github.com/yerbolat/emotionsense/actions/workflows/ci.yml/badge.svg)](https://github.com/yerbolat/emotionsense/actions/workflows/ci.yml)

---

## Архитектура

```
                 HTTP (JSON / multipart)
  клиент ───────────────────────────────▶ ┌───────────────┐
  (Swagger UI, curl, мобильное прил.)     │   api  (Go)   │
                                          │ Gin · JWT     │
                                          └──┬───┬───┬────┘
                     ┌───────────────────────┘   │   └──────────────────────┐
                     ▼                           ▼                          ▼
              ┌────────────┐              ┌────────────┐             ┌────────────┐
              │  Postgres  │              │   Redis    │             │   MinIO    │
              │ users,     │              │ статусы,   │             │ фото (S3)  │
              │ analyses,  │              │ кэш stats, │             └────────────┘
              │ scores     │              │ rate limit │                    ▲
              └────────────┘              └────────────┘                    │
                     ▲                           ▲                          │
                     │                           │        Kafka             │
                     │                           │   image.uploaded         │
                     │                    ┌──────┴──────┐ ◀──────────────── api
                     └────────────────────┤ worker (Go) │
                                          │  ×N реплик  │──── image.failed (DLQ)
                                          └──────┬──────┘
                                                 │ gRPC
                                                 ▼
                                          ┌─────────────┐
                                          │ ml (Python) │
                                          │  DeepFace   │
                                          └─────────────┘
```

**Поток запроса**

1. `POST /api/v1/analyses` с изображением. API валидирует файл (JPEG/PNG,
   размер), сохраняет в MinIO, создаёт запись `analyses` со статусом `pending`,
   пишет статус в Redis, публикует событие в Kafka и сразу отвечает `202 Accepted`.
2. Воркер читает событие, атомарно переводит анализ в `processing`
   (идемпотентность при повторной доставке), скачивает фото, вызывает ML по gRPC
   с ретраями и экспоненциальной задержкой, сохраняет результат транзакцией.
   После `N` неудач — статус `failed` и событие в DLQ-топик.
3. Клиент опрашивает `GET /api/v1/analyses/{id}`: пока задача не завершена, ответ
   отдаётся из Redis без обращения к Postgres.

Подробнее о решениях и альтернативах — в [`docs/architecture.md`](docs/architecture.md).

## Стек

| Слой            | Технология                                  |
|-----------------|---------------------------------------------|
| HTTP            | Go 1.24, Gin, валидация `go-playground`     |
| Auth            | JWT (HS256) + refresh-токены с ротацией, bcrypt, роли `user`/`admin` |
| БД              | PostgreSQL 16, pgx v5, SQL-миграции (embed + golang-migrate CLI) |
| Кэш             | Redis 7 (статусы задач, статистика, rate limiting) |
| Файлы           | MinIO (S3 API)                              |
| Очередь         | Kafka 3.7 (KRaft), `segmentio/kafka-go`, consumer group, ручной commit, DLQ |
| RPC             | gRPC + Protocol Buffers                     |
| ML              | Python 3.11, DeepFace (готовая модель), gRPC-сервер |
| Наблюдаемость   | `log/slog` (JSON в prod), request-id, `/health`, `/ready` |
| Документация    | OpenAPI 3.0 + Swagger UI (`/swagger`)       |
| Инфраструктура  | Docker (distroless), Docker Compose, GitHub Actions, golangci-lint |

## Быстрый старт

Требуется Docker с Compose v2. Первая сборка ML-образа занимает несколько минут
(скачиваются TensorFlow и веса модели).

```bash
git clone https://github.com/yerbolat/emotionsense.git
cd emotionsense
make up            # поднимает api, 2 воркера, ml, postgres, redis, minio, kafka
make logs          # смотреть логи
```

После старта:

| Сервис        | Адрес                               |
|---------------|-------------------------------------|
| API           | http://localhost:8080/api/v1        |
| Swagger UI    | http://localhost:8080/swagger       |
| Health        | http://localhost:8080/ready         |
| MinIO console | http://localhost:9001 (minioadmin / minioadmin) |
| Kafka UI      | http://localhost:8090 (`make up-tools`) |

Демо-сценарий одной командой (нужен `jq`):

```bash
make demo IMG=path/to/face.jpg
```

Вручную:

```bash
# 1. Регистрация
curl -s -X POST localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"me@example.com","password":"supersecret"}' | jq .

# 2. Загрузка фото (подставьте access_token из шага 1)
curl -s -X POST localhost:8080/api/v1/analyses \
  -H "Authorization: Bearer $TOKEN" -F image=@face.jpg
# → {"id":"4f1c...","status":"pending"}

# 3. Результат
curl -s localhost:8080/api/v1/analyses/4f1c... -H "Authorization: Bearer $TOKEN" | jq .
# → {"status":"done","dominant":"happy","scores":[{"emotion":"happy","score":0.87},...]}

# 4. Статистика за неделю
curl -s 'localhost:8080/api/v1/stats/me?days=7' -H "Authorization: Bearer $TOKEN" | jq .
```

Чтобы сделать пользователя админом:

```bash
docker compose -f deploy/docker-compose.yml exec postgres \
  psql -U emotion -d emotionsense -c "UPDATE users SET role='admin' WHERE email='me@example.com'"
```

## API

Полная спецификация — [`docs/openapi.yaml`](docs/openapi.yaml) и Swagger UI на `/swagger`.

| Метод  | Путь                    | Описание                                   | Доступ |
|--------|-------------------------|--------------------------------------------|--------|
| POST   | `/auth/register`        | Регистрация, возвращает пару токенов       | —      |
| POST   | `/auth/login`           | Вход                                       | —      |
| POST   | `/auth/refresh`         | Ротация refresh-токена                     | —      |
| POST   | `/auth/logout`          | Отозвать все refresh-токены                | user   |
| GET    | `/auth/me`              | Текущий пользователь                       | user   |
| POST   | `/analyses`             | Загрузить фото → `202` + `id`              | user   |
| GET    | `/analyses`             | История (пагинация)                        | user   |
| GET    | `/analyses/{id}`        | Статус / результат                         | user   |
| DELETE | `/analyses/{id}`        | Удалить анализ и файл                      | user   |
| GET    | `/stats/me?days=7`      | Распределение эмоций за период             | user   |
| GET    | `/admin/stats`          | Глобальная статистика                      | admin  |
| GET    | `/admin/users`          | Список пользователей                       | admin  |
| GET    | `/health`, `/ready`     | Liveness / readiness                       | —      |

Все ошибки в едином формате:

```json
{ "code": "INVALID_INPUT", "message": "validation failed", "details": { "email": "email" } }
```

## Структура проекта

```
cmd/
  api/            точка входа HTTP API
  worker/         точка входа Kafka-воркера
internal/
  config/         конфигурация из env, профили dev/stage/prod
  logger/         slog, логгер в context
  domain/         сущности (User, Analysis, события)
  dto/            структуры запросов/ответов + теги валидации
  apperror/       типизированные ошибки → HTTP-статусы
  service/        бизнес-логика: auth, analysis, processor, stats (+ тесты с моками)
  repository/
    postgres/     pgx-репозитории, миграции (+ интеграционные тесты)
    redis/        статусы, кэш, rate limit
  storage/minio/  объектное хранилище
  transport/
    http/         Gin-роутер, хендлеры, middleware (+ httptest-тесты)
    kafka/        producer / consumer
  mlclient/       gRPC-клиент к ML (+ тесты через bufconn)
pkg/
  jwt/            выпуск и проверка токенов
  hash/           bcrypt, SHA-256, случайные токены
proto/            контракт gRPC
gen/              сгенерированный Go-код из proto
migrations/       SQL-миграции (встраиваются в бинарник)
ml-service/       Python gRPC-сервер с DeepFace
deploy/           Dockerfile, docker-compose
docs/             OpenAPI, Swagger UI, architecture.md
scripts/          demo.sh
```

Слои зависят только внутрь: `transport → service → repository/storage/...`,
причём сервисы работают через интерфейсы (`internal/service/ports.go`), что
позволяет тестировать бизнес-логику без инфраструктуры.

## Разработка

```bash
make infra-up      # только postgres, redis, minio, kafka, ml в Docker
cp .env.example .env
make run-api       # go run ./cmd/api   (порт 8080)
make run-worker    # go run ./cmd/worker (порт 8081 для /health)
```

Полезные команды:

```bash
make test               # unit-тесты с -race
make test-integration   # интеграционные (Postgres из make infra-up)
make test-cover         # покрытие
make lint               # golangci-lint
make proto              # перегенерировать gRPC-код
make migrate-new name=add_index   # новая миграция
make help               # все команды
```

## Тестирование

- **Unit** — сервисы с in-memory моками портов (`internal/service/*_test.go`):
  сценарии ретраев, DLQ, идемпотентности, инвалидации кэша, владения ресурсами.
- **HTTP** — `httptest` + стабы сервисов: валидация, маппинг ошибок в статусы,
  JWT и роли, multipart-загрузка, быстрый путь статуса из кэша.
- **gRPC** — `bufconn`: in-memory сервер вместо Python-сервиса.
- **Интеграционные** — репозитории на реальном Postgres (пропускаются без
  `TEST_POSTGRES_DSN`; в CI поднимаются через GitHub Actions services).

## Конфигурация

Все настройки — через переменные окружения (см. [`.env.example`](.env.example)).
`APP_ENV=dev|stage|prod` переключает формат логов и строгость валидации
(в `prod` обязателен `JWT_SECRET` ≥ 16 символов).

## Надёжность

- **At-least-once** доставка: оффсет Kafka коммитится только после записи
  результата. Повторная доставка безопасна — воркер атомарно проверяет статус.
- **Ретраи** инференса с экспоненциальной задержкой; битые изображения не
  ретраятся; после исчерпания попыток — `failed` + DLQ.
- **Graceful shutdown**: SIGTERM → API дорабатывает активные запросы, воркер
  возвращает текущие задачи в `pending`.
- **Fail-open rate limiter**: недоступность Redis не блокирует API.
- **Readiness** проверяет Postgres, Redis и MinIO; воркер ждёт готовности ML.

## Что можно развить

Telegram-бот как клиент, Prometheus + Grafana, WebSocket вместо опроса,
анализ видео покадрово, замена DeepFace на облачный Vision API без изменения
Go-кода (достаточно новой реализации `mlclient.Analyzer`).

## Лицензия

MIT
