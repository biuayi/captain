-- 0005_review_perf: 加速 ListParticipants 的 LATERAL 子查询
-- （取每个 participation 最近一条 form step）。审查发现：原 UNIQUE
-- (participation_id, step_id) 不含 step_type/occurred_at，2万级有放大风险。
CREATE INDEX IF NOT EXISTS idx_psr_partn_type_time
    ON participation_step_record (participation_id, step_type, occurred_at DESC);
