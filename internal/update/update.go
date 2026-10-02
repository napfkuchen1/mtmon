// Package update checks GitHub for newer mtmon releases and stages them for a privileged swap.
//
// The service itself never replaces its own binary (it runs as an unprivileged user under ProtectSystem=strict).
// Apply downloads, verifies and test-runs the new binary, puts it into <data_dir>/update/mtmon.new and exits
// with a non-zero status; systemd restarts the unit and its ExecStartPre helper (deploy/apply-update.sh, root)
// performs the swap, or rolls it back when the previous start never became healthy.
package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	DefaultRepo = "napfkuchen1/mtmon"
	DefaultAPI  = "https://api.github.com"
	AssetName   = "mtmon-linux-amd64.tar.gz"
	// HelperPath is the root-run swap helper installed by the installer.
	HelperPath = "/usr/local/bin/mtmon-apply-update"

	CacheTTL       = 6 * time.Hour
	retryAfterFail = 30 * time.Minute
	CheckTimeout   = 10 * time.Second
	MaxDownload    = 60 << 20  // compressed asset
	maxExtract     = 150 << 20 // decompressed archive stream
	MaxNotes       = 6000
	ConfirmAfter   = 60 * time.Second
	ExitDelay      = time.Second
	ExitCode       = 75 // EX_TEMPFAIL: non-zero so Restart=on-failure restarts the unit

	ModeOff    = "off"
	ModeNotify = "notify"
	ModeAuto   = "auto"

	StateIdle        = "idle"
	StateDownloading = "downloading"
	StateStaged      = "staged"
	StateRestarting  = "restarting"
	StateFailed      = "failed"

	metaMode    = "update_mode"
	metaAttempt = "update_attempt" // "<tag>|<result>|<unix>"
)

// KV is the small persistent key/value store (store.Store satisfies it).
type KV interface {
	GetMeta(k string) string
	SetMeta(k, v string) error
}

// builtinHosts are the only hosts release assets may be fetched from.
var builtinHosts = map[string]bool{
	"github.com":                           true,
	"api.github.com":                       true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+$`)

// Options configures a Manager. Zero values use production defaults.
type Options struct {
	Repo    string // owner/name
	API     string // API base URL (tests / demo)
	Current string // running version
	Dir     string // state directory (<data_dir>/update)
	Meta    KV
	Client  *http.Client
	// ExtraHosts (host[:port]) are additionally allowed for downloads and make plain http acceptable for them.
	// Only for tests and the local demo; set through MTMON_UPDATE_API in production binaries.
	ExtraHosts []string
	HelperPath string // when set, Apply refuses unless this file exists
	Health     func() error
	OnRestart  func() // called ExitDelay after a successful staging; default os.Exit(ExitCode)
	Now        func() time.Time
	Log        *slog.Logger
	Confirm    time.Duration // uptime before the post-swap health check (default 60 s)
	ExitDelay  time.Duration
}

// Manager holds update state.
type Manager struct {
	o      Options
	client *http.Client

	mu        sync.Mutex
	rel       *release
	checkedAt time.Time
	attemptAt time.Time
	lastErr   string
	apply     ApplyStatus
	started   time.Time
}

type release struct {
	Tag         string  `json:"tag_name"`
	Body        string  `json:"body"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt string  `json:"published_at"`
	Prerelease  bool    `json:"prerelease"`
	Draft       bool    `json:"draft"`
	Assets      []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// ApplyStatus describes the progress of an installation.
type ApplyStatus struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Version string `json:"version,omitempty"`
	Since   int64  `json:"since,omitempty"`
}

// Rollback describes a release that was installed but rolled back by the helper.
type Rollback struct {
	At      int64  `json:"at"`
	Version string `json:"version,omitempty"`
	Reason  string `json:"reason"`
}

// Status is what the API returns.
type Status struct {
	Current     string      `json:"current"`
	Latest      string      `json:"latest"`
	Available   bool        `json:"available"`
	Comparable  bool        `json:"comparable"`
	Mode        string      `json:"mode"`
	Repo        string      `json:"repo"`
	Notes       string      `json:"notes"`
	HTMLURL     string      `json:"html_url"`
	PublishedAt string      `json:"published_at"`
	LastCheck   int64       `json:"last_check"`
	LastError   string      `json:"last_error"`
	CanApply    bool        `json:"can_apply"`
	CannotApply string      `json:"cannot_apply,omitempty"`
	Pending     bool        `json:"confirm_pending"`
	Apply       ApplyStatus `json:"apply"`
	LastRollbck *Rollback   `json:"last_rollback"`
}

// New creates a Manager. It performs no network access.
func New(o Options) (*Manager, error) {
	if o.Repo == "" {
		o.Repo = DefaultRepo
	}
	if !repoRe.MatchString(o.Repo) {
		return nil, fmt.Errorf("invalid update repo %q (want owner/name)", o.Repo)
	}
	if o.API == "" {
		o.API = DefaultAPI
	}
	o.API = strings.TrimRight(o.API, "/")
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Confirm == 0 {
		o.Confirm = ConfirmAfter
	}
	if o.ExitDelay == 0 {
		o.ExitDelay = ExitDelay
	}
	if o.OnRestart == nil {
		o.OnRestart = func() { os.Exit(ExitCode) }
	}
	m := &Manager{o: o, apply: ApplyStatus{State: StateIdle}, started: o.Now()}
	m.client = o.Client
	if m.client == nil {
		m.client = &http.Client{}
	}
	// copy so we can install our own redirect policy without mutating a caller's client
	c := *m.client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return m.allowedURL(req.URL)
	}
	m.client = &c
	m.initStale()
	return m, nil
}

// initStale reports an update that was staged earlier but never installed (e.g. helper missing).
func (m *Manager) initStale() {
	if m.o.Dir == "" {
		return
	}
	if _, err := os.Lstat(filepath.Join(m.o.Dir, "mtmon.new")); err == nil {
		m.apply = ApplyStatus{State: StateFailed, Since: m.o.Now().Unix(),
			Message: "A downloaded update was found but not installed. The helper " + HelperPath + " may be missing: run the installer's update once."}
		m.clearStaged()
	}
}

func (m *Manager) clearStaged() {
	os.Remove(filepath.Join(m.o.Dir, "mtmon.new"))
	os.Remove(filepath.Join(m.o.Dir, "pending"))
}

// ---- policy ----

func (m *Manager) allowedURL(u *url.URL) error {
	host := strings.ToLower(u.Hostname())
	for _, h := range m.o.ExtraHosts {
		if strings.EqualFold(h, u.Host) {
			if u.Scheme == "https" || u.Scheme == "http" {
				return nil
			}
		}
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing non-https URL %q", u.Redacted())
	}
	if u.User != nil {
		return errors.New("refusing URL with credentials")
	}
	if !builtinHosts[host] || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("host %q is not an allowed download host", u.Host)
	}
	return nil
}

// Mode returns the stored update mode (default notify).
func (m *Manager) Mode() string {
	if m.o.Meta != nil {
		switch v := m.o.Meta.GetMeta(metaMode); v {
		case ModeOff, ModeNotify, ModeAuto:
			return v
		}
	}
	return ModeNotify
}

// SetMode persists the mode.
func (m *Manager) SetMode(mode string) error {
	switch mode {
	case ModeOff, ModeNotify, ModeAuto:
	default:
		return fmt.Errorf("invalid mode %q", mode)
	}
	if m.o.Meta == nil {
		return errors.New("no settings store")
	}
	return m.o.Meta.SetMeta(metaMode, mode)
}

// ---- status ----

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() Status {
	s := Status{Current: m.o.Current, Mode: m.Mode(), Repo: m.o.Repo, LastError: m.lastErr, Apply: m.apply}
	cur, ok := ParseVersion(m.o.Current)
	s.Comparable = ok
	if m.rel != nil {
		s.Latest = m.rel.Tag
		s.Notes = trimNotes(m.rel.Body)
		s.HTMLURL = m.rel.HTMLURL
		s.PublishedAt = m.rel.PublishedAt
		if lv, lok := ParseVersion(m.rel.Tag); lok && ok && Compare(lv, cur) > 0 {
			s.Available = true
		}
	}
	if !m.checkedAt.IsZero() {
		s.LastCheck = m.checkedAt.Unix()
	} else if !m.attemptAt.IsZero() {
		s.LastCheck = m.attemptAt.Unix()
	}
	s.CanApply = true
	switch {
	case m.o.Dir == "":
		s.CanApply, s.CannotApply = false, "no state directory"
	case m.o.HelperPath != "" && !isFile(m.o.HelperPath):
		s.CanApply, s.CannotApply = false, "The update helper "+m.o.HelperPath+" is not installed in this container. Run the installer's update once (see docs/runbook.md); after that, updates can be installed from here."
	}
	if m.o.Dir != "" {
		s.Pending = isFile(filepath.Join(m.o.Dir, "confirm-pending"))
		s.LastRollbck = readRollback(filepath.Join(m.o.Dir, "last-rollback"))
	}
	return s
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func trimNotes(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if utf8.RuneCountInString(s) <= MaxNotes {
		return s
	}
	r := []rune(s)
	return string(r[:MaxNotes]) + "…"
}

func kvFile(p string) map[string]string {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func readRollback(p string) *Rollback {
	kv := kvFile(p)
	if kv == nil {
		return nil
	}
	at, _ := strconv.ParseInt(kv["ts"], 10, 64)
	r := kv["reason"]
	if len(r) > 300 {
		r = r[:300]
	}
	return &Rollback{At: at, Version: kv["version"], Reason: r}
}

// ---- check ----

// Check queries GitHub for the latest release. Unless force is set a fresh cached result is reused.
func (m *Manager) Check(ctx context.Context, force bool) Status {
	m.mu.Lock()
	now := m.o.Now()
	if !force {
		fresh := m.rel != nil && now.Sub(m.checkedAt) < CacheTTL
		backoff := !m.attemptAt.IsZero() && now.Sub(m.attemptAt) < retryAfterFail && m.lastErr != ""
		if fresh || backoff {
			defer m.mu.Unlock()
			return m.statusLocked()
		}
	}
	m.attemptAt = now
	m.mu.Unlock()

	rel, err := m.fetchLatest(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.lastErr = err.Error()
		m.o.Log.Warn("update check failed", "err", err)
	} else {
		m.lastErr = ""
		m.rel = rel
		m.checkedAt = m.o.Now()
	}
	return m.statusLocked()
}

func (m *Manager) userAgent() string {
	return "mtmon/" + m.o.Current + " (+https://github.com/" + m.o.Repo + ")"
}

func (m *Manager) fetchLatest(ctx context.Context) (*release, error) {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	u := m.o.API + "/repos/" + m.o.Repo + "/releases/latest"
	if pu, err := url.Parse(u); err != nil || m.allowedURL(pu) != nil {
		return nil, fmt.Errorf("update API URL not allowed: %s", u)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", m.userAgent())
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update check: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404:
		return nil, errors.New("no release published yet")
	case resp.StatusCode == 403 || resp.StatusCode == 429:
		return nil, fmt.Errorf("GitHub rate limit or access denied (HTTP %d); try again later", resp.StatusCode)
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("unreadable release data: %w", err)
	}
	if rel.Prerelease || rel.Draft {
		return nil, errors.New("latest release is a pre-release; ignored")
	}
	if _, ok := ParseVersion(rel.Tag); !ok {
		return nil, fmt.Errorf("release tag %q is not a SemVer version", rel.Tag)
	}
	return &rel, nil
}

// ---- apply ----

// Apply starts downloading and staging the latest release in the background.
func (m *Manager) Apply(auto bool) error {
	m.mu.Lock()
	st := m.statusLocked()
	switch {
	case m.apply.State == StateDownloading || m.apply.State == StateStaged || m.apply.State == StateRestarting:
		m.mu.Unlock()
		return errors.New("an update is already in progress")
	case !st.CanApply:
		m.mu.Unlock()
		return errors.New(st.CannotApply)
	case !st.Available:
		m.mu.Unlock()
		return errors.New("no newer release available")
	case st.Pending:
		m.mu.Unlock()
		return errors.New("the previous update has not been confirmed yet; wait a minute and try again")
	}
	rel := *m.rel
	m.apply = ApplyStatus{State: StateDownloading, Version: rel.Tag, Since: m.o.Now().Unix(), Message: "Downloading " + rel.Tag}
	m.mu.Unlock()
	m.recordAttempt(rel.Tag, "started")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := m.stage(ctx, &rel); err != nil {
			m.o.Log.Error("update failed", "version", rel.Tag, "err", err)
			m.recordAttempt(rel.Tag, "failed")
			m.setApply(ApplyStatus{State: StateFailed, Version: rel.Tag, Message: err.Error()})
			return
		}
		m.recordAttempt(rel.Tag, "staged")
		m.setApply(ApplyStatus{State: StateRestarting, Version: rel.Tag, Message: "Restarting into " + rel.Tag})
		m.o.Log.Info("update staged, restarting", "version", rel.Tag, "auto", auto)
		time.AfterFunc(m.o.ExitDelay, m.o.OnRestart)
	}()
	return nil
}

func (m *Manager) setApply(a ApplyStatus) {
	a.Since = m.o.Now().Unix()
	m.mu.Lock()
	m.apply = a
	m.mu.Unlock()
}

func (m *Manager) recordAttempt(tag, result string) {
	if m.o.Meta != nil {
		m.o.Meta.SetMeta(metaAttempt, tag+"|"+result+"|"+strconv.FormatInt(m.o.Now().Unix(), 10))
	}
}

// attempted returns the release tag and result of the last install attempt.
func (m *Manager) attempted() (tag, result string) {
	if m.o.Meta == nil {
		return "", ""
	}
	p := strings.Split(m.o.Meta.GetMeta(metaAttempt), "|")
	if len(p) >= 2 {
		return p[0], p[1]
	}
	return "", ""
}

func (m *Manager) findAsset(rel *release, name string) (asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return asset{}, false
}

// stage downloads, verifies and test-runs the release binary and writes it to <Dir>/mtmon.new.
func (m *Manager) stage(ctx context.Context, rel *release) error {
	tarAsset, ok := m.findAsset(rel, AssetName)
	if !ok {
		return fmt.Errorf("release %s has no %s asset", rel.Tag, AssetName)
	}
	sumAsset, ok := m.findAsset(rel, AssetName+".sha256")
	if !ok {
		return fmt.Errorf("release %s has no %s.sha256 asset", rel.Tag, AssetName)
	}
	for _, a := range []asset{tarAsset, sumAsset} {
		pu, err := url.Parse(a.URL)
		if err != nil {
			return fmt.Errorf("bad asset URL: %w", err)
		}
		if err := m.allowedURL(pu); err != nil {
			return err
		}
	}
	sumData, err := m.download(ctx, sumAsset.URL, 4096)
	if err != nil {
		return fmt.Errorf("checksum download: %w", err)
	}
	want, err := parseSum(sumData)
	if err != nil {
		return err
	}
	data, err := m.download(ctx, tarAsset.URL, MaxDownload)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return errors.New("checksum mismatch: the download is corrupt or was tampered with; nothing was installed")
	}
	bin, err := extractBinary(data)
	if err != nil {
		return err
	}
	return m.writeStaged(ctx, bin, rel.Tag, want)
}

func parseSum(b []byte) (string, error) {
	f := strings.Fields(string(b))
	if len(f) == 0 || len(f[0]) != 64 {
		return "", errors.New("malformed .sha256 file")
	}
	h := strings.ToLower(f[0])
	if _, err := hex.DecodeString(h); err != nil {
		return "", errors.New("malformed .sha256 file")
	}
	return h, nil
}

func (m *Manager) download(ctx context.Context, u string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", m.userAgent())
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > max {
		return nil, fmt.Errorf("file too large (%d bytes, limit %d)", resp.ContentLength, max)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("file too large (limit %d bytes)", max)
	}
	return b, nil
}

func (m *Manager) writeStaged(ctx context.Context, bin []byte, tag, sum string) error {
	if err := os.MkdirAll(m.o.Dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.o.Dir, ".mtmon.new.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// the binary must run and report exactly the release tag
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, tmpName, "version")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the downloaded binary does not run: %w", err)
	}
	if v := strings.TrimSpace(out.String()); v != tag {
		return fmt.Errorf("the downloaded binary reports version %q, expected %q", v, tag)
	}
	// marker first, then the binary: the helper acts on mtmon.new
	pend := fmt.Sprintf("version=%s\nsha256=%s\nts=%d\n", tag, sum, m.o.Now().Unix())
	if err := os.WriteFile(filepath.Join(m.o.Dir, "pending"), []byte(pend), 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(m.o.Dir, "mtmon.new")); err != nil {
		return err
	}
	return nil
}

// ---- background loop ----

// Run performs periodic checks, the nightly auto-install and the post-swap health confirmation until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	go m.confirmLoop(ctx)
	sleep := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	if !sleep(20 * time.Second) { // never delay startup
		return
	}
	for {
		if mode := m.Mode(); mode != ModeOff {
			cctx, cancel := context.WithTimeout(ctx, CheckTimeout+2*time.Second)
			m.Check(cctx, false)
			cancel()
			if mode == ModeAuto {
				m.maybeAuto(ctx)
			}
		}
		if !sleep(time.Minute) {
			return
		}
	}
}

// inWindow reports whether t lies in the nightly install window (03:00-04:00 local time).
func inWindow(t time.Time) bool { return t.Hour() == 3 }

func (m *Manager) maybeAuto(ctx context.Context) {
	now := m.o.Now()
	if !inWindow(now) {
		return
	}
	// make sure we decide on fresh data
	m.mu.Lock()
	stale := now.Sub(m.checkedAt) > time.Hour
	m.mu.Unlock()
	if stale {
		cctx, cancel := context.WithTimeout(ctx, CheckTimeout+2*time.Second)
		m.Check(cctx, true)
		cancel()
	}
	st := m.Status()
	if !st.Available || !st.CanApply || st.Pending {
		return
	}
	if tag, _ := m.attempted(); tag == st.Latest {
		return // at most once per release; a failed attempt is not retried automatically
	}
	m.o.Log.Info("auto-update: installing", "from", st.Current, "to", st.Latest)
	if err := m.Apply(true); err != nil {
		m.o.Log.Warn("auto-update not started", "err", err)
	}
}

// confirmLoop deletes confirm-pending once the service has been up and healthy long enough.
func (m *Manager) confirmLoop(ctx context.Context) {
	if m.o.Dir == "" {
		return
	}
	marker := filepath.Join(m.o.Dir, "confirm-pending")
	if !isFile(marker) {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(m.o.Confirm):
	}
	retry := 10 * time.Second
	if m.o.Confirm < retry {
		retry = m.o.Confirm
	}
	for {
		if m.o.Health == nil || m.o.Health() == nil {
			os.Remove(marker)
			os.Remove(filepath.Join(m.o.Dir, "last-rollback")) // an older failure is obsolete now
			m.o.Log.Info("update confirmed: new version is healthy", "version", m.o.Current)
			return
		}
		m.o.Log.Warn("post-update self-check failed, retrying")
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}
