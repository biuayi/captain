-- 0001_init: core schema (see docs/ARCHITECTURE.md §7)
-- gen_random_uuid() is built-in since PostgreSQL 13.

CREATE TABLE IF NOT EXISTS organizer (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text        NOT NULL,
    login_name    text        NOT NULL UNIQUE,
    password_hash text        NOT NULL,
    status        text        NOT NULL DEFAULT 'active', -- active | disabled
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS admin_user (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    login_name    text        NOT NULL UNIQUE,
    password_hash text        NOT NULL,
    status        text        NOT NULL DEFAULT 'active',
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS flow_config (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organizer_id uuid        NOT NULL REFERENCES organizer(id),
    name         text        NOT NULL,
    version      int         NOT NULL DEFAULT 1,
    schema_json  jsonb       NOT NULL,
    published    boolean     NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS event (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organizer_id          uuid        NOT NULL REFERENCES organizer(id),
    name                  text        NOT NULL,
    status                text        NOT NULL DEFAULT 'active', -- draft | active | ended
    start_at              timestamptz NOT NULL,
    end_at                timestamptz NOT NULL,
    expected_count        int         NOT NULL DEFAULT 0,
    screen_template_code  text        NOT NULL DEFAULT 'ink-wash-default',
    flow_config_id        uuid        NOT NULL REFERENCES flow_config(id),
    created_at            timestamptz NOT NULL DEFAULT now(),
    CHECK (start_at < end_at)
);
CREATE INDEX IF NOT EXISTS idx_event_organizer ON event(organizer_id);

CREATE TABLE IF NOT EXISTS participant (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       uuid        NOT NULL REFERENCES event(id),
    participant_key text       NOT NULL,
    identity_type  text        NOT NULL DEFAULT 'anon', -- anon | phone
    identity_value text,
    profile        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, participant_key)
);

CREATE TABLE IF NOT EXISTS participation (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       uuid        NOT NULL REFERENCES event(id),
    participant_id uuid        NOT NULL REFERENCES participant(id),
    checkin_at     timestamptz,
    status         text        NOT NULL DEFAULT 'in_progress', -- in_progress | completed
    last_step_id   text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, participant_id)
);

CREATE TABLE IF NOT EXISTS participation_step_record (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    participation_id uuid        NOT NULL REFERENCES participation(id),
    step_id          text        NOT NULL,
    step_type        text        NOT NULL,
    payload          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (participation_id, step_id)
);

CREATE TABLE IF NOT EXISTS export_job (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organizer_id uuid        NOT NULL REFERENCES organizer(id),
    event_id     uuid        NOT NULL REFERENCES event(id),
    format       text        NOT NULL DEFAULT 'csv',
    status       text        NOT NULL DEFAULT 'pending', -- pending | running | done | failed
    storage_key  text,
    error        text,
    requested_at timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);
CREATE INDEX IF NOT EXISTS idx_export_event ON export_job(event_id);
