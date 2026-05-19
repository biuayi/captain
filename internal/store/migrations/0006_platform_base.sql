-- 0006_platform_base: SS-0 平台基座（DESIGN §4/§SS-0）。
-- 活动方权限位 + 软删 + perm_version；平台密钥(加密)；审计(append-only)；
-- export_job 增 kind(默认 participants，兼容既有 csv 导出)。
-- additive：ADD COLUMN IF NOT EXISTS / CREATE TABLE IF NOT EXISTS，幂等可重入。

ALTER TABLE organizer
    ADD COLUMN IF NOT EXISTS can_create_event   boolean     NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS can_view_records   boolean     NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS can_export_records boolean     NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS deleted_at         timestamptz,
    ADD COLUMN IF NOT EXISTS perm_version       int         NOT NULL DEFAULT 1;

CREATE INDEX IF NOT EXISTS idx_organizer_active
    ON organizer (created_at DESC) WHERE deleted_at IS NULL;

-- 平台动态密钥：value_enc = AES-256-GCM(nonce||ct)（cryptobox）；masked 仅尾4位。
CREATE TABLE IF NOT EXISTS platform_config (
    key        text        PRIMARY KEY,
    value_enc  bytea       NOT NULL,
    masked     text        NOT NULL DEFAULT '',
    updated_by uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 审计：append-only，禁 UPDATE/DELETE（仅应用层约束）。
CREATE TABLE IF NOT EXISTS audit_log (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_role text        NOT NULL,            -- admin | organizer | system
    actor_id   uuid,
    action     text        NOT NULL,
    target     text,
    meta       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    request_id text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_created       ON audit_log (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_action_created ON audit_log (action, created_at DESC);

ALTER TABLE export_job
    ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'participants';
    -- participants | db_dump | lottery_audit | warnings
