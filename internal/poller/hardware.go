// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// hwEvery is the hardware poll interval in base ticks (seconds): at most one round every five minutes. The
// values (optical power, PoE state, LTE signal) change slowly and every reading costs one POST per port.
const hwEvery = 300

// Upper bounds per round, so a large switch cannot turn the hardware poll into a flood.
const (
	hwMaxPoe = 64
	hwMaxSfp = 16
	hwMaxLte = 4
)

// hwDue reports whether the hardware poll runs on this tick: once at the start, then every hwEvery ticks (scaled
// like the other slow polls when the router is busy).
func hwDue(tick, slow int64) bool {
	if slow < 1 {
		slow = 1
	}
	return tick == 1 || tick%(hwEvery*slow) == 0
}

// hwState is what one worker knows about the monitor capability per feature. It lives in memory: a restart or a
// changed device configuration probes once more.
type hwState struct {
	poe, sfp, lte string // store.HWOn / HWOff / HWNone / "" (unknown)
}

// Events may also implement Hardware to learn about each new reading (the alert engine does).
type hardwareEvents interface {
	Hardware(device string, h store.HWDevice)
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

// hwPoll reads SFP optics, PoE-out state and LTE signal of one device. Every feature is probed on its own:
// a list GET finds the ports (no hardware → nothing more is asked, quietly), the first monitor answer turns the
// feature on, and a 403 turns it off for good – no retry on later polls and no hint to grant more rights.
func (w *worker) hwPoll(ctx context.Context) {
	h := store.HWDevice{TS: time.Now().Unix()}
	h.PoE = w.hwPoe(ctx)
	h.SFP = w.hwSfp(ctx)
	h.LTE = w.hwLte(ctx)
	h.Caps = store.HWCaps{SFP: w.hw.sfp, PoE: w.hw.poe, LTE: w.hw.lte}
	if h.Empty() && h.Caps == (store.HWCaps{}) {
		return
	}
	w.m.St.SetHW(w.d.Name, h)
	if ev, ok := w.m.Ev.(hardwareEvents); ok {
		ev.Hardware(w.d.Name, h)
	}
}

// hwList runs the detection step shared by the three features. It returns the ports to ask, or nil when the
// feature is off, absent or the list could not be read this time.
func (w *worker) hwList(ctx context.Context, state *string, path string, pick func(Row) string, props ...string) []string {
	if *state == store.HWOff || *state == store.HWNone {
		return nil
	}
	cc, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := w.c.GetProps(cc, path, props...)
	switch {
	case errors.Is(err, ErrAuth):
		*state = store.HWOff // not allowed to even list it
		return nil
	case isNotFound(err):
		*state = store.HWNone
		return nil
	case err != nil:
		return nil // transient: try again next round
	}
	var out []string
	for _, r := range rows {
		if n := pick(r); n != "" {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		*state = store.HWNone
	}
	return out
}

// hwAsk sends one monitor call and maintains the capability state. ok is false when the feature must not be
// asked any more or the call failed.
func (w *worker) hwAsk(ctx context.Context, state *string, path, port string, props ...string) (Row, bool) {
	cc, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := w.c.Monitor(cc, path, port, props...)
	switch {
	case err == nil:
		*state = store.HWOn
		return r, true
	case errors.Is(err, ErrMonitorForbidden):
		*state = store.HWOff
	case errors.Is(err, ErrMonitorNotFound):
		*state = store.HWNone
	}
	return nil, false
}

func (w *worker) hwPoe(ctx context.Context) []store.HWPoe {
	ports := w.hwList(ctx, &w.hw.poe, "/interface/ethernet/poe", func(r Row) string {
		if r["poe-out"] == "off" {
			return ""
		}
		return r["name"]
	}, "name", "poe-out")
	var out []store.HWPoe
	for i, p := range ports {
		if i >= hwMaxPoe {
			break
		}
		r, ok := w.hwAsk(ctx, &w.hw.poe, "interface/ethernet/poe/monitor", p,
			"name", "poe-out-status", "poe-out-power-pair", "poe-out-power")
		if w.hw.poe == store.HWOff || w.hw.poe == store.HWNone {
			return nil
		}
		if !ok || r["poe-out-status"] == "" {
			continue
		}
		st := strings.ToLower(r["poe-out-status"])
		out = append(out, store.HWPoe{Iface: p, Status: st, Pair: r["poe-out-power-pair"],
			PowerW: numPtr(r, "poe-out-power"), Fault: st == "short-circuit" || st == "overload"})
	}
	return out
}

func isSfpName(s string) bool {
	s = strings.ToLower(s)
	return strings.HasPrefix(s, "sfp") || strings.HasPrefix(s, "qsfp")
}

func (w *worker) hwSfp(ctx context.Context) []store.HWSfp {
	ports := w.hwList(ctx, &w.hw.sfp, "/interface/ethernet", func(r Row) string {
		if isSfpName(r["name"]) || isSfpName(r["default-name"]) {
			return r["name"]
		}
		return ""
	}, "name", "default-name")
	var out []store.HWSfp
	for i, p := range ports {
		if i >= hwMaxSfp {
			break
		}
		r, ok := w.hwAsk(ctx, &w.hw.sfp, "interface/ethernet/monitor", p,
			"name", "sfp-module-present", "sfp-rx-power", "sfp-tx-power", "sfp-temperature", "sfp-supply-voltage")
		if w.hw.sfp == store.HWOff || w.hw.sfp == store.HWNone {
			return nil
		}
		if !ok {
			continue
		}
		s := store.HWSfp{Iface: p, RxDBm: numPtr(r, "sfp-rx-power"), TxDBm: numPtr(r, "sfp-tx-power"),
			TempC: numPtr(r, "sfp-temperature"), VoltV: numPtr(r, "sfp-supply-voltage")}
		s.Present = r.Bool("sfp-module-present") || s.RxDBm != nil || s.TxDBm != nil
		if !s.Present { // an empty cage is not worth a line
			continue
		}
		out = append(out, s)
	}
	return out
}

func (w *worker) hwLte(ctx context.Context) []store.HWLte {
	ports := w.hwList(ctx, &w.hw.lte, "/interface/lte", func(r Row) string { return r["name"] }, "name")
	var out []store.HWLte
	for i, p := range ports {
		if i >= hwMaxLte {
			break
		}
		r, ok := w.hwAsk(ctx, &w.hw.lte, "interface/lte/monitor", p,
			"name", "status", "access-technology", "rsrp", "rsrq", "sinr", "rssi")
		if w.hw.lte == store.HWOff || w.hw.lte == store.HWNone {
			return nil
		}
		if !ok {
			continue
		}
		l := store.HWLte{Iface: p, Tech: r["access-technology"], Status: r["status"],
			RSRP: numPtr(r, "rsrp"), RSRQ: numPtr(r, "rsrq"), SINR: numPtr(r, "sinr"), RSSI: numPtr(r, "rssi")}
		if l.RSRP == nil && l.RSRQ == nil && l.SINR == nil && l.RSSI == nil && l.Status == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}
