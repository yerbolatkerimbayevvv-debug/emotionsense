# EmotionSense

Сервис, который определяет эмоцию человека по фотографии лица. Загружаешь фото,
через пару секунд получаешь что-то вроде `happy 87%, neutral 9%, sad 4%`, плюс
историю своих загрузок и статистику за период.

Это мой выпускной проект курса Golang Developer в BITLAB Academy. Основная цель
была не само распознавание (модель я взял готовую), а построить вокруг неё
нормальный бэкенд: с очередью, хранилищем, авторизацией, тестами и Docker'ом.

[![CI](https://github.com/yerbolatkerimbayevvv-debug/emotionsense/actions/workflows/ci.yml/badge.svg)](https://github.com/yerbolatkerimbayevvv-debug/emotionsense/actions/workflows/ci.yml)

## Как это устроено

Обработка асинхронная. Инференс нейросети на CPU занимает 1-3 секунды, и
держать HTTP-соединение открытым всё это время плохая идея, поэтому:

```
клиент ──HTTP──▶ api ──Kafka──▶ worker ──gRPC──▶ ml (python)
                  │                │
                  ▼                ▼
          Postgres/Redis/MinIO   Postgres/Redis
```

1. `POST /api/v1/analyses` принимает картинку. API проверяет, что это JPEG/PNG
   нормального размера, кладёт файл в MinIO, создаёт запись в Postgres со
   статусом `pending`, публикует событие в Kafka и сразу отвечает `202` с id.
2. Воркер читает событие, забирает файл, дёргает ML-сервис по gRPC, сохраняет
   результат. Если ML упал — ретраит с нарастающей задержкой, после трёх неудач
   помечает анализ как `failed` и пишет событие в отдельный DLQ-топик.
3. Клиент опрашивает `GET /api/v1/analyses/{id}`. Пока задача не готова, статус
   отдаётся из Redis, чтобы не долбить Postgres каждую секунду.

Воркеров можно поднять несколько (в compose их два) — они делят партиции
Kafka между собой. Повторная доставка сообщения безопасна: воркер атомарно
переводит статус `pending -> processing` одним UPDATE, и если запись уже не
в pending, просто пропускает её.

Подробнее про то, почему именно Kafka, зачем Redis при наличии Postgres и т.д. —
написал в [docs/architecture.md](docs/architecture.md).

## Стек

- Go 1.24, Gin
- PostgreSQL 16 через pgx, миграции в SQL
- Redis — статусы задач, кэш статистики, rate limit
- MinIO как S3-хранилище для фото
- Kafka (KRaft, без ZooKeeper), клиент segmentio/kafka-go
- gRPC + protobuf между воркером и ML
- Python 3.11 + DeepFace в отдельном контейнере — единственное место не на Go
- JWT (access 15 мин) + refresh-токены с ротацией, bcrypt, роли user/admin
- slog для логов, OpenAPI-спека + Swagger UI
- Docker, docker compose, GitHub Actions, golangci-lint

## Запуск

Нужен Docker с Compose v2. Первый раз ML-образ собирается долго (минут 5-10,
тянет TensorFlow и веса модели), дальше кэшируется.

```bash
git clone https://github.com/yerbolatkerimbayevvv-debug/emotionsense.git
cd emotionsense
make up
make logs
```

Что где после старта:

- API: http://localhost:8080/api/v1
- Swagger: http://localhost:8080/swagger
- readiness: http://localhost:8080/ready
- консоль MinIO: http://localhost:9001 (minioadmin / minioadmin)
- Kafka UI: http://localhost:8090 — если поднять через `make up-tools`

Быстро проверить, что всё работает (нужен `jq`):

```bash
make demo IMG=photo.jpg
```

Скрипт зарегистрирует пользователя, загрузит фото, подождёт результат и покажет
статистику. То же самое руками:

```bash
# регистрация
curl -s -X POST localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"me@example.com","password":"supersecret"}' | jq .

# загрузка (TOKEN — access_token из ответа выше)
curl -s -X POST localhost:8080/api/v1/analyses \
  -H "Authorization: Bearer $TOKEN" -F image=@photo.jpg
# {"id":"4f1c...","status":"pending"}

# результат
curl -s localhost:8080/api/v1/analyses/4f1c... -H "Authorization: Bearer $TOKEN" | jq .

# статистика за неделю
curl -s 'localhost:8080/api/v1/stats/me?days=7' -H "Authorization: Bearer $TOKEN" | jq .
```

Админа пока назначаю прямо в базе:

```bash
docker compose -f deploy/docker-compose.yml exec postgres \
  psql -U emotion -d emotionsense -c "UPDATE users SET role='admin' WHERE email='me@example.com'"
```

## Эндпоинты

Полная спека в [docs/openapi.yaml](docs/openapi.yaml), она же отдаётся в Swagger UI.

```
POST   /auth/register        регистрация, сразу возвращает токены
POST   /auth/login
POST   /auth/refresh         старый refresh-токен удаляется, выдаётся новая пара
POST   /auth/logout          отзывает все refresh-токены пользователя
GET    /auth/me

POST   /analyses             multipart, поле image -> 202 + id
GET    /analyses             история с пагинацией
GET    /analyses/{id}        статус или готовый результат
DELETE /analyses/{id}        удаляет запись и файл из MinIO

GET    /stats/me?days=7      какие эмоции были доминирующими за период

GET    /admin/stats          только для role=admin
GET    /admin/users

GET    /health  /ready
```

Ошибки везде в одном формате:

```json
{"code": "INVALID_INPUT", "message": "validation failed", "details": {"email": "email"}}
```

## Структура

```
cmd/api, cmd/worker      два бинарника
internal/
  config, logger         env-конфиг с профилями dev/stage/prod, slog
  domain, dto            сущности отдельно от структур API
  apperror               ошибки с кодами, middleware переводит их в HTTP-статусы
  service                бизнес-логика, зависит только от интерфейсов (ports.go)
  repository/postgres    pgx, миграции
  repository/redis       статусы, кэш, rate limit
  storage/minio
  transport/http         роутер, хендлеры, middleware
  transport/kafka        producer и consumer
  mlclient               gRPC-клиент к модели
pkg/jwt, pkg/hash
proto/, gen/             контракт и сгенерированный код
migrations/              SQL, встраиваются в бинарник через embed
ml-service/              python-сервер
deploy/                  Dockerfile, compose
docs/                    openapi, swagger, architecture.md
```

Зависимости идут только внутрь: хендлеры знают про сервисы, сервисы про
интерфейсы репозиториев, репозитории про SQL. Благодаря этому сервисный слой
тестируется in-memory моками без поднятия базы.

## Разработка без полного compose

Удобнее поднимать в Docker только инфраструктуру, а Go-сервисы гонять с хоста:

```bash
make infra-up        # postgres, redis, minio, kafka, ml
cp .env.example .env
make run-api         # в одном терминале
make run-worker      # в другом
```

Остальное:

```bash
make test              # юнит-тесты с -race
make test-integration  # тесты репозиториев на живом Postgres
make lint
make proto             # перегенерировать код из .proto
make help              # полный список
```

## Тесты

- сервисы — с in-memory моками: ретраи, DLQ, идемпотентность, инвалидация
  кэша, проверка что нельзя прочитать чужой анализ
- хендлеры — через httptest: валидация, статусы ошибок, JWT, роли, multipart
- gRPC-клиент — через bufconn, без реального сервера
- репозитории — интеграционные на Postgres, пропускаются если не задан
  `TEST_POSTGRES_DSN`; в CI поднимается через services

## Конфигурация

Всё через переменные окружения, список в [.env.example](.env.example).
`APP_ENV` переключает dev/stage/prod: в dev читается `.env` и логи текстовые,
в prod логи в JSON и `JWT_SECRET` обязателен.


