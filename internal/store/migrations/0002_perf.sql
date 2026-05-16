-- 0002_perf: hot-path index (codex DB review).
-- CheckinCount + 10s reconcile run:
--   SELECT count(*) FROM participation WHERE event_id=$1 AND checkin_at IS NOT NULL
-- A partial index keeps that read cheap at 2万 scale WITHOUT adding a counter
-- table/trigger (no write-path amplification on every checkin).
--
-- Non-CONCURRENT on purpose: the embedded migrator runs each file in one
-- transaction; CONCURRENTLY can't run in a tx. v1 tables are empty at migrate
-- time so the brief lock is irrelevant. Production should rebuild this index
-- CONCURRENTLY out-of-band if ever applied to a hot table.
--
-- Everything else codex flagged as "defer": existing UNIQUE keys already cover
-- UpsertParticipant / EnsureParticipation / RecordStep; updates are by PK.
-- participant & participation deliberately NOT merged (identity root vs
-- per-event lifecycle). identity_type stays text (+CHECK added in 0003).

CREATE INDEX IF NOT EXISTS idx_participation_event_checked_in
    ON participation (event_id)
    WHERE checkin_at IS NOT NULL;
