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
	"github.com/hertz/captain/internal/organizer"
	"github.com/hertz/captain/internal/participation"
	"github.com/hertz/captain/internal/realtime"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/seed"
	"github.com/hertz/captain/internal/storage"
	"github.com/hertz/captain/internal/store"
	"github.com/hertz/captain/internal/token"
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
		if err := seed.Run(rootCtx, st.PG, sig, cfg.PublicBaseURL); err != nil {
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

	pa := &participation.Handler{Repo: r, Sig: sig, RT: rt,
		RL: httpx.NewRateLimiter(st.Redis), JS: st.JS}
	og := &organizer.Handler{Repo: r, Sig: sig, RT: rt, Export: exp,
		Store: strg, BaseURL: cfg.PublicBaseURL}
	ad := &admin.Handler{Repo: r, Sig: sig}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	// participation (anonymous, ARCHITECTURE §2)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}", pa.Bootstrap)
	mux.HandleFunc("POST /api/v1/p/e/{event_id}/steps/{step_id}/submit", pa.Submit)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}/count", pa.Count)
	mux.HandleFunc("GET /api/v1/p/e/{event_id}/stream", pa.Stream)

	// organizer
	mux.HandleFunc("POST /api/v1/org/login", og.Login)
	mux.HandleFunc("GET /api/v1/org/events", og.Events)
	mux.HandleFunc("GET /api/v1/org/events/{id}", og.Event)
	mux.HandleFunc("GET /api/v1/org/events/{id}/participants", og.Participants)
	mux.HandleFunc("GET /api/v1/org/events/{id}/entry", og.EntryLink)
	mux.HandleFunc("POST /api/v1/org/events/{id}/export", og.CreateExport)
	mux.HandleFunc("GET /api/v1/org/exports/{job_id}", og.ExportStatus)
	mux.HandleFunc("GET /api/v1/org/exports/{job_id}/download", og.ExportDownload)

	// admin (super-admin, separate auth domain)
	mux.HandleFunc("POST /api/v1/admin/login", ad.Login)
	mux.HandleFunc("GET /api/v1/admin/organizers", ad.ListOrganizers)
	mux.HandleFunc("POST /api/v1/admin/organizers", ad.CreateOrganizer)
	mux.HandleFunc("POST /api/v1/admin/organizers/{id}/status", ad.SetOrganizerStatus)

	// demo pages (throwaway, REQUIREMENTS §11.5)
	mux.HandleFunc("GET /m/{event_id}", webui.Mobile())
	mux.HandleFunc("GET /screen/{event_id}", webui.Screen())
	mux.HandleFunc("GET /admin", webui.Admin())
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
