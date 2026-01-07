# Dockhand

Dockhand is a small, lightweight updater daemon for local Docker containers. It periodically polls configured registries/images and safely performs updates for running containers (pull → recreate → healthcheck → rollback on failure). Think of it as a modern, minimal alternative to Watchtower/Ouroboros.

Getting started (developer)

Prerequisites
- Go 1.23+
- Docker (to test end-to-end)

Build

```bash
make build
```

Run

```bash
# run locally (daemon mode)
./bin/dockhand --poll-interval=10m

# run once (useful for cron jobs)
./bin/dockhand --run-once --poll-interval=10m

# dry-run (detect updates, send notifications, but do not apply)
./bin/dockhand --dry-run --poll-interval=10m

# build container
make docker-build
```

Configuration

Dockhand supports a YAML configuration file. Example `dockhand.yaml` (expanded):

```yaml
poll_interval: 10m
patch_window: ""
manage_latest_only: true
opt_out: true
notification_level: "all"
verify_timeout: 10s
verify_interval: 500ms
hook_timeout: 5m
circuit_breaker_threshold: 3
circuit_breaker_cooldown: 10m
metrics_enabled: false
metrics_port: 9090
dry_run: false
# Notifiers:
discord_webhook: ""
slack_webhook: ""
teams_webhook: ""
generic_webhook_url: ""
gotify_url: ""
gotify_token: ""
pushover_user: ""
pushover_token: ""
apprise_url: ""
telegram_token: ""
telegram_chat_id: ""
mastodon_server: ""
mastodon_token: ""
email_host: ""
email_port: 587
email_user: ""
email_pass: ""
email_to: []
```

- `opt_out: true` (default) means Dockhand will manage containers unless they have label `dockhand.disable=true`.
- Set `opt_out: false` to switch to opt-in mode (only manage containers with `dockhand.enable=true`).

Environment variables

You can also configure Dockhand using environment variables (handy for Docker Compose). Variables override values from a config file, and CLI flags override environment variables.

- `DOCKHAND_POLL_INTERVAL` (duration, e.g. `10m`)
- `DOCKHAND_PATCH_WINDOW` (string, e.g. `00:00-02:00`)
- `DOCKHAND_MANAGE_LATEST_ONLY` (bool, `true`/`false`)
- `DOCKHAND_OPT_OUT` (bool, `true`/`false`)
- `DOCKHAND_DRY_RUN` (bool, `true`/`false`) — detect updates but do not apply changes
- `DOCKHAND_NOTIFICATION_LEVEL` (string, `all`/`failure`/`none`) — controls which notifications are sent
- `DOCKHAND_LOG_LEVEL` (string, e.g. `debug`, `info`) — sets logger verbosity
- `DOCKHAND_LOG_FILE` (string) — optional file path for logs
- `DOCKHAND_SANITIZE_NAMES` (bool, `true`/`false`, default: `true`) — when true, Docker container names are normalized (lowercased and stripped of disallowed characters)
- `DOCKHAND_METRICS_ENABLED` (bool, `true`/`false`, default: `false`) — enable built-in metrics server (Prometheus + JSON status)
- `DOCKHAND_METRICS_PORT` (int, e.g. `9090`) — port for metrics server
- `DOCKHAND_INFLUX_URL` (string) — optional InfluxDB v2 endpoint for pushing historical metrics
- `DOCKHAND_INFLUX_BUCKET`, `DOCKHAND_INFLUX_ORG`, `DOCKHAND_INFLUX_TOKEN` — Influx configuration (used if `DOCKHAND_INFLUX_URL` is set)
- `DOCKHAND_STATE_DIR` (string) — optional directory to store Dockhand state files (used to record rename operations for safe recovery); **default:** `/var/lib/dockhand` on Unix-like systems (falls back to current working directory if `/var/lib/dockhand` is not writable). When running Dockhand in a container, map this directory to a persistent volume (e.g., `- DOCKHAND_STATE_DIR=/var/lib/dockhand` with a `volumes:` entry in docker-compose).
- `DOCKHAND_INFLUX_INTERVAL` (duration, e.g. `1m`)
- `DOCKHAND_HOOK_TIMEOUT` (duration, e.g. `30s` or `5m`) — timeout for any container-level `dockhand.pre-update` hook; default: `5m`

- `DOCKHAND_HOST_SOCKET_PATH` (string) — optional path to the Docker socket on the *host* machine. Use this when the host socket is located in a non-standard path (e.g., rootless Docker or NAS setups). Default: `/var/run/docker.sock`. When set, ensure you mount that host path into the container at `/var/run/docker.sock` (e.g., `- /volume1/custom/docker.sock:/var/run/docker.sock`).
- `DOCKHAND_CIRCUIT_BREAKER_THRESHOLD` (int) — number of repeated pull failures before suppressing further failure notifications for the same image; default: `3`
- `DOCKHAND_CIRCUIT_BREAKER_COOLDOWN` (duration, e.g. `10m`) — duration to suppress repeated pull failure notifications after the threshold is hit; default: `10m`
- `DOCKHAND_STATE_DIR` (string) — optional directory to store Dockhand state files (used to record rename operations for safe recovery); default: OS temp directory

Notifier-related env vars (examples):
- `DOCKHAND_DISCORD_WEBHOOK` — Discord webhook URL
- `DOCKHAND_SLACK_WEBHOOK` — Slack webhook URL
- `DOCKHAND_TEAMS_WEBHOOK` — Microsoft Teams webhook URL
- `DOCKHAND_GENERIC_WEBHOOK_URL` — Generic webhook URL (receives `{title,message,agent}`)
- `DOCKHAND_GOTIFY_URL`, `DOCKHAND_GOTIFY_TOKEN` — Gotify server and token
- `DOCKHAND_PUSHOVER_USER`, `DOCKHAND_PUSHOVER_TOKEN` — Pushover creds
- `DOCKHAND_APPRISE_URL` — Apprise gateway URL (optional)
- `DOCKHAND_TELEGRAM_TOKEN`, `DOCKHAND_TELEGRAM_CHAT_ID` — Telegram bot token & chat id
- `DOCKHAND_MASTODON_SERVER`, `DOCKHAND_MASTODON_TOKEN` — Mastodon server and access token
- `DOCKHAND_EMAIL_HOST`, `DOCKHAND_EMAIL_PORT`, `DOCKHAND_EMAIL_USER`, `DOCKHAND_EMAIL_PASS`, `DOCKHAND_EMAIL_TO` — SMTP settings (comma-separated `DOCKHAND_EMAIL_TO`)

Notification services supported (via config/env): Discord, Slack, Teams, Telegram, Mastodon, Email (SMTP), Generic Webhook, Gotify, Pushover, Apprise. Configure them via the `Config` fields or environment variables (e.g. `DOCKHAND_DISCORD_WEBHOOK`, `DOCKHAND_GOTIFY_URL`, `DOCKHAND_PUSHOVER_TOKEN`, `DOCKHAND_GENERIC_WEBHOOK_URL`, `DOCKHAND_APPRISE_URL`).

Behavior details

- Patch window: By default, the patch window is empty, meaning Dockhand will apply updates immediately (24/7) whenever they are detected. To restrict updates to a safe time (e.g., midnight), set `patch_window: "00:00-02:00"`.
- Safe recovery: Dockhand will attempt to recover containers left in an `-old-` renamed state only if it previously recorded the rename operation (to avoid interfering with externally renamed containers). Rename records are stored in a small state file (by default under the OS temporary directory). You can set `DOCKHAND_STATE_DIR` to control where the state file is stored.
- Notifications: when a container update succeeds or fails, Dockhand will send notifier messages to configured services (Discord/Slack/Teams/Generic webhook, etc.). Notifications are sent asynchronously so slow providers won't block updates; on shutdown Dockhand waits up to 5s for pending notifications to complete. For Generic webhooks the payload is a simple title/message/agent JSON object. Sample Generic payload:

```json
{
  "title": "Container updated: c1",
  "message": "image=sha256:new old_image=sha256:old",
  "agent": "Dockhand"
}

```
- Metrics / Observability: Dockhand exposes three complementary outputs:
  - Prometheus (/metrics): standard Prometheus metrics endpoint for scraping.
  - JSON API (/status): a convenience endpoint returning a JSON snapshot of counters for systems like Zabbix or Uptime Kuma.
  - InfluxDB (push): optional push-based reporting to InfluxDB v2 when `DOCKHAND_INFLUX_URL` and related settings are configured.

Lifecycle hooks
- `dockhand.pre-update`: If set on a container, the given command (string) will be executed inside the running container before attempting an update. If the command fails, the update is aborted. Note: the command is run via `sh -c` inside the container, so the target image must include a POSIX shell (e.g., `sh`), otherwise the hook will fail. For scratch/distroless images, prefer host-side hooks or ensure a shell is present in the image.
- `dockhand.post-update`: If set, the command will be executed inside the new container after it starts; if it fails, Dockhand will roll back to the previous container.

Registry authentication
- For private registries, provide `DOCKHAND_REGISTRY_USER` and `DOCKHAND_REGISTRY_PASS` environment variables to enable basic pull authentication, or mount `/root/.docker/config.json` for full docker credential support.

CLI flags
- `--config` (string): path to a YAML/JSON configuration file (overrides defaults).
- `--poll-interval` (duration): interval between update checks (e.g. `10m`, `1h`). Default: 5 minutes.
- `--run-once`: run a single reconciliation pass and exit (useful in cron jobs).
- `--dry-run`: detect available updates and send notifications, but do not apply changes (safe testing mode).

- Image cleanup: after a successful update Dockhand will attempt to remove the old image (best-effort) to reclaim disk space; failures are logged but won't stop progress.

Notes & edge cases

Running Dockhand as non-root with Docker socket access
- For security, the Docker socket should not be mounted into a container running as root unless you trust the image. The recommended approach is:
  1. Create a `docker` group on the host and add an unprivileged user to that group.
  2. Run the container with the same `gid` or use `--group-add` so the process can access `/var/run/docker.sock` without being root.

  Example (create group and run with group-add):

  ```bash
  sudo groupadd -f docker
  sudo usermod -aG docker $USER
  docker run --rm -it \
    --group-add docker \
    -v /var/run/docker.sock:/var/run/docker.sock \
    dockhand:latest
  ```

  Note: Dockhand now performs a startup check and will error out if it detects permission denied accessing `/var/run/docker.sock`. If you see a fatal error about socket permissions, ensure the container user has group access to the socket (e.g., `--group-add docker`) or bind-mount with a user/group that matches the host's docker group.

  If you prefer to run as non-root inside the container (recommended), ensure the non-root user belongs to a group that has access to the socket. This improves security compared to running processes as root.


- Networking & volumes: Dockhand attempts to preserve the previous container's `HostConfig` and `NetworkingConfig` so ports, volumes, and networks are retained. However, custom networking setups should be tested — Dockhand will attempt to re-attach the new container to the same networks as the previous one.
- Environment variables: Dockhand reuses the previous container's environment (so changes in image defaults are not automatically merged). If you rely on image-updated default env vars, consider updating your container definitions.

Docker Compose example

```yaml
version: '3.8'
services:
  dockhand:
    image: izm1chael/dockhand:latest
    environment:
      - DOCKHAND_POLL_INTERVAL=10m
      - DOCKHAND_MANAGE_LATEST_ONLY=true
      - DOCKHAND_OPT_OUT=true
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    restart: unless-stopped
```

> Publishing & pulling images: We publish multi-architecture images (linux/amd64, linux/arm64) to GHCR and Docker Hub as `izm1chael/dockhand`.
> Pull from Docker Hub with `docker pull izm1chael/dockhand:latest` or GHCR with `docker pull ghcr.io/izm1chael/dockhand:latest`.

Binary releases

Pre-built binaries are available for each release on GitHub. We publish binaries for multiple platforms and architectures:

- Linux (amd64, arm64)
- macOS / Darwin (amd64, arm64)
- Windows (amd64, arm64)

Download the latest release from the [GitHub Releases](https://github.com/izm1chael/dockhand/releases/latest) page. Extract the archive and run the binary directly:

```bash
# Example: Download and run latest version (Linux amd64)
# Fetch the latest version tag
LATEST=$(curl -s https://api.github.com/repos/izm1chael/dockhand/releases/latest | grep '"tag_name"' | cut -d '"' -f 4)
wget https://github.com/izm1chael/dockhand/releases/download/${LATEST}/dockhand_${LATEST}_linux_amd64.tar.gz
tar -xzf dockhand_${LATEST}_linux_amd64.tar.gz
./dockhand --poll-interval=10m
```

## How to Update Dockhand

Dockhand can automatically update itself when running as a container. When an update is detected for its own container:

1. Dockhand spawns a temporary worker container using the new image.
2. The worker waits for the main daemon to shut down gracefully.
3. The worker recreates the Dockhand container with the new image.
4. The worker exits automatically after the update completes.

This automated self-update sequence happens seamlessly without manual intervention. Dockhand detects its own container by checking if the image or container name contains "dockhand".

Manual update (optional)

If you need to manually trigger an update or prefer not to rely on automatic updates, you can use this one-liner:

```bash
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  izm1chael/dockhand:latest --run-once
```

This will perform a single reconciliation pass, detect your running Dockhand container is outdated, and replace it with the latest version.

Integration tests (DinD)

There is an end-to-end integration test that uses Docker-in-Docker and a local registry. This test is skipped by default; to run it locally (requires privileged Docker):

```bash
RUN_DIND_TESTS=1 go test ./... -run TestE2ERecreateUsingLocalRegistry
```