-- 0006b_export_job_dbdump: db_dump 导出任务无 organizer/event 归属，
-- 放宽 export_job.organizer_id/event_id 为可空（DESIGN §SS-0, SS0-16）。
-- 既有 participants 导出仍写这两列，行为不变；幂等。
ALTER TABLE export_job ALTER COLUMN organizer_id DROP NOT NULL;
ALTER TABLE export_job ALTER COLUMN event_id     DROP NOT NULL;
