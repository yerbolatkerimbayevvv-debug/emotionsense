FROM python:3.11-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    DEEPFACE_HOME=/models \
    TF_CPP_MIN_LOG_LEVEL=2

RUN apt-get update && apt-get install -y --no-install-recommends \
        libglib2.0-0 libgl1 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Генерируем Python-код из общего контракта proto.
COPY proto/emotion/v1/emotion.proto ./proto/emotion.proto
RUN python -m grpc_tools.protoc -I proto --python_out=. --grpc_python_out=. proto/emotion.proto

COPY ml-service/server.py .

# Скачиваем веса модели на этапе сборки, чтобы контейнер стартовал быстро
# и не зависел от сети в рантайме.
RUN python - <<'EOF'
import numpy as np
from deepface import DeepFace
DeepFace.analyze(np.zeros((224, 224, 3), dtype=np.uint8), actions=["emotion"], enforce_detection=False, silent=True)
print("weights downloaded")
EOF

EXPOSE 50051

HEALTHCHECK --interval=15s --timeout=5s --start-period=60s --retries=5 \
    CMD python -c "import grpc; from grpc_health.v1 import health_pb2, health_pb2_grpc; \
    ch = grpc.insecure_channel('localhost:50051'); \
    r = health_pb2_grpc.HealthStub(ch).Check(health_pb2.HealthCheckRequest(service=''), timeout=3); \
    exit(0 if r.status == health_pb2.HealthCheckResponse.SERVING else 1)" || exit 1

CMD ["python", "server.py"]
