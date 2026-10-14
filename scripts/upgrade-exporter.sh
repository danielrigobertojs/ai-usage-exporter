#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval
#
# Replaces an exporter started outside a service manager.  In particular, this
# does not interpret a relative executable name in the operator's directory:
# it resolves it against the listener's own working directory before stopping
# anything.
set -euo pipefail

usage() {
  echo "Usage: $0 --port PORT --new-binary PATH" >&2
  exit 2
}

port=""
new_binary=""
while (($#)); do
  case "$1" in
    --port) port=${2:-}; shift 2 ;;
    --new-binary) new_binary=${2:-}; shift 2 ;;
    *) usage ;;
  esac
done
[[ -n "$port" && -n "$new_binary" ]] || usage

listener_pid() {
  local found
  found=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t | sort -u)
  if [[ -z "$found" || $(printf '%s\n' "$found" | wc -l | tr -d ' ') -ne 1 ]]; then
    echo "Expected exactly one listener on TCP port $port; refusing to stop anything." >&2
    return 1
  fi
  printf '%s\n' "$found"
}

process_cwd() {
  local pid=$1 cwd
  if [[ -d "/proc/$pid/cwd" ]]; then
    cwd=$(readlink "/proc/$pid/cwd")
  else
    cwd=$(lsof -n -p "$pid" -a -d cwd -Fn | sed -n 's/^n//p' | head -n 1)
  fi
  [[ -n "$cwd" && "$cwd" = /* ]] || {
    echo "Cannot determine an absolute working directory for PID $pid; refusing to stop it." >&2
    return 1
  }
  printf '%s\n' "$cwd"
}

resolve_listener_binary() {
  local pid=$1 command cwd executable
  command=$(ps -p "$pid" -o command=)
  executable=${command%%[[:space:]]*}
  cwd=$(process_cwd "$pid")
  case "$executable" in
    ./*) BINARY_PATH="$cwd/${executable#./}" ;;
    /*) BINARY_PATH=$executable ;;
    *)
      echo "PID $pid was not started with an absolute or ./ executable path: $command" >&2
      return 1
      ;;
  esac
  [[ -f "$BINARY_PATH" && -x "$BINARY_PATH" ]] || {
    echo "Resolved executable $BINARY_PATH does not exist or is not executable; refusing to stop PID $pid." >&2
    return 1
  }
  OLD_COMMAND=$command
  LISTENER_CWD=$cwd
}

absolute_path() {
  local path=$1 dir base
  dir=$(cd "$(dirname "$path")" && pwd -P)
  base=$(basename "$path")
  printf '%s/%s\n' "$dir" "$base"
}

pid=$(listener_pid)
resolve_listener_binary "$pid"
NEW_BINARY=$(absolute_path "$new_binary")
[[ -f "$NEW_BINARY" && -x "$NEW_BINARY" ]] || {
  echo "New binary $NEW_BINARY does not exist or is not executable." >&2
  exit 1
}

printf 'PID=%s\nCWD=%s\nBINARY_PATH=%s\nCOMMAND=%s\n' "$pid" "$LISTENER_CWD" "$BINARY_PATH" "$OLD_COMMAND"

# read -a deliberately avoids evaluating the recorded command line. Exporter
# flags are ordinary argv entries; an unsupported quoted command line fails
# safely rather than being passed to a shell.
read -r -a argv <<<"$OLD_COMMAND"
[[ ${argv[0]} == ./* || ${argv[0]} == /* ]] || {
  echo "Cannot safely replay command line for PID $pid." >&2
  exit 1
}
unset 'argv[0]'

# These checks are immediately before kill: a stale PID, vanished cwd, or a
# changed executable turns the operation into a no-op instead of an outage.
[[ -d "$LISTENER_CWD" && -f "$BINARY_PATH" && -x "$BINARY_PATH" ]] || {
  echo "Listener cwd or executable changed before upgrade; refusing to stop PID $pid." >&2
  exit 1
}

install -m 0755 "$NEW_BINARY" "$BINARY_PATH"
installed_binary=$BINARY_PATH
kill "$pid"
while kill -0 "$pid" 2>/dev/null; do
  # A manually launched child can remain a zombie until its original shell
  # reaps it. It no longer owns the port, so waiting for it forever would turn
  # a successful replacement into an avoidable outage.
  state=$(ps -p "$pid" -o stat= 2>/dev/null | tr -d '[:space:]' || true)
  [[ "$state" == Z* ]] && break
  sleep 1
done

nohup "$BINARY_PATH" "${argv[@]}" >"$LISTENER_CWD/ai-usage-exporter.log" 2>&1 &

for _ in $(seq 1 30); do
  if new_pid=$(listener_pid 2>/dev/null); then
    resolve_listener_binary "$new_pid"
    if [[ "$BINARY_PATH" == "$installed_binary" ]]; then
      # The second resolver has confirmed that the listener is executing the
      # absolute path just installed, not an identically named operator-cwd file.
      cmp -s "$NEW_BINARY" "$installed_binary" || {
        echo "Installed binary differs from the verified replacement." >&2
        exit 1
      }
      printf 'Restarted PID=%s BINARY_PATH=%s\n' "$new_pid" "$installed_binary"
      exit 0
    fi
  fi
  sleep 1
done

echo "Replacement exporter did not become the sole listener on TCP port $port." >&2
exit 1
