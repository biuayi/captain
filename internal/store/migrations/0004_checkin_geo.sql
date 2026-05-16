-- 0004_checkin_geo: 签到位置落库（REQ-CHANGE-002 T-080）。
-- 位置为可选个人信息：可空，不阻断签到；导出含列。
ALTER TABLE participation
    ADD COLUMN IF NOT EXISTS checkin_lat      double precision,
    ADD COLUMN IF NOT EXISTS checkin_lng      double precision,
    ADD COLUMN IF NOT EXISTS checkin_accuracy double precision;
