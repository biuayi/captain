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

// PF-02: cross-tenant access is denied at the repo layer.
func TestTenantIsolation(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	ctx := context.Background()
	s := time.Now().UnixNano()
	orgA, _ := r.CreateOrganizer(ctx, "A", fmt.Sprintf("a-%d", s), "h")
	orgB, _ := r.CreateOrganizer(ctx, "B", fmt.Sprintf("b-%d", s), "h")
	fA, _ := r.CreateFlowConfig(ctx, orgA, "f", []byte(`{"version":2,"flowId":"f","name":"n","entryStepId":"s1","steps":[{"id":"s1","type":"checkin"}]}`))
	evA, _ := r.CreateEvent(ctx, orgA, "EA", time.Now(), time.Now().Add(time.Hour), 1, "ink-wash-default", fA)

	if evs, _ := r.EventsByOrganizer(ctx, orgB); len(evs) != 0 {
		t.Fatalf("org B sees %d events of org A", len(evs))
	}
	if err := r.SetEventStatus(ctx, evA, orgB, "active"); err != repo.ErrNotFound {
		t.Fatalf("cross-tenant SetEventStatus = %v, want ErrNotFound", err)
	}
	if err := r.SetEventIdentityFlags(ctx, evA, orgB, true, true, true); err != repo.ErrNotFound {
		t.Fatalf("cross-tenant SetEventIdentityFlags = %v, want ErrNotFound", err)
	}
	if err := r.SetEventTimezone(ctx, evA, orgB, "UTC"); err != repo.ErrNotFound {
		t.Fatalf("cross-tenant SetEventTimezone = %v, want ErrNotFound", err)
	}
	if !r.FlowOwned(ctx, fA, orgA) || r.FlowOwned(ctx, fA, orgB) {
		t.Fatal("FlowOwned tenant scoping broken")
	}
}

// PF-03: concurrency — whitelist single-claim + multi-day checkin idempotency.
func TestConcurrencyClaimAndCheckinDay(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	ctx := context.Background()
	s := time.Now().UnixNano()
	orgID, _ := r.CreateOrganizer(ctx, "C", fmt.Sprintf("c-%d", s), "h")
	fID, _ := r.CreateFlowConfig(ctx, orgID, "f", []byte(`{"version":2,"flowId":"f","name":"n","entryStepId":"s1","steps":[{"id":"s1","type":"checkin"}]}`))
	evID, _ := r.CreateEvent(ctx, orgID, "E", time.Now(), time.Now().Add(time.Hour), 1, "ink-wash-default", fID)
	_, _ = r.InsertWhitelist(ctx, evID, orgID, "b", []repo.WLImportRow{{EmployeeNumber: "E1", Name: "n", PhoneLast4: "1234"}})
	we, _ := r.MatchWhitelistLogin(ctx, evID, "E1", "")

	// many devices race to claim one whitelist entry → exactly one winner
	var wins int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pidKey := fmt.Sprintf("k-%d-%d", s, i)
			wid := we.ID
			pid, _, _ := r.UpsertParticipantFull(ctx, repo.ParticipantUpsert{
				EventID: evID, ParticipantKey: pidKey, IdentityType: "staff_whitelist",
				ParticipantType: "staff", FingerprintHash: fmt.Sprintf("fp%d", i), WhitelistEntryID: &wid,
			})
			if ok, _ := r.ClaimWhitelistWithJTI(ctx, we.ID, pid, fmt.Sprintf("fp%d", i), fmt.Sprintf("j%d", i)); ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("whitelist claim race: %d winners, want 1", wins)
	}

	// concurrent same-day checkin → exactly one checkin_day row
	pid, _, _ := r.UpsertParticipantFull(ctx, repo.ParticipantUpsert{
		EventID: evID, ParticipantKey: "cd-" + fmt.Sprint(s), IdentityType: "external_fingerprint",
		ParticipantType: "external", FingerprintHash: "cd",
	})
	pp, _ := r.EnsureParticipation(ctx, evID, pid)
	day := time.Now().Format("2006-01-02")
	var firsts int64
	var wg2 sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			if ins, _ := r.MarkCheckinDay(ctx, pp, evID, day, nil, nil, nil); ins {
				mu.Lock()
				firsts++
				mu.Unlock()
			}
		}()
	}
	wg2.Wait()
	if firsts != 1 {
		t.Fatalf("checkin_day idempotency: %d firsts, want 1", firsts)
	}
	if n, _ := r.DistinctCheckinDays(ctx, pp); n != 1 {
		t.Fatalf("distinct days = %d, want 1", n)
	}
}
