// Package export turns a queued export_job into a CSV stored in object
// storage, driven by the durable NATS subject export.requested
// (ARCHITECTURE §6/§8). Async so large events don't block the request.
package export

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"time"

	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/storage"
	"github.com/nats-io/nats.go/jetstream"
)

const SubjectRequested = "export.requested"
const SubjectCompleted = "export.completed"

type Worker struct {
	js   jetstream.JetStream
	repo *repo.Repo
	st   storage.Storage
	dsn  string // for pg_dump (db_dump jobs, SS0-15)
}

func New(js jetstream.JetStream, r *repo.Repo, st storage.Storage, pgDSN string) *Worker {
	return &Worker{js: js, repo: r, st: st, dsn: pgDSN}
}

type requestMsg struct {
	JobID string `json:"job_id"`
}

// Request enqueues an export job for async processing.
func (w *Worker) Request(ctx context.Context, jobID string) error {
	b, _ := json.Marshal(requestMsg{JobID: jobID})
	_, err := w.js.Publish(ctx, SubjectRequested, b)
	return err
}

// Run blocks, consuming export requests with at-least-once delivery.
func (w *Worker) Run(ctx context.Context) error {
	cons, err := w.js.CreateOrUpdateConsumer(ctx, "CAPTAIN", jetstream.ConsumerConfig{
		Durable:       "export-worker",
		FilterSubject: SubjectRequested,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    5,
	})
	if err != nil {
		return err
	}
	_, err = cons.Consume(func(msg jetstream.Msg) {
		var rm requestMsg
		if json.Unmarshal(msg.Data(), &rm) != nil {
			_ = msg.Term()
			return
		}
		if err := w.process(ctx, rm.JobID); err != nil {
			log.Printf("export: job %s failed: %v", rm.JobID, err)
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	})
	return err
}

func (w *Worker) process(ctx context.Context, jobID string) error {
	kind, err := w.repo.ExportJobKind(ctx, jobID)
	if err != nil {
		return err
	}
	if kind == "db_dump" {
		return w.processDBDump(ctx, jobID)
	}

	eventID, _, err := w.repo.ExportJobBare(ctx, jobID)
	if err != nil {
		return err
	}
	if err := w.repo.SetExportRunning(ctx, jobID); err != nil {
		return err
	}

	rows, err := w.repo.ListParticipants(ctx, eventID)
	if err != nil {
		w.fail(ctx, jobID, err)
		return nil // failure recorded; don't redeliver forever
	}

	// 动态列：登记信息字段随活动方采集内容自动出现（取所有行 form 键并集）。
	keySet := map[string]struct{}{}
	for _, r := range rows {
		for k := range r.Form {
			keySet[k] = struct{}{}
		}
	}
	formKeys := make([]string, 0, len(keySet))
	for k := range keySet {
		formKeys = append(formKeys, k)
	}
	sort.Strings(formKeys)

	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM so Excel reads Chinese correctly
	cw := csv.NewWriter(&buf)
	header := []string{"participant_id", "identity_type", "identity_value",
		"status", "last_step", "checkin_at", "first_seen_at",
		"checkin_lat", "checkin_lng", "checkin_accuracy"}
	for _, k := range formKeys {
		header = append(header, "登记_"+k)
	}
	_ = cw.Write(header)
	for _, r := range rows {
		checkin := ""
		if r.CheckinAt != nil {
			checkin = r.CheckinAt.Format(time.RFC3339)
		}
		geo := func(f *float64) string {
			if f == nil {
				return ""
			}
			return fmt.Sprintf("%v", *f)
		}
		rec := []string{
			r.ParticipantID, r.IdentityType, csvSafe(r.IdentityValue),
			r.Status, r.LastStep, checkin, r.FirstSeenAt.Format(time.RFC3339),
			geo(r.Lat), geo(r.Lng), geo(r.Accuracy),
		}
		for _, k := range formKeys {
			rec = append(rec, csvSafe(fmt.Sprintf("%v", r.Form[k])))
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		w.fail(ctx, jobID, err)
		return nil
	}

	key := "exports/" + jobID + ".csv"
	if _, err := w.st.Put(key, &buf); err != nil {
		w.fail(ctx, jobID, err)
		return nil
	}
	if err := w.repo.FinishExportJob(ctx, jobID, "done", key, ""); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]string{"job_id": jobID, "status": "done"})
	_, _ = w.js.Publish(ctx, SubjectCompleted, b)
	log.Printf("export: job %s done (%d rows) -> %s", jobID, len(rows), key)
	return nil
}

// csvSafe neutralizes CSV formula injection: cells starting with = + - @ (or
// tab/CR) are prefixed with a single quote so Excel/Sheets treat them as text.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func (w *Worker) fail(ctx context.Context, jobID string, e error) {
	_ = w.repo.FinishExportJob(ctx, jobID, "failed", "", e.Error())
}

// processDBDump streams `pg_dump --no-owner` into object storage (SS0-15).
// Super-admin only; the job carries no tenant/event. pg_dump must be on PATH
// (a deploy prerequisite; PF-04 gated integration node).
func (w *Worker) processDBDump(ctx context.Context, jobID string) error {
	if err := w.repo.SetExportRunning(ctx, jobID); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "pg_dump", "--no-owner", "--no-privileges", w.dsn)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		w.fail(ctx, jobID, err)
		return nil
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		w.fail(ctx, jobID, fmt.Errorf("pg_dump start: %w (pg_dump on PATH?)", err))
		return nil
	}
	key := "db-exports/" + jobID + ".sql"
	if _, err := w.st.Put(key, stdout); err != nil {
		_ = cmd.Wait()
		w.fail(ctx, jobID, fmt.Errorf("store: %w", err))
		return nil
	}
	if err := cmd.Wait(); err != nil {
		w.fail(ctx, jobID, fmt.Errorf("pg_dump: %v: %s", err, stderr.String()))
		return nil
	}
	if err := w.repo.FinishExportJob(ctx, jobID, "done", key, ""); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]string{"job_id": jobID, "status": "done"})
	_, _ = w.js.Publish(ctx, SubjectCompleted, b)
	log.Printf("export: db_dump %s done -> %s", jobID, key)
	return nil
}
