# ml-service

Единственный компонент проекта не на Go. Это намеренно: обучение и запуск
нейросетей — задача Python-экосистемы, а Go-сервисы отвечают за API,
очереди, хранение и надёжность.

Сервис — тонкая gRPC-обёртка над готовой моделью
[DeepFace](https://github.com/serengil/deepface) (распознавание 7 эмоций:
happy, sad, angry, surprise, fear, disgust, neutral). Контракт описан в
`proto/emotion/v1/emotion.proto` и общий с Go-клиентом.

## Запуск локально (без Docker)

```bash
cd ml-service
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python -m grpc_tools.protoc -I ../proto/emotion/v1 --python_out=. --grpc_python_out=. ../proto/emotion/v1/emotion.proto
python server.py
```

## Переменные окружения

| Переменная         | По умолчанию | Описание                                  |
|--------------------|--------------|-------------------------------------------|
| `GRPC_PORT`        | `50051`      | Порт gRPC-сервера                         |
| `GRPC_WORKERS`     | `4`          | Размер пула потоков                       |
| `DETECTOR_BACKEND` | `opencv`     | Детектор лиц: opencv, ssd, mtcnn, retinaface |
| `LOG_LEVEL`        | `INFO`       | Уровень логирования                       |
