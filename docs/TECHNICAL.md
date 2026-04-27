# siren — Technical Design

This document describes the architecture of `siren`, a per-host monitoring process that reports service errors to a single operator over Discord DM.

## 1. Goals and constraints

**Goals**

- Detect failures of services running on the same host with low latency.
- Deliver an actionable Discord DM to the operator.
- Stay running when the monitored service does not.
- Be cheap to deploy: one binary / one process per host.

**Constraints**

- Single operator. No channels, no roles, no fan-out.
- Same-host only. Cross-host correlation is explicitly out of scope.
- Best-effort delivery. siren is not a replacement for a real on-call system.
- **Linux only** in v1.

## 1.1 Implementation choices (v1)

| Concern              | Choice                                                  |
| -------------------- | ------------------------------------------------------- |
| Language             | Go (1.23+)                                              |
| Module path          | `siren` *(placeholder — swap for repo URL when known)* |
| Discord library      | [`bwmarrin/discordgo`](https://github.com/bwmarrin/discordgo) |
| Log tailing          | [`nxadm/tail`](https://github.com/nxadm/tail) (handles rotation) |
| Config format        | YAML (`gopkg.in/yaml.v3`)                               |
| State store          | Flat JSON file under `state_dir`                        |
| Logging (siren’s own) | stdlib `log/slog`                                      |
| Distribution         | Static Linux binary + systemd unit file in repo         |
| Supervisor           | systemd (restart=on-failure)                            |

## 2. Deployment model

```
┌──────────────────────────── host ────────────────────────────┐
│                                                              │
│   ┌───────────────┐    ┌───────────────┐    ┌─────────────┐  │
│   │  src/bots     │    │  other svc A  │    │ other svc B │  │
│   └──────┬────────┘    └──────┬────────┘    └──────┬──────┘  │
│          │ logs / exit codes  │                    │         │
│          ▼                    ▼                    ▼         │
│   ┌──────────────────────────────────────────────────────┐   │
│   │                       siren                          │   │
│   │  collectors → normalizer → dedup → notifier          │   │
│   └──────────────────────────┬───────────────────────────┘   │
│                              │                               │
└──────────────────────────────┼───────────────────────────────┘
                               │ HTTPS (Discord API)
                               ▼
                       ┌───────────────┐
                       │   Operator    │
                       │   (Discord)   │
                       └───────────────┘
```

siren runs as its **own process** on each host, alongside (but independent of) the services it monitors. The same model works under systemd, Docker Compose, or bare processes — there is no requirement that siren share a pod, container, or process group with the target service.

One siren instance = one host = one operator.

## 3. Components

### 3.1 Collectors

Collectors are the inputs. Each collector watches one source and emits raw events. v1 ships four:

| Collector       | Source                                          | Emits                                |
| --------------- | ----------------------------------------------- | ------------------------------------ |
| Log tail        | Log files (auto-detect plain text vs JSON-line) | Lines matching error regex / level   |
| Journal         | systemd-journald, scoped to one unit            | Records at/below a PRIORITY threshold (optional regex on `MESSAGE`) |
| Process watcher | PID / systemd unit                              | Non-zero exit, unexpected stop       |
| Health probe    | HTTP or TCP endpoint                            | Up → down and down → up transitions  |

**Log tail** detects format per source on first read: if the first non-empty line parses as a JSON object containing a recognized level field (`level`, `lvl`, `severity`), the source is treated as JSON-line and severity is read from that field; otherwise it falls back to regex matching on plain text. On startup each tailer seeks to **EOF** — historical lines are not replayed.

**Journal** subscribes to `journalctl -u <unit> -f -o json --since now -n 0`, parses each JSON record, drops anything with `PRIORITY` numerically greater than the configured threshold (default `err`/3), optionally filters `MESSAGE` against a regex, and emits an `Event` with `source=journal`. The subprocess is restarted with bounded backoff if it exits while siren is still running; in-flight lines during a restart are dropped (see §7). Reading another unit's journal requires the siren user to be a member of `systemd-journal` — no root needed.

Collectors are pluggable. Adding a new source means implementing the collector interface and wiring it in via config.

### 3.2 Normalizer

Converts heterogeneous collector output into a single internal `Event` shape:

```
Event {
  service:   string     // logical service name
  source:    string     // collector that produced it (log|process|probe)
  severity:  enum       // info | warn | error | critical
  summary:   string     // one-line human description
  detail:    string     // multi-line context (stack trace, last N log lines)
  timestamp: time
  fingerprint: string   // stable hash for dedup
}
```

The fingerprint is derived from `(service, source, normalized summary)` so that repeated occurrences of "the same" failure collapse together.

### 3.3 Dedup / rate limiter

Prevents alert storms. Rules:

- First occurrence of a fingerprint → notify immediately.
- Subsequent occurrences within a **suppression window** (e.g. 5 min) → counted, not sent.
- When the window closes, if the count > 1, send a single follow-up summarizing the burst.
- A global token bucket caps total DMs per minute as a backstop.

State is kept in-memory; persistence is optional (see §5).

### 3.4 Notifier

The Discord client. Responsibilities:

- Maintain a Discord gateway connection (needed for inbound commands, see §3.5).
- Open and cache the DM channel with the configured operator user ID.
- Format events into Discord messages (embeds for severity colour, code blocks for `detail`).
- Apply the **redaction pipeline** (see §3.6) to `summary` and `detail` before sending.
- Retry with exponential backoff on Discord API errors.
- When Discord is unreachable, buffer outbound events in a **bounded in-memory queue (drop-oldest)**. The queue size is configurable; v1 default `256`.
- Send a one-line **startup** DM (`siren up on <host>`) and a best-effort **shutdown** DM on SIGTERM/SIGINT. No periodic heartbeat.

The notifier is the only component that talks to the network. Everything upstream is local.

### 3.5 Command handler (operator → siren)

siren listens on its gateway connection for DMs from the configured operator user ID. Messages from any other user are ignored. Commands are plain-text, prefix-based:

| Command                        | Effect                                                                 |
| ------------------------------ | ---------------------------------------------------------------------- |
| `!ack <fingerprint-or-id>`     | Mark an active alert acknowledged; suppresses follow-up rollups for it. |
| `!mute <service> <duration>`   | Drop all events for `service` for the given duration (e.g. `1h`, `30m`). |
| `!unmute <service>`            | Remove an active mute.                                                 |
| `!status`                      | Reply with current services, mutes, queue depth, and uptime.           |
| `!help`                        | List available commands.                                               |

Mute state is held in memory and persisted alongside dedup state (§5) so a restart does not silently un-mute.

### 3.6 Redaction

Before any string leaves siren toward Discord, it passes through an ordered list of regex redaction rules from config. Each rule replaces matches with a fixed token (default `[REDACTED]`). Rules apply to `summary` and `detail` only, never to siren’s own structured fields (service name, fingerprint, timestamp).

Redaction is best-effort and intended to catch obvious secrets (tokens, keys); it is not a substitute for not logging secrets in the first place.

## 4. Data flow

1. A monitored service writes an error to its log file (or exits non-zero, or fails a health probe).
2. The matching **collector** picks up the raw signal.
3. The **normalizer** turns it into an `Event` with a stable fingerprint.
4. The **dedup** stage decides whether this event should notify now, be suppressed, or trigger a rollup.
5. The **notifier** sends a DM to the operator and records the send.

Each stage is a simple function over the previous stage's output, connected by an in-process channel/queue. There is no broker.

## 5. State

siren is mostly stateless. The state it keeps is:

- The dedup table (fingerprint → last-seen, count, suppression deadline).
- Active mutes (service → expiry).
- Acknowledged-fingerprint set with TTL.

Log tailers do **not** persist file offsets; on restart they seek to EOF (§3.1), so any errors emitted while siren is down are not replayed.

State lives in a single JSON file (`<state_dir>/siren.state.json`) written atomically (temp + rename) on change, debounced. Losing the file is non-fatal: dedup resets and any active mutes are lost.

## 6. Configuration

A single YAML config file per host. Sketch:

```yaml
operator:
  discord_user_id: "123456789012345678"

discord:
  token_env: SIREN_DISCORD_TOKEN
  outbound_queue_size: 256   # drop-oldest when full

services:
  - name: bots
    log_paths:
      - /var/log/bots/*.log
    log_match:
      # Used when the source is plain text. Ignored for JSON-line logs.
      regex: '(?i)\b(error|panic|fatal)\b'
    log_levels: [error, fatal]   # Used when the source is JSON-line.
    process:
      systemd_unit: bots.service
    health:
      url: http://127.0.0.1:8080/healthz
      interval: 15s
    journal:
      unit: bots.service          # _SYSTEMD_UNIT match
      priority: err               # max PRIORITY kept; default err (3)
      regex: '(?i)\b(error|panic|fatal)\b'   # optional MESSAGE filter

dedup:
  suppression_window: 5m
  max_dm_per_minute: 10

redact:
  - pattern: '(?i)bearer\s+[A-Za-z0-9._-]+'
  - pattern: '[A-Za-z0-9]{32,}'   # long opaque tokens
    replacement: '[TOKEN]'

state_dir: /var/lib/siren
```

Secrets (the Discord bot token) come from the environment variable named in `discord.token_env`, never the config file. Config is loaded at startup; reload requires a restart in v1.

## 7. Failure modes

| Failure                           | siren behaviour                                                    |
| --------------------------------- | ------------------------------------------------------------------ |
| Monitored service crashes         | Detected by process watcher → DM sent.                             |
| Log file rotated                  | Tailer reopens by inode/path; offset reset for the new file.       |
| Discord API down                  | Notifier retries with backoff; events queued up to a bounded size. |
| Operator's DMs closed             | Notifier logs the error locally; nothing else can be done.         |
| siren itself crashes              | Supervisor (systemd / container runtime) restarts it.              |
| Host is down                      | siren cannot report. This is an accepted limitation of same-host monitoring. |

Note the last row: same-host monitoring cannot report that the host is gone. If that matters, pair siren with an external liveness check (out of scope here).

## 8. Security

- Bot token is read from an environment variable, never logged.
- siren only **reads** from the local filesystem and **writes** to Discord; it does not expose any network listener.
- The operator's Discord user ID is treated as configuration, not a secret.
- Log content forwarded in DMs may contain sensitive data. Operators should be aware that error details are sent to Discord's servers.

## 9. Extensibility

The collector interface is the main extension point. New sources (e.g. Docker events, journald, a custom IPC socket) can be added without touching the dedup or notifier stages.

The notifier is intentionally Discord-specific. A different transport would be a different tool, not a siren plugin — keeping the surface area small is a goal.

## 10. Repository layout (planned)

```
.
├─ cmd/siren/                # main entrypoint
├─ internal/
│  ├─ config/                # YAML loader + validation
│  ├─ event/                 # Event type, fingerprinting
│  ├─ collector/             # log, process, probe collectors
│  ├─ dedup/                 # suppression + rate limiting
│  ├─ redact/                # regex redaction pipeline
│  ├─ notifier/              # discordgo client, queue, formatting
│  ├─ command/               # operator command parser/handler
│  └─ state/                 # JSON state file load/save
├─ deploy/
│  └─ systemd/siren.service
├─ docs/
├─ .github/workflows/         # CI: build, test, lint
├─ Makefile
├─ go.mod
└─ README.md
```
