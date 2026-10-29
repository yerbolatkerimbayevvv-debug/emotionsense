-- Анализы изображений и результаты распознавания
CREATE TABLE IF NOT EXISTS analyses (
    id            uuid PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    image_key     text NOT NULL,
    status        text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'processing', 'done', 'failed')),
    face_detected boolean,
    dominant      text,
    error_msg     text,
    attempts      int NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);
CREATE INDEX IF NOT EXISTS idx_analyses_user_created ON analyses(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_analyses_status ON analyses(status);
CREATE INDEX IF NOT EXISTS idx_analyses_dominant ON analyses(dominant) WHERE dominant IS NOT NULL;

CREATE TABLE IF NOT EXISTS emotion_scores (
    analysis_id uuid NOT NULL REFERENCES analyses(id) ON DELETE CASCADE,
    emotion     text NOT NULL,
    score       numeric(6,5) NOT NULL CHECK (score >= 0 AND score <= 1),
    PRIMARY KEY (analysis_id, emotion)
);
