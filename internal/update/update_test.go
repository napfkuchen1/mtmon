package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKV) GetMeta(key string) string { k.mu.Lock(); defer k.mu.Unlock(); return k.m[key] }
func (k *memKV) SetMeta(key, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = v
	return nil
}

type tarEntry struct {
	name string
	body string
	typ  byte
	link string
}

func mkTar(t *testing.T, es ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range es {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: typ, Linkname: e.link}
		if typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func script(ver string) string { return "#!/bin/sh\necho " + ver + "\n" }

// fake GitHub: API + asset server in one.
type fakeGH struct {
	srv      *httptest.Server
	tag      string
	body     string
	tarball  []byte
	sum      string // override of the checksum file content; "" = correct
	apiHits  atomic.Int32
	ua       atomic.Value
	status   int
	assetURL func(path string) string
	prerel   bool
}

func newFake(t *testing.T, tag string, tarball []byte) *fakeGH {
	f := &fakeGH{tag: tag, tarball: tarball, status: 200}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.apiHits.Add(1)
		f.ua.Store(r.Header.Get("User-Agent"))
		if f.status != 200 {
			w.WriteHeader(f.status)
			return
		}
		base := f.srv.URL
		turl, surl := base+"/dl/"+AssetName, base+"/dl/"+AssetName+".sha256"
		if f.assetURL != nil {
			turl = f.assetURL("/dl/" + AssetName)
		}
		json.NewEncoder(w).Encode(map[string]any{"tag_name": f.tag, "body": f.body, "html_url": base + "/rel", "published_at": "2026-10-01T10:00:00Z", "prerelease": f.prerel,
			"assets": []map[string]any{{"name": AssetName, "browser_download_url": turl}, {"name": AssetName + ".sha256", "browser_download_url": surl}}})
	})
	mux.HandleFunc("/dl/"+AssetName, func(w http.ResponseWriter, r *http.Request) { w.Write(f.tarball) })
	mux.HandleFunc("/dl/"+AssetName+".sha256", func(w http.ResponseWriter, r *http.Request) {
		if f.sum != "" {
			w.Write([]byte(f.sum))
			return
		}
		h := sha256.Sum256(f.tarball)
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(h[:]), AssetName)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGH) mgr(t *testing.T, cur string, mod ...func(*Options)) (*Manager, chan struct{}) {
	t.Helper()
	u, _ := url.Parse(f.srv.URL)
	restarted := make(chan struct{}, 1)
	o := Options{Repo: "o/r", API: f.srv.URL, Current: cur, Dir: filepath.Join(t.TempDir(), "update"), Meta: &memKV{},
		ExtraHosts: []string{u.Host}, ExitDelay: 10 * time.Millisecond, OnRestart: func() { restarted <- struct{}{} }}
	for _, m := range mod {
		m(&o)
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return m, restarted
}

func TestSemver(t *testing.T) {
	lt := [][2]string{{"0.6.0", "0.7.0"}, {"v0.9.0", "v0.10.0"}, {"1.0.0-rc.1", "1.0.0"}, {"1.0.0-alpha", "1.0.0-alpha.1"},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta"}, {"1.0.0-beta.2", "1.0.0-beta.11"}, {"1.0.0-1", "1.0.0-alpha"}, {"v1.2.3", "v1.2.4"}}
	for _, c := range lt {
		a, ok1 := ParseVersion(c[0])
		b, ok2 := ParseVersion(c[1])
		if !ok1 || !ok2 {
			t.Fatalf("parse %v", c)
		}
		if Compare(a, b) != -1 || Compare(b, a) != 1 || Compare(a, a) != 0 {
			t.Errorf("%s should be < %s", c[0], c[1])
		}
	}
	for _, bad := range []string{"dev", "", "v0.6.0-3-gabc1234", "v0.6.0-dirty", "v0.6.0-3-gabc1234-dirty", "abc1234", "1.2", "01.2.3", "v1.2.3.4"} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("%q must not be comparable", bad)
		}
	}
	if v, ok := ParseVersion("v1.2.3+build.5"); !ok || v.Patch != 3 {
		t.Error("build metadata must be accepted")
	}
}

func TestCheckAvailableAndCache(t *testing.T) {
	f := newFake(t, "v0.7.0", nil)
	f.body = strings.Repeat("x", 9000)
	m, _ := f.mgr(t, "v0.6.0")
	st := m.Check(context.Background(), false)
	if !st.Available || st.Latest != "v0.7.0" || st.LastError != "" || !st.Comparable || st.LastCheck == 0 {
		t.Fatalf("status %+v", st)
	}
	if n := len([]rune(st.Notes)); n != MaxNotes+1 {
		t.Errorf("notes not trimmed: %d", n)
	}
	if ua, _ := f.ua.Load().(string); !strings.HasPrefix(ua, "mtmon/v0.6.0") {
		t.Errorf("user agent %q", ua)
	}
	m.Check(context.Background(), false)
	m.Check(context.Background(), false)
	if f.apiHits.Load() != 1 {
		t.Errorf("cache not used: %d hits", f.apiHits.Load())
	}
	m.Check(context.Background(), true)
	if f.apiHits.Load() != 2 {
		t.Errorf("force must bypass cache: %d", f.apiHits.Load())
	}
	// cache expires after 6 h
	now := time.Now()
	m.o.Now = func() time.Time { return now.Add(CacheTTL + time.Minute) }
	m.Check(context.Background(), false)
	if f.apiHits.Load() != 3 {
		t.Errorf("expired cache must refetch: %d", f.apiHits.Load())
	}
}

func TestNoUpdateForDevOrSameOrOlder(t *testing.T) {
	f := newFake(t, "v0.7.0", nil)
	for cur, want := range map[string]bool{"dev": false, "v0.6.0-3-gabc1234": false, "v0.7.0": false, "v0.8.0": false, "v0.6.9": true} {
		m, _ := f.mgr(t, cur)
		st := m.Check(context.Background(), true)
		if st.Available != want {
			t.Errorf("current %q: available=%v want %v", cur, st.Available, want)
		}
		if st.Latest != "v0.7.0" {
			t.Errorf("manual check must still report latest for %q", cur)
		}
		if cur == "dev" || strings.Contains(cur, "-g") {
			if st.Comparable {
				t.Errorf("%q must not be comparable", cur)
			}
			if err := m.Apply(false); err == nil {
				t.Errorf("apply must be refused for %q", cur)
			}
		}
	}
}

func TestCheckErrors(t *testing.T) {
	f := newFake(t, "v0.7.0", nil)
	m, _ := f.mgr(t, "v0.6.0")
	f.status = 500
	if st := m.Check(context.Background(), true); st.LastError == "" || st.Available {
		t.Errorf("want error: %+v", st)
	}
	f.status = 200
	f.prerel = true
	if st := m.Check(context.Background(), true); st.Available || st.LastError == "" {
		t.Errorf("prerelease must be ignored: %+v", st)
	}
	f.prerel = false
	f.status = 404
	if st := m.Check(context.Background(), true); !strings.Contains(st.LastError, "no release") {
		t.Errorf("404: %+v", st)
	}
	// error backoff: a non-forced check right after a failure does not hit the server again
	hits := f.apiHits.Load()
	m.Check(context.Background(), false)
	if f.apiHits.Load() != hits {
		t.Error("failed check must back off")
	}
	// success clears the error
	f.status = 200
	if st := m.Check(context.Background(), true); st.LastError != "" || !st.Available {
		t.Errorf("recovery: %+v", st)
	}
}

func TestCheckTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()
	u, _ := url.Parse(slow.URL)
	m, _ := New(Options{Repo: "o/r", API: slow.URL, Current: "v0.6.0", ExtraHosts: []string{u.Host}})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if st := m.Check(ctx, true); st.LastError == "" {
		t.Error("expected timeout error")
	}
}

func goodTar(t *testing.T, tag string) []byte {
	return mkTar(t, tarEntry{name: "mtmon/", typ: tar.TypeDir}, tarEntry{name: "mtmon/README.md", body: "hi"}, tarEntry{name: "mtmon/mtmon", body: script(tag)})
}

func waitState(t *testing.T, m *Manager, want string) ApplyStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a := m.Status().Apply; a.State == want {
			return a
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state never became %s: %+v", want, m.Status().Apply)
	return ApplyStatus{}
}

func TestApplyStagesBinary(t *testing.T) {
	f := newFake(t, "v0.7.0", goodTar(t, "v0.7.0"))
	m, restarted := f.mgr(t, "v0.6.0")
	if err := m.Apply(false); err == nil {
		t.Fatal("apply before check must fail")
	}
	m.Check(context.Background(), true)
	if err := m.Apply(false); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(false); err == nil {
		t.Error("second apply while running must be refused")
	}
	waitState(t, m, StateRestarting)
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("restart hook not called")
	}
	fi, err := os.Stat(filepath.Join(m.o.Dir, "mtmon.new"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("staged file: %v %v", fi, err)
	}
	if di, _ := os.Stat(m.o.Dir); di.Mode().Perm() != 0o750 {
		t.Errorf("dir mode %v", di.Mode().Perm())
	}
	pend, _ := os.ReadFile(filepath.Join(m.o.Dir, "pending"))
	if !strings.Contains(string(pend), "version=v0.7.0") {
		t.Errorf("pending marker: %q", pend)
	}
	ents, _ := os.ReadDir(m.o.Dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".mtmon.new.") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if tag, res := m.attempted(); tag != "v0.7.0" || res != "staged" {
		t.Errorf("attempt record %q %q", tag, res)
	}
}

func TestApplyRejections(t *testing.T) {
	cases := map[string]struct {
		tar  []byte
		sum  string
		want string
		mod  func(*fakeGH)
	}{
		"checksum mismatch": {tar: goodTar(t, "v0.7.0"), sum: strings.Repeat("0", 64) + "  x\n", want: "checksum mismatch"},
		"malformed sum":     {tar: goodTar(t, "v0.7.0"), sum: "nope", want: "malformed"},
		"traversal":         {tar: mkTar(t, tarEntry{name: "mtmon/mtmon", body: script("v0.7.0")}, tarEntry{name: "../evil", body: "x"}), want: "unsafe archive entry"},
		"absolute":          {tar: mkTar(t, tarEntry{name: "/etc/passwd", body: "x"}), want: "unsafe archive entry"},
		"symlink":           {tar: mkTar(t, tarEntry{name: "mtmon/mtmon", typ: tar.TypeSymlink, link: "/etc/passwd"}), want: "unsafe archive entry"},
		"missing binary":    {tar: mkTar(t, tarEntry{name: "mtmon/other", body: "x"}), want: "does not contain"},
		"wrong version":     {tar: goodTar(t, "v0.6.5"), want: "reports version"},
		"not runnable":      {tar: mkTar(t, tarEntry{name: "mtmon/mtmon", body: "garbage"}), want: "does not run"},
		"not gzip":          {tar: []byte("plain text"), want: "gzip"},
		"disallowed host": {tar: goodTar(t, "v0.7.0"), want: "not an allowed", mod: func(f *fakeGH) {
			f.assetURL = func(p string) string { return "https://evil.example.com" + p }
		}},
		"http scheme": {tar: goodTar(t, "v0.7.0"), want: "non-https", mod: func(f *fakeGH) {
			f.assetURL = func(p string) string { return "http://github.com" + p }
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, "v0.7.0", c.tar)
			f.sum = c.sum
			if c.mod != nil {
				c.mod(f)
			}
			m, restarted := f.mgr(t, "v0.6.0")
			m.Check(context.Background(), true)
			if err := m.Apply(false); err != nil {
				t.Fatal(err)
			}
			a := waitState(t, m, StateFailed)
			if !strings.Contains(a.Message, c.want) {
				t.Errorf("message %q does not contain %q", a.Message, c.want)
			}
			if _, err := os.Stat(filepath.Join(m.o.Dir, "mtmon.new")); err == nil {
				t.Error("nothing may be staged after a failure")
			}
			select {
			case <-restarted:
				t.Error("must not restart after failure")
			case <-time.After(60 * time.Millisecond):
			}
			if tag, res := m.attempted(); tag != "v0.7.0" || res != "failed" {
				t.Errorf("attempt record %q %q", tag, res)
			}
		})
	}
}

func TestRedirectToDisallowedHostRejected(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("evil")) }))
	defer evil.Close()
	f := newFake(t, "v0.7.0", goodTar(t, "v0.7.0"))
	// the first-party asset URL answers with a redirect to a host that is not allowed
	f.srv.Config.Handler.(*http.ServeMux).HandleFunc("/redir/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/x", http.StatusFound)
	})
	f.assetURL = func(p string) string { return f.srv.URL + "/redir" + p }
	m, _ := f.mgr(t, "v0.6.0")
	m.Check(context.Background(), true)
	if err := m.Apply(false); err != nil {
		t.Fatal(err)
	}
	a := waitState(t, m, StateFailed)
	if !strings.Contains(a.Message, "refusing") && !strings.Contains(a.Message, "not an allowed download host") {
		t.Errorf("message %q", a.Message)
	}
}

func TestSizeCap(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// chunked, no Content-Length: the reader must stop at the cap
		fl := w.(http.Flusher)
		chunk := make([]byte, 1<<20)
		for i := 0; i < 70; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			fl.Flush()
		}
	}))
	defer big.Close()
	u, _ := url.Parse(big.URL)
	m, _ := New(Options{Repo: "o/r", Current: "v0.6.0", ExtraHosts: []string{u.Host}})
	_, err := m.download(context.Background(), big.URL+"/f", MaxDownload)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("want size error, got %v", err)
	}
	if _, err := m.download(context.Background(), big.URL+"/f", 100<<20); err != nil {
		t.Errorf("under the limit must work: %v", err)
	}
}

func TestAllowedURL(t *testing.T) {
	m, _ := New(Options{Repo: "o/r", Current: "v1.0.0"})
	ok := []string{"https://github.com/o/r/releases/download/v1/x", "https://api.github.com/x", "https://objects.githubusercontent.com/a?b=c", "https://release-assets.githubusercontent.com/a"}
	bad := []string{"http://github.com/x", "https://github.com.evil.com/x", "https://evilgithub.com/x", "https://github.com:8443/x", "https://user:pw@github.com/x",
		"ftp://github.com/x", "https://raw.githubusercontent.com/x", "https://127.0.0.1/x"}
	for _, s := range ok {
		u, _ := url.Parse(s)
		if err := m.allowedURL(u); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range bad {
		u, _ := url.Parse(s)
		if err := m.allowedURL(u); err == nil {
			t.Errorf("%s must be rejected", s)
		}
	}
}

func TestBadRepo(t *testing.T) {
	for _, r := range []string{"a", "../x/y", "a/b/c", "a/b?x=1", "a b/c"} {
		if _, err := New(Options{Repo: r}); err == nil {
			t.Errorf("repo %q must be rejected", r)
		}
	}
}

func TestModeStoredAndDefault(t *testing.T) {
	m, _ := New(Options{Meta: &memKV{}, Current: "v1.0.0"})
	if m.Mode() != ModeNotify {
		t.Error("default must be notify")
	}
	for _, md := range []string{ModeOff, ModeAuto, ModeNotify} {
		if err := m.SetMode(md); err != nil || m.Mode() != md {
			t.Errorf("set %s: %v %s", md, err, m.Mode())
		}
	}
	if m.SetMode("bogus") == nil {
		t.Error("bogus mode accepted")
	}
}

func TestAutoWindowAndOncePerRelease(t *testing.T) {
	if !inWindow(time.Date(2026, 10, 2, 3, 0, 0, 0, time.Local)) || !inWindow(time.Date(2026, 10, 2, 3, 59, 0, 0, time.Local)) ||
		inWindow(time.Date(2026, 10, 2, 4, 0, 0, 0, time.Local)) || inWindow(time.Date(2026, 10, 2, 2, 59, 0, 0, time.Local)) {
		t.Error("window must be 03:00-03:59 local")
	}
	f := newFake(t, "v0.7.0", mkTar(t, tarEntry{name: "mtmon/mtmon", body: script("v9.9.9")})) // will fail verification
	m, _ := f.mgr(t, "v0.6.0")
	night := time.Date(2026, 10, 2, 3, 30, 0, 0, time.Local)
	m.o.Now = func() time.Time { return night }
	m.SetMode(ModeAuto)
	m.maybeAuto(context.Background())
	waitState(t, m, StateFailed)
	hits := f.apiHits.Load()
	m.setApply(ApplyStatus{State: StateIdle})
	m.maybeAuto(context.Background()) // same release must not be retried
	time.Sleep(50 * time.Millisecond)
	if m.Status().Apply.State != StateIdle {
		t.Error("auto-update retried a failed release")
	}
	_ = hits
	// outside the window nothing happens
	m2, _ := f.mgr(t, "v0.6.0")
	m2.o.Now = func() time.Time { return time.Date(2026, 10, 2, 14, 0, 0, 0, time.Local) }
	m2.Check(context.Background(), true)
	m2.maybeAuto(context.Background())
	if m2.Status().Apply.State != StateIdle {
		t.Error("auto-update ran outside the window")
	}
}

func TestHelperMissingBlocksApply(t *testing.T) {
	f := newFake(t, "v0.7.0", goodTar(t, "v0.7.0"))
	m, _ := f.mgr(t, "v0.6.0", func(o *Options) { o.HelperPath = "/nonexistent/helper" })
	st := m.Check(context.Background(), true)
	if st.CanApply || st.CannotApply == "" {
		t.Errorf("status %+v", st)
	}
	if m.Apply(false) == nil {
		t.Error("apply must be refused without helper")
	}
}

func TestPendingBlocksApplyAndConfirm(t *testing.T) {
	f := newFake(t, "v0.7.0", goodTar(t, "v0.7.0"))
	healthy := atomic.Bool{}
	m, _ := f.mgr(t, "v0.6.0", func(o *Options) {
		o.Confirm = 20 * time.Millisecond
		o.Health = func() error {
			if healthy.Load() {
				return nil
			}
			return fmt.Errorf("down")
		}
	})
	os.MkdirAll(m.o.Dir, 0o750)
	marker := filepath.Join(m.o.Dir, "confirm-pending")
	os.WriteFile(marker, []byte("version=v0.6.0\n"), 0o640)
	os.WriteFile(filepath.Join(m.o.Dir, "last-rollback"), []byte("ts=1\nreason=old\n"), 0o640)
	m.Check(context.Background(), true)
	if err := m.Apply(false); err == nil || !strings.Contains(err.Error(), "not been confirmed") {
		t.Errorf("apply while pending: %v", err)
	}
	if !m.Status().Pending || m.Status().LastRollbck == nil {
		t.Error("status must show pending marker and rollback")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.confirmLoop(ctx)
	time.Sleep(100 * time.Millisecond)
	if !isFile(marker) {
		t.Fatal("marker removed although unhealthy")
	}
	healthy.Store(true)
	deadline := time.Now().Add(15 * time.Second)
	for isFile(marker) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if isFile(marker) {
		t.Error("marker not removed after healthy check")
	}
	if isFile(filepath.Join(m.o.Dir, "last-rollback")) {
		t.Error("stale rollback record must be cleared after a confirmed update")
	}
}

func TestStaleStagedFileReported(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mtmon.new"), []byte("x"), 0o755)
	m, _ := New(Options{Dir: dir, Current: "v0.6.0"})
	if s := m.Status(); s.Apply.State != StateFailed || !strings.Contains(s.Apply.Message, "not installed") {
		t.Errorf("%+v", s.Apply)
	}
	if _, err := os.Stat(filepath.Join(dir, "mtmon.new")); err == nil {
		t.Error("stale staged binary must be removed")
	}
}

func TestExtractBinaryDirect(t *testing.T) {
	b, err := extractBinary(mkTar(t, tarEntry{name: "./mtmon/mtmon", body: "BIN"}))
	if err != nil || string(b) != "BIN" {
		t.Errorf("%q %v", b, err)
	}
	if _, err := extractBinary(mkTar(t, tarEntry{name: "mtmon/mtmon", body: "a"}, tarEntry{name: "mtmon/mtmon", body: "b"})); err == nil {
		t.Error("duplicate entry accepted")
	}
}
