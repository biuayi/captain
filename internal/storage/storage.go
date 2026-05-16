// Package storage abstracts object storage. Local FS driver runs without a
// server; aliyun OSS driver (REQUIREMENTS §10-P2 / T-026) activates when
// CAPTAIN_STORAGE_DRIVER=aliyun + OSS creds are configured.
package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

type Storage interface {
	// Put stores data under key and returns it back.
	Put(key string, r io.Reader) (string, error)
	// Open returns a reader for a previously stored key.
	Open(key string) (io.ReadCloser, error)
}

type Options struct {
	Driver       string
	Dir          string
	OSSEndpoint  string
	OSSBucket    string
	OSSKeyID     string
	OSSKeySecret string
}

func New(o Options) (Storage, error) {
	switch o.Driver {
	case "local", "":
		return &localFS{root: o.Dir}, nil
	case "aliyun":
		if o.OSSEndpoint == "" || o.OSSBucket == "" || o.OSSKeyID == "" || o.OSSKeySecret == "" {
			return nil, errors.New("storage: aliyun 需 CAPTAIN_OSS_ENDPOINT/BUCKET/KEY_ID/KEY_SECRET")
		}
		return newAliyun(o)
	default:
		return nil, errors.New("storage: unknown driver " + o.Driver)
	}
}

type localFS struct{ root string }

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
