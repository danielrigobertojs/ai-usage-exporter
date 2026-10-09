#ADR-003: Apache-2.0 Licensing and Attribution Kit

- Date: 2026-10-05
- Status: Accepted
- Review: reconsider if the applicant decides on dual licensing or
open core (see "Reopening condition")

## Context

The applicant requested open source with **permanent recognition of the
repository and author**. The original bootstrap (JCB-307) left a `LICENSE`
of Apache-2.0 already in the repository, but not the rest of the attribution kit
which the license alone does not guarantee.

MIT, the most permissive obvious alternative, does not meet the requirement: it forces
retain the copyright notice in copies and substantial portions, but not
It has a mechanism for propagating notices to forks, it does not require declaration
modifications, does not say anything about brands, and allows a fork to rebrand the
project, bury the `LICENSE` and comply legally while the attribution
remains invisible to any end user.

## Decision

**Apache License 2.0** as the project license. The clauses that
solve the requirement:

- **§4(b)** — derivative works must bear prominent notices that
the file was modified.
- **§4(c)** — must retain copyright, patent, trademark and
attribution of the original.
- **§4(d)** — this repository's `NOTICE` file is propagated: any
distributed derivative must include a legible copy of its notices, in its
own `NOTICE`, in the source distribution, **or in the output that the
generated derivative**. This last way is the one we use: `version.String()`
includes `internal/license.Attribution()`, so credit travels with the
running binary, not just the source code.
- **§6** — does not grant trademark rights; the project name is still
of the author. Documented in `TRADEMARK.md`.
- Express grant of patents (§3), with termination if someone litigates for
patents against the project.

Copyright holder: natural person **Daniel Rigoberto Jacobo Sandoval**
(decided by the applicant on 2026-10-02). Applied in `NOTICE`,
`internal/license`, the SPDX headers of each `.go`, and `CITATION.cff`.

Contribution mechanism: **DCO** (`Signed-off-by:` by commit, verified
in CI), not CLA. See "Discarded alternatives".

## Honest limit

Apache-2.0 does not force a visible credit on a product interface
closed that incorporates this code as an internal dependency without
redistribute it — §4 only applies to whoever *distributes* the Work or Derivative
Works. No OSI-approved license reliably accomplishes that. The
that try (the BSD-4-clause publicity clause, the licenses
"attribution assurance") are incompatible with GPL, generate proliferation
of licenses and scare away adoption. They are not considered.

What we did achieve, and that is what was within the reach of this ticket: the
attribution travels with each **distribution** of the binary or code
(`NOTICE`, SPDX headers, `version.String()`, the HTTP `User-Agent`, and the
`ai_usage_build_info` metric when the collector exists).

## Discarded alternatives

**MIT.** See "Context" — does not meet the persistent attribution requirement.

**Licenses with advertising clause or "attribution assurance" (e.g.
BSD-4-clause).** They do force a more visible credit, but they are incompatible
with GPL (proliferation of licenses, GPL is copyleft but this project is not
it is, so it doesn't affect directly, but it does affect any
downstream consumer that combines this dependency with GPL software) and the
FSF and Debian point them out as problematic. Adoption risk higher than
the marginal attribution benefit they give over Apache-2.0 §4(d).

**CLA instead of DCO.** A CLA typically licenses or assigns copyright
of contribution to the maintainer beyond what Apache-2.0 already grants
in its §5, and is the mechanism that would enable dual or open licensing
core. This project does not pursue either of the two (see "Out of reach"
of the ticket that originated this ADR), so the DCO — one more attestation
light, with no additional give — is sufficient and reduces friction to
contribute.

## Consequences

- `scripts/check-spdx.sh` in CI build fails if any `.go` loses its
SPDX/copyright header — the mechanism that survives copy-paste of a
loose file out of your git history.
- `scripts/gen-third-party.sh` in CI build fails if a dependency
new is copyleft (GPL, AGPL, LGPL, MPL) or has no detectable license,
and if `THIRD_PARTY_LICENSES.md` committed becomes outdated vs.
`go.mod`.
- Changing already exposed metric names still requires discussion
in the corresponding issue — this ADR does not change that, but
`ai_usage_build_info` gains the `project` and `license` labels while still
be a single series.
- If dual or open core licensing is desired in the future, this ADR must
reopen: it implies replacing DCO with CLA and is a business decision, not
engineering — explicitly out of scope here.

## Reopening condition

Reopen if the applicant decides to pursue dual or open core licensing
(it would require CLA instead of DCO), or if it is decided to register the trademark before a
office (IMPI or other) — both are business/legal procedures outside the
scope of this ticket.
