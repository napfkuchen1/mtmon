package store

import (
	"strings"
	"time"
)

type FwEvent struct {
	TS      int64  `json:"ts"`
	Device  string `json:"device"`
	Prefix  string `json:"prefix"`
	Chain   string `json:"chain"`
	InIf    string `json:"in_if"`
	OutIf   string `json:"out_if"`
	Proto   string `json:"proto"`
	Flags   string `json:"flags"`
	Src     string `json:"src"`
	SPort   int    `json:"sport"`
	Dst     string `json:"dst"`
	DPort   int    `json:"dport"`
	NAT     string `json:"nat"`
	Len     int    `json:"len"`
	MAC     string `json:"mac"`
	CState  string `json:"cstate"`
	Verdict string `json:"verdict,omitempty"` // blocked | allowed | logged (from the rule meta)
	Rule    string `json:"rule,omitempty"`    // human description of the rule
	Count   int    `json:"count,omitempty"`
	// Human names for the two endpoints, filled in by the API layer (never stored).
	SrcName string `json:"src_name,omitempty"`
	SrcOrg  string `json:"src_org,omitempty"`
	SrcCC   string `json:"src_country,omitempty"`
	DstName string `json:"dst_name,omitempty"`
	DstOrg  string `json:"dst_org,omitempty"`
	DstCC   string `json:"dst_country,omitempty"`
}

type FwRule struct {
	Device  string `json:"device"`
	Prefix  string `json:"prefix"`
	Chain   string `json:"chain"`
	Action  string `json:"action"`
	Descr   string `json:"descr"`
	Managed bool   `json:"managed"`
}

const maxFwQueue = 100000
const FwRowCap = 2_000_000

func (s *Store) EnqueueFw(es ...FwEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fwQ)+len(es) > maxFwQueue {
		s.FwDropped += uint64(len(es))
		return
	}
	s.fwQ = append(s.fwQ, es...)
}

func (s *Store) FlushFw() error {
	s.mu.Lock()
	es := s.fwQ
	s.fwQ = nil
	s.mu.Unlock()
	if len(es) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	ins, err := tx.Prepare(`INSERT INTO fw_events(ts,device,prefix,chain,in_if,out_if,proto,flags,src,sport,dst,dport,nat,len,mac,cstate) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer ins.Close()
	hourly := map[[3]any]int{}
	for _, e := range es {
		ins.Exec(e.TS, e.Device, e.Prefix, e.Chain, e.InIf, e.OutIf, e.Proto, e.Flags, e.Src, e.SPort, e.Dst, e.DPort, e.NAT, e.Len, e.MAC, e.CState)
		hourly[[3]any{e.TS / 3600 * 3600, e.Device, e.Prefix}]++
	}
	for k, n := range hourly {
		tx.Exec(`INSERT INTO fw_hourly(ts,device,prefix,n) VALUES(?,?,?,?) ON CONFLICT DO UPDATE SET n=n+excluded.n`, k[0], k[1], k[2], n)
	}
	return tx.Commit()
}

// SaveFwRules registers the prefix→rule meaning for a device (replaces earlier entries of the same prefixes).
func (s *Store) SaveFwRules(rs []FwRule) {
	for _, r := range rs {
		s.DB.Exec(`INSERT INTO fw_rules(device,prefix,chain,action,descr,managed) VALUES(?,?,?,?,?,?)
			ON CONFLICT(device,prefix) DO UPDATE SET chain=excluded.chain, action=excluded.action, descr=excluded.descr, managed=excluded.managed`,
			r.Device, r.Prefix, r.Chain, r.Action, r.Descr, b2i(r.Managed))
	}
}

func (s *Store) FwRules() ([]FwRule, error) {
	rows, err := s.DB.Query(`SELECT device,prefix,chain,action,coalesce(descr,''),managed FROM fw_rules ORDER BY device,prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FwRule{}
	for rows.Next() {
		var r FwRule
		var m int
		rows.Scan(&r.Device, &r.Prefix, &r.Chain, &r.Action, &r.Descr, &m)
		r.Managed = m == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// Verdict maps a rule's action to what the user cares about.
func Verdict(action string) string {
	switch action {
	case "drop", "reject":
		return "blocked"
	case "accept", "passthrough":
		return "allowed"
	}
	return "logged"
}

type FwFilter struct {
	Since, Until int64
	Device       string
	Src, Dst     string // exact IP
	Peer         string // IP on either side
	DPort        int
	Verdict      string // blocked | allowed
	Limit        int
}

func (s *Store) ruleIndex() map[[2]string]FwRule {
	m := map[[2]string]FwRule{}
	rs, _ := s.FwRules()
	for _, r := range rs {
		m[[2]string{r.Device, r.Prefix}] = r
	}
	return m
}

func (s *Store) annotate(e *FwEvent, idx map[[2]string]FwRule) {
	if r, ok := idx[[2]string{e.Device, e.Prefix}]; ok {
		e.Verdict, e.Rule = Verdict(r.Action), strings.TrimSpace(r.Chain+" "+r.Action+" – "+r.Descr)
		return
	}
	e.Verdict = "logged"
	if e.Prefix != "" {
		e.Rule = e.Chain + " – prefix " + e.Prefix
	} else {
		e.Rule = e.Chain
	}
}

// FwEvents returns recent events, newest first, annotated with the rule meaning.
func (s *Store) FwEvents(f FwFilter) ([]FwEvent, error) {
	if f.Limit <= 0 || f.Limit > 2000 {
		f.Limit = 200
	}
	if f.Until == 0 {
		f.Until = time.Now().Unix() + 60
	}
	q := `SELECT ts,device,prefix,chain,in_if,out_if,proto,flags,src,sport,dst,dport,nat,len,mac,cstate FROM fw_events WHERE ts>=? AND ts<=?`
	args := []any{f.Since, f.Until}
	if f.Device != "" {
		q += ` AND device=?`
		args = append(args, f.Device)
	}
	if f.Src != "" {
		q += ` AND src=?`
		args = append(args, f.Src)
	}
	if f.Dst != "" {
		q += ` AND dst=?`
		args = append(args, f.Dst)
	}
	if f.Peer != "" {
		q += ` AND (src=? OR dst=?)`
		args = append(args, f.Peer, f.Peer)
	}
	if f.DPort > 0 {
		q += ` AND dport=?`
		args = append(args, f.DPort)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	lim := f.Limit
	if f.Verdict != "" {
		lim = 5000 // filter on verdict after annotation
	}
	args = append(args, lim)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := s.ruleIndex()
	out := []FwEvent{}
	for rows.Next() {
		var e FwEvent
		if err := rows.Scan(&e.TS, &e.Device, &e.Prefix, &e.Chain, &e.InIf, &e.OutIf, &e.Proto, &e.Flags, &e.Src, &e.SPort, &e.Dst, &e.DPort, &e.NAT, &e.Len, &e.MAC, &e.CState); err != nil {
			return nil, err
		}
		s.annotate(&e, idx)
		if f.Verdict != "" && e.Verdict != f.Verdict {
			continue
		}
		out = append(out, e)
		if len(out) >= f.Limit {
			break
		}
	}
	return out, rows.Err()
}

type FwSummary struct {
	Rules   []FwRuleHits `json:"rules"`
	Series  []FwPoint    `json:"series"`
	TopSrc  []FwTop      `json:"top_blocked_src"`
	TopDst  []FwTop      `json:"top_blocked_dst"`
	Blocked int64        `json:"blocked"`
	Allowed int64        `json:"allowed"`
	Logged  int64        `json:"logged"`
}

type FwRuleHits struct {
	Device  string `json:"device"`
	Prefix  string `json:"prefix"`
	Rule    string `json:"rule"`
	Verdict string `json:"verdict"`
	Hits    int64  `json:"hits"`
}

type FwPoint struct {
	TS      int64 `json:"ts"`
	Blocked int64 `json:"blocked"`
	Allowed int64 `json:"allowed"`
}

type FwTop struct {
	Key   string `json:"key"`
	Port  int    `json:"port,omitempty"`
	Hits  int64  `json:"hits"`
	Label string `json:"label,omitempty"`
	// Filled in by the API layer: name (client label / DNS name), AS organisation, country code.
	Name    string `json:"name,omitempty"`
	Org     string `json:"org,omitempty"`
	Country string `json:"country,omitempty"`
	Local   bool   `json:"local,omitempty"`
}

func (s *Store) FwSummary(since int64) (*FwSummary, error) {
	idx := s.ruleIndex()
	sum := &FwSummary{Rules: []FwRuleHits{}, Series: []FwPoint{}, TopSrc: []FwTop{}, TopDst: []FwTop{}}
	rows, err := s.DB.Query(`SELECT device,prefix,sum(n) FROM fw_hourly WHERE ts>=? GROUP BY device,prefix ORDER BY 3 DESC`, since/3600*3600)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h FwRuleHits
		rows.Scan(&h.Device, &h.Prefix, &h.Hits)
		fe := FwEvent{Device: h.Device, Prefix: h.Prefix}
		s.annotate(&fe, idx)
		h.Rule, h.Verdict = fe.Rule, fe.Verdict
		sum.Rules = append(sum.Rules, h)
		switch h.Verdict {
		case "blocked":
			sum.Blocked += h.Hits
		case "allowed":
			sum.Allowed += h.Hits
		default:
			sum.Logged += h.Hits
		}
	}
	rows.Close()
	// series
	pts := map[int64]*FwPoint{}
	r2, err := s.DB.Query(`SELECT ts,device,prefix,n FROM fw_hourly WHERE ts>=? ORDER BY ts`, since/3600*3600)
	if err == nil {
		for r2.Next() {
			var ts, n int64
			var d, p string
			r2.Scan(&ts, &d, &p, &n)
			fe := FwEvent{Device: d, Prefix: p}
			s.annotate(&fe, idx)
			pt := pts[ts]
			if pt == nil {
				pt = &FwPoint{TS: ts}
				pts[ts] = pt
			}
			if fe.Verdict == "blocked" {
				pt.Blocked += n
			} else {
				pt.Allowed += n
			}
		}
		r2.Close()
	}
	var keys []int64
	for k := range pts {
		keys = append(keys, k)
	}
	sortInt64(keys)
	for _, k := range keys {
		sum.Series = append(sum.Series, *pts[k])
	}
	// top blocked sources / destinations from raw events (raw retention window)
	blockedPrefixes := []any{}
	ph := ""
	for k, r := range idx {
		if Verdict(r.Action) == "blocked" {
			blockedPrefixes = append(blockedPrefixes, k[1])
			ph += ",?"
		}
	}
	if ph != "" {
		args := append([]any{since}, blockedPrefixes...)
		sum.TopSrc = s.fwTop(`SELECT src, 0, count(*) FROM fw_events WHERE ts>=? AND prefix IN (`+ph[1:]+`) GROUP BY src ORDER BY 3 DESC LIMIT 10`, args)
		sum.TopDst = s.fwTop(`SELECT dst, dport, count(*) FROM fw_events WHERE ts>=? AND prefix IN (`+ph[1:]+`) GROUP BY dst, dport ORDER BY 3 DESC LIMIT 10`, args)
	}
	return sum, nil
}

func (s *Store) fwTop(q string, args []any) []FwTop {
	out := []FwTop{}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var t FwTop
		rows.Scan(&t.Key, &t.Port, &t.Hits)
		out = append(out, t)
	}
	return out
}

func sortInt64(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func (s *Store) cleanupFw(rawCut, cut int64) {
	s.DB.Exec(`DELETE FROM fw_events WHERE ts < ?`, rawCut)
	var span int64
	if s.DB.QueryRow(`SELECT coalesce(max(rowid)-min(rowid),0) FROM fw_events`).Scan(&span); span > FwRowCap {
		s.DB.Exec(`DELETE FROM fw_events WHERE rowid <= (SELECT max(rowid) FROM fw_events) - ?`, FwRowCap)
	}
	s.DB.Exec(`DELETE FROM fw_hourly WHERE ts < ?`, cut)
}
