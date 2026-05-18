// Package repo is the only place that talks SQL. Every organizer-scoped
// query takes organizer_id explicitly (multi-tenant defence, ARCHITECTURE §2).
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hertz/captain/internal/audit"
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
	// organizer-only permission snapshot (for JWT embed, SS0-08)
	CanCreateEvent   bool
	CanViewRecords   bool
	CanExportRecords bool
	PermVersion      int
}

// Perms maps the snapshot to the JWT perm map.
func (c Cred) Perms() map[string]bool {
	return map[string]bool{
		"can_create_event":   c.CanCreateEvent,
		"can_view_records":   c.CanViewRecords,
		"can_export_records": c.CanExportRecords,
	}
}

// OrganizerByLogin returns active (non-soft-deleted) organizer credentials
// plus the permission snapshot.
func (r *Repo) OrganizerByLogin(ctx context.Context, login string) (Cred, error) {
	var c Cred
	err := r.pg.QueryRow(ctx,
		`SELECT id, password_hash, status, name,
		        can_create_event, can_view_records, can_export_records, perm_version
		   FROM organizer WHERE login_name=$1 AND deleted_at IS NULL`, login,
	).Scan(&c.ID, &c.PasswordHash, &c.Status, &c.Name,
		&c.CanCreateEvent, &c.CanViewRecords, &c.CanExportRecords, &c.PermVersion)
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

// ListOrganizers returns non-soft-deleted organizers with permission bits.
func (r *Repo) ListOrganizers(ctx context.Context) ([]domain.Organizer, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT id, name, login_name, status,
		        can_create_event, can_view_records, can_export_records,
		        perm_version, created_at
		   FROM organizer WHERE deleted_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Organizer
	for rows.Next() {
		var o domain.Organizer
		if err := rows.Scan(&o.ID, &o.Name, &o.LoginName, &o.Status,
			&o.CanCreateEvent, &o.CanViewRecords, &o.CanExportRecords,
			&o.PermVersion, &o.CreatedAt); err != nil {
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

// SoftDeleteOrganizer marks the organizer deleted (data retained) and disables
// it so it can no longer log in (SS0-02). Idempotent.
func (r *Repo) SoftDeleteOrganizer(ctx context.Context, id string) error {
	ct, err := r.pg.Exec(ctx,
		`UPDATE organizer SET deleted_at=now(), status='disabled'
		  WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetOrganizerPassword sets a new bcrypt hash (SS0-04).
func (r *Repo) ResetOrganizerPassword(ctx context.Context, id, passwordHash string) error {
	ct, err := r.pg.Exec(ctx,
		`UPDATE organizer SET password_hash=$2 WHERE id=$1 AND deleted_at IS NULL`,
		id, passwordHash)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetOrganizerPermissions updates the three permission bits and bumps
// perm_version so existing JWT snapshots are invalidated (SS0-06). Returns the
// new perm_version.
func (r *Repo) SetOrganizerPermissions(ctx context.Context, id string, canCreate, canView, canExport bool) (int, error) {
	var pv int
	err := r.pg.QueryRow(ctx,
		`UPDATE organizer
		    SET can_create_event=$2, can_view_records=$3, can_export_records=$4,
		        perm_version=perm_version+1
		  WHERE id=$1 AND deleted_at IS NULL
		  RETURNING perm_version`,
		id, canCreate, canView, canExport).Scan(&pv)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return pv, err
}

// OrganizerPermVersion returns the authoritative perm_version (PG truth used
// by the OrgPerm middleware fallback, P0-14).
func (r *Repo) OrganizerPermVersion(ctx context.Context, id string) (int, error) {
	var pv int
	err := r.pg.QueryRow(ctx,
		`SELECT perm_version FROM organizer WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&pv)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return pv, err
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

// ---- T-021: 活动 / 流程编排 CRUD（均按 organizer 租户作用域）----

func (r *Repo) CreateFlowConfig(ctx context.Context, organizerID, name string, schema []byte) (string, error) {
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO flow_config (organizer_id, name, schema_json)
		 VALUES ($1,$2,$3::jsonb) RETURNING id`, organizerID, name, schema).Scan(&id)
	return id, err
}

type FlowRow struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   int       `json:"version"`
	Published bool      `json:"published"`
	CreatedAt time.Time `json:"created_at"`
}

func (r *Repo) ListFlowConfigs(ctx context.Context, organizerID string) ([]FlowRow, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT id, name, version, published, created_at FROM flow_config
		 WHERE organizer_id=$1 ORDER BY created_at DESC`, organizerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FlowRow
	for rows.Next() {
		var f FlowRow
		if err := rows.Scan(&f.ID, &f.Name, &f.Version, &f.Published, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FlowOwned reports whether a flow_config belongs to the organizer.
func (r *Repo) FlowOwned(ctx context.Context, flowID, organizerID string) bool {
	var n int
	_ = r.pg.QueryRow(ctx,
		`SELECT count(*) FROM flow_config WHERE id=$1 AND organizer_id=$2`,
		flowID, organizerID).Scan(&n)
	return n == 1
}

func (r *Repo) CreateEvent(ctx context.Context, organizerID, name string, start, end time.Time, expected int, screenTpl, flowID string) (string, error) {
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO event (organizer_id, name, status, start_at, end_at,
		        expected_count, screen_template_code, flow_config_id)
		 VALUES ($1,$2,'draft',$3,$4,$5,$6,$7) RETURNING id`,
		organizerID, name, start, end, expected, screenTpl, flowID).Scan(&id)
	return id, err
}

// UpdateEvent updates mutable fields, tenant-scoped. Returns ErrNotFound if
// the event isn't this organizer's.
func (r *Repo) UpdateEvent(ctx context.Context, id, organizerID, name string, start, end time.Time, expected int, screenTpl, flowID, status string) error {
	ct, err := r.pg.Exec(ctx,
		`UPDATE event SET name=$3, start_at=$4, end_at=$5, expected_count=$6,
		        screen_template_code=$7, flow_config_id=$8, status=$9
		 WHERE id=$1 AND organizer_id=$2`,
		id, organizerID, name, start, end, expected, screenTpl, flowID, status)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) SetEventStatus(ctx context.Context, id, organizerID, status string) error {
	ct, err := r.pg.Exec(ctx,
		`UPDATE event SET status=$3 WHERE id=$1 AND organizer_id=$2`,
		id, organizerID, status)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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

// SetCheckinGeo records the (optional) checkin location once (REQ-CHANGE-002 T-080).
func (r *Repo) SetCheckinGeo(ctx context.Context, participationID string, lat, lng, acc float64) error {
	_, err := r.pg.Exec(ctx,
		`UPDATE participation SET checkin_lat=$2, checkin_lng=$3, checkin_accuracy=$4
		 WHERE id=$1 AND checkin_lat IS NULL`, participationID, lat, lng, acc)
	return err
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
	Form          map[string]any // 最近一次 form step 采集的登记信息（动态字段）
	Lat           *float64
	Lng           *float64
	Accuracy      *float64
	CheckinAt     *time.Time
	Status        string
	LastStep      string
	FirstSeenAt   time.Time
}

func (r *Repo) ListParticipants(ctx context.Context, eventID string) ([]ParticipantRow, error) {
	// 登记信息存在 participation_step_record(step_type='form')，取最近一条；
	// 这样活动方修改采集字段后，列表/导出自动反映实际采集内容。
	rows, err := r.pg.Query(ctx,
		`SELECT p.id, p.identity_type, COALESCE(p.identity_value,''), p.profile,
		        pt.checkin_at, pt.status, COALESCE(pt.last_step_id,''), p.first_seen_at,
		        COALESCE(f.payload,'{}'::jsonb),
		        pt.checkin_lat, pt.checkin_lng, pt.checkin_accuracy
		 FROM participant p
		 JOIN participation pt ON pt.participant_id = p.id
		 LEFT JOIN LATERAL (
		   SELECT payload FROM participation_step_record sr
		   WHERE sr.participation_id = pt.id AND sr.step_type='form'
		   ORDER BY sr.occurred_at DESC LIMIT 1
		 ) f ON true
		 WHERE p.event_id=$1
		 ORDER BY p.first_seen_at ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ParticipantRow
	for rows.Next() {
		var pr ParticipantRow
		var prof, form []byte
		if err := rows.Scan(&pr.ParticipantID, &pr.IdentityType, &pr.IdentityValue,
			&prof, &pr.CheckinAt, &pr.Status, &pr.LastStep, &pr.FirstSeenAt, &form,
			&pr.Lat, &pr.Lng, &pr.Accuracy); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(prof, &pr.Profile)
		_ = json.Unmarshal(form, &pr.Form)
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

// ---- REQ-CHANGE-001: whitelist + fingerprint identity ----

type ParticipantUpsert struct {
	EventID          string
	ParticipantKey   string
	IdentityType     string
	IdentityValue    string
	ParticipantType  string // staff | external
	FingerprintHash  string
	WhitelistEntryID *string // non-nil only for staff
}

// UpsertParticipantFull inserts (or returns existing) participant carrying the
// REQ-CHANGE-001 columns. Idempotent on (event_id, participant_key).
func (r *Repo) UpsertParticipantFull(ctx context.Context, p ParticipantUpsert) (id string, created bool, err error) {
	err = r.pg.QueryRow(ctx,
		`INSERT INTO participant
		   (event_id, participant_key, identity_type, identity_value,
		    participant_type, fingerprint_hash, whitelist_entry_id)
		 VALUES ($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),$7)
		 ON CONFLICT (event_id, participant_key) DO NOTHING
		 RETURNING id`,
		p.EventID, p.ParticipantKey, p.IdentityType, p.IdentityValue,
		p.ParticipantType, p.FingerprintHash, p.WhitelistEntryID).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	err = r.pg.QueryRow(ctx,
		`SELECT id FROM participant WHERE event_id=$1 AND participant_key=$2`,
		p.EventID, p.ParticipantKey).Scan(&id)
	return id, false, err
}

type WLEntry struct {
	ID, Name, PhoneLast4, Status string
	ClaimedFP, ClaimedPID        string
}

func (r *Repo) WhitelistByEmployee(ctx context.Context, eventID, employeeNumber string) (WLEntry, error) {
	var e WLEntry
	var cfp, cpid *string
	err := r.pg.QueryRow(ctx,
		`SELECT id, name, phone_last4, status, claimed_fingerprint_hash, claimed_participant_id
		   FROM event_whitelist_entry WHERE event_id=$1 AND employee_number=$2`,
		eventID, employeeNumber,
	).Scan(&e.ID, &e.Name, &e.PhoneLast4, &e.Status, &cfp, &cpid)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	if cfp != nil {
		e.ClaimedFP = *cfp
	}
	if cpid != nil {
		e.ClaimedPID = *cpid
	}
	return e, err
}

// ClaimWhitelist binds an entry to the first device (codex concurrency-safe
// conditional UPDATE). Returns true only for the winning claimer.
func (r *Repo) ClaimWhitelist(ctx context.Context, entryID, participantID, fpHash string) (bool, error) {
	ct, err := r.pg.Exec(ctx,
		`UPDATE event_whitelist_entry
		    SET status='claimed', claimed_participant_id=$2,
		        claimed_fingerprint_hash=$3, claimed_at=now(), updated_at=now()
		  WHERE id=$1 AND status='unused'
		    AND claimed_participant_id IS NULL AND claimed_fingerprint_hash IS NULL`,
		entryID, participantID, fpHash)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

func (r *Repo) WhitelistClaimState(ctx context.Context, entryID string) (status, claimedPID, claimedFP string, err error) {
	var cp, cf *string
	err = r.pg.QueryRow(ctx,
		`SELECT status, claimed_participant_id, claimed_fingerprint_hash
		   FROM event_whitelist_entry WHERE id=$1`, entryID,
	).Scan(&status, &cp, &cf)
	if cp != nil {
		claimedPID = *cp
	}
	if cf != nil {
		claimedFP = *cf
	}
	return
}

type WLImportRow struct {
	EmployeeNumber, Name, Phone, PhoneLast4 string
}

// InsertWhitelist bulk-inserts entries, skipping duplicates
// (event_id, employee_number). Returns inserted count.
func (r *Repo) InsertWhitelist(ctx context.Context, eventID, organizerID, batchID string, rows []WLImportRow) (int, error) {
	n := 0
	for _, w := range rows {
		ct, err := r.pg.Exec(ctx,
			`INSERT INTO event_whitelist_entry
			   (event_id, organizer_id, employee_number, name, phone_number, phone_last4, import_batch_id)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)
			 ON CONFLICT (event_id, employee_number) DO NOTHING`,
			eventID, organizerID, w.EmployeeNumber, w.Name, w.Phone, w.PhoneLast4, batchID)
		if err != nil {
			return n, err
		}
		n += int(ct.RowsAffected())
	}
	return n, nil
}

type WLRow struct {
	EmployeeNumber string `json:"employee_number"`
	Name           string `json:"name"`
	PhoneLast4     string `json:"phone_last4"`
	Status         string `json:"status"`
}

func (r *Repo) ListWhitelist(ctx context.Context, eventID, organizerID string) ([]WLRow, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT employee_number, name, phone_last4, status
		   FROM event_whitelist_entry
		  WHERE event_id=$1 AND organizer_id=$2
		  ORDER BY created_at ASC`, eventID, organizerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WLRow
	for rows.Next() {
		var w WLRow
		if err := rows.Scan(&w.EmployeeNumber, &w.Name, &w.PhoneLast4, &w.Status); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ---- P0-09/10: audit_log (append-only) ----

func (r *Repo) AppendAudit(ctx context.Context, e audit.Entry) error {
	meta := e.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	b, _ := json.Marshal(meta)
	_, err := r.pg.Exec(ctx,
		`INSERT INTO audit_log (actor_role, actor_id, action, target, meta, request_id)
		 VALUES ($1, NULLIF($2,'')::uuid, $3, NULLIF($4,''), $5::jsonb, NULLIF($6,''))`,
		e.ActorRole, e.ActorID, e.Action, e.Target, b, e.RequestID)
	return err
}

// ListAudit returns audit rows filtered by optional action and time window,
// newest first, capped at limit.
func (r *Repo) ListAudit(ctx context.Context, action string, from, to time.Time, limit int) ([]audit.Row, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.pg.Query(ctx,
		`SELECT id, actor_role, COALESCE(actor_id::text,''), action, COALESCE(target,''),
		        meta, COALESCE(request_id,''), created_at
		   FROM audit_log
		  WHERE ($1='' OR action=$1)
		    AND ($2::timestamptz IS NULL OR created_at >= $2)
		    AND ($3::timestamptz IS NULL OR created_at <= $3)
		  ORDER BY created_at DESC
		  LIMIT $4`,
		action, nullTime(from), nullTime(to), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []audit.Row
	for rows.Next() {
		var a audit.Row
		var meta []byte
		if err := rows.Scan(&a.ID, &a.ActorRole, &a.ActorID, &a.Action, &a.Target,
			&meta, &a.RequestID, &a.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(meta, &a.Meta)
		out = append(out, a)
	}
	return out, rows.Err()
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ---- P0-11/SS0-10: platform_config (encrypted at rest) ----

// UpsertPlatformConfig stores the already-encrypted value + display mask.
func (r *Repo) UpsertPlatformConfig(ctx context.Context, key string, valueEnc []byte, masked, updatedBy string) error {
	_, err := r.pg.Exec(ctx,
		`INSERT INTO platform_config (key, value_enc, masked, updated_by, updated_at)
		 VALUES ($1,$2,$3,NULLIF($4,'')::uuid, now())
		 ON CONFLICT (key) DO UPDATE
		   SET value_enc=EXCLUDED.value_enc, masked=EXCLUDED.masked,
		       updated_by=EXCLUDED.updated_by, updated_at=now()`,
		key, valueEnc, masked, updatedBy)
	return err
}

// GetPlatformConfig returns the encrypted blob + mask for a key.
func (r *Repo) GetPlatformConfig(ctx context.Context, key string) (valueEnc []byte, masked string, err error) {
	err = r.pg.QueryRow(ctx,
		`SELECT value_enc, masked FROM platform_config WHERE key=$1`, key,
	).Scan(&valueEnc, &masked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	return valueEnc, masked, err
}

// ListPlatformConfigKeys returns (key, masked) for all stored configs.
func (r *Repo) ListPlatformConfigKeys(ctx context.Context) (map[string]string, error) {
	rows, err := r.pg.Query(ctx, `SELECT key, masked FROM platform_config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, m string
		if err := rows.Scan(&k, &m); err != nil {
			return nil, err
		}
		out[k] = m
	}
	return out, rows.Err()
}

// ---- SS0-14/16: db_dump export jobs (no tenant/event) ----

func (r *Repo) CreateDBExportJob(ctx context.Context) (string, error) {
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO export_job (kind, format, status)
		 VALUES ('db_dump','sql','pending') RETURNING id`).Scan(&id)
	return id, err
}

// DBExportJob returns a db_dump job by id (super-admin only; no tenant scope).
func (r *Repo) DBExportJob(ctx context.Context, id string) (domain.ExportJob, error) {
	var j domain.ExportJob
	var key, errMsg *string
	err := r.pg.QueryRow(ctx,
		`SELECT id, kind, format, status, storage_key, error, requested_at, finished_at
		   FROM export_job WHERE id=$1 AND kind='db_dump'`, id,
	).Scan(&j.ID, &j.Kind, &j.Format, &j.Status, &key, &errMsg, &j.RequestedAt, &j.FinishedAt)
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

// ExportJobKind returns the job kind (worker dispatch).
func (r *Repo) ExportJobKind(ctx context.Context, id string) (string, error) {
	var kind string
	err := r.pg.QueryRow(ctx, `SELECT kind FROM export_job WHERE id=$1`, id).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return kind, err
}

// ---- SS-1: templates ----

func (r *Repo) CreateTemplate(ctx context.Context, kind, code, name string, version int, organizerID *string, manifest []byte) (string, error) {
	if len(manifest) == 0 {
		manifest = []byte("{}")
	}
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO template (kind, code, name, version, organizer_id, manifest)
		 VALUES ($1,$2,$3,$4,$5,$6::jsonb) RETURNING id`,
		kind, code, name, version, organizerID, manifest).Scan(&id)
	return id, err
}

func scanTemplates(rows pgx.Rows) ([]domain.Template, error) {
	defer rows.Close()
	var out []domain.Template
	for rows.Next() {
		var t domain.Template
		var org *string
		var man []byte
		if err := rows.Scan(&t.ID, &t.Kind, &t.Code, &t.Name, &t.Version,
			&t.Status, &org, &man, &t.CreatedAt); err != nil {
			return nil, err
		}
		if org != nil {
			t.OrganizerID = *org
		}
		_ = json.Unmarshal(man, &t.Manifest)
		out = append(out, t)
	}
	return out, rows.Err()
}

const tplCols = `id, kind, code, name, version, status, organizer_id, manifest, created_at`

// ListTemplates (admin) returns all templates of a kind ("" = any).
func (r *Repo) ListTemplates(ctx context.Context, kind string) ([]domain.Template, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT `+tplCols+` FROM template WHERE ($1='' OR kind=$1) ORDER BY created_at DESC`, kind)
	if err != nil {
		return nil, err
	}
	return scanTemplates(rows)
}

// ListTemplatesForOrganizer returns published templates visible to an org:
// globals (organizer_id IS NULL) plus that org's own customs.
func (r *Repo) ListTemplatesForOrganizer(ctx context.Context, kind, organizerID string) ([]domain.Template, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT `+tplCols+` FROM template
		  WHERE status='published' AND ($1='' OR kind=$1)
		    AND (organizer_id IS NULL OR organizer_id::text=$2)
		  ORDER BY created_at DESC`, kind, organizerID)
	if err != nil {
		return nil, err
	}
	return scanTemplates(rows)
}

func (r *Repo) UpdateTemplateStatus(ctx context.Context, id, status string) error {
	ct, err := r.pg.Exec(ctx, `UPDATE template SET status=$2 WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) AddTemplateAsset(ctx context.Context, templateID, storageKey, mime, role string, size int64) (string, error) {
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO template_asset (template_id, storage_key, mime, size, role)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		templateID, storageKey, mime, size, role).Scan(&id)
	return id, err
}

// TemplateCodeAllowed reports whether a code is a published template of kind
// visible to organizerID (global or that org's custom) — event validation.
func (r *Repo) TemplateCodeAllowed(ctx context.Context, kind, code, organizerID string) bool {
	if code == "" {
		return false
	}
	var n int
	_ = r.pg.QueryRow(ctx,
		`SELECT count(*) FROM template
		  WHERE kind=$1 AND code=$2 AND status='published'
		    AND (organizer_id IS NULL OR organizer_id::text=$3)`,
		kind, code, organizerID).Scan(&n)
	return n > 0
}

// AnyTemplatePublished reports whether the registry has ≥1 published template
// of kind visible to organizerID (used for permissive event validation:
// enforce only once templates exist, SS1-08).
func (r *Repo) AnyTemplatePublished(ctx context.Context, kind, organizerID string) bool {
	var n int
	_ = r.pg.QueryRow(ctx,
		`SELECT count(*) FROM template
		  WHERE kind=$1 AND status='published'
		    AND (organizer_id IS NULL OR organizer_id::text=$2)`,
		kind, organizerID).Scan(&n)
	return n > 0
}

// Pool exposes the pool for seed bootstrapping only.
func (r *Repo) Pool() *pgxpool.Pool { return r.pg }
