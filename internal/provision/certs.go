package provision

import (
	"sort"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

// parseCerts picks, for every service that names a certificate, that certificate's expiry from the /certificate
// listing (field "invalid-after"). A certificate that is not in the listing is skipped (nothing is guessed).
func parseCerts(svcCert map[string]string, rows []poller.Row) []Cert {
	by := map[string]poller.Row{}
	for _, r := range rows {
		by[r["name"]] = r
	}
	out := []Cert{}
	for svc, name := range svcCert {
		if r, ok := by[name]; ok {
			out = append(out, Cert{Name: name, Service: svc, Expires: parseFileTime(r["invalid-after"])})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}
