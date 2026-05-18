package storage

import (
	"io"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// aliyunOSS implements Storage over Aliyun OSS (T-026). Used only when
// configured; default deployments stay on local FS.
type aliyunOSS struct{ bucket *oss.Bucket }

func newAliyun(o Options) (*aliyunOSS, error) {
	c, err := oss.New(o.OSSEndpoint, o.OSSKeyID, o.OSSKeySecret)
	if err != nil {
		return nil, err
	}
	b, err := c.Bucket(o.OSSBucket)
	if err != nil {
		return nil, err
	}
	return &aliyunOSS{bucket: b}, nil
}

func (a *aliyunOSS) Put(key string, r io.Reader) (string, error) {
	if err := a.bucket.PutObject(key, r); err != nil {
		return "", err
	}
	return key, nil
}

func (a *aliyunOSS) Open(key string) (io.ReadCloser, error) {
	return a.bucket.GetObject(key)
}

// SignedURL returns a time-limited OSS GET URL for private-read objects.
func (a *aliyunOSS) SignedURL(key string, ttl time.Duration) (string, error) {
	secs := int64(ttl.Seconds())
	if secs <= 0 {
		secs = 600
	}
	return a.bucket.SignURL(key, oss.HTTPGet, secs)
}
