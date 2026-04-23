# siren

`siren` is a lightweight monitoring process that runs on the same machine as the services it watches (such as `src/bots`) and notifies a single operator over Discord via direct message.

It is **not** a multi-tenant alerting platform. Communication is intentionally **1-on-1**: one siren instance, one operator, one DM channel.

## What it does

- Tails logs and probes the health of co-located services.
- Detects errors, crashes, restarts, and unhealthy states.
- Sends a Discord DM to the configured operator with enough context to act.
- De-duplicates noisy alerts so a crash loop does not flood the DM.

## Why same-host

Running siren as a separate process on the same host as the services it monitors means:

- No network plumbing or auth between monitor and target.
- Direct access to log files, exit codes, and process state.
- Failure of a monitored service does not take siren down with it.
- Each host gets its own siren — alerts are naturally scoped to that machine.

## Quick start

1. Configure a Discord bot token and the operator's Discord user ID.
2. Point siren at the services / log paths to monitor.
3. Run siren as a separate process (systemd unit, container, bare process, etc.) on the same machine as the target services.

Configuration details and deployment patterns live in [docs/TECHNICAL.md](docs/TECHNICAL.md).

## Scope

In scope:

- Detecting failures on the local machine.
- Notifying a single operator via Discord DM.
- Basic alert grouping and rate limiting.

Out of scope:

- Multi-user notifications, channels, or roles.
- Metrics dashboards, long-term storage, or analytics.
- Cross-host aggregation (run one siren per host instead).
- Remediation or auto-restart of failed services.

## Related

- `src/bots` — the primary service siren is typically deployed alongside.

## Documentation

- [docs/TECHNICAL.md](docs/TECHNICAL.md) — architecture, components, and data flow.
