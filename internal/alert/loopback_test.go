// SPDX-License-Identifier: AGPL-3.0-only

package alert

// The tests serve their targets from httptest on loopback.
func init() { allowLoopback = true }
