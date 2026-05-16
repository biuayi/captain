// Package storage abstracts object storage so the local FS driver works
// without a server while aliyun OSS can be slotted in later (REQUIREMENTS §10-P2).
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

// New selects a driver. "aliyun" is intentionally unimplemented in v1.
func New(driver, dir string) (Storage, error) {
	switch driver {
	case "local", "":
		return &localFS{root: dir}, nil
	case "aliyun":
		return nil, errors.New("storage: aliyun OSS driver not implemented in v1 (use local); see REQUIREMENTS §10-P2")
	default:
		return nil, errors.New("storage: unknown driver " + driver)
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
