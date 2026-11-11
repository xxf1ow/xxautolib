package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type releaseCacheTestFixture struct {
	mu                sync.Mutex
	server            *httptest.Server
	client            *ciCacheClient
	assets            map[int64]ciCacheAsset
	data              map[int64][]byte
	next              int64
	exists, immutable bool
	ops               []string
	fault             string
	status            int
	after             bool
	deny              int
	pageFailure       bool
	creates           int
}

type cacheTestRoundTripper func(*http.Request) (*http.Response, error)

func (f cacheTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCICacheGitHubDownloadAndRecords(t *testing.T) {
	ctx := context.Background()
	t.Run("response lost", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		cacheGitFault(t, "lost", f.client.Records.URL, nil)
		r, err := f.client.Put(ctx, a)
		if err != nil || r.Origin != a.Record.Origin || len(f.ops) != 1 {
			t.Fatalf("uncertain record commit not recovered: %v %v", err, f.ops)
		}
	})
	t.Run("record mismatch and malformed", func(t *testing.T) {
		for _, kind := range []string{"ID", "origin", "JSON"} {
			t.Run(kind, func(t *testing.T) {
				f := releaseCacheFixture(t)
				a := localCacheFixture(t)
				r := f.seed(t, a)
				if kind == "ID" {
					r.Archive.ID++
				}
				if kind == "origin" {
					r.Origin.Repository = "other/repo"
				}
				body, _ := json.Marshal(r)
				if kind == "JSON" {
					body = []byte("invalid")
				}
				f.setRecord(t, r.Archive.Name, body)
				s, err := f.client.Snapshot(ctx, false)
				if err != nil || len(s.Records) != 0 || s.RecordErrors[r.Archive.Name] == "" {
					t.Fatalf("bad pair accepted: %+v %v", s, err)
				}
			})
		}
	})
	t.Run("download checksum", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		r := f.seed(t, a)
		f.data[r.Archive.ID] = []byte("wrong bytes")
		dir := t.TempDir()
		if _, err := f.client.Download(ctx, r, dir); err == nil {
			t.Fatal("corrupt download accepted")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("failed download left an archive")
		}
	})
	t.Run("redirect strips authorization", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		r := f.seed(t, a)
		received := "unset"
		download := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			received = req.Header.Get("Authorization")
			io.WriteString(w, "verified archive bytes")
		}))
		defer download.Close()
		original := f.client.HTTP.Transport
		f.client.HTTP = &http.Client{Transport: cacheTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/repos/owner/repo/releases/assets/"+strconv.FormatInt(r.Archive.ID, 10) {
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{download.URL}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			}
			return original.RoundTrip(req)
		})}
		if _, err := f.client.Download(ctx, r, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if received != "" {
			t.Fatal("download redirect leaked authorization")
		}
	})
	t.Run("permanent validation failure", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		f.deny = 422
		waited := false
		f.client.Wait = func(context.Context, time.Duration) error { waited = true; return nil }
		if _, err := f.client.Put(ctx, a); err == nil || waited {
			t.Fatal("unclassified 422 retried")
		}
	})
}

func releaseCacheFixture(t *testing.T) *releaseCacheTestFixture {
	t.Helper()
	f := &releaseCacheTestFixture{assets: map[int64]ciCacheAsset{}, data: map[int64][]byte{}, next: 100, exists: true}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	git := cacheGitTestFixture(t, "owner/repo")
	if _, err := git.store.Snapshot(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	f.client = &ciCacheClient{Records: git.store, Repository: "owner/repo", Token: "test-secret", HTTP: f.server.Client(), APIBase: f.server.URL, UploadBase: f.server.URL, Wait: func(context.Context, time.Duration) error { return nil }}
	return f
}

func (f *releaseCacheTestFixture) add(name string, data []byte, state string) ciCacheAsset {
	f.next++
	a := ciCacheAsset{ID: f.next, Name: name, State: state, Size: int64(len(data)), Digest: "sha256:" + testDigest(data)}
	f.assets[a.ID] = a
	f.data[a.ID] = bytes.Clone(data)
	return a
}

func (f *releaseCacheTestFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-secret" {
		w.WriteHeader(401)
		return
	}
	if f.deny != 0 {
		w.WriteHeader(f.deny)
		return
	}
	base := "/repos/" + f.client.Repository + "/releases"
	jsonOut := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	if r.URL.Path == base+"/tags/autolib-cache" || (r.URL.Path == base && r.Method == "POST") {
		if r.Method == "POST" {
			f.creates++
			f.exists = true
			f.ops = append(f.ops, "create")
		}
		if !f.exists {
			w.WriteHeader(404)
			return
		}
		jsonOut(map[string]any{"id": 9, "tag_name": "autolib-cache", "immutable": f.immutable, "draft": false})
		return
	}
	if r.URL.Path == base+"/9/assets" && r.Method == "GET" {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		if f.pageFailure && page == 2 {
			w.WriteHeader(403)
			return
		}
		all := []ciCacheAsset{}
		for _, a := range f.assets {
			all = append(all, a)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
		start := (page - 1) * 100
		if start > len(all) {
			start = len(all)
		}
		end := start + 100
		if end > len(all) {
			end = len(all)
		}
		jsonOut(all[start:end])
		return
	}
	var op string
	var a ciCacheAsset
	if strings.HasPrefix(r.URL.Path, base+"/assets/") {
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, base+"/assets/"), 10, 64)
		a = f.assets[id]
		if a.ID == 0 {
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			if r.Header.Get("Accept") != "application/octet-stream" {
				w.WriteHeader(400)
				return
			}
			w.Write(f.data[id])
			return
		}
		op = "delete:" + a.Name
	} else if r.URL.Path == base+"/9/assets" && r.Method == "POST" {
		op = "upload:" + r.URL.Query().Get("name")
	} else {
		w.WriteHeader(404)
		return
	}
	f.ops = append(f.ops, op)
	fault := f.fault == op
	if fault && !f.after {
		f.fault = ""
		w.WriteHeader(f.status)
		if f.status == 422 {
			io.WriteString(w, `{"errors":[{"resource":"ReleaseAsset","field":"name","code":"already_exists"}]}`)
		}
		return
	}
	if r.Method == "DELETE" {
		delete(f.assets, a.ID)
		delete(f.data, a.ID)
	} else {
		name := r.URL.Query().Get("name")
		for _, existing := range f.assets {
			if existing.Name == name {
				w.WriteHeader(422)
				io.WriteString(w, `{"errors":[{"resource":"ReleaseAsset","field":"name","code":"already_exists"}]}`)
				return
			}
		}
		data, _ := io.ReadAll(r.Body)
		a = f.add(name, data, "uploaded")
		if fault && f.status == 502 {
			a.State = "starter"
			a.Size = 0
			a.Digest = ""
			f.assets[a.ID] = a
			f.data[a.ID] = nil
		}
	}
	if fault {
		f.fault = ""
		w.WriteHeader(f.status)
		return
	}
	if r.Method == "DELETE" {
		w.WriteHeader(204)
	} else {
		jsonOut(a)
	}
}

func localCacheFixture(t *testing.T) ciLocalAsset {
	t.Helper()
	r, _ := cacheRecordFixture()
	data := []byte("verified archive bytes")
	r.Archive.Size = int64(len(data))
	r.Archive.SHA256 = testDigest(data)
	r.Archive.ID = 0
	path := filepath.Join(t.TempDir(), r.Archive.Name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return ciLocalAsset{Path: path, Record: r}
}

func (f *releaseCacheTestFixture) seed(t *testing.T, asset ciLocalAsset) ciCacheRecord {
	t.Helper()
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	r := asset.Record
	a := f.add(r.Archive.Name, data, "uploaded")
	r.Archive.ID = a.ID
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	f.setRecord(t, r.Archive.Name, body)
	return r
}

func (f *releaseCacheTestFixture) setRecord(t *testing.T, name string, body []byte) {
	t.Helper()
	snapshot, err := f.client.Records.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.client.Records.Update(context.Background(), snapshot.Head, name+".publication.json", body); err != nil {
		t.Fatal(err)
	}
}

func TestCICacheGitHubReplacement(t *testing.T) {
	ctx := context.Background()
	for _, point := range []string{"normal", "delete archive", "upload archive", "starter", "upload record", "lost record response", "duplicate"} {
		t.Run(point, func(t *testing.T) {
			f := releaseCacheFixture(t)
			asset := localCacheFixture(t)
			old := asset
			old.Record.Fingerprint = strings.Repeat("b", 64)
			prior := f.seed(t, old)
			f.add("unrelated-manual.txt", []byte("keep"), "uploaded")
			name := asset.Record.Archive.Name
			switch point {
			case "delete archive":
				f.fault = "delete:" + name
				f.status = 503
			case "upload archive":
				f.fault = "upload:" + name
				f.status = 503
			case "starter":
				f.fault = "upload:" + name
				f.status = 502
				f.after = true
			case "duplicate":
				f.fault = "upload:" + name
				f.status = 422
			case "upload record":
				cacheGitFault(t, "reject", f.client.Records.URL, nil)
			case "lost record response":
				cacheGitFault(t, "lost", f.client.Records.URL, nil)
			}
			if point != "normal" && point != "lost record response" {
				f.client.Wait = func(context.Context, time.Duration) error { return context.Canceled }
				if _, err := f.client.Put(ctx, asset); err == nil {
					t.Fatal("interrupted publication reported success")
				}
				f.client.Wait = func(context.Context, time.Duration) error { return nil }
				if point == "upload record" {
					t.Setenv("AUTOLIB_CI_GIT_FAULT", "")
					broken, err := f.client.Snapshot(ctx, false)
					if err != nil || broken.RecordErrors[name] == "" {
						t.Fatal("old record bound to replacement archive", err)
					}
				}
			}
			got, err := f.client.Put(ctx, asset)
			if err != nil {
				t.Fatal(err)
			}
			if got.Archive.ID == prior.Archive.ID {
				t.Fatal("record still refers to old archive")
			}
			snapshot, err := f.client.Snapshot(ctx, false)
			if err != nil || !reflect.DeepEqual(snapshot.Records[name], got) || len(snapshot.Assets) != 2 {
				t.Fatalf("replacement lost final binding or unrelated attachment: %v %+v", err, snapshot)
			}
			if len(f.ops) < 2 || f.ops[0] != "delete:"+name {
				t.Fatalf("unsafe archive replacement order: %v", f.ops)
			}
			for _, op := range f.ops {
				if strings.Contains(op, ".publication.json") {
					t.Fatal("GitHub sidecar mutation", op)
				}
			}
			before := len(f.ops)
			reused, err := f.client.Put(ctx, asset)
			if err != nil || reused.Origin != got.Origin || len(f.ops) != before {
				t.Fatal("valid cache was rewritten", err)
			}
			local, err := f.client.Download(ctx, got, t.TempDir())
			if err != nil || !reflect.DeepEqual(local.Record, got) || mustReadText(t, local.Path) != "verified archive bytes" {
				t.Fatal("download changed bytes or producer", err)
			}
		})
	}
}

func TestCICacheGitHubRecords(t *testing.T) {
	ctx := context.Background()
	t.Run("no GitHub sidecar reads", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		record := f.seed(t, a)
		f.add(record.Archive.Name+".publication.json", []byte("untrusted legacy"), "uploaded")
		f.client.HTTP = &http.Client{Transport: cacheTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/assets/") {
				t.Fatal("normal record read used GitHub asset download")
			}
			return f.server.Client().Transport.RoundTrip(req)
		})}
		s, err := f.client.Snapshot(ctx, false)
		if err != nil || s.RecordHead == "" || s.Records[record.Archive.Name].Origin != record.Origin {
			t.Fatal("Git record snapshot lost provenance", err)
		}
	})
	t.Run("orphan ownership", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		record := f.seed(t, a)
		delete(f.assets, record.Archive.ID)
		s, err := f.client.Snapshot(ctx, false)
		if err != nil || s.RecordErrors[record.Archive.Name] == "" || s.Records[record.Archive.Name].Archive.ID != record.Archive.ID {
			t.Fatal("orphan was reused or ownership lost", err)
		}
	})
	t.Run("unconfirmed record", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		cacheGitFault(t, "reject", f.client.Records.URL, nil)
		if _, err := f.client.Put(ctx, a); err == nil {
			t.Fatal("unconfirmed record reported stored")
		}
		if len(f.assets) != 1 {
			t.Fatal("archive upload was not retained")
		}
	})
}

func TestCICacheGitHubBoundaries(t *testing.T) {
	t.Run("pagination and failure", func(t *testing.T) {
		f := releaseCacheFixture(t)
		for i := 0; i < 101; i++ {
			f.add(fmt.Sprint(i), []byte("x"), "uploaded")
		}
		s, err := f.client.Snapshot(context.Background(), false)
		if err != nil || len(s.Assets) != 101 {
			t.Fatalf("incomplete pagination: %d %v", len(s.Assets), err)
		}
		f.pageFailure = true
		if _, err := f.client.Snapshot(context.Background(), false); err == nil {
			t.Fatal("partial list accepted")
		}
	})
	t.Run("missing and immutable", func(t *testing.T) {
		f := releaseCacheFixture(t)
		f.exists = false
		if _, err := f.client.Snapshot(context.Background(), false); err == nil {
			t.Fatal("reader created cache")
		}
		if f.creates != 0 {
			t.Fatal("reader wrote")
		}
		if _, err := f.client.Snapshot(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		if _, err := f.client.Snapshot(context.Background(), true); err != nil || f.creates != 1 {
			t.Fatal("duplicate initialization")
		}
		f.immutable = true
		if _, err := f.client.Snapshot(context.Background(), true); err == nil {
			t.Fatal("immutable cache accepted")
		}
	})
	t.Run("managed orphan and unknown names", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		orphan := f.add(a.Record.Archive.Name, []byte("manual same managed name"), "uploaded")
		unknown := f.add("unknown.tar.zst", []byte("keep"), "uploaded")
		if _, err := f.client.Put(context.Background(), a); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.assets[orphan.ID]; ok {
			t.Fatal("orphan not repaired")
		}
		if _, ok := f.assets[unknown.ID]; !ok {
			t.Fatal("unknown asset deleted")
		}
	})
	for _, kind := range []string{"size", "2 GiB", "digest", "type"} {
		t.Run(kind, func(t *testing.T) {
			f := releaseCacheFixture(t)
			a := localCacheFixture(t)
			switch kind {
			case "size":
				a.Record.Archive.Size++
			case "2 GiB":
				a.Record.Archive.Size = 2 << 30
			case "digest":
				a.Record.Archive.SHA256 = strings.Repeat("0", 64)
			case "type":
				os.Remove(a.Path)
				os.Mkdir(a.Path, 0755)
			}
			if _, err := f.client.Put(context.Background(), a); err == nil {
				t.Fatal("invalid local asset accepted")
			}
			if len(f.ops) != 0 {
				t.Fatal("invalid input modified remote")
			}
		})
	}
	t.Run("retry budget and authentication", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		waits := []time.Duration{}
		f.client.Wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
		f.deny = 503
		_, err := f.client.Put(context.Background(), a)
		if err == nil || strings.Contains(err.Error(), "test-secret") {
			t.Fatal("missing or unsafe failure")
		}
		expected := []time.Duration{2, 4, 8, 16, 32, 32, 32, 32, 32, 32}
		for i := range expected {
			expected[i] *= time.Second
		}
		if !reflect.DeepEqual(waits, expected) {
			t.Fatalf("retry waits: %v", waits)
		}
		waits = nil
		f.deny = 401
		if _, err := f.client.Put(context.Background(), a); err == nil || len(waits) != 0 {
			t.Fatal("authentication retried")
		}
	})
	t.Run("different names concurrent", func(t *testing.T) {
		f := releaseCacheFixture(t)
		a := localCacheFixture(t)
		b := localCacheFixture(t)
		b.Record.Key.Name = "d"
		b.Record.Archive.Name = b.Record.Key.archiveName()
		errs := make(chan error, 2)
		for _, a := range []ciLocalAsset{a, b} {
			go func() { _, err := f.client.Put(context.Background(), a); errs <- err }()
		}
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		if len(f.assets) != 2 {
			t.Fatal("independent writes conflicted")
		}
	})
}
