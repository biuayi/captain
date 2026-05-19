// Package storage abstracts object storage. Local FS driver runs without a
// server; aliyun OSS driver (REQUIREMENTS §10-P2 / T-026) activates when
// CAPTAIN_STORAGE_DRIVER=aliyun + OSS creds are configured.
package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Storage interface {
	// Put stores data under key and returns it back.
	Put(key string, r io.Reader) (string, error)
	// Open returns a reader for a previously stored key.
	Open(key string) (io.ReadCloser, error)
	// SignedURL returns a time-limited read URL. The aliyun driver returns a
	// real OSS-signed URL; the local driver returns a proxy path served by
	// the app (no public object store) (DESIGN §SS-1, SS1-03).
	SignedURL(key string, ttl time.Duration) (string, error)
}

type Options struct {
	Driver       string
	Dir          string
	OSSEndpoint  string
	OSSBucket    string
	OSSKeyID     string
	OSSKeySecret string
	Secret       string // HMAC secret for local signed-download links (S1)
}

func New(o Options) (Storage, error) {
	switch o.Driver {
	case "local", "":
		return &localFS{root: o.Dir, secret: o.Secret}, nil
	case "aliyun":
		if o.OSSEndpoint == "" || o.OSSBucket == "" || o.OSSKeyID == "" || o.OSSKeySecret == "" {
			return nil, errors.New("storage: aliyun 需 CAPTAIN_OSS_ENDPOINT/BUCKET/KEY_ID/KEY_SECRET")
		}
		return newAliyun(o)
	default:
		return nil, errors.New("storage: unknown driver " + o.Driver)
	}
}

type localFS struct {
	root   string
	secret string
}

func (l *localFS) path(key string) string { return filepath.Join(l.root, filepath.Clean("/"+key)) }

func (l *localFS) Put(key string, r io.Reader) (string, error) {
	p := l.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	return key, nil
}

func (l *localFS) Open(key string) (io.ReadCloser, error) {
	return os.Open(l.path(key))
}

// SignedURL for local FS returns an app-proxied path carrying an expiring
// HMAC so the /dl handler can authorize without a session (S1). No secret
// configured → unsigned (dev/local only).
func (l *localFS) SignedURL(key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	base := "/dl/" + key
	if l.secret == "" {
		return base, nil
	}
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	return base + "?exp=" + exp + "&sig=" + url.QueryEscape(downloadSig(l.secret, key, exp)), nil
}

// SafeName sanitizes a client-supplied filename before it becomes part of a
// storage key: keep [A-Za-z0-9._-], collapse the rest to '_', drop leading
// dots/separators, cap length (S2 — defense in depth even though localFS is
// root-confined and keys are HMAC-gated).
func SafeName(name string) string {
	b := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b = append(b, r)
		default:
			b = append(b, '_')
		}
	}
	s := strings.TrimLeft(string(b), "._-")
	if len(s) > 80 {
		s = s[len(s)-80:]
	}
	if s == "" {
		s = "file"
	}
	return s
}

func downloadSig(secret, key, exp string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(key + "\n" + exp))
	return hex.EncodeToString(m.Sum(nil))
}

// VerifyDownload authorizes a /dl request: constant-time HMAC match and not
// expired. When secret=="" verification is disabled (dev/local) and any
// request passes — production MUST set CAPTAIN_TOKEN_SECRET.
func VerifyDownload(secret, key, exp, sig string) bool {
	if secret == "" {
		return true
	}
	ts, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > ts {
		return false
	}
	want := downloadSig(secret, key, exp)
	return subtle.ConstantTimeCompare([]byte(want), []byte(sig)) == 1
}
