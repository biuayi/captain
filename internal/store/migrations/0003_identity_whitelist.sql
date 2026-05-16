-- 0003_identity_whitelist: REQ-CHANGE-001 (codex schema定稿).
-- 指纹身份 + 活动方预导入白名单。additive：旧 anon/phone 行迁移为 external。

CREATE TABLE IF NOT EXISTS event_whitelist_entry (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id                 uuid        NOT NULL REFERENCES event(id),
    organizer_id             uuid        NOT NULL REFERENCES organizer(id),
    employee_number          text        NOT NULL,
    name                     text        NOT NULL,
    phone_number             text        NOT NULL,
    phone_last4              text        NOT NULL,
    status                   text        NOT NULL DEFAULT 'unused',
    claimed_participant_id   uuid        REFERENCES participant(id) ON DELETE RESTRICT,
    claimed_fingerprint_hash text,
    claimed_at               timestamptz,
    blocked_reason           text,
    import_batch_id          text,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ewe_employee_number_nonempty CHECK (btrim(employee_number) <> ''),
    CONSTRAINT ewe_name_nonempty            CHECK (btrim(name) <> ''),
    CONSTRAINT ewe_phone_last4_format       CHECK (phone_last4 ~ '^[0-9]{4}$'),
    CONSTRAINT ewe_status_check             CHECK (status IN ('unused','claimed','blocked')),
    CONSTRAINT ewe_claimed_shape_check CHECK (
        (status <> 'claimed' OR (claimed_participant_id IS NOT NULL
            AND claimed_fingerprint_hash IS NOT NULL AND claimed_at IS NOT NULL))
        AND (status <> 'blocked' OR blocked_reason IS NOT NULL)
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_ewe_event_employee
    ON event_whitelist_entry (event_id, employee_number);
CREATE INDEX IF NOT EXISTS idx_ewe_org_event_status
    ON event_whitelist_entry (organizer_id, event_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ewe_claimed_participant
    ON event_whitelist_entry (claimed_participant_id) WHERE claimed_participant_id IS NOT NULL;

ALTER TABLE participant
    ADD COLUMN IF NOT EXISTS fingerprint_hash   text,
    ADD COLUMN IF NOT EXISTS participant_type   text NOT NULL DEFAULT 'external',
    ADD COLUMN IF NOT EXISTS whitelist_entry_id uuid;

ALTER TABLE participant
    ADD CONSTRAINT participant_type_check
        CHECK (participant_type IN ('staff','external')),
    ADD CONSTRAINT participant_identity_type_check
        CHECK (identity_type IN ('anon','phone','staff_whitelist','external_fingerprint')),
    ADD CONSTRAINT participant_whitelist_entry_fkey
        FOREIGN KEY (whitelist_entry_id) REFERENCES event_whitelist_entry(id) ON DELETE RESTRICT,
    ADD CONSTRAINT participant_staff_link_shape_check CHECK (
        (participant_type = 'staff'    AND whitelist_entry_id IS NOT NULL)
        OR (participant_type = 'external' AND whitelist_entry_id IS NULL)
    );

CREATE UNIQUE INDEX IF NOT EXISTS uniq_participant_event_whitelist_entry
    ON participant (event_id, whitelist_entry_id) WHERE whitelist_entry_id IS NOT NULL;
