package syslog

import (
	"regexp"
	"strings"
)

// LogEvent is a non-firewall RouterOS log line: system, account, critical, dhcp, wireless/wifi, wireguard, script, …
type LogEvent struct {
	TS     int64
	Device string
	Topics string // as sent, e.g. "system,info,account"
	Level  string // debug | info | warning | error | critical ("" when the topics name none)
	Msg    string
}

var (
	reBSDHeader = regexp.MustCompile(`^[A-Z][a-z]{2}\s+\d+\s+\d\d:\d\d:\d\d\s+\S+\s+`)
	rePRI       = regexp.MustCompile(`^<\d+>`)
	reTopics    = regexp.MustCompile(`^([a-z][a-z0-9-]*(?:,[a-z][a-z0-9-]*)*)\s+(.+)$`)
)

var levelRank = map[string]int{"debug": 1, "info": 2, "warning": 3, "error": 4, "critical": 5}

// singleTopics are the topic words accepted on their own (a line like "critical router rebooted"); anything with a
// comma is a topic list.
var singleTopics = map[string]bool{"system": true, "account": true, "critical": true, "dhcp": true, "wireless": true,
	"wifi": true, "wireguard": true, "script": true, "error": true, "warning": true, "info": true, "interface": true, "caps": true}

// ParseLog reads a RouterOS syslog payload that is not a firewall line: optional <PRI> and BSD header, then the
// topic list, then the message. ok is false for text that does not start with a topic list.
func ParseLog(msg string) (LogEvent, bool) {
	msg = strings.TrimSpace(msg)
	msg = rePRI.ReplaceAllString(msg, "")
	msg = strings.TrimSpace(reBSDHeader.ReplaceAllString(msg, ""))
	m := reTopics.FindStringSubmatch(msg)
	if m == nil {
		return LogEvent{}, false
	}
	topics := strings.Split(m[1], ",")
	if len(topics) == 1 && !singleTopics[topics[0]] {
		return LogEvent{}, false
	}
	for _, t := range topics {
		if t == "firewall" {
			return LogEvent{}, false // handled (or reported as unreadable) by the firewall parser
		}
	}
	e := LogEvent{Topics: m[1], Msg: strings.TrimSpace(m[2])}
	best := 0
	for _, t := range topics {
		if r := levelRank[t]; r > best {
			best, e.Level = r, t
		}
	}
	return e, true
}

// HasTopic reports whether the topic list contains t.
func (e LogEvent) HasTopic(t string) bool {
	for _, x := range strings.Split(e.Topics, ",") {
		if x == t {
			return true
		}
	}
	return false
}
