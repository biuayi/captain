// Package repo is the only place that talks SQL. Every organizer-scoped
// query takes organizer_id explicitly (multi-tenant defence, ARCHITECTURE §2).
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"time"

	"github.com/hertz/captain/internal/audit"
	"github.com/hertz/captain/internal/domain"
	"github.com/hertz/captain/internal/lottery"
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
	ID, Name, PhoneLast4, Status      string
	Company, PhoneNumber              string
	ClaimedFP, ClaimedPID, ClaimedJTI string
}

// MatchWhitelistLogin locates a whitelist entry by the login key
// (event_id, company_norm, employee_number); the handler then verifies
// phone_last4 plus optional name/phone per the event's identity flags
// (SS2-05). company_norm must equal identity.CompanyNorm(company).
func (r *Repo) MatchWhitelistLogin(ctx context.Context, eventID, employeeNumber, companyNorm string) (WLEntry, error) {
	var e WLEntry
	var name, phone, cfp, cpid, cjti *string
	err := r.pg.QueryRow(ctx,
		`SELECT id, COALESCE(name,''), COALESCE(company,''), COALESCE(phone_number,''),
		        phone_last4, status,
		        claimed_fingerprint_hash, claimed_participant_id, claimed_jwt_jti
		   FROM event_whitelist_entry
		  WHERE event_id=$1 AND employee_number=$2
		    AND lower(btrim(coalesce(company,'')))=$3`,
		eventID, employeeNumber, companyNorm,
	).Scan(&e.ID, &name, &e.Company, &phone, &e.PhoneLast4, &e.Status, &cfp, &cpid, &cjti)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	if name != nil {
		e.Name = *name
	}
	if phone != nil {
		e.PhoneNumber = *phone
	}
	if cfp != nil {
		e.ClaimedFP = *cfp
	}
	if cpid != nil {
		e.ClaimedPID = *cpid
	}
	if cjti != nil {
		e.ClaimedJTI = *cjti
	}
	return e, err
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
	EmployeeNumber, Name, Phone, PhoneLast4, Company string
}

// InsertWhitelist bulk-inserts entries, skipping duplicates on the
// (event_id, company_norm, employee_number) unique key. name/phone are
// optional (NULL when empty); phone_last4 required (D2). Returns inserted count.
func (r *Repo) InsertWhitelist(ctx context.Context, eventID, organizerID, batchID string, rows []WLImportRow) (int, error) {
	n := 0
	for _, w := range rows {
		ct, err := r.pg.Exec(ctx,
			`INSERT INTO event_whitelist_entry
			   (event_id, organizer_id, employee_number, name, phone_number,
			    phone_last4, company, import_batch_id)
			 VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,NULLIF($7,''),$8)
			 ON CONFLICT (event_id, (lower(btrim(coalesce(company,'')))), employee_number)
			 DO NOTHING`,
			eventID, organizerID, w.EmployeeNumber, w.Name, w.Phone,
			w.PhoneLast4, w.Company, batchID)
		if err != nil {
			return n, err
		}
		n += int(ct.RowsAffected())
	}
	return n, nil
}

// ClaimWhitelistWithJTI claims an unused entry for the first device and
// binds the active session jti (SS2-07; concurrency-safe conditional UPDATE,
// same pattern as ClaimWhitelist).
func (r *Repo) ClaimWhitelistWithJTI(ctx context.Context, entryID, participantID, fpHash, jti string) (bool, error) {
	ct, err := r.pg.Exec(ctx,
		`UPDATE event_whitelist_entry
		    SET status='claimed', claimed_participant_id=$2,
		        claimed_fingerprint_hash=$3, claimed_jwt_jti=$4,
		        claimed_at=now(), updated_at=now()
		  WHERE id=$1 AND status='unused'
		    AND claimed_participant_id IS NULL AND claimed_fingerprint_hash IS NULL`,
		entryID, participantID, fpHash, jti)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

// SetWhitelistJTI rotates the active session jti for an already-claimed
// entry (re-login from the bound identity → previous session invalidated).
func (r *Repo) SetWhitelistJTI(ctx context.Context, entryID, jti string) error {
	_, err := r.pg.Exec(ctx,
		`UPDATE event_whitelist_entry SET claimed_jwt_jti=$2, updated_at=now()
		  WHERE id=$1`, entryID, jti)
	return err
}

// UnbindWhitelist releases a device binding so the user can re-login from a
// new device: clears claimed_*, resets status, and detaches the old
// participant (records retained) freeing the partial unique index (SS2-14).
func (r *Repo) UnbindWhitelist(ctx context.Context, entryID, organizerID string) (oldJTI string, err error) {
	tx, err := r.pg.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var jti *string
	err = tx.QueryRow(ctx,
		`SELECT claimed_jwt_jti FROM event_whitelist_entry
		  WHERE id=$1 AND organizer_id=$2 FOR UPDATE`,
		entryID, organizerID).Scan(&jti)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	// Reset only the entry. The participant row keeps whitelist_entry_id
	// (staff-shape constraint) and its records; on re-login the deterministic
	// participant_key resolves to the same participant and re-claims the now
	// unused entry — no detach needed (SS2-14).
	if _, err = tx.Exec(ctx,
		`UPDATE event_whitelist_entry
		    SET status='unused', claimed_participant_id=NULL,
		        claimed_fingerprint_hash=NULL, claimed_jwt_jti=NULL,
		        claimed_at=NULL, updated_at=now()
		  WHERE id=$1`, entryID); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	if jti != nil {
		oldJTI = *jti
	}
	return oldJTI, nil
}

// EventIdentity returns the identity-factor config for an event (SS2-04/07).
type EventIdentity struct {
	Timezone          string
	StrictFingerprint bool
	RequireName       bool
	RequirePhone      bool
	MultiCompany      bool
}

func (r *Repo) EventIdentity(ctx context.Context, eventID string) (EventIdentity, error) {
	var e EventIdentity
	err := r.pg.QueryRow(ctx,
		`SELECT timezone, strict_fingerprint, identity_require_name,
		        identity_require_phone, identity_multi_company
		   FROM event WHERE id=$1`, eventID,
	).Scan(&e.Timezone, &e.StrictFingerprint, &e.RequireName, &e.RequirePhone, &e.MultiCompany)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// SetEventIdentityFlags persists the identity-factor switches (SS2-04;
// tenant-scoped). Returns ErrNotFound if not this organizer's event.
func (r *Repo) SetEventIdentityFlags(ctx context.Context, eventID, organizerID string, reqName, reqPhone, multiCompany bool) error {
	ct, err := r.pg.Exec(ctx,
		`UPDATE event SET identity_require_name=$3, identity_require_phone=$4,
		        identity_multi_company=$5
		  WHERE id=$1 AND organizer_id=$2`,
		eventID, organizerID, reqName, reqPhone, multiCompany)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ParticipantActiveJTI returns the whitelist entry's current session jti for
// a participant (顶号 check, SS2-09). "" if unbound.
func (r *Repo) ParticipantActiveJTI(ctx context.Context, participantID string) (string, error) {
	var jti *string
	err := r.pg.QueryRow(ctx,
		`SELECT e.claimed_jwt_jti
		   FROM participant p
		   JOIN event_whitelist_entry e ON e.id = p.whitelist_entry_id
		  WHERE p.id=$1`, participantID).Scan(&jti)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if jti == nil {
		return "", err
	}
	return *jti, err
}

// AddWarning appends a D3 participation warning (participation_id optional).
func (r *Repo) AddWarning(ctx context.Context, participationID *string, eventID, kind string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, _ := json.Marshal(detail)
	_, err := r.pg.Exec(ctx,
		`INSERT INTO participation_warning (participation_id, event_id, kind, detail)
		 VALUES ($1,$2,$3,$4::jsonb)`, participationID, eventID, kind, b)
	return err
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

// ---- SS-3: exam question bank ----

type ExamQ struct {
	Idx     int      `json:"idx"`
	Stem    string   `json:"stem"`
	Options []string `json:"options"`
	Correct []int    `json:"correct"`
	Score   int      `json:"score"`
	Multi   bool     `json:"multi"`
}

// ReplaceExamQuestions overwrites the bank for (event, step) (SS3-06).
func (r *Repo) ReplaceExamQuestions(ctx context.Context, eventID, stepID string, qs []ExamQ) (int, error) {
	tx, err := r.pg.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx,
		`DELETE FROM exam_question WHERE event_id=$1 AND step_id=$2`, eventID, stepID); err != nil {
		return 0, err
	}
	for i, q := range qs {
		opt, _ := json.Marshal(q.Options)
		cor, _ := json.Marshal(q.Correct)
		sc := q.Score
		if sc <= 0 {
			sc = 1
		}
		if _, err = tx.Exec(ctx,
			`INSERT INTO exam_question (event_id, step_id, idx, stem, options, correct, score, multi)
			 VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7,$8)`,
			eventID, stepID, i, q.Stem, opt, cor, sc, q.Multi); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(qs), nil
}

func (r *Repo) ListExamQuestions(ctx context.Context, eventID, stepID string) ([]ExamQ, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT idx, stem, options, correct, score, multi FROM exam_question
		  WHERE event_id=$1 AND step_id=$2 ORDER BY idx`, eventID, stepID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExamQ
	for rows.Next() {
		var q ExamQ
		var opt, cor []byte
		if err := rows.Scan(&q.Idx, &q.Stem, &opt, &cor, &q.Score, &q.Multi); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(opt, &q.Options)
		_ = json.Unmarshal(cor, &q.Correct)
		out = append(out, q)
	}
	return out, rows.Err()
}

// ---- SS-3/SS-5: lottery pools / prizes / membership / rig ----

func (r *Repo) UpsertLotteryPool(ctx context.Context, eventID, stepID, code, name string, isDefault bool) (string, error) {
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO lottery_pool (event_id, step_id, code, name, is_default)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (event_id, step_id, code)
		   DO UPDATE SET name=EXCLUDED.name, is_default=EXCLUDED.is_default
		 RETURNING id`, eventID, stepID, code, name, isDefault).Scan(&id)
	return id, err
}

func (r *Repo) UpsertLotteryPrize(ctx context.Context, eventID, stepID, poolCode, code, name, level string, stock, weight int, imageKey string) (string, error) {
	// weight 0 = rig-only (excluded from weighted random); negative clamped.
	if weight < 0 {
		weight = 0
	}
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO lottery_prize (event_id, step_id, pool_id, code, name, level, stock, weight, image_key)
		 SELECT $1,$2,p.id,$4,$5,$6,$7,$8,NULLIF($9,'')
		   FROM lottery_pool p
		  WHERE p.event_id=$1 AND p.step_id=$2 AND p.code=$3
		 ON CONFLICT (event_id, step_id, code) DO UPDATE
		   SET name=EXCLUDED.name, level=EXCLUDED.level, stock=EXCLUDED.stock,
		       weight=EXCLUDED.weight, image_key=EXCLUDED.image_key, pool_id=EXCLUDED.pool_id
		 RETURNING id`,
		eventID, stepID, poolCode, code, name, level, stock, weight, imageKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound // pool code unknown
	}
	return id, err
}

type LotteryMemberRow struct{ EmployeeNumber, PoolCode string }

// ImportLotteryMembership assigns members to pools (UNIQUE enforces mutual
// exclusion: a re-import for the same employee updates the pool).
func (r *Repo) ImportLotteryMembership(ctx context.Context, eventID, stepID, createdBy string, rows []LotteryMemberRow) (int, error) {
	n := 0
	for _, m := range rows {
		ct, err := r.pg.Exec(ctx,
			`INSERT INTO lottery_membership (event_id, step_id, employee_number, pool_id, created_by)
			 SELECT $1,$2,$3,p.id,NULLIF($5,'')::uuid FROM lottery_pool p
			  WHERE p.event_id=$1 AND p.step_id=$2 AND p.code=$4
			 ON CONFLICT (event_id, step_id, employee_number)
			   DO UPDATE SET pool_id=EXCLUDED.pool_id`,
			eventID, stepID, m.EmployeeNumber, m.PoolCode, createdBy)
		if err != nil {
			return n, err
		}
		n += int(ct.RowsAffected())
	}
	return n, nil
}

type LotteryRigRow struct{ EmployeeNumber, PrizeCode string }

// ImportLotteryRig records pre-determined winners; each row validated so the
// prize belongs to the member's assigned pool (default pool if unassigned).
// Returns (accepted, rejected).
func (r *Repo) ImportLotteryRig(ctx context.Context, eventID, stepID, createdBy string, rows []LotteryRigRow) (accepted, rejected int, err error) {
	for _, rg := range rows {
		var poolID string
		e := r.pg.QueryRow(ctx,
			`SELECT pool_id FROM lottery_membership
			  WHERE event_id=$1 AND step_id=$2 AND employee_number=$3`,
			eventID, stepID, rg.EmployeeNumber).Scan(&poolID)
		if errors.Is(e, pgx.ErrNoRows) {
			_ = r.pg.QueryRow(ctx,
				`SELECT id FROM lottery_pool
				  WHERE event_id=$1 AND step_id=$2 AND is_default LIMIT 1`,
				eventID, stepID).Scan(&poolID)
		} else if e != nil {
			return accepted, rejected, e
		}
		var prizePool string
		e = r.pg.QueryRow(ctx,
			`SELECT pool_id FROM lottery_prize
			  WHERE event_id=$1 AND step_id=$2 AND code=$3`,
			eventID, stepID, rg.PrizeCode).Scan(&prizePool)
		if e != nil || poolID == "" || prizePool != poolID {
			rejected++
			continue
		}
		if _, e = r.pg.Exec(ctx,
			`INSERT INTO lottery_rig_entry (event_id, step_id, employee_number, prize_code, created_by)
			 VALUES ($1,$2,$3,$4,NULLIF($5,'')::uuid)
			 ON CONFLICT (event_id, step_id, employee_number)
			   DO UPDATE SET prize_code=EXCLUDED.prize_code`,
			eventID, stepID, rg.EmployeeNumber, rg.PrizeCode, createdBy); e != nil {
			return accepted, rejected, e
		}
		accepted++
	}
	return accepted, rejected, nil
}

// SetEventTimezone sets the activity timezone (D4, tenant-scoped).
func (r *Repo) SetEventTimezone(ctx context.Context, eventID, organizerID, tz string) error {
	ct, err := r.pg.Exec(ctx,
		`UPDATE event SET timezone=$3 WHERE id=$1 AND organizer_id=$2`,
		eventID, organizerID, tz)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LotterySummary returns per-pool prize stock/drawn + rig count (SS5-08).
func (r *Repo) LotterySummary(ctx context.Context, eventID, stepID string) (map[string]any, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT p.code, p.name, p.is_default,
		        COALESCE(sum(z.stock),0), COALESCE(sum(z.drawn),0)
		   FROM lottery_pool p
		   LEFT JOIN lottery_prize z ON z.pool_id=p.id
		  WHERE p.event_id=$1 AND p.step_id=$2
		  GROUP BY p.id, p.code, p.name, p.is_default
		  ORDER BY p.code`, eventID, stepID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pools := []map[string]any{}
	for rows.Next() {
		var code, name string
		var isDef bool
		var stock, drawn int
		if err := rows.Scan(&code, &name, &isDef, &stock, &drawn); err != nil {
			return nil, err
		}
		pools = append(pools, map[string]any{
			"code": code, "name": name, "is_default": isDef,
			"stock": stock, "drawn": drawn, "remaining": stock - drawn})
	}
	var rig int
	_ = r.pg.QueryRow(ctx,
		`SELECT count(*) FROM lottery_rig_entry WHERE event_id=$1 AND step_id=$2`,
		eventID, stepID).Scan(&rig)
	return map[string]any{"pools": pools, "rigged": rig}, rows.Err()
}

// ---- SS-4: runtime — checkin days, stage progression, funnel ----

// MarkCheckinDay records one calendar-day checkin (idempotent per day).
// Returns true only the first time that day is seen.
func (r *Repo) MarkCheckinDay(ctx context.Context, participationID, eventID, day string, lat, lng, acc *float64) (bool, error) {
	ct, err := r.pg.Exec(ctx,
		`INSERT INTO checkin_day (participation_id, event_id, day_date, lat, lng, accuracy)
		 VALUES ($1,$2,$3::date,$4,$5,$6)
		 ON CONFLICT (participation_id, day_date) DO NOTHING`,
		participationID, eventID, day, lat, lng, acc)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

func (r *Repo) DistinctCheckinDays(ctx context.Context, participationID string) (int, error) {
	var n int
	err := r.pg.QueryRow(ctx,
		`SELECT count(*) FROM checkin_day WHERE participation_id=$1`, participationID).Scan(&n)
	return n, err
}

// SetStageDone marks a stage complete (stage_done[stage]=now) and advances
// current_stage to nextStage (caller computes next from the flow).
func (r *Repo) SetStageDone(ctx context.Context, participationID, stage, nextStage string) error {
	_, err := r.pg.Exec(ctx,
		`UPDATE participation
		    SET stage_done = stage_done || jsonb_build_object($2::text, now()),
		        current_stage = NULLIF($3,'')
		  WHERE id=$1`, participationID, stage, nextStage)
	return err
}

func (r *Repo) SetCurrentStage(ctx context.Context, participationID, stage string) error {
	_, err := r.pg.Exec(ctx,
		`UPDATE participation SET current_stage=$2 WHERE id=$1 AND current_stage IS DISTINCT FROM $2`,
		participationID, stage)
	return err
}

func (r *Repo) MarkCompletedAll(ctx context.Context, participationID string) error {
	_, err := r.pg.Exec(ctx,
		`UPDATE participation SET completed_all=true, completed_at=now(), status='completed'
		  WHERE id=$1 AND completed_all=false`, participationID)
	return err
}

// StageState returns the participation id, stage_done set and current_stage
// for a participant (gating + /me, SS4-05/14).
func (r *Repo) StageState(ctx context.Context, eventID, participantID string) (partID string, done map[string]bool, current string, err error) {
	var raw []byte
	var cur *string
	err = r.pg.QueryRow(ctx,
		`SELECT id, stage_done, current_stage FROM participation
		  WHERE event_id=$1 AND participant_id=$2`, eventID, participantID,
	).Scan(&partID, &raw, &cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", map[string]bool{}, "", ErrNotFound
	}
	if err != nil {
		return "", nil, "", err
	}
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	done = map[string]bool{}
	for k := range m {
		done[k] = true
	}
	if cur != nil {
		current = *cur
	}
	return partID, done, current, nil
}

// SetDataFields stores the record columns (partial; "" leaves a column).
func (r *Repo) SetDataFields(ctx context.Context, participationID, f1, f2, deviceID string) error {
	_, err := r.pg.Exec(ctx,
		`UPDATE participation
		    SET data_field_1 = COALESCE(NULLIF($2,''), data_field_1),
		        data_field_2 = COALESCE(NULLIF($3,''), data_field_2),
		        device_id    = COALESCE(NULLIF($4,''), device_id)
		  WHERE id=$1`, participationID, f1, f2, deviceID)
	return err
}

// ParticipatedCount = distinct participations that completed ≥1 stage
// (D5 "参与人数"; the big-screen number, SS4-11/13).
func (r *Repo) ParticipatedCount(ctx context.Context, eventID string) (int64, error) {
	var n int64
	err := r.pg.QueryRow(ctx,
		`SELECT count(*) FROM participation
		  WHERE event_id=$1 AND stage_done <> '{}'::jsonb`, eventID).Scan(&n)
	return n, err
}

// EventFunnel returns per-current_stage counts + participated/completed
// totals (D5 organizer funnel, SS7-05).
func (r *Repo) EventFunnel(ctx context.Context, eventID string) (map[string]any, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT COALESCE(current_stage,'(none)'), count(*) FROM participation
		  WHERE event_id=$1 GROUP BY current_stage`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stages := map[string]int64{}
	for rows.Next() {
		var s string
		var c int64
		if err := rows.Scan(&s, &c); err != nil {
			return nil, err
		}
		stages[s] = c
	}
	var participated, completed int64
	_ = r.pg.QueryRow(ctx, `SELECT count(*) FROM participation WHERE event_id=$1 AND stage_done<>'{}'::jsonb`, eventID).Scan(&participated)
	_ = r.pg.QueryRow(ctx, `SELECT count(*) FROM participation WHERE event_id=$1 AND completed_all`, eventID).Scan(&completed)
	return map[string]any{
		"by_stage": stages, "participated": participated, "completed": completed,
	}, rows.Err()
}

// ---- SS-5: lottery draw (atomic, idempotent, auditable) ----

type LotteryResult struct {
	PrizeID    string `json:"prize_id,omitempty"`
	PrizeCode  string `json:"prize_code,omitempty"`
	PrizeName  string `json:"prize_name,omitempty"`
	Level      string `json:"prize_level"`
	PoolID     string `json:"pool_id,omitempty"`
	ResolvedBy string `json:"resolved_by"` // rig | random | miss
	Repeat     bool   `json:"repeat"`      // true if a prior draw was returned
}

// DrawLottery performs one draw for a participant with strict guarantees:
// per-participant serialization via a transaction advisory lock, one-draw
// idempotency (lottery_result UNIQUE), pool-scoped rig-then-weighted-random,
// and atomic stock decrement (conditional UPDATE — same pattern as
// MarkCheckin/ClaimWhitelist). employeeNumber may be "" (→ default pool/miss).
func (r *Repo) DrawLottery(ctx context.Context, eventID, stepID, participantID, employeeNumber string) (LotteryResult, error) {
	tx, err := r.pg.Begin(ctx)
	if err != nil {
		return LotteryResult{}, err
	}
	defer tx.Rollback(ctx)

	lockKey := eventID + ":" + stepID + ":" + participantID
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return LotteryResult{}, err
	}

	// idempotent: prior result wins
	var res LotteryResult
	var pid, poolID *string
	e := tx.QueryRow(ctx,
		`SELECT prize_id, pool_id, COALESCE(prize_level,''), resolved_by
		   FROM lottery_result WHERE event_id=$1 AND step_id=$2 AND participant_id=$3`,
		eventID, stepID, participantID).Scan(&pid, &poolID, &res.Level, &res.ResolvedBy)
	if e == nil {
		res.Repeat = true
		if pid != nil {
			res.PrizeID = *pid
		}
		if poolID != nil {
			res.PoolID = *poolID
		}
		return res, tx.Commit(ctx)
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return LotteryResult{}, e
	}

	// locate pool: membership → default
	var pool string
	pe := tx.QueryRow(ctx,
		`SELECT pool_id::text FROM lottery_membership
		  WHERE event_id=$1 AND step_id=$2 AND employee_number=$3`,
		eventID, stepID, employeeNumber).Scan(&pool)
	if errors.Is(pe, pgx.ErrNoRows) {
		_ = tx.QueryRow(ctx,
			`SELECT id::text FROM lottery_pool
			  WHERE event_id=$1 AND step_id=$2 AND is_default LIMIT 1`,
			eventID, stepID).Scan(&pool)
	} else if pe != nil {
		return LotteryResult{}, pe
	}

	finalize := func(prizeID, prizeCode, prizeName, level, resolvedBy, poolID string) (LotteryResult, error) {
		var pIDArg, poolArg any
		if prizeID != "" {
			pIDArg = prizeID
		}
		if poolID != "" {
			poolArg = poolID
		}
		if _, ierr := tx.Exec(ctx,
			`INSERT INTO lottery_result
			   (event_id, step_id, participant_id, pool_id, prize_id, prize_level, resolved_by)
			 VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7)`,
			eventID, stepID, participantID, poolArg, pIDArg, level, resolvedBy); ierr != nil {
			return LotteryResult{}, ierr
		}
		return LotteryResult{PrizeID: prizeID, PrizeCode: prizeCode, PrizeName: prizeName,
			Level: level, PoolID: poolID, ResolvedBy: resolvedBy}, tx.Commit(ctx)
	}

	if pool == "" {
		return finalize("", "", "", "none", "miss", "")
	}

	decrement := func(prizeCode string) (id, name, level string, ok bool) {
		e := tx.QueryRow(ctx,
			`UPDATE lottery_prize SET drawn=drawn+1
			  WHERE event_id=$1 AND step_id=$2 AND pool_id=$3 AND code=$4 AND drawn<stock
			  RETURNING id::text, name, level`,
			eventID, stepID, pool, prizeCode).Scan(&id, &name, &level)
		return id, name, level, e == nil
	}

	// pool-internal rig: predetermined winner
	var rigCode string
	if e := tx.QueryRow(ctx,
		`SELECT prize_code FROM lottery_rig_entry
		  WHERE event_id=$1 AND step_id=$2 AND employee_number=$3`,
		eventID, stepID, employeeNumber).Scan(&rigCode); e == nil {
		if id, name, lvl, ok := decrement(rigCode); ok {
			return finalize(id, rigCode, name, lvl, "rig", pool)
		}
		// rigged prize exhausted → fall through to random
	}

	// weighted random over pool's remaining stock
	rows, err := tx.Query(ctx,
		`SELECT id::text, code, level, weight, (stock-drawn)
		   FROM lottery_prize
		  WHERE event_id=$1 AND step_id=$2 AND pool_id=$3 AND drawn<stock`,
		eventID, stepID, pool)
	if err != nil {
		return LotteryResult{}, err
	}
	type cand struct{ id, code, level string }
	var cands []lottery.Candidate
	var meta []cand
	for rows.Next() {
		var c lottery.Candidate
		var m cand
		if err := rows.Scan(&c.PrizeID, &m.code, &c.Level, &c.Weight, &c.Remaining); err != nil {
			rows.Close()
			return LotteryResult{}, err
		}
		m.id, m.level = c.PrizeID, c.Level
		c.Code = m.code
		cands = append(cands, c)
		meta = append(meta, m)
	}
	rows.Close()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for attempts := 0; attempts < len(cands)+1 && len(cands) > 0; attempts++ {
		i := lottery.WeightedPick(cands, rng)
		if i < 0 {
			break
		}
		if id, name, lvl, ok := decrement(meta[i].code); ok {
			return finalize(id, meta[i].code, name, lvl, "random", pool)
		}
		cands[i].Remaining = 0 // lost the race on this prize; retry others
	}
	return finalize("", "", "", "none", "miss", pool)
}

// CreateExportJobKind creates an organizer/event-scoped export job of a
// given kind (participants | lottery_audit | warnings) (SS5-09/SS7-08).
func (r *Repo) CreateExportJobKind(ctx context.Context, organizerID, eventID, kind string) (string, error) {
	var id string
	err := r.pg.QueryRow(ctx,
		`INSERT INTO export_job (organizer_id, event_id, kind, format, status)
		 VALUES ($1,$2,$3,'csv','pending') RETURNING id`,
		organizerID, eventID, kind).Scan(&id)
	return id, err
}

// ParticipantEmployee returns the whitelist employee_number bound to a
// participant ("" for external/anon — resolves to default pool/miss).
func (r *Repo) ParticipantEmployee(ctx context.Context, participantID string) (string, error) {
	var emp *string
	err := r.pg.QueryRow(ctx,
		`SELECT w.employee_number
		   FROM participant p
		   LEFT JOIN event_whitelist_entry w ON w.id=p.whitelist_entry_id
		  WHERE p.id=$1`, participantID).Scan(&emp)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if emp == nil {
		return "", err
	}
	return *emp, err
}

// LotteryResultOf returns a participant's recorded draw (SS5-06).
func (r *Repo) LotteryResultOf(ctx context.Context, eventID, stepID, participantID string) (LotteryResult, error) {
	var res LotteryResult
	var pid, poolID, name *string
	err := r.pg.QueryRow(ctx,
		`SELECT lr.prize_id::text, lr.pool_id::text, COALESCE(lr.prize_level,''),
		        lr.resolved_by, lp.name
		   FROM lottery_result lr
		   LEFT JOIN lottery_prize lp ON lp.id=lr.prize_id
		  WHERE lr.event_id=$1 AND lr.step_id=$2 AND lr.participant_id=$3`,
		eventID, stepID, participantID).Scan(&pid, &poolID, &res.Level, &res.ResolvedBy, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, ErrNotFound
	}
	if pid != nil {
		res.PrizeID = *pid
	}
	if poolID != nil {
		res.PoolID = *poolID
	}
	if name != nil {
		res.PrizeName = *name
	}
	return res, err
}

// LotteryAuditRows streams every draw for audit export (SS5-09).
type LotteryAuditRow struct {
	EmployeeNumber, Name, Pool, ResolvedBy, PrizeCode, PrizeLevel string
	DrawnAt                                                       time.Time
}

func (r *Repo) LotteryAuditRows(ctx context.Context, eventID, stepID string) ([]LotteryAuditRow, error) {
	rows, err := r.pg.Query(ctx,
		`SELECT COALESCE(w.employee_number,''), COALESCE(w.name,''),
		        COALESCE(po.code,''), lr.resolved_by,
		        COALESCE(pz.code,''), COALESCE(lr.prize_level,''), lr.drawn_at
		   FROM lottery_result lr
		   JOIN participant pa ON pa.id=lr.participant_id
		   LEFT JOIN event_whitelist_entry w ON w.id=pa.whitelist_entry_id
		   LEFT JOIN lottery_pool po ON po.id=lr.pool_id
		   LEFT JOIN lottery_prize pz ON pz.id=lr.prize_id
		  WHERE lr.event_id=$1 AND ($2=''
		     OR lr.step_id=$2)
		  ORDER BY lr.drawn_at`, eventID, stepID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LotteryAuditRow
	for rows.Next() {
		var a LotteryAuditRow
		if err := rows.Scan(&a.EmployeeNumber, &a.Name, &a.Pool, &a.ResolvedBy,
			&a.PrizeCode, &a.PrizeLevel, &a.DrawnAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Pool exposes the pool for seed bootstrapping only.
func (r *Repo) Pool() *pgxpool.Pool { return r.pg }
