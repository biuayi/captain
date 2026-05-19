// Package seed bootstraps a working v2 demo (super-admin + organizer + an
// active event with the full R1-R4 flow, whitelist for strong-identity
// login, an exam question and a lottery pool/prize) so `docker compose up`
// is immediately exercisable. No-op once an organizer exists (G1/SS0-18).
package seed

import (
	"context"
	"log"
	"time"

	"github.com/hertz/captain/internal/token"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// v2 demo flow: R1 单日签到 → R2 问卷(文本+图片) → R3 考试 → R4 抽奖.
const demoFlow = `{
  "version": 2,
  "flowId": "demo-v2",
  "name": "示例活动 · 内部员工",
  "entryStepId": "r1",
  "steps": [
    { "id": "r1", "type": "checkin", "stage": "R1", "title": "每日签到", "required": true,
      "nextStepId": "r2", "config": { "days": 1, "countTowardAttendance": true } },
    { "id": "r2", "type": "form", "stage": "R2", "title": "调查问卷", "required": true,
      "nextStepId": "r3", "config": { "fields": [
        { "id": "opinion", "type": "textarea", "label": "您的建议", "required": false },
        { "id": "photo_image", "type": "image", "label": "现场照片", "required": false }
      ]}},
    { "id": "r3", "type": "exam", "stage": "R3", "title": "在线考试", "required": true,
      "nextStepId": "r4", "config": { "mode": "all", "passScore": 1, "attemptLimit": 3 } },
    { "id": "r4", "type": "lottery", "stage": "R4", "title": "在线抽奖", "required": true,
      "nextStepId": null, "config": { "drawLimit": 1, "grandPushToScreen": true } }
  ]
}`

// Run seeds demo data and logs the participant entry URL + event_token.
func Run(ctx context.Context, pool *pgxpool.Pool, sig *token.Signer, baseURL, adminPwPlain, orgPwPlain string) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organizer`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return logExistingEvent(ctx, pool, sig, baseURL)
	}

	adminPw, _ := bcrypt.GenerateFromPassword([]byte(adminPwPlain), bcrypt.DefaultCost)
	if _, err := pool.Exec(ctx,
		`INSERT INTO admin_user (login_name, password_hash) VALUES ('admin',$1)`,
		string(adminPw)); err != nil {
		return err
	}

	orgPw, _ := bcrypt.GenerateFromPassword([]byte(orgPwPlain), bcrypt.DefaultCost)
	var orgID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO organizer (name, login_name, password_hash)
		 VALUES ('示例企业','demo',$1) RETURNING id`,
		string(orgPw)).Scan(&orgID); err != nil {
		return err
	}

	var flowID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO flow_config (organizer_id, name, schema_json)
		 VALUES ($1,'示例活动 v2',$2::jsonb) RETURNING id`,
		orgID, demoFlow).Scan(&flowID); err != nil {
		return err
	}

	start := time.Now().Add(-time.Hour)
	end := time.Now().Add(30 * 24 * time.Hour)
	var eventID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO event (organizer_id, name, status, start_at, end_at,
		        expected_count, screen_template_code, flow_config_id)
		 VALUES ($1,'示例活动 · 周年庆','active',$2,$3,5000,'ink-wash-default',$4)
		 RETURNING id`,
		orgID, start, end, flowID).Scan(&eventID); err != nil {
		return err
	}

	seedWhitelist(ctx, pool, eventID, orgID)
	seedExam(ctx, pool, eventID)
	seedLottery(ctx, pool, eventID)

	log.Printf("seed: v2 demo — admin/%s  demo/%s  登录(白名单 E1001/E1002/E1003 手机后4位 1234/5678/9012)",
		adminPwPlain, orgPwPlain)
	return logEvent(sig, baseURL, eventID, end)
}

// seedWhitelist idempotently ensures the demo login whitelist exists.
// Conflict target = the v2 unique key (event_id, company_norm, employee_number).
func seedWhitelist(ctx context.Context, pool *pgxpool.Pool, eventID, orgID string) {
	staff := [][3]string{
		{"E1001", "张三", "+8613800001234"},
		{"E1002", "李四", "+8613900005678"},
		{"E1003", "王五", "+8613700009012"},
	}
	for _, s := range staff {
		last4 := s[2][len(s[2])-4:]
		if _, err := pool.Exec(ctx,
			`INSERT INTO event_whitelist_entry
			   (event_id, organizer_id, employee_number, name, phone_number, phone_last4, import_batch_id)
			 VALUES ($1,$2,$3,$4,$5,$6,'seed')
			 ON CONFLICT (event_id, (lower(btrim(coalesce(company,'')))), employee_number)
			 DO NOTHING`,
			eventID, orgID, s[0], s[1], s[2], last4); err != nil {
			log.Printf("seed: whitelist %s: %v", s[0], err)
		}
	}
}

func seedExam(ctx context.Context, pool *pgxpool.Pool, eventID string) {
	if _, err := pool.Exec(ctx,
		`INSERT INTO exam_question (event_id, step_id, idx, stem, options, correct, score, multi)
		 SELECT $1,'r3',0,'本公司成立于哪一年？',
		        '["2010","2015","2020"]'::jsonb,'[1]'::jsonb,5,false
		 WHERE NOT EXISTS (SELECT 1 FROM exam_question WHERE event_id=$1 AND step_id='r3')`,
		eventID); err != nil {
		log.Printf("seed: exam: %v", err)
	}
}

func seedLottery(ctx context.Context, pool *pgxpool.Pool, eventID string) {
	var poolID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO lottery_pool (event_id, step_id, code, name, is_default)
		 VALUES ($1,'r4','default','默认奖池',true)
		 ON CONFLICT (event_id, step_id, code) DO UPDATE SET name=EXCLUDED.name
		 RETURNING id`, eventID).Scan(&poolID); err != nil {
		log.Printf("seed: lottery pool: %v", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO lottery_prize (event_id, step_id, pool_id, code, name, level, stock, weight)
		 VALUES ($1,'r4',$2,'prize1','纪念奖','normal',1000,1)
		 ON CONFLICT (event_id, step_id, code) DO NOTHING`,
		eventID, poolID); err != nil {
		log.Printf("seed: lottery prize: %v", err)
	}
}

func logExistingEvent(ctx context.Context, pool *pgxpool.Pool, sig *token.Signer, baseURL string) error {
	var id, orgID string
	var end time.Time
	err := pool.QueryRow(ctx,
		`SELECT id, organizer_id, end_at FROM event ORDER BY created_at ASC LIMIT 1`,
	).Scan(&id, &orgID, &end)
	if err != nil {
		return nil
	}
	return logEvent(sig, baseURL, id, end)
}

func logEvent(sig *token.Signer, baseURL, eventID string, end time.Time) error {
	et, err := sig.Sign(token.Claims{
		Kind: token.KindEvent, EventID: eventID,
		NotBefore: time.Now().Add(-time.Hour).Unix(),
		ExpiresAt: end.Add(24 * time.Hour).Unix(),
	})
	if err != nil {
		return err
	}
	log.Printf("seed: 示例活动 v2 event_id=%s", eventID)
	log.Printf("seed: 用户侧(扫码) %s/m/%s?et=%s", baseURL, eventID, et)
	log.Printf("seed: 大屏       %s/screen/%s", baseURL, eventID)
	log.Printf("seed: event_token=%s", et)
	return nil
}
