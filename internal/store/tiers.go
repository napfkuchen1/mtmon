package store

import "time"

// hybridMinAge is the window age from which per-destination lists are answered from the daily table for the full
// days plus the hourly table for the partial day at the start: scanning a week of hourly rows (one row per client,
// destination and hour) takes seconds on a busy network, the daily table has no client dimension and is far smaller.
const hybridMinAge = 25 * time.Hour

// dimSource is the row source for lists without a client dimension (hosts, services, countries, ports, ...) over a
// window of 25 h up to 8 days: hourly rows from `since` up to the first midnight (UTC), daily rows from there on
// (including today). The result is exact to the hour like the plain hourly table. ok is false for other windows.
func dimSource(since int64) (src string, args []any, ok bool) {
	if !olderThan(since, hybridMinAge) || olderThan(since, 8*24*time.Hour) {
		return "", nil, false
	}
	first := (since + 86399) / 86400 * 86400
	const cols = `rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows`
	return `(SELECT ` + cols + ` FROM rollup_1h INDEXED BY sqlite_autoindex_rollup_1h_1 WHERE ts>=? AND ts<?
		UNION ALL SELECT ` + cols + ` FROM rollup_1d WHERE ts>=?)`, []any{since / 3600 * 3600, first, first}, true
}
