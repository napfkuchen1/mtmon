package api

import (
	"net/netip"
	"regexp"
	"strings"
)

var (
	noiseSuffix = regexp.MustCompile(`(?i)[-_ ]?(?:[0-9a-f]{6,}|\d{3,})$`)
	spaces      = regexp.MustCompile(`\s+`)
)

// vendorKinds maps a vendor name fragment to a device description. Only used when there is no hostname.
var vendorKinds = []struct{ frag, kind string }{
	{"espressif", "ESP IoT device"}, {"raspberry", "Raspberry Pi"}, {"sonos", "Sonos speaker"},
	{"signify", "Philips Hue"}, {"philips", "Philips device"}, {"amazon", "Amazon device"},
	{"apple", "Apple device"}, {"samsung", "Samsung device"}, {"google", "Google device"},
	{"xiaomi", "Xiaomi device"}, {"tp-link", "TP-Link device"}, {"avm", "FRITZ! device"},
	{"ubiquiti", "Ubiquiti device"}, {"synology", "Synology NAS"}, {"qnap", "QNAP NAS"},
	{"nintendo", "Nintendo console"}, {"sony", "Sony device"}, {"lg elec", "LG device"},
	{"intel", "PC / laptop"}, {"microsoft", "Microsoft device"}, {"hewlett", "HP device"},
}

// GuessLabel proposes a human name for a client nobody named yet. conf is "medium" when it comes from a
// hostname or neighbor identity, "low" when only the vendor is known; "" label = no idea.
func GuessLabel(hostname, vendor, mac, ip string) (label, reason, conf string) {
	h := strings.TrimSpace(hostname)
	if _, err := netip.ParseAddr(h); err == nil {
		h = ""
	}
	if h != "" && !strings.EqualFold(strings.ReplaceAll(h, "-", ":"), mac) {
		if i := strings.IndexByte(h, '.'); i > 0 { // drop domain (.lan, .fritz.box …)
			h = h[:i]
		}
		if clean := strings.TrimSpace(noiseSuffix.ReplaceAllString(h, "")); len(clean) >= 3 {
			h = clean
		}
		h = spaces.ReplaceAllString(strings.NewReplacer("-", " ", "_", " ").Replace(h), " ")
		return strings.TrimSpace(h), "from hostname", "medium"
	}
	v := strings.ToLower(vendor)
	for _, k := range vendorKinds {
		if strings.Contains(v, k.frag) {
			return k.kind + " " + macSuffix(mac), "from vendor", "low"
		}
	}
	if strings.HasPrefix(vendor, "Private") {
		return "Phone/laptop " + macSuffix(mac), "randomized MAC", "low"
	}
	if vendor != "" {
		return vendor + " " + macSuffix(mac), "from vendor", "low"
	}
	return "", "", ""
}
