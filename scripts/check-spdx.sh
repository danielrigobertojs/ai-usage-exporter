#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval
#
# Fails with exit 1 and lists every tracked .go file missing the two-line
# SPDX/copyright header. Catches the common way attribution gets lost: a
# single file copy-pasted without its header.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

missing=()
while IFS= read -r -d '' file; do
  head=$(head -n 2 "$file")
  if ! grep -q '^// SPDX-License-Identifier: Apache-2.0$' <<<"$head"; then
    missing+=("$file")
    continue
  fi
  if ! grep -q '^// Copyright (c) [0-9]\{4\} .\+$' <<<"$head"; then
    missing+=("$file")
  fi
done < <(git ls-files -z '*.go')

if ((${#missing[@]} > 0)); then
  echo "Missing SPDX/copyright header in ${#missing[@]} file(s):" >&2
  printf '  %s\n' "${missing[@]}" >&2
  exit 1
fi

echo "All tracked .go files carry the SPDX/copyright header."
