// Package webui serves the public UI. The real UI is the check-in-kiosk
// React monorepo, whose built dist is embedded here at build time
// (internal/webui/embed/{mobile,admin,bigscreen}, gitignored — produced by
// the frontend build step). screen.html stays as the big-screen until the
// codex redesign is integrated. Brand assets live under assets/.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed screen.html
var files embed.FS

//go:embed assets/*.png
var assets embed.FS

//go:embed embed
var reactFS embed.FS

func page(name string) http.HandlerFunc {
	b, _ := files.ReadFile(name)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}
}

// Screen serves the live big-screen (/screen/{event_id}).
func Screen() http.HandlerFunc { return page("screen.html") }

// ReactIndex serves a React app's index.html for its SPA route. `inject`,
// if non-empty, is spliced before </head> (used to pass the obfuscated
// admin path segment to the admin app, T-083).
func ReactIndex(app, inject string) http.HandlerFunc {
	b, _ := reactFS.ReadFile("embed/" + app + "/index.html")
	html := string(b)
	if inject != "" {
		html = strings.Replace(html, "</head>", inject+"</head>", 1)
	}
	out := []byte(html)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(out)
	}
}

// ReactStatic serves a React app's hashed assets (mount under its vite base).
func ReactStatic(app string) http.Handler {
	sub, _ := fs.Sub(reactFS, "embed/"+app)
	fsrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fsrv.ServeHTTP(w, r)
	})
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
