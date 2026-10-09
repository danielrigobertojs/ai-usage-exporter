<!-- SPDX-License-Identifier: Apache-2.0 -->

## ADR-007: Degraded startup when the initial scan fails

Date: 2026-10-09. Review: 2027-01-09.

The exporter does not exit when the startup scan times out or fails. It serves
`/metrics` with its metadata metrics and `ai_usage_scan_success 0`, but does
not publish usage metrics from a partial aggregate. The next rescan can recover
normally. `--fail-on-startup-scan-error` and
`AI_USAGE_FAIL_ON_STARTUP_SCAN_ERROR=true` restore strict mode.

Always failing was rejected because Prometheus cannot distinguish it from an
unreachable host. Publishing a partial result was also rejected: gauges could
decrease and create a misleading reading. This decision will be reviewed on the
specified date after evaluating operational timeout data.
