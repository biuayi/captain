package repo_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/testdb"
)

func TestDrawLottery_Concurrency(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	ctx := context.Background()
	suf := time.Now().UnixNano()
	orgID, _ := r.CreateOrganizer(ctx, "C", fmt.Sprintf("c-%d", suf), "h")
	flowID, _ := r.CreateFlowConfig(ctx, orgID, "f",
		[]byte(`{"version":2,"flowId":"f","name":"n","entryStepId":"s1","steps":[{"id":"s1","type":"checkin"}]}`))
	evID, _ := r.CreateEvent(ctx, orgID, "E", time.Now().Add(-time.Hour),
		time.Now().Add(48*time.Hour), 10, "ink-wash-default", flowID)
	const step = "L"

	if _, err := r.UpsertLotteryPool(ctx, evID, step, "A", "默认池", true); err != nil {
		t.Fatal(err)
	}
	const stock = 3
	if _, err := r.UpsertLotteryPrize(ctx, evID, step, "A", "p1", "奖", "normal", stock, 1, ""); err != nil {
		t.Fatal(err)
	}
	// rigged member + prize (separate limited prize so rig is observable)
	// weight 0 → never picked by weighted random; only reachable via rig.
	if _, err := r.UpsertLotteryPrize(ctx, evID, step, "A", "grand", "大奖", "grand", 1, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ImportLotteryMembership(ctx, evID, step, "", []repo.LotteryMemberRow{{EmployeeNumber: "LEAD", PoolCode: "A"}}); err != nil {
		t.Fatal(err)
	}
	if a, _, err := r.ImportLotteryRig(ctx, evID, step, "", []repo.LotteryRigRow{{EmployeeNumber: "LEAD", PrizeCode: "grand"}}); err != nil || a != 1 {
		t.Fatalf("rig import a=%d %v", a, err)
	}

	mkPart := func(tag string) string {
		p, _, err := r.UpsertParticipantFull(ctx, repo.ParticipantUpsert{
			EventID: evID, ParticipantKey: tag, IdentityType: "external_fingerprint",
			ParticipantType: "external", FingerprintHash: tag,
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	const N = 25
	parts := make([]string, N)
	for i := range parts {
		parts[i] = mkPart(fmt.Sprintf("u-%d-%d", suf, i))
	}
	leadPart := mkPart(fmt.Sprintf("lead-%d", suf))

	var mu sync.Mutex
	wins, miss := 0, 0
	var wg sync.WaitGroup
	for _, p := range parts {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			res, err := r.DrawLottery(ctx, evID, step, pid, "") // no membership → default pool A
			if err != nil {
				t.Errorf("draw: %v", err)
				return
			}
			mu.Lock()
			if res.ResolvedBy == "random" && res.PrizeCode == "p1" {
				wins++
			} else if res.ResolvedBy == "miss" {
				miss++
			}
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	if wins != stock {
		t.Fatalf("oversell/undersell: wins=%d want=%d (miss=%d)", wins, stock, miss)
	}
	if wins+miss != N {
		t.Fatalf("accounting off: wins=%d miss=%d N=%d", wins, miss, N)
	}

	// idempotent: re-draw a participant returns the same recorded result
	first, _ := r.LotteryResultOf(ctx, evID, step, parts[0])
	again, err := r.DrawLottery(ctx, evID, step, parts[0], "")
	if err != nil || !again.Repeat || again.ResolvedBy != first.ResolvedBy {
		t.Fatalf("idempotency broken: first=%+v again=%+v err=%v", first, again, err)
	}

	// rigged participant always gets the rigged grand prize
	rg, err := r.DrawLottery(ctx, evID, step, leadPart, "LEAD")
	if err != nil || rg.ResolvedBy != "rig" || rg.PrizeCode != "grand" {
		t.Fatalf("rig not honored: %+v %v", rg, err)
	}
}
