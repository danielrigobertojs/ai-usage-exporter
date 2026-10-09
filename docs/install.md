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
