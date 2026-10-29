# Универсальный многостадийный Dockerfile для Go-сервисов.
# Собирается с --build-arg SERVICE=api или SERVICE=worker.

FROM golang:1.24-alpine AS builder
ARG SERVICE
WORKDIR /src

# Кэшируем зависимости отдельным слоем
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/${SERVICE} ./cmd/${SERVICE}

# Минимальный рантайм: только бинарник и CA-сертификаты
FROM gcr.io/distroless/static-debian12:nonroot
ARG SERVICE
COPY --from=builder /out/${SERVICE} /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
