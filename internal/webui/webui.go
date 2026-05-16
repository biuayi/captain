// Package webui serves the minimal 水墨 demo pages so the vertical slice is
// visually verifiable. These are throwaway — the React monorepo
// (check-in-kiosk) replaces them later (REQUIREMENTS §11.5).
// Brand assets under assets/ are official 寻道大千 art the organizer supplied.
package webui

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed mobile.html screen.html admin.html
var files embed.FS

//go:embed assets/*.png
var assets embed.FS

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

// Admin serves the console with the (possibly obfuscated) admin API base
// injected, so the embedded page calls the right slug (T-083).
func Admin(adminAPIBase string) http.HandlerFunc {
	raw, _ := files.ReadFile("admin.html")
	html := strings.ReplaceAll(string(raw), "__ADMIN_BASE__", adminAPIBase)
	b := []byte(html)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}
}

// Asset serves embedded brand images at /assets/{name} (png only).
func Asset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !strings.HasSuffix(name, ".png") || strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}
		b, err := assets.ReadFile("assets/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(b)
	}
}
