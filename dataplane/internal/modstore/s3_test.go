package modstore_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore"
	"github.com/kudesn1k1/WasmHooks/dataplane/internal/modstore/storetest"
)

const testBucket = "modules"

// fakeS3 answers GET /<bucket>/<key> from a map, like the slice of S3 the
// store uses. Override handles a request first when it returns true.
type fakeS3 struct {
	objects  map[string][]byte // key without bucket, e.g. "<hex>.wasm"
	override func(w http.ResponseWriter, r *http.Request) bool

	mu       sync.Mutex
	requests []string
}

func (f *fakeS3) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()

	if f.override != nil && f.override(w, r) {
		return
	}
	key, ok := strings.CutPrefix(r.URL.Path, "/"+testBucket+"/")
	if !ok || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "unexpected request")
		return
	}
	data, ok := f.objects[key]
	if !ok {
		writeS3Error(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("ETag", `"fake"`)
	w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func writeS3Error(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + code +
		`</Code><Message>` + msg + `</Message></Error>`))
}

func newS3Store(t *testing.T, f *fakeS3) *modstore.S3 {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s, err := modstore.NewS3(modstore.S3Options{
		Endpoint:  strings.TrimPrefix(srv.URL, "http://"),
		Bucket:    testBucket,
		Region:    "us-east-1",
		AccessKey: "ak",
		SecretKey: "sk",
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return s
}

func TestS3_Behaviour(t *testing.T) {
	storetest.Run(t, func(t *testing.T, objects map[string][]byte) modstore.Store {
		f := &fakeS3{objects: map[string][]byte{}}
		for hash, content := range objects {
			hexDigest, err := modstore.ParseHash(hash)
			if err != nil {
				t.Fatalf("ParseHash(%q): %v", hash, err)
			}
			f.objects[hexDigest+".wasm"] = content
		}
		return newS3Store(t, f)
	})
}

func TestS3_Get_MakesOnlyTheObjectRequest(t *testing.T) {
	content := []byte("\x00asm module")
	hash := modstore.HashOf(content)
	hexDigest, _ := modstore.ParseHash(hash)
	f := &fakeS3{objects: map[string][]byte{hexDigest + ".wasm": content}}
	s := newS3Store(t, f)

	if _, err := s.Get(context.Background(), hash); err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := "GET /" + testBucket + "/" + hexDigest + ".wasm"
	for _, r := range f.Requests() {
		if r != want {
			t.Errorf("unexpected request %q, want only %q", r, want)
		}
	}
	if len(f.Requests()) == 0 {
		t.Fatal("no request reached the fake")
	}
}

func TestS3_Get_ServerError(t *testing.T) {
	f := &fakeS3{override: func(w http.ResponseWriter, r *http.Request) bool {
		writeS3Error(w, http.StatusInternalServerError, "InternalError", "boom")
		return true
	}}
	s := newS3Store(t, f)

	_, err := s.Get(context.Background(), modstore.HashOf([]byte("x")))
	if err == nil {
		t.Fatal("Get succeeded, want error")
	}
	for _, not := range []error{modstore.ErrNotFound, modstore.ErrHashMismatch, modstore.ErrBadHash} {
		if errors.Is(err, not) {
			t.Fatalf("error %v must not be %v", err, not)
		}
	}
}

func TestS3_Get_TooLarge(t *testing.T) {
	big := make([]byte, modstore.MaxModuleBytes+1)
	hash := modstore.HashOf(big)
	hexDigest, _ := modstore.ParseHash(hash)
	f := &fakeS3{objects: map[string][]byte{hexDigest + ".wasm": big}}
	s := newS3Store(t, f)

	_, err := s.Get(context.Background(), hash)
	if err == nil {
		t.Fatal("Get succeeded, want error")
	}
	for _, not := range []error{modstore.ErrNotFound, modstore.ErrHashMismatch} {
		if errors.Is(err, not) {
			t.Fatalf("error %v must not be %v", err, not)
		}
	}
}

func TestNewS3_Validation(t *testing.T) {
	ok := modstore.S3Options{Endpoint: "localhost:9000", Bucket: testBucket, Region: "us-east-1"}
	if _, err := modstore.NewS3(ok); err != nil {
		t.Fatalf("valid options: %v", err)
	}
	noBucket := ok
	noBucket.Bucket = ""
	if _, err := modstore.NewS3(noBucket); err == nil {
		t.Error("empty Bucket: want error")
	}
	noEndpoint := ok
	noEndpoint.Endpoint = ""
	if _, err := modstore.NewS3(noEndpoint); err == nil {
		t.Error("empty Endpoint: want error")
	}
}

// Store must be satisfied by *S3.
var _ modstore.Store = (*modstore.S3)(nil)
