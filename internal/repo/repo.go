// Package repo is the only place that talks SQL. Every organizer-scoped
// query takes organizer_id explicitly (multi-tenant defence, ARCHITECTURE §2).
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hertz/captain/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("repo: not found")

type Repo struct{ pg *pgxpool.Pool }

func New(pg *pgxpool.Pool) *Repo { return &Repo{pg: pg} }

// ---- auth lookups ----

type Cred struct {
	ID           string
	PasswordHash string
	Status       string
	Name         string
}

func (r *Repo) OrganizerByLogin(ctx context.Context, login string) (Cred, error) {
	var c Cred
	err := r.pg.QueryRow(ctx,
		`SELECT id, password_hash, status, name FROM organizer WHERE login_name=$1`, login,
	).Scan(&c.ID, &c.PasswordHash, &c.Status, &c.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (r *Repo) AdminByLogin(ctx context.Context, login string) (Cred, error) {
	var c Cred
	err := r.pg.QueryRow(ctx,
		`SELECT id, password_hash, status, login_name FROM admin_user WHERE login_name=$1`, login,
	).Scan(&c.ID, &c.PasswordHash, &c.Status, &c.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// ---- admin: organizer management ----

func (r *Repo) ListOrganizers(ctx context.Context) ([]domain.Organizer, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT id, name, login_name, status, created_at FROM organizer ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Organizer
	for rows.Next() {
		var o domain.Organizer
		if err := rows.Scan(&o.ID, &o.Name, &o.LoginName, &o.Status, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *Repo) CreateOrganizer(ctx context.Context, name, login, passwordHash string) (string, error) {
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO organizer (name, login_name, password_hash) VALUES ($1,$2,$3) RETURNING id`,
		name, login, passwordHash).Scan(&id)
	return id, err
}

func (r *Repo) SetOrganizerStatus(ctx context.Context, id, status string) error {
	ct, err := r.pg.Exec(ctx, `UPDATE organizer SET status=$2 WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- event / flow ----

func (r *Repo) Event(ctx context.Context, id string) (domain.Event, error) {
	var e domain.Event
	err := r.pg.QueryRow(ctx,
		`SELECT id, organizer_id, name, status, start_at, end_at, expected_count,
		        screen_template_code, flow_config_id, created_at
		 FROM event WHERE id=$1`, id,
	).Scan(&e.ID, &e.OrganizerID, &e.Name, &e.Status, &e.StartAt, &e.EndAt,
		&e.ExpectedCount, &e.ScreenTemplateCode, &e.FlowConfigID, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

func (r *Repo) EventsByOrganizer(ctx context.Context, organizerID string) ([]domain.Event, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT id, organizer_id, name, status, start_at, end_at, expected_count,
		        screen_template_code, flow_config_id, created_at
		 FROM event WHERE organizer_id=$1 ORDER BY created_at DESC`, organizerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Event
	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(&e.ID, &e.OrganizerID, &e.Name, &e.Status, &e.StartAt,
			&e.EndAt, &e.ExpectedCount, &e.ScreenTemplateCode, &e.FlowConfigID,
			&e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FlowSchema returns the raw schema_json for a flow config.
func (r *Repo) FlowSchema(ctx context.Context, flowID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := r.pg.QueryRow(ctx,
		`SELECT schema_json FROM flow_config WHERE id=$1`, flowID,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return raw, err
}

// ---- participation ----

// UpsertParticipant inserts a participant for (event, key) if absent.
// Returns the participant id and whether it was newly created.
func (r *Repo) UpsertParticipant(ctx context.Context, eventID, key, idType, idVal string) (id string, created bool, err error) {
	err = r.pg.QueryRow(ctx,
		`INSERT INTO participant (event_id, participant_key, identity_type, identity_value)
		 VALUES ($1,$2,$3,NULLIF($4,''))
		 ON CONFLICT (event_id, participant_key) DO NOTHING
		 RETURNING id`, eventID, key, idType, idVal,
	).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	// conflict: row already exists, fetch its id
	err = r.pg.QueryRow(ctx,
		`SELECT id FROM participant WHERE event_id=$1 AND participant_key=$2`,
		eventID, key).Scan(&id)
	return id, false, err
}

// EnsureParticipation creates the participation row if absent (idempotent).
func (r *Repo) EnsureParticipation(ctx context.Context, eventID, participantID string) (string, error) {
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO participation (event_id, participant_id)
		 VALUES ($1,$2)
		 ON CONFLICT (event_id, participant_id) DO UPDATE SET event_id=EXCLUDED.event_id
		 RETURNING id`, eventID, participantID).Scan(&id)
	return id, err
}

// MarkCheckin sets checkin_at once; returns true only on the first time.
func (r *Repo) MarkCheckin(ctx context.Context, participationID string) (bool, error) {
	ct, err := r.pg.Exec(ctx,
		`UPDATE participation SET checkin_at=now()
		 WHERE id=$1 AND checkin_at IS NULL`, participationID)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

func (r *Repo) RecordStep(ctx context.Context, participationID, stepID, stepType string, payload any) error {
	b, _ := json.Marshal(payload)
	_, err := r.pg.Exec(ctx,
		`INSERT INTO participation_step_record (participation_id, step_id, step_type, payload)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (participation_id, step_id) DO NOTHING`,
		participationID, stepID, stepType, b)
	return err
}

func (r *Repo) SetLastStep(ctx context.Context, participationID, stepID, status string) error {
	_, err := r.pg.Exec(ctx,
		`UPDATE participation SET last_step_id=$2, status=$3 WHERE id=$1`,
		participationID, stepID, status)
	return err
}

// CheckinCount is the source of truth: distinct checked-in participants.
func (r *Repo) CheckinCount(ctx context.Context, eventID string) (int64, error) {
	var n int64
	err := r.pg.QueryRow(ctx,
		`SELECT count(*) FROM participation WHERE event_id=$1 AND checkin_at IS NOT NULL`,
		eventID).Scan(&n)
	return n, err
}

func (r *Repo) ActiveEventIDs(ctx context.Context) ([]string, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT id FROM event WHERE status='active' AND end_at > now()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type ParticipantRow struct {
	ParticipantID string
	IdentityType  string
	IdentityValue string
	Profile       map[string]any
	CheckinAt     *time.Time
	Status        string
	LastStep      string
	FirstSeenAt   time.Time
}

func (r *Repo) ListParticipants(ctx context.Context, eventID string) ([]ParticipantRow, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT p.id, p.identity_type, COALESCE(p.identity_value,''), p.profile,
		        pt.checkin_at, pt.status, COALESCE(pt.last_step_id,''), p.first_seen_at
		 FROM participant p
		 JOIN participation pt ON pt.participant_id = p.id
		 WHERE p.event_id=$1
		 ORDER BY p.first_seen_at ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ParticipantRow
	for rows.Next() {
		var pr ParticipantRow
		var prof []byte
		if err := rows.Scan(&pr.ParticipantID, &pr.IdentityType, &pr.IdentityValue,
			&prof, &pr.CheckinAt, &pr.Status, &pr.LastStep, &pr.FirstSeenAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(prof, &pr.Profile)
		out = append(out, pr)
	}
	return out, rows.Err()
}

// ---- export jobs ----

func (r *Repo) CreateExportJob(ctx context.Context, organizerID, eventID string) (string, error) {
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO export_job (organizer_id, event_id, format, status)
		 VALUES ($1,$2,'csv','pending') RETURNING id`,
		organizerID, eventID).Scan(&id)
	return id, err
}

func (r *Repo) ExportJob(ctx context.Context, id, organizerID string) (domain.ExportJob, error) {
	var j domain.ExportJob
	var key, errMsg *string
	err := r.pg.QueryRow(ctx,
		`SELECT id, organizer_id, event_id, format, status, storage_key, error,
		        requested_at, finished_at
		 FROM export_job WHERE id=$1 AND organizer_id=$2`, id, organizerID,
	).Scan(&j.ID, &j.OrganizerID, &j.EventID, &j.Format, &j.Status, &key, &errMsg,
		&j.RequestedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	if key != nil {
		j.StorageKey = *key
	}
	if errMsg != nil {
		j.Error = *errMsg
	}
	return j, err
}

func (r *Repo) FinishExportJob(ctx context.Context, id, status, storageKey, errMsg string) error {
	_, err := r.pg.Exec(ctx,
		`UPDATE export_job
		 SET status=$2, storage_key=NULLIF($3,''), error=NULLIF($4,''), finished_at=now()
		 WHERE id=$1`, id, status, storageKey, errMsg)
	return err
}

func (r *Repo) SetExportRunning(ctx context.Context, id string) error {
	_, err := r.pg.Exec(ctx, `UPDATE export_job SET status='running' WHERE id=$1`, id)
	return err
}

// ExportJobBare is the worker-side lookup (no tenant scoping; the worker
// trusts the job id it dequeued from the durable stream).
func (r *Repo) ExportJobBare(ctx context.Context, id string) (eventID, organizerID string, err error) {
	err = r.pg.QueryRow(ctx,
		`SELECT event_id, organizer_id FROM export_job WHERE id=$1`, id,
	).Scan(&eventID, &organizerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return eventID, organizerID, err
}

// Pool exposes the pool for seed bootstrapping only.
func (r *Repo) Pool() *pgxpool.Pool { return r.pg }
