package blobstore

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Store for tests. Presigned URLs are synthetic but
// carry the key, so handler tests can assert the redirect target.
type Fake struct {
	mu      sync.Mutex
	objects map[string][]byte
	times   map[string]time.Time
	puts    int
	// Now, when set, supplies LastModified; tests that exercise the orphan
	// sweep's grace period need to place objects in the past.
	Now func() time.Time
}

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{
		objects: map[string][]byte{},
		times:   map[string]time.Time{},
	}
}

func (f *Fake) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

func (f *Fake) Put(ctx context.Context, key string, r io.Reader, size int64, mediaType string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	f.objects[key] = b
	f.times[key] = f.now()
	return nil
}

// Puts is how many times Put has been called. Counting calls, not objects, is
// what distinguishes a deduplicated upload from a re-uploaded one: both leave
// exactly one object behind under the same content-addressed key, so an
// object count proves nothing about whether the second PUT was issued.
// Test-only; not part of Store.
func (f *Fake) Puts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts
}

func (f *Fake) PresignGet(ctx context.Context, key string, ttl time.Duration, opts GetOptions) (string, error) {
	f.mu.Lock()
	_, ok := f.objects[key]
	f.mu.Unlock()
	if !ok {
		return "", ErrNotFound
	}
	q := url.Values{}
	q.Set("response-content-type", opts.ContentType)
	q.Set("response-content-disposition", opts.ContentDisposition)
	q.Set("response-cache-control", opts.CacheControl)
	q.Set("X-Amz-Expires", strings.TrimSuffix(ttl.String(), "0s"))
	return "https://fake.objectstorage.test/" + key + "?" + q.Encode(), nil
}

func (f *Fake) Delete(ctx context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[key]; !ok {
		return ErrNotFound
	}
	delete(f.objects, key)
	delete(f.times, key)
	return nil
}

func (f *Fake) List(ctx context.Context, prefix string) ([]Object, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Object
	for k, v := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, Object{Key: k, Size: int64(len(v)), LastModified: f.times[k]})
		}
	}
	return out, nil
}

// Open reads a stored object. Test-only; not part of Store.
func (f *Fake) Open(key string) (io.Reader, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return bytes.NewReader(b), nil
}

// PutAt stores an object with an explicit LastModified, for orphan-sweep
// grace-period tests.
func (f *Fake) PutAt(key string, content []byte, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = content
	f.times[key] = at
}

// Compile-time assertion that *Fake satisfies Store.
var _ Store = (*Fake)(nil)
