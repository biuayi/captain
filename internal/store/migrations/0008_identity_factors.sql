-- 0008_identity_factors: SS-2 参与者强身份（DESIGN §4/§SS-2, D2/D3/D4）。
-- 含约束变更（DROP ... IF EXISTS + 重建实现幂等）。

-- event 身份因子开关 + 活动时区 + 严格指纹
ALTER TABLE event
    ADD COLUMN IF NOT EXISTS timezone              text    NOT NULL DEFAULT 'Asia/Shanghai',
    ADD COLUMN IF NOT EXISTS strict_fingerprint    boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS identity_require_name  boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS identity_require_phone boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS identity_multi_company boolean NOT NULL DEFAULT false;

-- 白名单：company 消歧、claimed_jwt_jti 防一号多端；
-- phone_last4 仍必填(沿用 0003)；phone_number / name 放宽为可空(D2)。
ALTER TABLE event_whitelist_entry
    ADD COLUMN IF NOT EXISTS company         text,
    ADD COLUMN IF NOT EXISTS claimed_jwt_jti text;

ALTER TABLE event_whitelist_entry ALTER COLUMN phone_number DROP NOT NULL;
ALTER TABLE event_whitelist_entry ALTER COLUMN name         DROP NOT NULL;
ALTER TABLE event_whitelist_entry DROP CONSTRAINT IF EXISTS ewe_name_nonempty;

-- 替换唯一键：单企业 company_norm='' 兼容旧单企业；多企业用企业名消歧。
DROP INDEX IF EXISTS uniq_ewe_event_employee;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_ewe_event_company_employee
    ON event_whitelist_entry
       (event_id, (lower(btrim(coalesce(company,'')))), employee_number);

-- R1 多日签到去重与门禁（按 event.timezone 折算 day_date）。
CREATE TABLE IF NOT EXISTS checkin_day (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    participation_id uuid        NOT NULL REFERENCES participation(id),
    event_id         uuid        NOT NULL REFERENCES event(id),
    day_date         date        NOT NULL,
    checked_at       timestamptz NOT NULL DEFAULT now(),
    lat              double precision,
    lng              double precision,
    accuracy         double precision,
    UNIQUE (participation_id, day_date)
);
CREATE INDEX IF NOT EXISTS idx_checkin_day_event ON checkin_day (event_id);

-- D3 告警留痕（指纹不一致/顶号等），活动方可导出。
CREATE TABLE IF NOT EXISTS participation_warning (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    participation_id uuid,
    event_id         uuid        NOT NULL REFERENCES event(id),
    kind             text        NOT NULL,
    detail           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_pwarn_event ON participation_warning (event_id, created_at DESC);
