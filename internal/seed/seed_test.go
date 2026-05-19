package seed

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hertz/captain/internal/flow"
	"github.com/hertz/captain/internal/testdb"
)

// The seeded v2 demo flow must satisfy the flow-v2 engine, otherwise the
// demo event is unusable on first boot (G1/SS0-18).
func TestDemoFlowIsValidV2(t *testing.T) {
	f, err := flow.Parse([]byte(demoFlow))
	if err != nil {
		t.Fatalf("seed flow invalid: %v", err)
	}
	wantType := []string{"checkin", "form", "exam", "lottery"}
	if len(f.Steps) != len(wantType) {
		t.Fatalf("steps = %d, want %d", len(f.Steps), len(wantType))
	}
	for i, s := range f.Steps {
		if s.Type != wantType[i] {
			t.Errorf("step %d type=%q want %q", i, s.Type, wantType[i])
		}
	}
	if en := f.EnabledStages(); len(en) != 4 ||
		en[0] != "R1" || en[1] != "R2" || en[2] != "R3" || en[3] != "R4" {
		t.Fatalf("enabled stages = %v want R1..R4", en)
	}
}

// seedWhitelist must hit the v2 unique-key conflict target and be idempotent.
func TestSeedWhitelistIdempotent(t *testing.T) {
	pool := testdb.Pool(t)
	ctx := context.Background()
	suf := fmt.Sprint(time.Now().UnixNano())
	var orgID, flowID, evID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO organizer (name, login_name, password_hash) VALUES ('o','o-`+suf+`','h') RETURNING id`).
		Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO flow_config (organizer_id,name,schema_json) VALUES ($1,'f',$2::jsonb) RETURNING id`,
		orgID, demoFlow).Scan(&flowID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO event (organizer_id,name,status,start_at,end_at,expected_count,screen_template_code,flow_config_id)
		 VALUES ($1,'e','active',now()-interval '1h',now()+interval '1d',1,'ink-wash-default',$2) RETURNING id`,
		orgID, flowID).Scan(&evID); err != nil {
		t.Fatal(err)
	}
	seedWhitelist(ctx, pool, evID, orgID)
	seedWhitelist(ctx, pool, evID, orgID) // idempotent (ON CONFLICT)
	seedExam(ctx, pool, evID)
	seedExam(ctx, pool, evID)
	seedLottery(ctx, pool, evID)
	seedLottery(ctx, pool, evID)
	var wl, ex, pr int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM event_whitelist_entry WHERE event_id=$1`, evID).Scan(&wl)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM exam_question WHERE event_id=$1`, evID).Scan(&ex)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM lottery_prize WHERE event_id=$1`, evID).Scan(&pr)
	if wl != 3 || ex != 1 || pr != 1 {
		t.Fatalf("seed not idempotent: whitelist=%d exam=%d prize=%d (want 3/1/1)", wl, ex, pr)
	}
}
