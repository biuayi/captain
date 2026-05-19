package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hertz/captain/internal/admin"
	"github.com/hertz/captain/internal/httpx"
	"github.com/hertz/captain/internal/loginguard"
	"github.com/hertz/captain/internal/orgperm"
	"github.com/hertz/captain/internal/platformcfg"
	"github.com/hertz/captain/internal/repo"
	"github.com/hertz/captain/internal/storage"
	"github.com/hertz/captain/internal/templatecache"
	"github.com/hertz/captain/internal/testdb"
	"github.com/hertz/captain/internal/token"
	"github.com/hertz/captain/internal/turnstile"
)

func TestTemplateRegistry(t *testing.T) {
	r := repo.New(testdb.Pool(t))
	rdb := testdb.Redis(t)
	sig := token.New("s")
	st, _ := storage.New(storage.Options{Driver: "local", Dir: t.TempDir()})
	h := &admin.Handler{
		Repo: r, Sig: sig, Guard: loginguard.New(rdb), TS: turnstile.New("off", "", ""),
		PC:    platformcfg.New(r, "k", func(string) string { return "" }),
		OrgPC: orgperm.New(rdb), Store: st, TplC: templatecache.New(rdb),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /a/templates", h.CreateTemplate)
	mux.HandleFunc("PUT /a/templates/{id}", h.UpdateTemplate)
	mux.HandleFunc("POST /a/templates/{id}/assets", h.UploadTemplateAsset)
	mux.HandleFunc("GET /a/templates", h.ListTemplates)
	srv := httptest.NewServer(httpx.RequestID(mux))
	defer srv.Close()

	const adminID = "00000000-0000-0000-0000-0000000000aa"
	tok, _ := sig.Sign(token.Claims{Kind: token.KindAuth, Role: token.RoleAdmin, Subject: adminID, ExpiresAt: 1 << 62})
	cl := func(method, path, body string) (int, map[string]any) {
		rq, _ := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
		rq.Header.Set("Authorization", "Bearer "+tok)
		rs, err := http.DefaultClient.Do(rq)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(rs.Body).Decode(&m)
		return rs.StatusCode, m
	}

	tcode := fmt.Sprintf("ink-wash-%d", time.Now().UnixNano())
	code, m := cl(http.MethodPost, "/a/templates",
		fmt.Sprintf(`{"kind":"screen","code":%q,"name":"水墨","version":1,"manifest":{"palette":"ink"}}`, tcode))
	if code != http.StatusCreated {
		t.Fatalf("create tpl = %d (%v)", code, m)
	}
	id := m["id"].(string)

	// bad kind rejected
	if c, _ := cl(http.MethodPost, "/a/templates", `{"kind":"exe","code":"x","name":"y"}`); c != http.StatusBadRequest {
		t.Fatalf("bad kind = %d want 400", c)
	}

	// asset upload: reject non-whitelisted MIME, accept png
	upload := func(mime string) int {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		hdr := make(map[string][]string)
		hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="a.bin"`}
		hdr["Content-Type"] = []string{mime}
		pw, _ := mw.CreatePart(hdr)
		pw.Write([]byte("data"))
		mw.Close()
		rq, _ := http.NewRequest(http.MethodPost, srv.URL+"/a/templates/"+id+"/assets", &buf)
		rq.Header.Set("Authorization", "Bearer "+tok)
		rq.Header.Set("Content-Type", mw.FormDataContentType())
		rs, err := http.DefaultClient.Do(rq)
		if err != nil {
			t.Fatal(err)
		}
		rs.Body.Close()
		return rs.StatusCode
	}
	if c := upload("application/x-msdownload"); c != http.StatusUnsupportedMediaType {
		t.Fatalf("exe upload = %d want 415", c)
	}
	if c := upload("image/png"); c != http.StatusCreated {
		t.Fatalf("png upload = %d want 201", c)
	}

	// publish then org-visible
	if c, _ := cl(http.MethodPut, "/a/templates/"+id, `{"status":"published"}`); c != http.StatusOK {
		t.Fatalf("publish = %d", c)
	}
	vis, err := r.ListTemplatesForOrganizer(context.Background(), "screen", "any-org")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range vis {
		if v.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("published global template not visible to organizer")
	}
}
