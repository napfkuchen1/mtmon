package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Config history (#46): once a day (and on demand) the monitoring login reads a fixed set of REST lists, secrets and
// volatile fields are stripped, and a new snapshot is stored when something changed. The UI shows the diff.

const configCheckEvery = 24 * time.Hour

var configMu sync.Mutex

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/devices/{name}/config", a(s.configHistory))
		m.HandleFunc("POST /api/devices/{name}/config/snapshot", a(s.configSnapshotNow))
		m.HandleFunc("GET /api/devices/{name}/config/download", a(s.configDownload))
		m.HandleFunc("PUT /api/config-history", a(s.configKeepSave))
	})
}

// takeConfig reads the router, compares with the newest stored snapshot and stores a new one when it differs.
// The first snapshot is only the baseline. changes is empty when nothing changed.
func (s *Server) takeConfig(ctx context.Context, name string) (changes []store.ConfigChange, err error) {
	d, ok := s.Cfg.Device(name)
	if !ok {
		return nil, fmt.Errorf("unknown device")
	}
	gc := poller.NewGuardedClient(d, s.addrAllowed)
	snap, err := gc.ConfigSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	cur := store.ConfigSnap{}
	for sec, rows := range snap {
		list := make([]map[string]string, 0, len(rows))
		for _, r := range rows {
			list = append(list, r)
		}
		cur[sec] = list
	}
	configMu.Lock()
	defer configMu.Unlock()
	now := time.Now().Unix()
	s.St.SetConfigChecked(name, now)
	// who changed things since the last check: history entries newer than the id seen then (also advanced when the
	// check finds no difference, so old entries never get blamed for a later change)
	idKey := "cfgid:" + name
	after, _ := strconv.ParseInt(s.St.GetMeta(idKey), 10, 64)
	by, newest, planned := gc.ChangedBy(ctx, after)
	if newest > after {
		s.St.SetMeta(idKey, strconv.FormatInt(newest, 10))
	}
	changes = []store.ConfigChange{}
	times := s.St.ConfigSnapTimes(name)
	if len(times) > 0 {
		prev, err := s.St.ConfigSnapAt(name, times[0])
		if err == nil {
			if changes = store.DiffConfig(scrubStored(prev), cur); len(changes) == 0 {
				return changes, nil
			}
		}
	}
	if err := s.St.SaveConfigSnap(name, now, cur); err != nil {
		return nil, err
	}
	if len(times) > 0 && len(changes) > 0 && s.ConfigChanged != nil && !planned {
		if len(by) == 0 {
			by = []string{poller.SourceUnknown}
		}
		s.ConfigChanged(name, len(changes), by)
	}
	return changes, nil
}

// configWatchEvery is how often /system/history is polled; a new entry there triggers a snapshot right away
// (the daily full snapshot stays as a safety net).
const configWatchEvery = 2 * time.Minute

// WatchConfig polls /system/history of every UI-managed device and snapshots a device as soon as its history changed.
func (s *Server) WatchConfig(ctx context.Context) {
	t := time.NewTicker(configWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.watchConfigOnce(ctx)
		}
	}
}

func (s *Server) watchConfigOnce(ctx context.Context) {
	s.pruneConfigDaily(time.Now())
	rows, err := s.St.DeviceRows()
	if err != nil {
		return
	}
	for _, row := range rows {
		d, ok := s.Cfg.Device(row.Name)
		if !ok {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		mark := poller.NewGuardedClient(d, s.addrAllowed).HistoryMark(cctx)
		cancel()
		if mark == "" {
			continue
		}
		key := "cfghist:" + row.Name
		prev := s.St.GetMeta(key)
		if prev == mark {
			continue
		}
		s.St.SetMeta(key, mark)
		if prev == "" {
			continue // first sight is only the reference point
		}
		tctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		s.takeConfig(tctx, row.Name)
		cancel()
	}
}

// scrubStored applies the current volatile-field filter to an older stored snapshot, so fields that were
// only later recognised as runtime values do not show up as a change.
func scrubStored(old store.ConfigSnap) store.ConfigSnap {
	out := store.ConfigSnap{}
	for sec, rows := range old {
		list := make([]map[string]string, 0, len(rows))
		for _, r := range rows {
			if s, ok := poller.ScrubRowIn(sec, poller.Row(r)); ok {
				list = append(list, s)
			}
		}
		out[sec] = list
	}
	return out
}

// checkConfigDue snapshots the device when the last check is older than a day. Called from the capability refresh.
func (s *Server) checkConfigDue(name string) {
	if time.Since(time.Unix(s.St.ConfigCheckedAt(name), 0)) < configCheckEvery {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s.takeConfig(ctx, name)
}

// configHistory: GET /api/devices/{name}/config?from=&to= – snapshot times and the diff between two of them
// (default: newest against the one before).
func (s *Server) configHistory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.Cfg.Device(name); !ok {
		jerr(w, 404, "unknown device")
		return
	}
	times := s.St.ConfigSnapTimes(name)
	out := map[string]any{"snapshots": times, "keep_days": s.St.ConfigKeepDays(), "min_keep_days": store.MinConfigKeepDays, "checked": s.St.ConfigCheckedAt(name), "changes": []store.ConfigChange{}, "from": 0, "to": 0}
	if len(times) >= 2 {
		to, from := times[0], times[1]
		if v, _ := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64); v > 0 {
			to = v
		}
		if v, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64); v > 0 {
			from = v
		}
		a, errA := s.St.ConfigSnapAt(name, from)
		b, errB := s.St.ConfigSnapAt(name, to)
		if errA != nil || errB != nil {
			jerr(w, 404, "unknown snapshot")
			return
		}
		out["changes"], out["from"], out["to"] = store.DiffConfig(scrubStored(a), scrubStored(b)), from, to
	}
	jsonOut(w, out)
}

// configSnapshotNow: POST /api/devices/{name}/config/snapshot – read the router now.
func (s *Server) configSnapshotNow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	ch, err := s.takeConfig(ctx, name)
	if err != nil {
		jerr(w, 502, err.Error())
		return
	}
	jsonOut(w, map[string]any{"changed": len(ch), "snapshots": s.St.ConfigSnapTimes(name)})
}
