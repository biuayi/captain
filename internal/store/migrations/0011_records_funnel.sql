-- 0011_records_funnel: SS-4/SS-7 记录字段 + 环节漏斗（DESIGN §4/§SS-4, D5）。幂等。
ALTER TABLE participation
    ADD COLUMN IF NOT EXISTS data_field_1  text,
    ADD COLUMN IF NOT EXISTS data_field_2  text,
    ADD COLUMN IF NOT EXISTS device_id     text,
    ADD COLUMN IF NOT EXISTS current_stage text,
    ADD COLUMN IF NOT EXISTS stage_done    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS completed_all boolean     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS completed_at  timestamptz;

CREATE INDEX IF NOT EXISTS idx_participation_event_stage
    ON participation (event_id, current_stage);
CREATE INDEX IF NOT EXISTS idx_participation_event_completed
    ON participation (event_id) WHERE completed_all;
