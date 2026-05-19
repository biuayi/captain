package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The admin SPA dual-route (cmd/server/main.go) serves ONE embedded build at
// two paths, distinguished only by the runtime globals spliced into the shell
// by webui.ReactIndex:
//
//	GET /console  → window.__ADMIN_MODE__="org";  __ADMIN_SEG__=""
//	GET /<seg>    → window.__ADMIN_MODE__="super"; __ADMIN_SEG__="<seg>"
//
// These are the EXACT inject payloads main.go passes. This pins the wiring
// mechanism (splice-before-</head>, served as text/html) the A7 dual-route
// depends on — webui only, no business logic. (Mirrors how mobile's
// GET /m/{event_id} reuses the same ReactIndex splice with an empty inject.)
func TestReactIndexAdminDualRouteInject(t *testing.T) {
	const seg = "ax9f2"
	cases := []struct {
		name   string
		inject string
		want   []string
	}{
		{
			name:   "org /console",
			inject: `<script>window.__ADMIN_MODE__="org";window.__ADMIN_SEG__=""</script>`,
			want:   []string{`window.__ADMIN_MODE__="org"`, `window.__ADMIN_SEG__=""`},
		},
		{
			name:   "super obfuscated seg",
			inject: `<script>window.__ADMIN_MODE__="super";window.__ADMIN_SEG__="` + seg + `"</script>`,
			want:   []string{`window.__ADMIN_MODE__="super"`, `window.__ADMIN_SEG__="` + seg + `"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := ReactIndex("admin", tc.inject)
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Fatalf("Content-Type = %q, want text/html", ct)
			}
			body := rec.Body.String()
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Fatalf("body missing %q\n---\n%s", w, body)
				}
			}
			// Spliced BEFORE </head> (the embedded admin index.html shell),
			// ahead of the bundled module script — so the shell reads the
			// globals on first evaluation.
			head := strings.Index(body, "</head>")
			scriptPos := strings.Index(body, tc.inject)
			if head < 0 || scriptPos < 0 || scriptPos > head {
				t.Fatalf("inject not spliced before </head> (head=%d inject=%d)", head, scriptPos)
			}
		})
	}
}

// The hashed assets must mount under /a-static/ (StripPrefix in main.go), kept
// distinct from mobile's /m-static/. This pins that the admin sub-FS resolves
// the real embedded bundle (index.html referencing /a-static/assets/).
func TestReactStaticAdminServesEmbeddedIndex(t *testing.T) {
	h := ReactStatic("admin")
	rec := httptest.NewRecorder()
	// http.FileServer 301-redirects /index.html → /; request the dir root,
	// which serves the embedded index.html directly.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/a-static/assets/") {
		t.Fatalf("embedded admin index.html does not reference /a-static/assets/")
	}
}
