/**
 * DEMO_TIME_SCALE (`configs/demo.env`, `docs/ARCHITECTURE.md` section 17):
 * the factor the Decision Engine compresses every wall-clock wait it
 * schedules by (retry delay, contact cooldown, mandate lead time, nudge
 * delay). A real duration this run actually took, multiplied by this
 * constant, recovers the uncompressed duration a judge can check by hand:
 * one real second here stands for DEMO_TIME_SCALE seconds, about 3.5
 * simulated days, of what that same recovery cycle would take without the
 * compression (`CLAUDE.md`: "DEMO_TIME_SCALE=300000 makes one real second
 * about 3.5 simulated days").
 *
 * It deliberately does NOT compress RECOVERY_WINDOW: that guardrail
 * compares against a record's real elapsed age directly
 * (`services/decision-engine/cmd/main.go`'s `guardrailsFrom`,
 * `docs/INCIDENTS.md` 2026-08-31), because no scale factor can compress the
 * wall clock an age is measured on. This module only ever multiplies a real
 * elapsed duration to show what it represents, it never divides a duration
 * to schedule one, so that asymmetry does not apply here.
 *
 * Hardcoded rather than fetched from the Gateway: `docs/API_GATEWAY.md` is
 * the frozen wire contract for record and batch data, not operational
 * config, the same reason the frontend does not fetch `LLM_SAMPLE_RATE` or
 * `RECOVERY_WINDOW` either. Must be kept equal to `configs/demo.env`'s
 * `DEMO_TIME_SCALE`; a change there needs a matching change here
 * (`docs/DECISIONS.md`).
 */
export const DEMO_TIME_SCALE = 300000;

/** RECOVERY_WINDOW (`.env.example`): 7 real days, not scaled (see above). */
export const RECOVERY_WINDOW_DAYS = 7;

const MS_PER_MINUTE = 60 * 1000;
const MS_PER_HOUR = 60 * MS_PER_MINUTE;
const MS_PER_DAY = 24 * MS_PER_HOUR;

/**
 * Converts a real elapsed duration (ms) into the simulated duration it
 * represents. Negative input clamps to 0: a duration before the reference
 * point has nothing simulated to show.
 */
export function simulatedElapsedMs(realElapsedMs: number): number {
  return Math.max(realElapsedMs, 0) * DEMO_TIME_SCALE;
}

/**
 * Renders the simulated equivalent of a real elapsed duration, framed
 * against the 7-day recovery window since that is the real business
 * timescale this run is standing in for, e.g. "day 3.5 of the 7-day
 * recovery window". Resolution steps down for a short elapsed duration so
 * the number itself stays legible (whole minutes/hours below one day)
 * rather than showing false decimal precision on a number too small to
 * mean anything at day granularity.
 */
export function formatSimulatedElapsed(realElapsedMs: number): string {
  const simMs = simulatedElapsedMs(realElapsedMs);
  const simDays = simMs / MS_PER_DAY;
  if (simDays >= 1) {
    const decimals = simDays < 10 ? 1 : 0;
    return `day ${simDays.toFixed(decimals)} of the ${RECOVERY_WINDOW_DAYS}-day recovery window`;
  }
  const simHours = simMs / MS_PER_HOUR;
  if (simHours >= 1) {
    return `hour ${simHours.toFixed(0)} of the ${RECOVERY_WINDOW_DAYS}-day recovery window`;
  }
  const simMinutes = Math.round(simMs / MS_PER_MINUTE);
  return `${simMinutes} min into the ${RECOVERY_WINDOW_DAYS}-day recovery window`;
}

function pluralize(value: number, unit: string): string {
  return `${value} ${unit}${value === 1 ? '' : 's'}`;
}

/**
 * Renders the simulated equivalent of a real elapsed GAP between two audit
 * trail entries, e.g. "8 days" or "1 hour" or "5 min". Unlike
 * formatSimulatedElapsed, this is not framed as a position in the 7-day
 * recovery window ("day N of..."), because a gap between two entries is a
 * duration, not a point in time; RecordDrawer composes it with the real-time
 * equivalent (`+2.3s real, +8 days simulated`), which is the actually
 * useful figure when reading a trail compressed by DEMO_TIME_SCALE
 * (docs/DEMO_READINESS.md Unit AN). Shares the same day/hour rounding as
 * formatSimulatedElapsed so the two surfaces never disagree about which
 * side of a boundary a value falls on.
 */
export function formatSimulatedGap(realElapsedMs: number): string {
  const simMs = simulatedElapsedMs(realElapsedMs);
  const simDays = simMs / MS_PER_DAY;
  if (simDays >= 1) {
    const decimals = simDays < 10 ? 1 : 0;
    return pluralize(Number(simDays.toFixed(decimals)), 'day');
  }
  const simHours = simMs / MS_PER_HOUR;
  if (simHours >= 1) {
    return pluralize(Math.round(simHours), 'hour');
  }
  const simMinutes = Math.round(simMs / MS_PER_MINUTE);
  return `${simMinutes} min`;
}
