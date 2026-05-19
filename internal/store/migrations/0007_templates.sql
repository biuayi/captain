-- 0007_templates: SS-1 模板注册与分发（DESIGN §4/§SS-1）。
-- 超管上传/管理多款大屏模板与流程页模板；organizer_id NULL=全局，
-- 非空=该租户定制。配置驱动 + 静态资源(OSS)，禁可执行包。幂等。

CREATE TABLE IF NOT EXISTS template (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind         text        NOT NULL,
    code         text        NOT NULL,
    name         text        NOT NULL,
    version      int         NOT NULL DEFAULT 1,
    status       text        NOT NULL DEFAULT 'draft',   -- draft | published | disabled
    organizer_id uuid        REFERENCES organizer(id),    -- NULL = global
    manifest     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT template_kind_check   CHECK (kind IN ('screen','flow_page')),
    CONSTRAINT template_status_check CHECK (status IN ('draft','published','disabled'))
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_template_code_version
    ON template (code, version);
CREATE INDEX IF NOT EXISTS idx_template_kind_status
    ON template (kind, status);
CREATE INDEX IF NOT EXISTS idx_template_owner
    ON template (organizer_id) WHERE organizer_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS template_asset (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id uuid        NOT NULL REFERENCES template(id) ON DELETE CASCADE,
    storage_key text        NOT NULL,
    mime        text        NOT NULL,
    size        bigint      NOT NULL DEFAULT 0,
    role        text        NOT NULL DEFAULT 'asset',     -- preview | asset | logo ...
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_template_asset_tpl
    ON template_asset (template_id);

ALTER TABLE event
    ADD COLUMN IF NOT EXISTS flow_template_code text NOT NULL DEFAULT '';
