// SPDX-License-Identifier: AGPL-3.0-only

package alert

import "testing"

func TestSignWebhook(t *testing.T) {
	if SignWebhook("", []byte("x")) != "" {
		t.Error("no secret must give no signature")
	}
	// HMAC-SHA256("key", "The quick brown fox jumps over the lazy dog")
	got := SignWebhook("key", []byte("The quick brown fox jumps over the lazy dog"))
	want := "sha256=f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}
