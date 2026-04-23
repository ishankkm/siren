# siren — Deployment

This guide covers installing and running `siren` on a Linux host alongside the
services it monitors. Architecture and design rationale live in
[TECHNICAL.md](TECHNICAL.md); this document is operational only.

## 1. Prerequisites

- Linux host (x86_64) with `systemd` (the `proc` collector uses `systemctl`).
- A Discord bot application:
  - Create one at <https://discord.com/developers/applications> → **New Application** → **Bot**.
  - Copy the bot **token**. This is the only secret siren needs.
  - Under **Privileged Gateway Intents**, enable **Message Content Intent**
    (required for inbound `!` commands in DMs).
- The operator's Discord **user ID** (enable Developer Mode in Discord, then
  right-click your user → **Copy User ID**).
- The bot must be able to DM the operator. The simplest way: invite the bot
  to any server you and the bot share, or share at least one mutual server.
  No special permissions are required beyond DM.

## 2. Build

From a checkout on a Linux build host (or cross-compile from anywhere):

```sh
make build-linux        # produces ./bin/siren (static, CGO disabled)
```

Or directly:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags '-s -w' -o bin/siren ./cmd/siren
```

The resulting binary is fully static and has no runtime dependencies.

## 3. Install on the host

The systemd unit shipped in [deploy/systemd/siren.service](../deploy/systemd/siren.service)
assumes the layout below. Adjust paths if you need to.

```sh
# 1. dedicated unprivileged user
sudo useradd --system --home /var/lib/siren --shell /usr/sbin/nologin siren

# 2. binary
sudo install -m 0755 bin/siren /usr/local/bin/siren

# 3. config + secrets
sudo install -d -m 0755 -o root -g root /etc/siren
sudo install -m 0640 -o root -g siren siren.example.yaml /etc/siren/siren.yaml
sudo install -m 0600 -o root -g siren deploy/systemd/siren.env.example /etc/siren/siren.env
sudoedit /etc/siren/siren.yaml   # set discord_user_id, services
sudoedit /etc/siren/siren.env    # set SIREN_DISCORD_TOKEN=...

# 4. state directory (matches state_dir in config)
sudo install -d -m 0750 -o siren -g siren /var/lib/siren

# 5. systemd unit
sudo install -m 0644 deploy/systemd/siren.service /etc/systemd/system/siren.service
sudo systemctl daemon-reload
```

The user `siren` needs **read** access to the log files of every service it
monitors. The simplest pattern is to add `siren` to the group that owns those
logs (e.g. `sudo usermod -aG bots siren`).

## 4. Configure

`/etc/siren/siren.yaml` follows [siren.example.yaml](../siren.example.yaml).
The required fields are:

| Field                       | Purpose                                                  |
| --------------------------- | -------------------------------------------------------- |
| `operator.discord_user_id`  | The single user who receives DMs and can issue commands. |
| `discord.token_env`         | Name of the env var holding the bot token (default `SIREN_DISCORD_TOKEN`). |
| `services[]`                | At least one service with at least one source.           |

Each service must declare at least one of: `log_paths`, `process.systemd_unit`,
`process.pid_file`, or `health.url`.

`/etc/siren/siren.env` should contain only the secret(s):

```
SIREN_DISCORD_TOKEN=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

Permissions: `0600`, owned by `root:siren`. The unit file loads it via
`EnvironmentFile=`.

## 5. Validate before enabling

Run siren in the foreground as the service user to catch config errors:

```sh
sudo -u siren env $(grep -v '^#' /etc/siren/siren.env | xargs) \
  /usr/local/bin/siren -config /etc/siren/siren.yaml
```

You should see a structured-log line like `siren starting` and immediately
receive a Discord DM saying `siren up`. DM the bot `!help` and `!status` to
verify the inbound path. `Ctrl-C` to stop; you should receive a
`siren shutting down` DM (best-effort).

## 6. Enable and start

```sh
sudo systemctl enable --now siren
sudo systemctl status siren
journalctl -u siren -f
```

`Restart=on-failure` is set in the unit, so siren will be restarted by systemd
if it crashes. The startup DM will also be re-sent on each restart.

## 7. Day-2 operations

### Updating siren

```sh
sudo systemctl stop siren
sudo install -m 0755 bin/siren /usr/local/bin/siren   # new build
sudo systemctl start siren
```

State (mutes, acks) survives restarts via `/var/lib/siren/siren.state.json`.

### Updating config

Config is **read at startup only** in v1 — there is no SIGHUP reload.

```sh
sudoedit /etc/siren/siren.yaml
sudo systemctl restart siren
```

### Operator commands (DM the bot)

| Command                       | Effect                                                       |
| ----------------------------- | ------------------------------------------------------------ |
| `!help`                       | Show available commands.                                     |
| `!status`                     | Uptime, queue depth, configured services, active mutes.      |
| `!ack <fingerprint>`          | Clear suppression for a fingerprint so a recurrence alerts.  |
| `!mute <service> <duration>`  | Drop all events for a service for a duration (e.g. `1h`).    |
| `!unmute <service>`           | Remove a mute.                                               |

The `<fingerprint>` value is shown in the footer of every alert embed.

### Log rotation

The `logc` collector uses `nxadm/tail` with `ReOpen=true`, so standard
rotation by `logrotate` (`copytruncate` or `create`) is handled automatically.
Files matching the configured glob that appear **after** siren starts will
not be picked up until the next restart.

## 8. Hardening notes

The shipped unit already enables:

- `NoNewPrivileges=true`
- `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`
- `ProtectKernelTunables=true`, `ProtectKernelModules=true`, `ProtectControlGroups=true`
- `RestrictSUIDSGID=true`, `LockPersonality=true`
- `ReadWritePaths=/var/lib/siren` (the only writable path)

Additional considerations:

- siren forwards log content to Discord. Ensure your `redact` rules cover any
  obvious secrets (tokens, API keys) that might appear in monitored logs.
- The bot token is loaded only from the env var named in
  `discord.token_env` and is never written to disk by siren or logged.
- siren does not open any listening socket. All network traffic is outbound
  HTTPS to Discord.

## 9. Troubleshooting

**Bot never DMs.** Verify the operator and bot share at least one server, the
bot's **Message Content Intent** is enabled in the developer portal, and the
operator's user ID in config matches their actual Discord ID.

**`unit not found` warnings from `proc` collector.** The configured
`systemd_unit` doesn't exist on this host, or `systemctl` isn't on `PATH` for
user `siren`. Remove the `process` block from that service or fix the unit name.

**Lots of "no files matched glob" warnings.** The log path glob doesn't
expand to anything at startup. Confirm the path on disk and that user `siren`
can read it (`sudo -u siren ls /var/log/yourapp/`).

**Suppressed-alert noise (events arriving but no DMs).** Check
`!status` for queue depth and active mutes; check `journalctl -u siren` for
`suppressed` (within the dedup window) or `rate-limited` (token bucket
exhausted) lines.

**Discord outage.** Events accumulate in the bounded in-memory queue
(`discord.outbound_queue_size`, default 256). When full, the oldest event is
dropped; you'll see `outbound queue full; dropped oldest event` in the log.

## 10. Uninstall

```sh
sudo systemctl disable --now siren
sudo rm /etc/systemd/system/siren.service
sudo systemctl daemon-reload
sudo rm -rf /usr/local/bin/siren /etc/siren /var/lib/siren
sudo userdel siren
```
