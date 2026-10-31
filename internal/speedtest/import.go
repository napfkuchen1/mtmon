// SPDX-License-Identifier: AGPL-3.0-only

package speedtest

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// ParseMySpeed reads an export of MySpeed (gnmyt/MySpeed, `GET /speedtests/export`, CSV or JSON): columns
// id, ping, jitter, download, upload, time, type, created, error. download/upload are Mbit/s, ping and jitter ms.
// Rows without a usable timestamp are counted as skipped. Rows that carry an error text become failed results.
func ParseMySpeed(data []byte) (rows []store.Speedtest, skipped int, err error) {
	data = bytes.TrimPrefix(bytes.TrimSpace(data), []byte("\xef\xbb\xbf"))
	if len(data) == 0 {
		return nil, 0, errors.New("empty file")
	}
	var recs []map[string]string
	if data[0] == '[' || data[0] == '{' {
		recs, err = jsonRecords(data)
	} else {
		recs, err = csvRecords(data)
	}
	if err != nil {
		return nil, 0, err
	}
	for _, r := range recs {
		ts := parseTime(first(r, "created", "timestamp", "date", "time_created"))
		if ts == 0 {
			skipped++
			continue
		}
		t := store.Speedtest{TS: ts, Provider: strings.ToLower(first(r, "type", "provider")), Server: "myspeed",
			DownBps: mbit(first(r, "download", "down")), UpBps: mbit(first(r, "upload", "up")),
			PingMS: num(first(r, "ping", "latency")), JitterMS: num(first(r, "jitter")), DurS: num(first(r, "time", "duration")),
			Err: shortErr(first(r, "error"))}
		if t.Provider == "" {
			t.Provider = "myspeed"
		}
		if t.Err != "" {
			t.DownBps, t.UpBps = 0, 0
		} else if t.DownBps <= 0 {
			skipped++ // neither a result nor an error: nothing to keep
			continue
		}
		rows = append(rows, t)
	}
	if len(rows) == 0 && skipped == 0 {
		return nil, 0, errors.New("no speed tests found in the file")
	}
	return rows, skipped, nil
}

func first(r map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(r[k]); v != "" && v != "null" {
			return v
		}
	}
	return ""
}

func num(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0
	}
	return f
}

func mbit(s string) float64 { return num(s) * 1e6 }

var timeLayouts = []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.000 -07:00", "2006-01-02 15:04:05 -07:00", "2006-01-02 15:04:05", "2006-01-02T15:04:05"}

// parseTime accepts ISO timestamps and unix seconds or milliseconds.
func parseTime(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1e12 {
			n /= 1000
		}
		if n < 1e9 {
			return 0
		}
		return n
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// shortErr reduces an error column to one readable line: MySpeed stores raw Ookla JSON log lines there, from
// which only the message is kept.
func shortErr(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		var o struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if json.Unmarshal([]byte(s), &o) == nil {
			if o.Message != "" {
				s = o.Message
			} else if o.Error != "" {
				s = o.Error
			}
		}
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func csvRecords(data []byte) ([]map[string]string, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	head, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("not a CSV file: %w", err)
	}
	for i := range head {
		head[i] = strings.ToLower(strings.TrimSpace(head[i]))
	}
	var out []map[string]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("bad CSV row: %w", err)
		}
		m := map[string]string{}
		for i, v := range rec {
			if i < len(head) {
				m[head[i]] = v
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func jsonRecords(data []byte) ([]map[string]string, error) {
	var arr []map[string]any
	if data[0] == '{' { // {"speedtests":[…]} or similar wrapper
		var w map[string]json.RawMessage
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("not a JSON file: %w", err)
		}
		found := false
		for _, v := range w {
			if json.Unmarshal(v, &arr) == nil && len(arr) > 0 {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("no list of speed tests in the JSON file")
		}
	} else if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("not a JSON file: %w", err)
	}
	var out []map[string]string
	for _, o := range arr {
		m := map[string]string{}
		for k, v := range o {
			switch t := v.(type) {
			case string:
				m[strings.ToLower(k)] = t
			case float64:
				m[strings.ToLower(k)] = strconv.FormatFloat(t, 'f', -1, 64)
			}
		}
		out = append(out, m)
	}
	return out, nil
}
