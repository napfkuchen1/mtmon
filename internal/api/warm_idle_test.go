// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"testing"
	"time"
)

// The warmer runs only while the UI is in use: no interactive request yet means idle, a request makes it busy,
// and the idle window (warmIdle) ends it again.
func TestWarmIdleGate(t *testing.T) {
	_, s := newTestServer(t)
	if !s.uiIdle() {
		t.Fatal("fresh server must be idle (no UI request yet)")
	}
	s.touchUI()
	if s.uiIdle() {
		t.Fatal("after an interactive request the server must be busy")
	}
	// age the last request past the idle window
	s.uiLast.Store(int64(time.Since(warmBase)-warmIdle-time.Minute) + 1)
	if !s.uiIdle() {
		t.Fatal("a request older than warmIdle must count as idle")
	}
}
