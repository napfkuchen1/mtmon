// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"regexp"
	"sort"
	"strings"
)

// boolDefaults lists the boolean properties mtmon writes, with the RouterOS default that stands in for an empty or
// unknown original value. RouterOS reports booleans as true/false over REST but only accepts yes/no in the terminal,
// and rejects an empty value everywhere (#343).
var boolDefaults = map[string]string{"log": "no", "disabled": "no", "enabled": "no", "passthrough": "yes"}

// rosValue is the one serializer for values sent to RouterOS: apply, rollback and the manual script all use it.
// Non-boolean properties pass unchanged (an empty log-prefix is valid).
func rosValue(key, v string) string {
	def, isBool := boolDefaults[key]
	if !isBool {
		return v
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "true":
		return "yes"
	case "no", "false":
		return "no"
	}
	return def
}

// rosBody applies rosValue to a whole request body. It also repairs undo bodies stored by older versions.
func rosBody(body map[string]string) map[string]string {
	if body == nil {
		return nil
	}
	out := make(map[string]string, len(body))
	for k, v := range body {
		out[k] = rosValue(k, v)
	}
	return out
}

var cliBare = regexp.MustCompile(`^[A-Za-z0-9_.:/,+*-]+$`)

// cliArg renders key=value for the RouterOS terminal.
func cliArg(key, v string) string {
	v = rosValue(key, v)
	if cliBare.MatchString(v) {
		return key + "=" + v
	}
	return key + `="` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(v) + `"`
}

// cliSet renders the arguments of a set command, .id first and the rest sorted.
func cliSet(body map[string]string) string {
	keys := make([]string, 0, len(body))
	for k := range body {
		if k != ".id" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys)+1)
	if id := body[".id"]; id != "" {
		args = append(args, id)
	}
	for _, k := range keys {
		args = append(args, cliArg(k, body[k]))
	}
	return strings.Join(args, " ")
}
