// Command server is the captain modular monolith entrypoint: it wires
// config -> stores -> modules -> HTTP and runs background workers
// (realtime fan-out, export consumer). See docs/ARCHITECTURE.md.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/hertz/captain/internal/admin"
	"github.com/hertz/captain/internal/config"
	"github.com/hertz/captain/internal/export"
	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/loginguard"
	"github.com/hertz/captain/internal/organizer"
	"github.com/hertz/captain/internal/orgperm"
	"github.com/hertz/captain/internal/participation"
	"github.com/hertz/captain/internal/platformcfg"
	"github.com/hertz/captain/internal/realtime"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/seed"
	"github.com/hertz/captain/internal/storage"
	"github.com/hertz/captain/internal/store"
	"github.com/hertz/captain/internal/templatecache"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
	"github.com/hertz/captain/internal/webui"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("captain: %v", err)
	}
}

func run() error {
	cfg := config.Load()
	log.Printf("captain starting addr=%s storage=%s", cfg.HTTPAddr, cfg.StorageDriver)
	if cfg.TokenSecret == "dev-only-insecure-secret-change-me" {
		log.Printf("WARNING: CAPTAIN_TOKEN_SECRET is the insecure default — set a strong secret before any non-dev use")
	}
	if cfg.IdentityPepper == "dev-only-insecure-pepper-change-me" {
		log.Printf("WARNING: CAPTAIN_IDENTITY_PEPPER is the insecure default — set a strong pepper before any non-dev use")
	}

	rootCtx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(rootCtx, cfg.PGDSN, cfg.RedisAddr, cfg.NATSURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := store.Migrate(rootCtx, st.PG); err != nil {
		return err
	}

	sig := token.New(cfg.TokenSecret)
	r := repo.New(st.PG)
	orgpc := orgperm.New(st.Redis)
	// platformcfg resolves secrets DB-first with env fallback (SS0-13).
	pcfg := platformcfg.New(r, cfg.ConfigKey, func(k string) string {
		switch k {
		case "cloudflare_turnstile_sitekey":
			return cfg.TurnstileSite
		case "cloudflare_turnstile_secret":
			return cfg.TurnstileSecret
		case "aliyun_oss_endpoint":
			return cfg.OSSEndpoint
		case "aliyun_oss_bucket":
			return cfg.OSSBucket
		case "aliyun_oss_key_id":
			return cfg.OSSKeyID
		case "aliyun_oss_key_secret":
			return cfg.OSSKeySecret
		}
		return ""
	})
	pget := func(k string) string { v, _ := pcfg.Get(rootCtx, k); return v }
	strg, err := storage.New(storage.Options{
		Driver: cfg.StorageDriver, Dir: cfg.StorageDir,
		OSSEndpoint: pget("aliyun_oss_endpoint"), OSSBucket: pget("aliyun_oss_bucket"),
		OSSKeyID: pget("aliyun_oss_key_id"), OSSKeySecret: pget("aliyun_oss_key_secret"),
	})
	if err != nil {
		return err
	}
	tplc := templatecache.New(st.Redis)
	rt := realtime.New(st.Redis, r)
	exp := export.New(st.JS, r, strg, cfg.PGDSN)

	if cfg.Seed {
		if err := seed.Run(rootCtx, st.PG, sig, cfg.PublicBaseURL, cfg.SeedAdminPw, cfg.SeedOrgPw); err != nil {
			log.Printf("seed: %v (continuing)", err)
		}
	}

	// background workers
	go rt.Run(rootCtx)
	go func() {
		if err := exp.Run(rootCtx); err != nil {
			log.Printf("export worker: %v", err)
		}
	}()

	guard := loginguard.New(st.Redis)
	ts := turnstile.New(cfg.TurnstileMode, pget("cloudflare_turnstile_sitekey"), pget("cloudflare_turnstile_secret"))
	if ts.Enabled() {
		log.Printf("turnstile: ENFORCE (sitekey set=%v)", cfg.TurnstileSite != "")
	} else {
		log.Printf("turnstile: off (set CAPTAIN_TURNSTILE_MODE=enforce + keys for prod)")
	}

	pa := &participation.Handler{Repo: r, Sig: sig, RT: rt,
		RL: httpx.NewRateLimiter(st.Redis), JS: st.JS, Pepper: cfg.IdentityPepper, TS: ts,
		Guard: guard, RDB: st.Redis, OpenLegacy: cfg.OpenParticipation}
	og := &organizer.Handler{Repo: r, Sig: sig, RT: rt, Export: exp,
		Store: strg, BaseURL: cfg.PublicBaseURL, Guard: guard, TS: ts, TplC: tplc, RDB: st.Redis}
	ad := &admin.Handler{Repo: r, Sig: sig, Guard: guard, TS: ts,
		PC: pcfg, OrgPC: orgpc, Export: exp, Store: strg, TplC: tplc}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	// participation (anonymous, ARCHITECTURE §2)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}", pa.Bootstrap)
	mux.HandleFunc("POST /api/v1/p/e/{event_id}/login", pa.Login)
	mux.HandleFunc("POST /api/v1/p/e/{event_id}/logout", pa.Logout)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}/me", pa.Me)
	mux.HandleFunc("POST /api/v1/p/e/{event_id}/steps/{step_id}/submit", pa.Submit)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}/count", pa.Count)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}/info", pa.Info)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}/qr", pa.QR)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}/stream", pa.Stream)
	mux.HandleFunc("GET /api/v1/p/config", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"turnstile_mode": cfg.TurnstileMode, "turnstile_sitekey": cfg.TurnstileSite})
	})

	// organizer
	// organizer routes (except login) go through OrgPerm middleware: it
	// verifies the JWT, enforces the per-route permission and rejects a
	// stale perm snapshot (SS0-09). Handlers read httpx.OrgClaims.
	op := func(perm string, h http.HandlerFunc) http.Handler {
		return httpx.OrgPerm(sig, orgpc, r.OrganizerPermVersion, perm)(h)
	}
	mux.HandleFunc("POST /api/v1/org/login", og.Login)
	mux.Handle("GET /api/v1/org/events", op("", og.Events))
	mux.Handle("POST /api/v1/org/flows", op("can_create_event", og.CreateFlow))
	mux.Handle("GET /api/v1/org/flows", op("", og.ListFlows))
	mux.Handle("GET /api/v1/org/templates", op("", og.Templates))
	mux.Handle("POST /api/v1/org/events", op("can_create_event", og.CreateEvent))
	mux.Handle("PUT /api/v1/org/events/{id}", op("can_create_event", og.UpdateEvent))
	mux.Handle("POST /api/v1/org/events/{id}/status", op("can_create_event", og.SetEventStatus))
	mux.Handle("GET /api/v1/org/events/{id}", op("", og.Event))
	mux.Handle("GET /api/v1/org/events/{id}/participants", op("can_view_records", og.Participants))
	mux.Handle("GET /api/v1/org/events/{id}/entry", op("", og.EntryLink))
	mux.Handle("POST /api/v1/org/events/{id}/whitelist/import", op("can_create_event", og.ImportWhitelist))
	mux.Handle("POST /api/v1/org/events/{id}/whitelist/{entry_id}/unbind", op("can_create_event", og.UnbindWhitelist))
	mux.Handle("POST /api/v1/org/events/{id}/config", op("can_create_event", og.EventConfig))
	mux.Handle("POST /api/v1/org/events/{id}/exam/import", op("can_create_event", og.ExamImport))
	mux.Handle("GET /api/v1/org/events/{id}/exam", op("", og.ExamGet))
	mux.Handle("POST /api/v1/org/events/{id}/lottery/pools", op("can_create_event", og.LotteryPools))
	mux.Handle("POST /api/v1/org/events/{id}/lottery/prizes", op("can_create_event", og.LotteryPrizes))
	mux.Handle("POST /api/v1/org/events/{id}/lottery/membership/import", op("can_create_event", og.LotteryMembershipImport))
	mux.Handle("POST /api/v1/org/events/{id}/lottery/rig/import", op("can_create_event", og.LotteryRigImport))
	mux.Handle("GET /api/v1/org/events/{id}/lottery/summary", op("", og.LotterySummary))
	mux.Handle("GET /api/v1/org/events/{id}/whitelist", op("", og.ListWhitelist))
	mux.Handle("POST /api/v1/org/events/{id}/export", op("can_export_records", og.CreateExport))
	mux.Handle("GET /api/v1/org/exports/{job_id}", op("", og.ExportStatus))
	mux.Handle("GET /api/v1/org/exports/{job_id}/download", op("can_export_records", og.ExportDownload))

	// admin (super-admin, separate auth domain) — path obfuscated by
	// CAPTAIN_ADMIN_PATH (T-083); the obvious /admin is intentionally
	// unregistered → 404.
	ap := cfg.AdminPath
	apiAdmin := "/api/v1/" + ap
	mux.HandleFunc("POST "+apiAdmin+"/login", ad.Login)
	mux.HandleFunc("GET "+apiAdmin+"/organizers", ad.ListOrganizers)
	mux.HandleFunc("POST "+apiAdmin+"/organizers", ad.CreateOrganizer)
	mux.HandleFunc("POST "+apiAdmin+"/organizers/{id}/status", ad.SetOrganizerStatus)
	mux.HandleFunc("DELETE "+apiAdmin+"/organizers/{id}", ad.DeleteOrganizer)
	mux.HandleFunc("POST "+apiAdmin+"/organizers/{id}/password", ad.ResetOrganizerPassword)
	mux.HandleFunc("PATCH "+apiAdmin+"/organizers/{id}/permissions", ad.SetOrganizerPermissions)
	mux.HandleFunc("GET "+apiAdmin+"/config", ad.GetConfig)
	mux.HandleFunc("PUT "+apiAdmin+"/config/{key}", ad.PutConfig)
	mux.HandleFunc("GET "+apiAdmin+"/audit", ad.ListAudit)
	mux.HandleFunc("POST "+apiAdmin+"/db-export", ad.CreateDBExport)
	mux.HandleFunc("GET "+apiAdmin+"/db-export/{job_id}", ad.DBExportStatus)
	mux.HandleFunc("GET "+apiAdmin+"/templates", ad.ListTemplates)
	mux.HandleFunc("POST "+apiAdmin+"/templates", ad.CreateTemplate)
	mux.HandleFunc("PUT "+apiAdmin+"/templates/{id}", ad.UpdateTemplate)
	mux.HandleFunc("DELETE "+apiAdmin+"/templates/{id}", ad.DeleteTemplate)
	mux.HandleFunc("POST "+apiAdmin+"/templates/{id}/assets", ad.UploadTemplateAsset)
	// local-storage signed-URL proxy (SS1-03): streams a stored object.
	mux.HandleFunc("GET /dl/{key...}", func(w http.ResponseWriter, rq *http.Request) {
		rc, err := strg.Open(rq.PathValue("key"))
		if err != nil {
			httpx.Fail(w, http.StatusNotFound, "not_found", "对象不存在")
			return
		}
		defer rc.Close()
		_, _ = io.Copy(w, rc)
	})

	// 正式 UI = check-in-kiosk React（dist 内嵌）；screen 暂用内嵌页待 codex 改版
	mux.HandleFunc("GET /m/{event_id}", webui.ReactIndex("mobile", ""))
	mux.Handle("GET /m-static/", http.StripPrefix("/m-static/", webui.ReactStatic("mobile")))
	mux.HandleFunc("GET /screen/{event_id}", webui.Screen())
	mux.Handle("GET /s-static/", http.StripPrefix("/s-static/", webui.ReactStatic("bigscreen")))
	mux.HandleFunc("GET /"+ap, webui.ReactIndex("admin",
		`<script>window.__ADMIN_SEG__="`+ap+`"</script>`))
	mux.Handle("GET /a-static/", http.StripPrefix("/a-static/", webui.ReactStatic("admin")))
	mux.HandleFunc("GET /assets/{name}", webui.Asset())

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpx.RequestID(httpx.Recover(httpx.AccessLog(mux))),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-rootCtx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("captain listening on %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
