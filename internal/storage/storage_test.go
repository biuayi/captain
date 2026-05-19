package storage

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLocalSignedURLAuth(t *testing.T) {
	l := &localFS{root: t.TempDir(), secret: "s3cret"}
	u, err := l.SignedURL("uploads/ev/p/1_a.png", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, "/dl/uploads/ev/p/1_a.png?exp=") || !strings.Contains(u, "&sig=") {
		t.Fatalf("signed url shape wrong: %s", u)
	}
	pu, _ := url.Parse(u)
	exp := pu.Query().Get("exp")
	sig := pu.Query().Get("sig")
	key := strings.TrimPrefix(pu.Path, "/dl/")

	if !VerifyDownload("s3cret", key, exp, sig) {
		t.Fatal("valid signature must verify")
	}
	if VerifyDownload("s3cret", key, exp, "deadbeef") {
		t.Fatal("tampered sig must fail")
	}
	if VerifyDownload("s3cret", "other/key", exp, sig) {
		t.Fatal("sig must be key-bound")
	}
	if VerifyDownload("s3cret", key, "1", sig) {
		t.Fatal("expired link must fail")
	}
	if VerifyDownload("wrong-secret", key, exp, sig) {
		t.Fatal("wrong secret must fail")
	}
	if !VerifyDownload("", key, "", "") {
		t.Fatal("empty secret disables verification (dev)")
	}
}

func TestSafeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"../../etc/passwd", "etc_passwd"},
		{"a b/c.png", "a_b_c.png"},
		{"....", "file"},
		{"", "file"},
		{"good_File-1.JPG", "good_File-1.JPG"},
		{"/leading", "leading"},
		{"x y\nz", "x_y_z"},
	}
	for _, c := range cases {
		if got := SafeName(c.in); got != c.want {
			t.Errorf("SafeName(%q)=%q want %q", c.in, got, c.want)
		}
	}
	if got := SafeName(strings.Repeat("a", 200) + ".png"); len(got) != 80 {
		t.Errorf("len(SafeName(long))=%d want 80", len(got))
	}
}
