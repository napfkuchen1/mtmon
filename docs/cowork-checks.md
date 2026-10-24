# What Cowork may check on the live instance

Cowork (the review session on the maintainer's computer) can **read** the real mtmon instance with an API token. It cannot change anything. This page lists what it is asked to verify, so developers in cloud sessions know which issues wait for it.

## Rules
- Access: `MTMON_URL` and `MTMON_TOKEN` (own named token, Settings → API tokens). Only `GET`; a `POST`/`PUT`/`DELETE` returns 401. No access to `/api/tokens` or `/api/access`.
- Cowork never writes to a router and never opens the setup wizard. Router changes happen only through the UI with the maintainer's admin login.
- Results go into the issue as a comment: pass/fail and the field names or counts that matter. **No MACs, IPs, host names or secrets** in issues, commits or PRs.
- A check that needs a change on the router (for example granting `read` for `/file`) is reported as a question to the maintainer, not done.
- When a check is done, remove the label `cowork` from the issue (or write what is still open).

## Checks after the release v0.17.0
| Issue | Call | Expected |
|---|---|---|
| #31 wired clients behind an unmanaged switch | `GET /api/clients?range=1h`, `GET /api/diagnostics` | `via` and device of those clients are the switch uplink side, not missing |
| #29 VPN traffic | `GET /api/clients/{mac}` of a VPN client | traffic and connections are filled, also for IPv6 |
| #35 speed | `GET /api/overview?range=24h`, `?range=30d`, `GET /api/firewall/summary?range=7d` | each answer in well under a second on the real database; report the times |
| #32 WAN list | `GET /api/diagnostics` → `wan_interfaces` | contains the real WAN port(s) and no LAN port |
| #38 / event log | `GET /api/devices/{name}/log` | lines arrive after the maintainer switched on *Router event log*; note which topics appear |
| #47 internet quality | `GET /api/internet` | non-zero samples, loss/latency plausible; if loss is 100 %, outbound TCP from the LXC is blocked |
| #48 update, certificate | `GET /api/devices/{name}` → `caps` | update and certificate fields filled; compare with `/system package update` and `/certificate print` shown by the maintainer |
| #49 backup age | `GET /api/devices/{name}` → `caps` | `files_readable`, `newest_backup`, `newest_export` plausible; if not readable, the rule stays silent (correct) |
| #50 Wi-Fi history | `GET /api/devices/{name}/wifi?range=24h`, `GET /api/clients/{mac}` | band series per AP, signal points per client |
| #51 groups | `GET /api/groups` | groups exist as created; suggestions only for ungrouped clients |
| #52 learning | `GET /api/learning`, `GET /api/system` → `learning_until` | empty after an update; set after a reset |

## Not Cowork's job (needs a person at the router)
- #46 `/export` through REST with the monitoring user: the maintainer runs `/rest/export` once with the `mtmon` user and reports the result.
- Anything that needs `help.mikrotik.com`: the cloud session has no access, so Cowork may look up the syntax and write it into the issue.
