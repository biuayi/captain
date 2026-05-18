package repo_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/testdb"
)

func TestSS7RecordsWarningsMyRecords(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	ctx := context.Background()
	suf := time.Now().UnixNano()
	orgID, _ := r.CreateOrganizer(ctx, "C", fmt.Sprintf("c-%d", suf), "h")
	flowID, _ := r.CreateFlowConfig(ctx, orgID, "f",
		[]byte(`{"version":2,"flowId":"f","name":"n","entryStepId":"s1","steps":[{"id":"s1","type":"checkin"}]}`))
	evID, _ := r.CreateEvent(ctx, orgID, "E", time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour), 10, "ink-wash-default", flowID)
	_, _ = r.InsertWhitelist(ctx, evID, orgID, "b", []repo.WLImportRow{
		{EmployeeNumber: "E9", Name: "赵六", Phone: "+8613500008888", PhoneLast4: "8888", Company: "甲公司"},
	})
	we, err := r.MatchWhitelistLogin(ctx, evID, "E9", "甲公司")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	wid := we.ID
	pid, _, err := r.UpsertParticipantFull(ctx, repo.ParticipantUpsert{
		EventID: evID, ParticipantKey: "k" + fmt.Sprint(suf), IdentityType: "staff_whitelist",
		ParticipantType: "staff", FingerprintHash: "fp-abc", WhitelistEntryID: &wid,
	})
	if err != nil {
		t.Fatal(err)
	}
	ppID, _ := r.EnsureParticipation(ctx, evID, pid)
	_ = r.RecordStep(ctx, ppID, "s1", "checkin", map[string]any{"ok": true})
	_ = r.SetDataFields(ctx, ppID, "文本一", "uploads/img-1", "dev-XYZ")

	rows, err := r.ListParticipants(ctx, evID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list = %d %v", len(rows), err)
	}
	p := rows[0]
	if p.Name != "赵六" || p.EmployeeNumber != "E9" || p.Company != "甲公司" ||
		p.PhoneLast4 != "8888" || p.PhoneNumber != "+8613500008888" ||
		p.Fingerprint != "fp-abc" || p.DeviceID != "dev-XYZ" ||
		p.DataField1 != "文本一" || p.DataField2 != "uploads/img-1" {
		t.Fatalf("record fields wrong: %+v", p)
	}

	// warnings
	_ = r.AddWarning(ctx, &ppID, evID, "fingerprint_mismatch", map[string]any{"emp": "E9"})
	ws, err := r.ListWarnings(ctx, evID)
	if err != nil || len(ws) != 1 || ws[0].Kind != "fingerprint_mismatch" {
		t.Fatalf("warnings = %+v %v", ws, err)
	}

	// my records
	mr, err := r.MyRecords(ctx, evID, pid)
	if err != nil {
		t.Fatalf("myrecords: %v", err)
	}
	if steps, _ := mr["steps"].([]map[string]any); len(steps) != 1 {
		t.Fatalf("myrecords steps = %v", mr["steps"])
	}
}
