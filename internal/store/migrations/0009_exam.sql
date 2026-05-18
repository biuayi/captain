-- 0009_exam: SS-3 R3 在线考试题库（DESIGN §4/§SS-3）。幂等。
CREATE TABLE IF NOT EXISTS exam_question (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id  uuid    NOT NULL REFERENCES event(id),
    step_id   text    NOT NULL,
    idx       int     NOT NULL,
    stem      text    NOT NULL,
    options   jsonb   NOT NULL DEFAULT '[]'::jsonb,
    correct   jsonb   NOT NULL DEFAULT '[]'::jsonb,  -- [optionIndex,...]
    score     int     NOT NULL DEFAULT 1,
    multi     boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_exam_q_event_step_idx
    ON exam_question (event_id, step_id, idx);
