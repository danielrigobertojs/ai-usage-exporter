<!-- SPDX-License-Identifier: Apache-2.0 -->

## ADR-008: Zero-fill only closed scopes; open scopes declare empty results

Date: 2026-10-11. Review: 2027-01-11.

Usage gauges are zero-filled only for closed, enumerable label scopes such as
`instance`, `tool`, and `window`. Dashboard expressions must preserve exactly
the same labels on both sides of `or`: a `sum by (L)` may use `0 * max by (L)`
over `ai_usage_provider_available`. Scalar aggregates without `by (...)` may
use `or vector(0)`.

`model` and `token_type` are open scopes. Panels grouped by either do not
invent zero-valued series. Their title and description state that an empty
result means no measured consumption, and a scalar total in the same row
renders zero so that the empty state is legible.

JCB-328 established the collector behavior without recording this decision;
JCB-331 and JCB-340 reopened the same question. We rejected query-side
zero-fill by `model`: it makes historical models appear in rankings and tables
despite no consumption in the selected window. We also rejected closing the
`window` axis in the collector. Although it does not invent a model, it would
materialize up to `6 × |tuples in window="all"|` series: about 690 instead of
187 in the measured fleet, on a set that grows as models are observed.

Reconsider closing the `window` axis if these gauges must feed `rate()` or
`increase()`, because gaps would then affect the calculation. Until then they
remain gauges re-derived from history under ADR-001.
