## Summary

<!-- What does this change do, and why? -->

## Testing

<!-- Commands run and their output: go build, go test ./... -race, go vet, gofmt -l . -->

## Checklist

- [ ] Every commit in this PR has a `Signed-off-by:` trailer (DCO —
      see [CONTRIBUTING.md](../CONTRIBUTING.md)). Use `git commit -s`.
- [ ] Every new or modified `.go` file carries the SPDX/copyright header
      (`scripts/check-spdx.sh` passes).
- [ ] If a dependency was added or changed, `THIRD_PARTY_LICENSES.md` was
      regenerated with `scripts/gen-third-party.sh` and committed.
- [ ] `go test ./... -race`, `go vet ./...`, and `gofmt -l .` pass locally.
