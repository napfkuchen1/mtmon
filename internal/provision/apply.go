package provision

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/daniel/mtmon/internal/poller"
	"github.com/daniel/mtmon/internal/store"
)

type StepResult struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	OK    bool   `json:"ok"`
	Msg   string `json:"msg,omitempty"`
}

type Result struct {
	OK         bool                  `json:"ok"`
	Steps      []StepResult          `json:"steps"`
	RolledBack bool                  `json:"rolled_back"`
	Rollback   []StepResult          `json:"rollback,omitempty"`
	Error      string                `json:"error,omitempty"`
	Backup     string                `json:"backup,omitempty"`
	MonUser    string                `json:"-"`
	MonPass    string                `json:"-"`
	FwMeta     []FwMeta              `json:"-"`
	Entries    []store.ManifestEntry `json:"-"`
}

// Undo is the stored inverse of one router change.
type Undo struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Body   map[string]string `json:"body,omitempty"`
	Guard  map[string]string `json:"guard,omitempty"` // fields that must still match before we delete
}

type runner struct {
	cl      *poller.Client
	ro      func(user, pass string) *poller.Client
	rec     func(store.ManifestEntry)
	entries []store.ManifestEntry
	seq     int
	res     *Result
}

func (r *runner) record(e store.ManifestEntry) {
	r.seq++
	e.Seq, e.State = r.seq, "applied"
	r.entries = append(r.entries, e)
	if r.rec != nil {
		r.rec(e)
	}
}

func undoJSON(u Undo) string { b, _ := json.Marshal(u); return string(b) }

func (r *runner) create(ctx context.Context, path string, body map[string]string, descr string, guard map[string]string) (string, error) {
	rows, err := r.cl.Do(ctx, "PUT", path, body)
	if err != nil {
		return "", err
	}
	id := ""
	if len(rows) > 0 {
		id = rows[0][".id"]
	}
	if id == "" {
		return "", fmt.Errorf("%s: router did not return an id", path)
	}
	r.record(store.ManifestEntry{Kind: "create", Descr: descr, Path: path, RID: id,
		Undo: undoJSON(Undo{Method: "DELETE", Path: path + "/" + id, Guard: guard})})
	return id, nil
}

// modify sets fields and remembers the previous values. id=="" addresses a single-record menu.
func (r *runner) modify(ctx context.Context, path, id string, body map[string]string, descr string) error {
	var cur poller.Row
	var rows []poller.Row
	var err error
	if id == "" {
		rows, err = r.cl.Get(ctx, path)
	} else {
		rows, err = r.cl.Get(ctx, path+"/"+id)
	}
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		cur = rows[0]
	}
	prev := map[string]string{}
	for k := range body {
		prev[k] = cur[k]
	}
	do := map[string]string{}
	for k, v := range body {
		do[k] = v
	}
	undo := map[string]string{}
	for k, v := range prev {
		undo[k] = v
	}
	if id != "" {
		do[".id"], undo[".id"] = id, id
	}
	if _, err := r.cl.Do(ctx, "POST", path+"/set", do); err != nil {
		return err
	}
	r.record(store.ManifestEntry{Kind: "modify", Descr: descr, Path: path, RID: id,
		Undo: undoJSON(Undo{Method: "POST", Path: path + "/set", Body: undo})})
	return nil
}

func (r *runner) backup(ctx context.Context) error {
	name := "mtmon-before-" + time.Now().Format("20060102-150405")
	if _, err := r.cl.Do(ctx, "POST", "/export", map[string]string{"file": name}); err != nil {
		return fmt.Errorf("config export failed (%v) - nothing was changed. Fix the cause or tick 'skip backup'", err)
	}
	r.res.Backup = name + ".rsc"
	return nil
}

func randPass(n int) string {
	const al = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		x, _ := rand.Int(rand.Reader, big.NewInt(int64(len(al))))
		b[i] = al[x.Int64()]
	}
	return string(b)
}

func (r *runner) createUser(ctx context.Context, o Options) error {
	if _, err := r.create(ctx, "/user/group", map[string]string{"name": monGroup, "policy": "read,api,rest-api", "comment": ManagedTag},
		"Benutzergruppe "+monGroup+" (read, api, rest-api)", map[string]string{"name": monGroup}); err != nil {
		return fmt.Errorf("group %q: %w (already exists? run offboarding of an old setup first)", monGroup, err)
	}
	r.res.MonUser, r.res.MonPass = o.MonUser, randPass(24)
	body := map[string]string{"name": o.MonUser, "group": monGroup, "password": r.res.MonPass, "comment": ManagedTag}
	if o.MtmonIP != "" {
		body["address"] = o.MtmonIP + "/32"
	}
	if _, err := r.create(ctx, "/user", body, "Benutzer "+o.MonUser+" (nur von "+o.MtmonIP+")", map[string]string{"name": o.MonUser}); err != nil {
		return fmt.Errorf("user %q: %w (already exists?)", o.MonUser, err)
	}
	return nil
}

func (r *runner) flow(ctx context.Context, k *Caps, o Options) error {
	set := map[string]string{"active-flow-timeout": "30s", "inactive-flow-timeout": "15s"}
	if !k.Flow.Enabled {
		set["enabled"], set["interfaces"], set["cache-entries"] = "yes", "all", "16k"
	}
	if err := r.modify(ctx, "/ip/traffic-flow", "", set, "Traffic-Flow Einstellungen (Timeouts"+map[bool]string{true: ", aktiviert"}[!k.Flow.Enabled]+")"); err != nil {
		return err
	}
	port := fmt.Sprint(o.FlowPort)
	tgt := map[string]string{"dst-address": o.MtmonIP, "port": port, "version": "ipfix"}
	if sa := srcArg(o); sa != "" {
		tgt["src-address"] = strings.TrimPrefix(sa, " src-address=")
	}
	_, err := r.create(ctx, "/ip/traffic-flow/target", tgt,
		"Traffic-Flow Ziel "+o.MtmonIP+":"+port, map[string]string{"dst-address": o.MtmonIP, "port": port})
	return err
}

func (r *runner) syslog(ctx context.Context, o Options) error {
	act := map[string]string{"name": syslogAction, "target": "remote", "remote": o.MtmonIP, "remote-port": fmt.Sprint(o.SyslogPort)}
	if sa := srcArg(o); sa != "" {
		act["src-address"] = strings.TrimPrefix(sa, " src-address=")
	}
	if _, err := r.create(ctx, "/system/logging/action", act, "Logging-Action "+syslogAction, map[string]string{"name": syslogAction}); err != nil {
		return err
	}
	_, err := r.create(ctx, "/system/logging", map[string]string{"topics": "firewall", "action": syslogAction},
		"Logging-Regel firewall → "+syslogAction, map[string]string{"action": syslogAction, "topics": "firewall"})
	return err
}

func (r *runner) fwLog(ctx context.Context, k *Caps, o Options) error {
	for _, id := range o.FwRules {
		f := k.rule(id)
		if f == nil {
			return fmt.Errorf("filter rule %s no longer exists", id)
		}
		descr := strings.TrimSpace(f.Comment)
		if descr == "" {
			descr = f.Summary
		}
		meta := FwMeta{Chain: f.Chain, Action: f.Action, Descr: descr, Managed: true}
		switch {
		case f.Log && f.LogPrefix != "":
			meta.Prefix, meta.Managed = f.LogPrefix, false // already logs with its own prefix: leave untouched
		case f.Log:
			meta.Prefix = prefixFor(id)
			if err := r.modify(ctx, "/ip/firewall/filter", id, map[string]string{"log-prefix": meta.Prefix}, "Log-Prefix "+meta.Prefix+" für "+f.Chain+"/"+f.Action); err != nil {
				return err
			}
		default:
			meta.Prefix = prefixFor(id)
			if err := r.modify(ctx, "/ip/firewall/filter", id, map[string]string{"log": "yes", "log-prefix": meta.Prefix}, "Logging an: "+f.Chain+"/"+f.Action+" "+f.Comment); err != nil {
				return err
			}
		}
		r.res.FwMeta = append(r.res.FwMeta, meta)
	}
	return nil
}

func (r *runner) logNew(ctx context.Context) error {
	_, err := r.create(ctx, "/ip/firewall/filter", map[string]string{"chain": "forward", "action": "passthrough", "connection-state": "new",
		"log": "yes", "log-prefix": "MTM-NEW", "comment": ManagedTag + ": log new connections"}, "Regel: neue Verbindungen loggen (MTM-NEW)",
		map[string]string{"log-prefix": "MTM-NEW"})
	if err == nil {
		r.res.FwMeta = append(r.res.FwMeta, FwMeta{Prefix: "MTM-NEW", Chain: "forward", Action: "accept", Descr: "passed all drop rules (default accept)", Managed: true})
	}
	return err
}

func (r *runner) fasttrack(ctx context.Context, k *Caps) error {
	for _, f := range k.Filter {
		if f.Action == "fasttrack-connection" && !f.Disabled {
			if err := r.modify(ctx, "/ip/firewall/filter", f.ID, map[string]string{"disabled": "yes"}, "FastTrack-Regel deaktiviert"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *runner) verify(ctx context.Context) error {
	if r.res.MonUser == "" {
		return nil
	}
	c := r.ro(r.res.MonUser, r.res.MonPass)
	var err error
	for i := 0; i < 3; i++ { // the router needs a moment to activate the new user
		if _, err = c.Get(ctx, "/system/resource"); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
	}
	return fmt.Errorf("login as %s failed (%v). Is %s really the address the router sees mtmon with?", r.res.MonUser, err, "the configured mtmon IP")
}

// Apply runs the plan. On any failure it immediately rolls back everything it changed.
func Apply(ctx context.Context, cl *poller.Client, ro func(user, pass string) *poller.Client, k *Caps, o Options, rec func(store.ManifestEntry)) *Result {
	res := &Result{}
	if o.MonUser == "" {
		o.MonUser = "mtmon"
	}
	r := &runner{cl: cl, ro: ro, rec: rec, res: res}
	for _, a := range buildActions(k, o) {
		err := a.run(ctx, r)
		sr := StepResult{Key: a.Key, Title: a.Title, OK: err == nil}
		if err != nil {
			sr.Msg = err.Error()
			res.Steps = append(res.Steps, sr)
			res.Error = fmt.Sprintf("%s: %v", a.Title, err)
			if len(r.entries) > 0 {
				res.RolledBack = true
				res.Rollback = Rollback(ctx, cl, r.entries, nil)
			}
			res.Entries = r.entries
			return res
		}
		res.Steps = append(res.Steps, sr)
	}
	res.OK, res.Entries = true, r.entries
	return res
}

// Rollback undoes manifest entries in reverse order. Entries whose router object was changed by someone
// else in the meantime (guard mismatch) are left alone and reported.
func Rollback(ctx context.Context, cl *poller.Client, entries []store.ManifestEntry, onState func(id int64, st string)) []StepResult {
	es := append([]store.ManifestEntry(nil), entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].Seq > es[j].Seq })
	var out []StepResult
	for _, e := range es {
		if e.State == "reverted" {
			continue
		}
		sr := StepResult{Key: e.Kind, Title: "Zurückgebaut: " + e.Descr, OK: true}
		st := "reverted"
		if err := undoOne(ctx, cl, e); err != nil {
			sr.OK, sr.Msg, st = false, err.Error(), "revert-failed"
		}
		if onState != nil {
			onState(e.ID, st)
		}
		out = append(out, sr)
	}
	return out
}

func undoOne(ctx context.Context, cl *poller.Client, e store.ManifestEntry) error {
	var u Undo
	if err := json.Unmarshal([]byte(e.Undo), &u); err != nil {
		return err
	}
	if u.Method == "DELETE" {
		rows, err := cl.Get(ctx, u.Path)
		if err != nil {
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "no such item") {
				return nil // already gone
			}
			if strings.Contains(err.Error(), "HTTP 400") { // RouterOS answers 400 "no such item" for a stale id
				return nil
			}
			return err
		}
		if len(rows) > 0 {
			for k, want := range u.Guard {
				if rows[0][k] != want {
					return fmt.Errorf("skipped: object %s changed since setup (%s=%q, expected %q) - remove it manually if desired", u.Path, k, rows[0][k], want)
				}
			}
		}
	}
	var body any
	if u.Body != nil {
		body = u.Body
	}
	_, err := cl.Do(ctx, u.Method, u.Path, body)
	if err != nil && u.Method == "DELETE" && (strings.Contains(err.Error(), "no such item") || strings.Contains(err.Error(), "HTTP 404")) {
		return nil
	}
	return err
}

// ManualScript renders RouterOS CLI commands that revert the given entries, for the case the router
// cannot be reached by mtmon any more (or the operator prefers to do it by hand).
func ManualScript(entries []store.ManifestEntry) string {
	es := append([]store.ManifestEntry(nil), entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].Seq > es[j].Seq })
	var b strings.Builder
	b.WriteString("# mtmon offboarding - paste into the RouterOS terminal (reverse order of setup)\n")
	for _, e := range es {
		if e.State == "reverted" {
			continue
		}
		var u Undo
		if json.Unmarshal([]byte(e.Undo), &u) != nil {
			continue
		}
		fmt.Fprintf(&b, "# %s\n", e.Descr)
		cli := func(p string) string { return "/" + strings.ReplaceAll(strings.Trim(p, "/"), "/", " ") }
		menu := cli(strings.TrimSuffix(u.Path, "/set"))
		if u.Method == "DELETE" {
			i := strings.LastIndex(u.Path, "/")
			fmt.Fprintf(&b, "%s remove %s\n", cli(u.Path[:i]), u.Path[i+1:])
			continue
		}
		keys := make([]string, 0, len(u.Body))
		for k := range u.Body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		id, args := "", []string{}
		for _, k := range keys {
			if k == ".id" {
				id = u.Body[k]
				continue
			}
			args = append(args, fmt.Sprintf("%s=%q", k, u.Body[k]))
		}
		fmt.Fprintf(&b, "%s set %s\n", menu, strings.TrimSpace(id+" "+strings.Join(args, " ")))
	}
	return b.String()
}

var _ = errors.New
