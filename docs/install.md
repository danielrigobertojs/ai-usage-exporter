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

The service has `Restart=on-failure`. The exporter rescans the history every
60 seconds by default; you do not need the refresh timer for live data. If you
prefer a frozen snapshot, set `AI_USAGE_SCAN_INTERVAL=0` in the main unit.

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

### Upgrade a host without a Go toolchain

Build the portable archives on any machine that has Go, from the exact commit
that you want to deploy. `make dist` creates the six release-compatible
archives plus `checksums.txt`; it does not need GoReleaser or a network
connection. Archive names are
`ai-usage-exporter_<version>_<os>_<arch>.tar.gz` for Darwin/Linux and
`ai-usage-exporter_<version>_<os>_<arch>.zip` for Windows.

```bash
# On the build machine, at the selected commit.
make dist

# Copy the selected archive and dist/checksums.txt to the destination host.
# For example: scp dist/ai-usage-exporter_<version>_darwin_arm64.tar.gz \
#   dist/checksums.txt exporter.example:/path/to/upgrade/
```

On the destination, confirm its architecture before selecting an archive, then
verify it before extracting it. Apple Silicon reports `arm64`; Intel macOS
reports `x86_64` and uses the `darwin_amd64` archive.

```bash
uname -m
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf ai-usage-exporter_<version>_darwin_arm64.tar.gz
```

Use the extracted binary with the manual-process helper below:

```bash
bash scripts/upgrade-exporter.sh --port 9477 --new-binary "$PWD/ai-usage-exporter"
```

The helper aborts without changing anything unless exactly one process listens
on the selected port. It reproduces the listener's literal argv, so an
invocation such as `-listen host:port` is retained automatically. The exporter
scans before it opens its listener (typically about five seconds); after a
restart, wait until `curl /metrics` responds before treating the upgrade as
complete.

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
release_json=$(curl -fsSL https://api.github.com/repos/danielrigobertojs/ai-usage-exporter/releases/latest) || {
  echo "No GitHub release is available; build the selected commit from source instead." >&2
  exit 1
}
printf '%s\n' "$release_json" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
```

Download and verify the chosen release using the platform-specific commands
above. If there is no release for the commit you need, build it from source;
`make build` records the checked-out commit in `ai_usage_build_info`:

```bash
TARGET_REF=<commit-or-tag>
git clone https://github.com/danielrigobertojs/ai-usage-exporter.git
cd ai-usage-exporter
git checkout "$TARGET_REF"
make build
./ai-usage-exporter version
```

Keep the expected release version or commit and the path to the verified new
binary available for the upgrade and verification steps below.

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

Select the process by the TCP port that it serves, not by its name. This avoids
selecting another exporter or the shell running the command. `lsof` is included
with macOS; install it on Linux if it is not already available. Set `PORT` to
the exporter port to upgrade, then review the exact command line before
continuing.

```bash
PORT=9477
PID=$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t | sort -u)
if [ -z "$PID" ] || [ "$(printf '%s\n' "$PID" | wc -l | tr -d ' ')" -ne 1 ]; then
  echo "Expected exactly one listener on TCP port $PORT; inspect it before continuing." >&2
  exit 1
fi
old_command=$(ps -p "$PID" -o command=)
cwd=$(lsof -n -p "$PID" -a -d cwd -Fn | sed -n 's/^n//p' | head -n 1)
case "$old_command" in
  *ai-usage-exporter*) ;;
  *) echo "PID $PID is not ai-usage-exporter; refusing to stop it." >&2; exit 1 ;;
esac
executable=${old_command%%[[:space:]]*}
case "$executable" in
  ./*) BINARY_PATH="$cwd/${executable#./}" ;;
  /*) BINARY_PATH="$executable" ;;
  *) echo "Cannot resolve an absolute exporter path from: $old_command" >&2; exit 1 ;;
esac
[ -n "$cwd" ] && [ -x "$BINARY_PATH" ] || {
  echo "Could not resolve the listener cwd and executable; refusing to stop it." >&2
  exit 1
}
printf 'PID=%s\nCWD=%s\nBINARY_PATH=%s\nCOMMAND=%s\n' "$PID" "$cwd" "$BINARY_PATH" "$old_command"
```

Set `NEW_BINARY` to the verified release or source build and run the helper.
The detection step above resolves `BINARY_PATH` to an absolute path, including
when the process was launched as `./ai-usage-exporter`. The helper repeats that
check immediately before stopping the process, installs only at that absolute
path, and relaunches with it. This prevents an operator's current directory
from selecting or overwriting another binary with the same name.

```bash
NEW_BINARY=/path/to/verified/ai-usage-exporter
bash scripts/upgrade-exporter.sh --port "$PORT" --new-binary "$NEW_BINARY"
```

Keep the log outside a temporary directory if the process must survive a logout
or restart.

### Verify the deployment

The helper confirms that the listener after restart resolves to the installed
absolute path. Then confirm that the running metric has the expected commit (or
expected release version):

```bash
curl -fsS http://127.0.0.1:9477/metrics | grep '^ai_usage_build_info{'
```

Also verify the functional result from Prometheus. The supported windows are
`1h`, `24h`, `7d`, `30d`, `mtd`, and `all`; see [the metric
reference](metrics.md) for their definitions. Substitute the exporter target
and a tool that is known to have historical usage. This checks every window and
fails if Prometheus returns an empty vector for any one of them:

```bash
PROMETHEUS_URL=http://prometheus.example:9090
for WINDOW in 1h 24h 7d 30d mtd all; do
  response=$(curl -fsSG "$PROMETHEUS_URL/api/v1/query" \
    --data-urlencode "query=ai_usage_tokens{instance=\"exporter.example:9477\",tool=\"opencode\",window=\"$WINDOW\"}")
  printf '%s\n' "$response"
  if printf '%s' "$response" | grep -q '"result":\[\]'; then
    echo "No series returned for window $WINDOW." >&2
    exit 1
  fi
done
```

Each JSON response must contain a non-empty `data.result` when that host has
matching historical usage. If a window is genuinely expected to be empty, omit
it deliberately from the loop rather than treating an empty result as success.

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
