// Command server is the captain modular monolith entrypoint: it wires
// config -> stores -> modules -> HTTP and runs background workers
// (realtime fan-out, export consumer). See docs/ARCHITECTURE.md.
package main

import (
	"context"
	"errors"
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
	"github.com/hertz/captain/internal/participation"
	"github.com/hertz/captain/internal/realtime"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/seed"
	"github.com/hertz/captain/internal/storage"
	"github.com/hertz/captain/internal/store"
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
	strg, err := storage.New(cfg.StorageDriver, cfg.StorageDir)
	if err != nil {
		return err
	}
	rt := realtime.New(st.Redis, r)
	exp := export.New(st.JS, r, strg)

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
	ts := turnstile.New(cfg.TurnstileMode, cfg.TurnstileSite, cfg.TurnstileSecret)
	if ts.Enabled() {
		log.Printf("turnstile: ENFORCE (sitekey set=%v)", cfg.TurnstileSite != "")
	} else {
		log.Printf("turnstile: off (set CAPTAIN_TURNSTILE_MODE=enforce + keys for prod)")
	}

	pa := &participation.Handler{Repo: r, Sig: sig, RT: rt,
		RL: httpx.NewRateLimiter(st.Redis), JS: st.JS, Pepper: cfg.IdentityPepper}
	og := &organizer.Handler{Repo: r, Sig: sig, RT: rt, Export: exp,
		Store: strg, BaseURL: cfg.PublicBaseURL, Guard: guard, TS: ts}
	ad := &admin.Handler{Repo: r, Sig: sig, Guard: guard, TS: ts}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	// participation (anonymous, ARCHITECTURE §2)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}", pa.Bootstrap)
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
	mux.HandleFunc("POST /api/v1/org/login", og.Login)
	mux.HandleFunc("GET /api/v1/org/events", og.Events)
	mux.HandleFunc("GET /api/v1/org/events/{id}", og.Event)
	mux.HandleFunc("GET /api/v1/org/events/{id}/participants", og.Participants)
	mux.HandleFunc("GET /api/v1/org/events/{id}/entry", og.EntryLink)
	mux.HandleFunc("POST /api/v1/org/events/{id}/whitelist/import", og.ImportWhitelist)
	mux.HandleFunc("GET /api/v1/org/events/{id}/whitelist", og.ListWhitelist)
	mux.HandleFunc("POST /api/v1/org/events/{id}/export", og.CreateExport)
	mux.HandleFunc("GET /api/v1/org/exports/{job_id}", og.ExportStatus)
	mux.HandleFunc("GET /api/v1/org/exports/{job_id}/download", og.ExportDownload)

	// admin (super-admin, separate auth domain) — path obfuscated by
	// CAPTAIN_ADMIN_PATH (T-083); the obvious /admin is intentionally
	// unregistered → 404.
	ap := cfg.AdminPath
	apiAdmin := "/api/v1/" + ap
	mux.HandleFunc("POST "+apiAdmin+"/login", ad.Login)
	mux.HandleFunc("GET "+apiAdmin+"/organizers", ad.ListOrganizers)
	mux.HandleFunc("POST "+apiAdmin+"/organizers", ad.CreateOrganizer)
	mux.HandleFunc("POST "+apiAdmin+"/organizers/{id}/status", ad.SetOrganizerStatus)

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
		Handler:           httpx.Recover(httpx.AccessLog(mux)),
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
