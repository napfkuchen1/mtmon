// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

func TestParseCerts(t *testing.T) {
	got := parseCerts(map[string]string{"www-ssl": "mtmon-ssl", "api-ssl": "gone"}, []poller.Row{
		{"name": "mtmon-ssl", "invalid-after": "2026-11-05 10:00:00"}, {"name": "other", "invalid-after": "2030-01-01 00:00:00"}})
	if len(got) != 1 || got[0].Service != "www-ssl" || got[0].Expires != time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("%+v", got)
	}
}
