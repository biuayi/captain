package repo_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/testdb"
)

func TestSS3ExamAndLottery(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	ctx := context.Background()
	suf := time.Now().UnixNano()
	orgID, _ := r.CreateOrganizer(ctx, "C", fmt.Sprintf("c-%d", suf), "h")
	flowID, _ := r.CreateFlowConfig(ctx, orgID, "f",
		[]byte(`{"version":2,"flowId":"f","name":"n","entryStepId":"s1","steps":[{"id":"s1","type":"checkin"}]}`))
	evID, _ := r.CreateEvent(ctx, orgID, "E", time.Now().Add(-time.Hour),
		time.Now().Add(48*time.Hour), 10, "ink-wash-default", flowID)

	// exam replace + list
	if n, err := r.ReplaceExamQuestions(ctx, evID, "q1", []repo.ExamQ{
		{Stem: "1+1", Options: []string{"1", "2"}, Correct: []int{1}, Score: 5},
		{Stem: "pick", Options: []string{"a", "b", "c"}, Correct: []int{0, 2}, Score: 3, Multi: true},
	}); err != nil || n != 2 {
		t.Fatalf("exam replace = %d %v", n, err)
	}
	if n, _ := r.ReplaceExamQuestions(ctx, evID, "q1", []repo.ExamQ{{Stem: "only", Options: []string{"x"}, Correct: []int{0}}}); n != 1 {
		t.Fatalf("exam re-replace should overwrite, got %d", n)
	}
	qs, _ := r.ListExamQuestions(ctx, evID, "q1")
	if len(qs) != 1 || qs[0].Stem != "only" {
		t.Fatalf("exam list after overwrite = %+v", qs)
	}

	// lottery pools (A leaders default? make B default), prizes per pool
	if _, err := r.UpsertLotteryPool(ctx, evID, "L", "A", "领导池", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpsertLotteryPool(ctx, evID, "L", "B", "普通池", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpsertLotteryPrize(ctx, evID, "L", "A", "grandA", "特等", "grand", 1, 1, ""); err != nil {
		t.Fatalf("prize A: %v", err)
	}
	if _, err := r.UpsertLotteryPrize(ctx, evID, "L", "B", "normB", "纪念", "normal", 100, 1, ""); err != nil {
		t.Fatalf("prize B: %v", err)
	}
	if _, err := r.UpsertLotteryPrize(ctx, evID, "L", "ZZZ", "x", "x", "normal", 1, 1, ""); err == nil {
		t.Fatal("prize with unknown pool must fail")
	}

	// membership: leader→A; mutual exclusion (re-import moves pool)
	if n, err := r.ImportLotteryMembership(ctx, evID, "L", "", []repo.LotteryMemberRow{
		{EmployeeNumber: "LEAD1", PoolCode: "A"},
		{EmployeeNumber: "EMP1", PoolCode: "B"},
	}); err != nil || n != 2 {
		t.Fatalf("membership import = %d %v", n, err)
	}

	// rig: LEAD1 → grandA (in pool A, matches membership) accepted;
	//      EMP1 → grandA (pool A, but EMP1 is pool B) rejected.
	acc, rej, err := r.ImportLotteryRig(ctx, evID, "L", "", []repo.LotteryRigRow{
		{EmployeeNumber: "LEAD1", PrizeCode: "grandA"},
		{EmployeeNumber: "EMP1", PrizeCode: "grandA"},
	})
	if err != nil || acc != 1 || rej != 1 {
		t.Fatalf("rig import = acc%d rej%d %v (want 1/1)", acc, rej, err)
	}

	sum, err := r.LotterySummary(ctx, evID, "L")
	if err != nil {
		t.Fatal(err)
	}
	if sum["rigged"].(int) != 1 {
		t.Fatalf("summary rigged = %v want 1", sum["rigged"])
	}
	if pools, _ := sum["pools"].([]map[string]any); len(pools) != 2 {
		t.Fatalf("summary pools = %v", sum["pools"])
	}
}
