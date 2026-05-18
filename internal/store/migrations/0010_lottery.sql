-- 0010_lottery: SS-5 多奖池抽奖（DESIGN §4/§SS-5, D7）。幂等。

CREATE TABLE IF NOT EXISTS lottery_pool (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id   uuid    NOT NULL REFERENCES event(id),
    step_id    text    NOT NULL,
    code       text    NOT NULL,
    name       text    NOT NULL,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, step_id, code)
);
-- 每 (event,step) 至多一个默认池
CREATE UNIQUE INDEX IF NOT EXISTS uniq_lottery_default_pool
    ON lottery_pool (event_id, step_id) WHERE is_default;

CREATE TABLE IF NOT EXISTS lottery_membership (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id        uuid NOT NULL REFERENCES event(id),
    step_id         text NOT NULL,
    employee_number text NOT NULL,
    pool_id         uuid NOT NULL REFERENCES lottery_pool(id),
    created_by      uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, step_id, employee_number)   -- 强制奖池互斥
);

CREATE TABLE IF NOT EXISTS lottery_prize (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id  uuid NOT NULL REFERENCES event(id),
    step_id   text NOT NULL,
    pool_id   uuid NOT NULL REFERENCES lottery_pool(id),
    code      text NOT NULL,
    name      text NOT NULL,
    level     text NOT NULL DEFAULT 'normal',
    stock     int  NOT NULL,
    drawn     int  NOT NULL DEFAULT 0,
    weight    int  NOT NULL DEFAULT 1,
    image_key text,
    UNIQUE (event_id, step_id, code),
    CONSTRAINT lottery_prize_level_check CHECK (level IN ('grand','normal','none')),
    CONSTRAINT lottery_prize_stock_check CHECK (drawn <= stock AND stock >= 0)
);

CREATE TABLE IF NOT EXISTS lottery_rig_entry (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id        uuid NOT NULL REFERENCES event(id),
    step_id         text NOT NULL,
    employee_number text NOT NULL,
    prize_code      text NOT NULL,
    created_by      uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, step_id, employee_number)
);

CREATE TABLE IF NOT EXISTS lottery_result (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       uuid NOT NULL REFERENCES event(id),
    step_id        text NOT NULL,
    participant_id uuid NOT NULL REFERENCES participant(id),
    pool_id        uuid REFERENCES lottery_pool(id),
    prize_id       uuid REFERENCES lottery_prize(id),
    prize_level    text,
    resolved_by    text NOT NULL,
    drawn_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, step_id, participant_id),     -- 一人一抽幂等
    CONSTRAINT lottery_result_resolved_check CHECK (resolved_by IN ('rig','random','miss'))
);
