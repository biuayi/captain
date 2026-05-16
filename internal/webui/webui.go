// Package webui serves the minimal 水墨 demo pages so the vertical slice is
// visually verifiable. These are throwaway — the React monorepo
// (check-in-kiosk) replaces them later (REQUIREMENTS §11.5).
package webui

import (
	"embed"
	"net/http"
)

//go:embed mobile.html screen.html
var files embed.FS

func page(name string) http.HandlerFunc {
	b, _ := files.ReadFile(name)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}
}

// Mobile serves the participant H5 page (/m/{event_id}).
func Mobile() http.HandlerFunc { return page("mobile.html") }

// Screen serves the live big-screen page (/screen/{event_id}).
func Screen() http.HandlerFunc { return page("screen.html") }
