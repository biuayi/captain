// Package seed bootstraps a demo super-admin, the 《寻道大千》organizer and
// its anniversary event so `docker compose up` shows a real themed flow
// (REQUIREMENTS §11.5). No-op once an organizer already exists.
package seed

import (
	"context"
	"log"
	"time"

	"github.com/hertz/captain/internal/token"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// 《寻道大千》周年庆 themed linear flow (ARCHITECTURE §4, 6 step types).
const xundaoFlow = `{
  "version": 1,
  "flowId": "xundao-anniversary-v1",
  "name": "寻道大千 · 周年庆典",
  "entryStepId": "s1",
  "steps": [
    { "id": "s1", "type": "checkin", "title": "踏入大千 · 签到", "required": true, "skippable": false, "nextStepId": "s2",
      "config": { "countTowardAttendance": true, "dedupeMode": "participant_key", "buttonText": "结缘签到",
        "theme": { "palette": "ink-wash", "mascot": "猪妖/鸟妖/鱼妖" },
        "subtitle": "前世道则大能，今世小妖重修——周年庆典，恭迎道友归山" } },
    { "id": "s2", "type": "form", "title": "道友名册", "required": true, "skippable": false, "nextStepId": "s3",
      "config": { "fields": [
        { "id": "name", "type": "text", "label": "道号/姓名", "required": true, "placeholder": "请输入" },
        { "id": "employee_no", "type": "text", "label": "工号（内部员工填）", "required": false, "placeholder": "选填" },
        { "id": "phone", "type": "phone", "label": "手机号", "required": false, "placeholder": "选填，用于领取礼遇" }
      ]}},
    { "id": "s3", "type": "game", "title": "试炼 · 大千问道", "required": true, "skippable": false, "nextStepId": "s4",
      "config": { "gameType": "quiz_single_choice", "attemptLimit": 1,
        "question": "《寻道大千》中，玩家以何种身份重入修行？",
        "options": ["人间剑客", "小妖之躯", "天庭仙官", "山中隐士"],
        "correctOptionIndex": 1 } },
    { "id": "s4", "type": "charity", "title": "仙道同行 · 公益", "required": true, "skippable": false, "nextStepId": "s5",
      "config": { "title": "以善入道 · 公益同行",
        "body": "周年庆典之际，《寻道大千》联动公益：你的每一次参与，平台将以道友之名捐出一份善意，护山林、助学子。",
        "ctaText": "我愿同行", "pledgeField": "pledge" } },
    { "id": "s5", "type": "reward", "title": "周年庆礼遇", "required": true, "skippable": false, "nextStepId": null,
      "config": { "title": "道友礼遇 · 周年同庆",
        "body": "感谢同行！凭兑换码于游戏内「大千阁」领取周年专属灵宠与法宝。",
        "redeemCode": "XDDQ-ANNIV-2026", "redeemNote": "游戏内 设置→兑换码 输入，活动结束后 7 日内有效" } }
  ]
}`

// Run seeds demo data and logs the participant entry URL + event_token.
func Run(ctx context.Context, pool *pgxpool.Pool, sig *token.Signer, baseURL string) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organizer`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return logExistingEvent(ctx, pool, sig, baseURL)
	}

	adminPw, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	if _, err := pool.Exec(ctx,
		`INSERT INTO admin_user (login_name, password_hash) VALUES ('admin',$1)`,
		string(adminPw)); err != nil {
		return err
	}

	orgPw, _ := bcrypt.GenerateFromPassword([]byte("xundao123"), bcrypt.DefaultCost)
	var orgID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO organizer (name, login_name, password_hash)
		 VALUES ('寻道大千','xundao',$1) RETURNING id`,
		string(orgPw)).Scan(&orgID); err != nil {
		return err
	}

	var flowID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO flow_config (organizer_id, name, schema_json)
		 VALUES ($1,'寻道大千 · 周年庆典',$2::jsonb) RETURNING id`,
		orgID, xundaoFlow).Scan(&flowID); err != nil {
		return err
	}

	start := time.Now().Add(-time.Hour)
	end := time.Now().Add(30 * 24 * time.Hour)
	var eventID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO event (organizer_id, name, status, start_at, end_at,
		        expected_count, screen_template_code, flow_config_id)
		 VALUES ($1,'寻道大千 · 周年庆典','active',$2,$3,5000,'ink-wash-default',$4)
		 RETURNING id`,
		orgID, start, end, flowID).Scan(&eventID); err != nil {
		return err
	}

	log.Printf("seed: demo data created (admin/admin123, xundao/xundao123)")
	return logEvent(sig, baseURL, eventID, end)
}

func logExistingEvent(ctx context.Context, pool *pgxpool.Pool, sig *token.Signer, baseURL string) error {
	var id string
	var end time.Time
	err := pool.QueryRow(ctx,
		`SELECT id, end_at FROM event ORDER BY created_at ASC LIMIT 1`).Scan(&id, &end)
	if err != nil {
		return nil // nothing to log
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
	log.Printf("seed: 寻道大千周年庆 event_id=%s", eventID)
	log.Printf("seed: 用户侧(扫码) %s/m/%s?et=%s", baseURL, eventID, et)
	log.Printf("seed: 大屏       %s/screen/%s", baseURL, eventID)
	log.Printf("seed: event_token=%s", et)
	return nil
}
