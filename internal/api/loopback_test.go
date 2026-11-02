// SPDX-License-Identifier: AGPL-3.0-only

package api

import "github.com/napfkuchen1/mtmon/internal/alert"

// The tests serve their webhook from httptest on loopback.
func init() { alert.AllowLoopbackForTests() }
