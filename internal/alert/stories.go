// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// One story per object (#188): events of the same client or access point that follow each other closely are one
// alert that is updated in place (count, first/last time, last reason, severity = highest). A story ends after
// storyQuiet without a new event; its alert then carries the summary, and one closing notification goes out.
const storyQuiet = 15 * time.Minute

// storyText builds the message of a story. closed is true for the summary written when it ends.
type storyText func(n, extra int, first, last time.Time, closed bool) (key string, params map[string]string, tail string)

type story struct {
	id          int64
	kind, subj  string
	first, last time.Time
	n, extra    int // n events, extra: related events of another kind folded in (failed DHCP offers)
	sev         string
	text        storyText
}

var sevRank = map[string]int{"info": 0, "warning": 1, "critical": 2}

func fillKey(key string, params map[string]string) string {
	kv := make([]string, 0, 2*len(params))
	for k, v := range params {
		kv = append(kv, "{"+k+"}", v)
	}
	return strings.NewReplacer(kv...).Replace(key)
}

func (s *story) alert(sev string, closed bool) store.Alert {
	key, params, tail := s.text(s.n, s.extra, s.first, s.last, closed)
	return store.Alert{ID: s.id, TS: s.first.Unix(), Kind: s.kind, Subject: s.subj, Severity: sev, Msg: fillKey(key, params) + tail,
		MsgKey: key, Params: params, Count: s.n, First: s.first.Unix(), Last: s.last.Unix()}
}

// storyEvent adds one event at ts to the story of kind|subject. It opens the story (with n events already seen,
// at least 1) when none is open. The first message is delivered like any alert, later events only update it.
func (e *Engine) storyEvent(kind, subject, sev string, ts time.Time, n int, text storyText) {
	k := kind + "|" + subject
	e.closeStale(ts)
	e.mu.Lock()
	if e.stories == nil {
		e.stories = map[string]*story{}
	}
	s := e.stories[k]
	if s != nil {
		s.n++
		if ts.After(s.last) {
			s.last = ts
		}
		if sevRank[sev] > sevRank[s.sev] {
			s.sev = sev
		}
		s.text = text
		a := s.alert(s.sev, false)
		e.mu.Unlock()
		if err := e.St.UpdateStory(a); err != nil {
			e.Log.Error("update story", "err", err)
		}
		return
	}
	e.mu.Unlock()
	if n < 1 {
		n = 1
	}
	s = &story{kind: kind, subj: subject, first: ts, last: ts, n: n, sev: e.infoOverride(kind, sev), text: text}
	a := s.alert(s.sev, false)
	id, ok := e.raiseID(kind, subject, s.sev, a.Msg, a.MsgKey, a.Params, a.Count, a.First, a.Last, 0)
	if !ok {
		return
	}
	s.id = id
	e.mu.Lock()
	e.stories[k] = s
	e.mu.Unlock()
}

// infoOverride reports the severity a kind is turned down to by the user setting.
func (e *Engine) infoOverride(kind, sev string) string {
	if sev != "info" && e.St.IsInfoKind(kind) {
		return "info"
	}
	return sev
}

// foldExtra counts a related event (a failed DHCP offer) into the open story of kind for this client suffix.
// It reports false when no story is open, so the caller raises its own alert.
func (e *Engine) foldExtra(kind, subjectSuffix string, ts time.Time, n int) bool {
	e.closeStale(ts)
	e.mu.Lock()
	var s *story
	for _, c := range e.stories {
		if c.kind == kind && strings.HasSuffix(c.subj, subjectSuffix) {
			s = c
			break
		}
	}
	if s == nil {
		e.mu.Unlock()
		return false
	}
	s.extra += n
	if ts.After(s.last) {
		s.last = ts
	}
	a := s.alert(s.sev, false)
	e.mu.Unlock()
	if err := e.St.UpdateStory(a); err != nil {
		e.Log.Error("update story", "err", err)
	}
	return true
}

// closeStale ends the stories that were quiet for storyQuiet before now: the alert gets its summary text and one
// closing notification is sent.
func (e *Engine) closeStale(now time.Time) {
	e.mu.Lock()
	var done []*story
	for k, s := range e.stories {
		if now.Sub(s.last) >= storyQuiet {
			done = append(done, s)
			delete(e.stories, k)
		}
	}
	e.mu.Unlock()
	for _, s := range done {
		a := s.alert(s.sev, true)
		if err := e.St.UpdateStory(a); err != nil {
			e.Log.Error("close story", "err", err)
		}
		n := a
		n.Severity = "info"
		n.TS = now.Unix()
		if e.Notify != nil {
			e.Notify(n)
		}
		go e.deliver(n)
	}
}

// storyOpen reports whether a story of this kind and subject is collecting events.
func (e *Engine) storyOpen(kind, subject string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stories[kind+"|"+subject] != nil
}

const (
	keyWifiDiscOpen   = "Client {who} keeps disconnecting from {where}: {n} times between {first} and {last}"
	keyWifiDiscClosed = "Client {who}: {n} disconnects {first}–{last} at {where}"
	tailGoodSignal    = "the signal is good, so this is probably not a coverage problem; check access-list reject rules for this client and roaming settings"
)

// wifiDiscText is the story text of a client that keeps disconnecting: the count and time span, the last reason,
// the failed DHCP offers that followed its reconnects, and the hint when the signal was good.
func wifiDiscText(who, where, why string, good bool, rej aclRule, rejected bool) storyText {
	return func(n, extra int, first, last time.Time, closed bool) (string, map[string]string, string) {
		key := keyWifiDiscOpen
		if closed {
			key = keyWifiDiscClosed
		}
		params := map[string]string{"who": who, "where": where, "n": strconv.Itoa(n),
			"first": first.Format("15:04"), "last": last.Format("15:04")}
		tail := ""
		if why != "" {
			tail += " – last reason: " + why
		}
		if extra > 0 {
			tail += " – " + strconv.Itoa(extra) + " failed DHCP offers after the reconnects"
		}
		if rejected {
			tail += rejectTail(rej)
		}
		if good {
			tail += " – " + tailGoodSignal
		}
		return key, params, tail
	}
}
