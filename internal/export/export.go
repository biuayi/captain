// Package export turns a queued export_job into a CSV stored in object
// storage, driven by the durable NATS subject export.requested
// (ARCHITECTURE §6/§8). Async so large events don't block the request.
package export

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"log"
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
}

func New(js jetstream.JetStream, r *repo.Repo, st storage.Storage) *Worker {
	return &Worker{js: js, repo: r, st: st}
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

	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM so Excel reads Chinese correctly
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"participant_id", "identity_type", "identity_value",
		"status", "last_step", "checkin_at", "first_seen_at", "profile"})
	for _, r := range rows {
		checkin := ""
		if r.CheckinAt != nil {
			checkin = r.CheckinAt.Format(time.RFC3339)
		}
		prof, _ := json.Marshal(r.Profile)
		_ = cw.Write([]string{
			r.ParticipantID, r.IdentityType, r.IdentityValue,
			r.Status, r.LastStep, checkin,
			r.FirstSeenAt.Format(time.RFC3339), string(prof),
		})
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

func (w *Worker) fail(ctx context.Context, jobID string, e error) {
	_ = w.repo.FinishExportJob(ctx, jobID, "failed", "", e.Error())
}
