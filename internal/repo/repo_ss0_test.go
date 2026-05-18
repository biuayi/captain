package repo_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/testdb"
)

func TestOrganizerPermsAndSoftDelete(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	ctx := context.Background()
	login := fmt.Sprintf("org-%d", time.Now().UnixNano())

	id, err := r.CreateOrganizer(ctx, "ACME", login, "hash1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	find := func() (perm string, pv int, present bool) {
		list, e := r.ListOrganizers(ctx)
		if e != nil {
			t.Fatalf("list: %v", e)
		}
		for _, o := range list {
			if o.ID == id {
				return fmt.Sprintf("%v/%v/%v", o.CanCreateEvent, o.CanViewRecords, o.CanExportRecords), o.PermVersion, true
			}
		}
		return "", 0, false
	}

	if p, pv, ok := find(); !ok || p != "true/true/true" || pv != 1 {
		t.Fatalf("defaults = (%q,%d,%v), want true/true/true,1,true", p, pv, ok)
	}

	pv, err := r.SetOrganizerPermissions(ctx, id, true, false, false)
	if err != nil || pv != 2 {
		t.Fatalf("setperms = (%d,%v), want 2,nil", pv, err)
	}
	if p, pvv, _ := find(); p != "true/false/false" || pvv != 2 {
		t.Fatalf("after setperms = (%q,%d)", p, pvv)
	}
	if got, _ := r.OrganizerPermVersion(ctx, id); got != 2 {
		t.Fatalf("OrganizerPermVersion = %d, want 2", got)
	}

	c, err := r.OrganizerByLogin(ctx, login)
	if err != nil || !c.CanCreateEvent || c.CanViewRecords || c.PermVersion != 2 {
		t.Fatalf("OrganizerByLogin snapshot wrong: %+v (%v)", c, err)
	}

	if err := r.ResetOrganizerPassword(ctx, id, "hash2"); err != nil {
		t.Fatalf("resetpw: %v", err)
	}

	if err := r.SoftDeleteOrganizer(ctx, id); err != nil {
		t.Fatalf("softdelete: %v", err)
	}
	if _, _, present := find(); present {
		t.Fatal("soft-deleted organizer still listed")
	}
	if _, err := r.OrganizerByLogin(ctx, login); err != repo.ErrNotFound {
		t.Fatalf("deleted login err = %v, want ErrNotFound", err)
	}
	if err := r.SoftDeleteOrganizer(ctx, id); err != repo.ErrNotFound {
		t.Fatalf("re-softdelete err = %v, want ErrNotFound (idempotent guard)", err)
	}
}
