-- +goose Up
-- The dashboard compares each job's first and latest successful runs, which this index answers without reading any other run
CREATE INDEX runs_job_status_queued ON runs (job_id, status, queued_at);

-- +goose Down
DROP INDEX runs_job_status_queued;
