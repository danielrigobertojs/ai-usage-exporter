# Install ai-usage-exporter

Release archives contain one static binary for each supported platform. Every
release also includes `checksums.txt`; verify an archive before extracting it.

## macOS

Download the archive for your CPU (`darwin_arm64` for Apple Silicon or
`darwin_amd64` for Intel), then verify and install it:

```bash
curl -LO https://github.com/danielrigobertojs/ai-usage-exporter/releases/download/v<VERSION>/ai-usage-exporter_v<VERSION>_darwin_arm64.tar.gz
curl -LO https://github.com/danielrigobertojs/ai-usage-exporter/releases/download/v<VERSION>/checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf ai-usage-exporter_v<VERSION>_darwin_arm64.tar.gz
install -m 0755 ai-usage-exporter /usr/local/bin/ai-usage-exporter
```

## Linux

For Debian and Ubuntu, install the matching `.deb` package:

```bash
curl -LO https://github.com/danielrigobertojs/ai-usage-exporter/releases/download/v<VERSION>/ai-usage-exporter_<VERSION>_linux_amd64.deb
sudo dpkg -i ai-usage-exporter_<VERSION>_linux_amd64.deb
```

For Fedora, RHEL, and other RPM-based distributions:

```bash
curl -LO https://github.com/danielrigobertojs/ai-usage-exporter/releases/download/v<VERSION>/ai-usage-exporter_<VERSION>_linux_amd64.rpm
sudo rpm -i ai-usage-exporter_<VERSION>_linux_amd64.rpm
```

Portable Linux tarballs are also available for `linux_amd64` and
`linux_arm64`. Download the corresponding archive, verify it against
`checksums.txt` with `sha256sum -c --ignore-missing checksums.txt`, and put the
extracted binary on your `PATH`.

### systemd

Copy `deploy/systemd/ai-usage-exporter.service` to
`/etc/systemd/system/`, then enable and start it:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now ai-usage-exporter.service
```

The service has `Restart=on-failure`. El exporter reescanea el historial
full scan every 60 seconds by default; you do not need the refresh timer to
tener datos vivos. Si prefieres un snapshot congelado, configura
`AI_USAGE_SCAN_INTERVAL=0` en la unidad principal.

## Windows

Download `ai-usage-exporter_v<VERSION>_windows_amd64.zip` (or
`windows_arm64`), verify it in PowerShell, and extract it:

```powershell
Invoke-WebRequest https://github.com/danielrigobertojs/ai-usage-exporter/releases/download/v<VERSION>/ai-usage-exporter_v<VERSION>_windows_amd64.zip -OutFile ai-usage-exporter.zip
Expand-Archive ai-usage-exporter.zip -DestinationPath .\ai-usage-exporter
.\ai-usage-exporter\ai-usage-exporter.exe version
```

Compare the SHA-256 value from `Get-FileHash .\ai-usage-exporter.zip -Algorithm SHA256`
with the matching entry in the release's `checksums.txt` before use.

## Docker

The release image is multi-architecture (`linux/amd64` and `linux/arm64`) and
runs as a non-root user. Mount only the provider directories you want scanned:

```bash
docker run --rm -p 127.0.0.1:9477:9477 \
  -v "$HOME/.claude/projects:/home/nonroot/.claude/projects:ro" \
  -v "$HOME/.codex/sessions:/home/nonroot/.codex/sessions:ro" \
  -e HOME=/home/nonroot \
  ghcr.io/danielrigobertojs/ai-usage-exporter:v<VERSION>
```

Run `docker run --rm ghcr.io/danielrigobertojs/ai-usage-exporter:v<VERSION> version`
to inspect the image build metadata, or append `doctor --output json` to check
provider discovery without serving metrics.

## Go install

```bash
go install github.com/danielrigobertojs/ai-usage-exporter/cmd/ai-usage-exporter@v<VERSION>
ai-usage-exporter version
```

## launchd

Copy `deploy/launchd/io.github.ai-usage-exporter.plist` into
`~/Library/LaunchAgents/`, then load it:

```bash
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/io.github.ai-usage-exporter.plist
```

The exporter in the main plist rescans every 60 seconds by default. To disable
it, add `AI_USAGE_SCAN_INTERVAL=0` to that plist's environment; you do not need
to load the refresh plist for live monitoring.

## Upgrade an existing installation

Use this procedure to replace a running exporter without changing its metric
configuration. A managed service is the recommended deployment method: an
upgrade replaces the binary and restarts one known service. A manually started
process can be upgraded too, but its command line must be preserved by hand.

### Check what is running and choose the target build

On the exporter host, inspect the build currently serving metrics and the
local binary. The `commit` and `version` labels identify the running build;
the value of the metric is always `1`.

```bash
curl -fsS http://127.0.0.1:9477/metrics | grep '^ai_usage_build_info{'
ai-usage-exporter version
```

To compare that commit with the current `main` commit without opening the
repository, run:

```bash
git ls-remote https://github.com/danielrigobertojs/ai-usage-exporter.git refs/heads/main | cut -f1
```

To identify the newest release version instead, run:

```bash
curl -fsSLI -o /dev/null -w '%{url_effective}\n' https://github.com/danielrigobertojs/ai-usage-exporter/releases/latest | sed 's#.*/##'
```

Download and verify the chosen release using the platform-specific commands
above. Keep the expected release version or commit available for the
verification step below.

### Upgrade a managed service

For macOS, install the replacement binary at the path in
`deploy/launchd/io.github.ai-usage-exporter.plist` (by default,
`/usr/local/bin/ai-usage-exporter`). The supplied plist is the recommended
LaunchAgent definition. Reloading it stops the old process and starts the new
binary:

```bash
launchctl bootout "gui/$(id -u)" ~/Library/LaunchAgents/io.github.ai-usage-exporter.plist
install -m 0755 ai-usage-exporter /usr/local/bin/ai-usage-exporter
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/io.github.ai-usage-exporter.plist
```

For Linux, install the replacement through the matching package or at the path
in `deploy/systemd/ai-usage-exporter.service` (by default, `/usr/bin`). For a
package upgrade, use the matching command below, then restart the supplied
systemd unit:

```bash
sudo dpkg -i ai-usage-exporter_<VERSION>_linux_amd64.deb  # Debian/Ubuntu
sudo rpm -U ai-usage-exporter_<VERSION>_linux_amd64.rpm  # Fedora/RHEL
sudo systemctl restart ai-usage-exporter.service
sudo systemctl status --no-pager ai-usage-exporter.service
```

The supplied systemd unit has `Restart=on-failure`, so it also starts the
exporter again after an unexpected failure. If the unit or plist has not yet
been installed, follow the platform setup instructions above first; do not
mix a manually started exporter with a service managing the same port.

### Upgrade a manually started process

First capture the exact command line and PID. Review the output before
continuing; it can include command-line arguments that must be retained.

```bash
pgrep -af ai-usage-exporter
PID=<pid-from-the-previous-command>
old_command=$(ps -p "$PID" -o command=)
printf '%s\n' "$old_command"
```

Stop that PID, replace the executable at the path reported by `command -v`,
and relaunch the recorded command. Replace `ai-usage-exporter` with the
verified release file you downloaded.

```bash
kill "$PID"
while kill -0 "$PID" 2>/dev/null; do sleep 1; done
install -m 0755 ai-usage-exporter "$(command -v ai-usage-exporter)"
nohup sh -c "$old_command" > ai-usage-exporter.log 2>&1 &
```

If the prior command used a binary outside `PATH`, replace the destination in
the `install` command with that absolute path. Keep the log outside a temporary
directory if the process must survive a logout or restart.

### Verify the deployment

Wait briefly for the exporter to start, then confirm that the running metric
has the expected commit (or expected release version):

```bash
curl -fsS http://127.0.0.1:9477/metrics | grep '^ai_usage_build_info{'
```

Also verify the functional result from Prometheus. Substitute the exporter
target and a tool/window that is known to have historical usage; for example,
this checks that a 30-day OpenCode series is present after the upgrade:

```bash
PROMETHEUS_URL=http://prometheus.example:9090
curl -fsSG "$PROMETHEUS_URL/api/v1/query" \
  --data-urlencode 'query=ai_usage_tokens{instance="exporter.example:9477",tool="opencode",window="30d"}'
```

The JSON response must contain a non-empty `data.result` when that host has
matching historical usage. If it does not, choose the appropriate known tool
and window before treating an empty result as an exporter regression.

### If the host or exporter does not respond

Separate a host outage from an exporter outage before replacing anything. From
another machine, first check basic reachability, then the exporter port and
endpoint:

```bash
ping -c 1 exporter.example
nc -vz exporter.example 9477
curl -fsS http://exporter.example:9477/metrics | grep '^ai_usage_build_info{'
```

If `ping` fails, investigate host or network availability. If `ping` succeeds
but the port or endpoint fails, the host is reachable and the exporter is not
listening: inspect `systemctl status ai-usage-exporter.service` or the
launchd/manual-process commands above before changing Prometheus targets.
