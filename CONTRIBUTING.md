# Contributing to ai-usage-exporter

## License and the Developer Certificate of Origin

All contributions are accepted under the [Apache License 2.0](LICENSE).
By submitting a contribution you agree that it is licensed under those
terms, per Section 5 of the license itself — no separate CLA is required.

This project uses the [Developer Certificate of Origin (DCO)](https://developercertificate.org/)
instead of a Contributor License Agreement. Every commit must carry a
`Signed-off-by:` trailer certifying that you wrote the change, or
otherwise have the right to submit it under the project's license:

```text
Signed-off-by: Your Name <your.email@example.com>
```

Add it automatically with `git commit -s`. A pull request with any commit
missing this trailer fails the `dco` CI check and cannot be merged.

**Why DCO and not a CLA:** a CLA assigns or licenses your copyright to the
project maintainer beyond what Apache-2.0 already grants, and is usually
needed to support dual-licensing or an open-core split. This project does
not dual-license (see [ADR-003](docs/adr/0003-license-and-attribution.md)),
so a DCO — a lighter-weight attestation with no separate grant — is
sufficient. Revisiting this is a business decision, out of scope for any
single issue.

## Before opening a pull request

Run, from the repository root:

```bash
gofmt -l .                  # must print nothing
go vet ./...
go build ./...
go test ./... -race
bash scripts/check-spdx.sh
```

If you added or changed a dependency, also run
`bash scripts/gen-third-party.sh` and commit the resulting
`THIRD_PARTY_LICENSES.md`.

## Code conventions

- Every `.go` file starts with:
  ```go
  // SPDX-License-Identifier: Apache-2.0
  // Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval
  ```
- No CGO. New dependencies must be justified in the issue they belong to
  and must not be copyleft-licensed (GPL, AGPL, LGPL, MPL) — the
  `third-party-licenses` CI job enforces this.
- `session_id` and full project paths are never metric labels. See
  `docs/metrics.md`.
