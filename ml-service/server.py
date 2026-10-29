"""
EmotionSense ML service.

Тонкая gRPC-обёртка над готовой предобученной моделью распознавания эмоций
(библиотека DeepFace). Вся бизнес-логика находится в Go-сервисах; здесь только
инференс: байты изображения -> распределение эмоций.
"""

import logging
import os
import time
from concurrent import futures

import cv2
import grpc
import numpy as np
from grpc_health.v1 import health, health_pb2, health_pb2_grpc

import emotion_pb2
import emotion_pb2_grpc

logging.basicConfig(
    level=os.getenv("LOG_LEVEL", "INFO"),
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)
log = logging.getLogger("ml-service")

MODEL_NAME = "DeepFace/emotion"
DETECTOR_BACKEND = os.getenv("DETECTOR_BACKEND", "opencv")
PORT = int(os.getenv("GRPC_PORT", "50051"))
MAX_WORKERS = int(os.getenv("GRPC_WORKERS", "4"))

# Импортируем лениво, чтобы ошибка загрузки модели была видна в логах явно.
try:
    from deepface import DeepFace  # noqa: E402
except Exception as exc:  # pragma: no cover
    log.exception("failed to import deepface: %s", exc)
    raise


def decode_image(data: bytes) -> np.ndarray | None:
    arr = np.frombuffer(data, dtype=np.uint8)
    img = cv2.imdecode(arr, cv2.IMREAD_COLOR)
    return img


class EmotionService(emotion_pb2_grpc.EmotionServiceServicer):
    def __init__(self) -> None:
        self.ready = False

    def warmup(self) -> None:
        """Прогревает модель на пустом кадре, чтобы первый запрос не ждал загрузки весов."""
        dummy = np.zeros((224, 224, 3), dtype=np.uint8)
        try:
            DeepFace.analyze(
                dummy,
                actions=["emotion"],
                enforce_detection=False,
                detector_backend=DETECTOR_BACKEND,
                silent=True,
            )
        except Exception as exc:  # pragma: no cover
            log.warning("warmup finished with warning: %s", exc)
        self.ready = True
        log.info("model warmed up, detector=%s", DETECTOR_BACKEND)

    def Health(self, request, context):
        return emotion_pb2.HealthResponse(ready=self.ready, model=MODEL_NAME)

    def Analyze(self, request, context):
        started = time.perf_counter()
        rid = request.request_id or "-"

        if not request.image:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "image is empty")

        img = decode_image(request.image)
        if img is None:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "cannot decode image")

        try:
            results = DeepFace.analyze(
                img,
                actions=["emotion"],
                enforce_detection=True,
                detector_backend=DETECTOR_BACKEND,
                silent=True,
            )
        except ValueError:
            # DeepFace бросает ValueError, если лицо не найдено.
            elapsed = int((time.perf_counter() - started) * 1000)
            log.info("rid=%s face not detected in %dms", rid, elapsed)
            return emotion_pb2.AnalyzeResponse(face_detected=False, inference_ms=elapsed)
        except Exception as exc:
            log.exception("rid=%s inference failed: %s", rid, exc)
            context.abort(grpc.StatusCode.INTERNAL, f"inference failed: {exc}")

        # DeepFace возвращает список лиц; берём самое крупное (первое).
        first = results[0] if isinstance(results, list) else results
        emotions: dict = first.get("emotion", {})
        dominant: str = first.get("dominant_emotion", "")

        # DeepFace отдаёт проценты (0..100), нормализуем в 0..1.
        total = sum(emotions.values()) or 1.0
        scores = [
            emotion_pb2.EmotionScore(emotion=name, score=float(value) / total)
            for name, value in sorted(emotions.items(), key=lambda kv: -kv[1])
        ]

        elapsed = int((time.perf_counter() - started) * 1000)
        log.info("rid=%s dominant=%s in %dms", rid, dominant, elapsed)
        return emotion_pb2.AnalyzeResponse(
            face_detected=True,
            dominant=dominant,
            scores=scores,
            inference_ms=elapsed,
        )


def serve() -> None:
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ("grpc.max_receive_message_length", 20 * 1024 * 1024),
            ("grpc.max_send_message_length", 20 * 1024 * 1024),
        ],
    )
    svc = EmotionService()
    emotion_pb2_grpc.add_EmotionServiceServicer_to_server(svc, server)

    # Стандартный gRPC health-check, чтобы Docker/K8s могли проверять готовность.
    health_servicer = health.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    server.add_insecure_port(f"[::]:{PORT}")
    server.start()
    log.info("gRPC server listening on :%d", PORT)

    svc.warmup()
    health_servicer.set("", health_pb2.HealthCheckResponse.SERVING)
    health_servicer.set("emotion.v1.EmotionService", health_pb2.HealthCheckResponse.SERVING)

    server.wait_for_termination()


if __name__ == "__main__":
    serve()
