package alert

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The notes behind the base text of config_changed and wifi_disconnects are translated segment by segment in the UI
// (" – " separates them). Every segment the engine can produce, in every combination of optional tails, must have a
// German text, or a German UI shows English fragments in the middle of a sentence (#195).
func TestOptionalTailSegmentsHaveGerman(t *testing.T) {
	files, _ := filepath.Glob("../../web/src/lib/i18n/*.js")
	if len(files) == 0 {
		t.Skip("web sources not found")
	}
	var dict strings.Builder
	for _, f := range files {
		b, _ := os.ReadFile(f)
		dict.Write(b)
	}
	b, _ := os.ReadFile("../../web/src/lib/i18n.svelte.js")
	dict.Write(b)
	d := dict.String()

	var tails []string
	// config_changed: the notes the engine appends in ConfigChanged
	tails = append(tails,
		" – changed by: admin; claude via SSH from 192.0.2.1; scheduler nightly; script; source unknown",
		" – changed by: admin (maintenance window)",
		" (maintenance window)",
		" – planned: scheduler nightly",
		" (made through mtmon)",
		" – changed by: admin (made through mtmon)")
	// wifi_disconnects: every combination of the optional parts
	for _, why := range []string{"", "connection lost, signal strength -51"} {
		for _, extra := range []int{0, 3} {
			for _, rej := range []aclRule{{}, {Comment: "pin-ap2", Iface: "cap-wifi1"}, {Comment: "x"}} {
				for _, rejected := range []bool{false, true} {
					for _, good := range []bool{false, true} {
						_, _, tail := wifiDiscText("AA:BB:CC:00:11:22", "ap1", why, good, rej, rejected)(6, extra, time.Now(), time.Now(), false)
						tails = append(tails, tail)
					}
				}
			}
		}
	}

	reFailed := regexp.MustCompile(`^\d+ failed DHCP offers after the reconnects$`)
	reReject := regexp.MustCompile(`^the client is rejected on (.+) by access-list rule '.*', probably a reject loop$`)
	has := func(key string) bool { return strings.Contains(d, `"`+key+`"`) }
	covered := func(seg string) bool {
		seg = strings.TrimSuffix(strings.TrimSuffix(seg, " (made through mtmon)"), " (maintenance window)")
		switch {
		case seg == "":
			return true
		case strings.HasPrefix(seg, "changed by: "):
			return strings.Contains(d, "'changed by: '")
		case strings.HasPrefix(seg, "planned: scheduler "):
			return has("planned: scheduler {name}")
		case strings.HasPrefix(seg, "last reason: "):
			return has("last reason: {why}")
		case reFailed.MatchString(seg):
			return has("{n} failed DHCP offers after the reconnects")
		}
		if m := reReject.FindStringSubmatch(seg); m != nil {
			if m[1] == "an access point" {
				return has("the client is rejected on an access point by access-list rule '{rule}', probably a reject loop")
			}
			return has("the client is rejected on {if} by access-list rule '{rule}', probably a reject loop")
		}
		return has(seg)
	}
	seen := map[string]bool{}
	for _, tail := range tails {
		tail = strings.TrimSuffix(strings.TrimSuffix(tail, " (made through mtmon)"), " (maintenance window)")
		for _, seg := range strings.Split(tail, " – ") {
			if !seen[seg] && !covered(seg) {
				t.Errorf("tail segment %q has no German text", seg)
			}
			seen[seg] = true
		}
	}
	for _, k := range []string{keyConfigChanged1, keyConfigChangedN} {
		if !has(k) {
			t.Errorf("message key %q has no German text", k)
		}
	}
	for _, s := range []string{"source unknown", "{user} via SSH", "{user} via SSH from {ip}"} {
		if !has(s) {
			t.Errorf("change source %q has no German text", s)
		}
	}
}
