import { describe, expect, it } from 'vitest';
import { DEMO_TIME_SCALE, formatSimulatedElapsed, formatSimulatedGap, simulatedElapsedMs } from '@/lib/demoTime';

// The compression direction matters and is easy to get backwards: a SMALL
// amount of real time represents a LARGE amount of simulated time (the
// scheduler divides a real wait by the scale to get something a demo can
// sit through; this module runs that arithmetic the other way, multiplying
// a real elapsed duration back up to what it stands for). Every case here
// is chosen to be checkable by hand against configs/demo.env's
// DEMO_TIME_SCALE=300000.
describe('demoTime', () => {
  it('matches the documented constant from configs/demo.env', () => {
    expect(DEMO_TIME_SCALE).toBe(300000);
  });

  it('multiplies real elapsed time by the scale, not divides', () => {
    // 1 real second -> 300000 simulated seconds (~3.47 days), the exact
    // relationship CLAUDE.md states in prose ("one real second is about
    // 3.5 simulated days").
    expect(simulatedElapsedMs(1000)).toBe(300000 * 1000);
  });

  it('clamps a negative real elapsed duration to zero rather than going negative', () => {
    expect(simulatedElapsedMs(-5000)).toBe(0);
  });

  it('formats a very small real elapsed time as minutes into the window', () => {
    // 1ms real * 300000 = 300,000ms simulated = 5 min exactly. At this
    // scale factor, minute-level resolution only ever shows up for a real
    // elapsed duration under about 12ms; anything a person would actually
    // wait through already reads in hours or days, which is the point.
    expect(formatSimulatedElapsed(1)).toBe('5 min into the 7-day recovery window');
  });

  it('formats real elapsed time under a simulated day as whole hours', () => {
    // 12ms real * 300000 = 3,600,000ms simulated = exactly 1 hour.
    expect(formatSimulatedElapsed(12)).toBe('hour 1 of the 7-day recovery window');
  });

  it('formats real elapsed time at or beyond a simulated day as days, one decimal under 10', () => {
    // 1 real second * 300000 = 300,000s simulated = 3.4722... days.
    expect(formatSimulatedElapsed(1000)).toBe('day 3.5 of the 7-day recovery window');
  });

  it('drops the decimal once the simulated day count reaches double digits', () => {
    // 3 real seconds * 300000 = 900,000s simulated = 10.41... days.
    expect(formatSimulatedElapsed(3000)).toBe('day 10 of the 7-day recovery window');
  });

  it('formats zero real elapsed time as the start of the window', () => {
    expect(formatSimulatedElapsed(0)).toBe('0 min into the 7-day recovery window');
  });
});

// formatSimulatedGap is the RecordDrawer audit trail's "how far apart were
// these two entries" figure, the genuinely useful number when reading a
// trail (docs/DEMO_READINESS.md Unit AN). It shares simulatedElapsedMs's
// arithmetic with formatSimulatedElapsed above, so the multiplication
// direction and DEMO_TIME_SCALE source are already covered by those cases;
// what is different here is a plain duration ("8 days"), not a position
// framed against the recovery window ("day 8 of the 7-day recovery
// window"), since a gap between two entries is not itself a point in the
// window. Every case is hand-checkable against DEMO_TIME_SCALE=300000.
describe('formatSimulatedGap', () => {
  it('formats a sub-minute real gap in minutes, matching formatSimulatedElapsed at the same input', () => {
    // 1ms real * 300000 = 300,000ms simulated = 5 min exactly.
    expect(formatSimulatedGap(1)).toBe('5 min');
  });

  it('formats zero real gap as zero minutes', () => {
    expect(formatSimulatedGap(0)).toBe('0 min');
  });

  it('formats a real gap under a simulated day in whole hours, singular at exactly one', () => {
    // 12ms real * 300000 = 3,600,000ms simulated = exactly 1 hour.
    expect(formatSimulatedGap(12)).toBe('1 hour');
  });

  it('pluralizes hours above one', () => {
    // 20ms real * 300000 = 6,000,000ms simulated = exactly 100 minutes = 1.667h, rounds to 2 hours.
    expect(formatSimulatedGap(20)).toBe('2 hours');
  });

  it('formats a real gap at or beyond a simulated day in days, one decimal under ten', () => {
    // 1 real second * 300000 = 300,000s simulated = 3.4722... days.
    expect(formatSimulatedGap(1000)).toBe('3.5 days');
  });

  it('drops the decimal once the simulated day count reaches double digits', () => {
    // 3 real seconds * 300000 = 900,000s simulated = 10.41... days.
    expect(formatSimulatedGap(3000)).toBe('10 days');
  });

  it('uses the singular "day" only when the gap rounds to exactly one', () => {
    // 288ms real * 300000 = 86,400,000ms simulated = exactly 1.0 day.
    expect(formatSimulatedGap(288)).toBe('1 day');
  });

  it('clamps a negative real gap to zero rather than going negative', () => {
    expect(formatSimulatedGap(-500)).toBe('0 min');
  });
});

// A continuous loadgen run spans real minutes, not the few seconds a seeded
// batch takes, and at 300000x that is years of simulated time. The window
// framing then reads as nonsense: a live stack showed
// "day 3347 of the 7-day recovery window" on its axis
// (docs/INCIDENTS.md 2026-09-03). Past the window the position is no longer
// a position in it, so the framing has to go.
describe('formatSimulatedElapsed past the recovery window', () => {
  it('drops the window framing once elapsed exceeds it', () => {
    // 16 real minutes at 300000x is about 3333 simulated days.
    const out = formatSimulatedElapsed(16 * 60 * 1000);
    expect(out).not.toContain('recovery window');
    expect(out).toBe('9.1 years of simulated time');
  });

  it('keeps the window framing for a normal seeded batch', () => {
    // A seeded batch settles in seconds. 1 real second is 3.5 simulated
    // days, 3 seconds is 10, 12 seconds is 42: all still readable as a
    // position in a recovery cycle, and worth keeping.
    expect(formatSimulatedElapsed(1000)).toContain('recovery window');
    expect(formatSimulatedElapsed(12000)).toContain('recovery window');
  });
});
