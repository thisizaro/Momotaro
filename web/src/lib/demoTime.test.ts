import { describe, expect, it } from 'vitest';
import { DEMO_TIME_SCALE, formatSimulatedElapsed, simulatedElapsedMs } from '@/lib/demoTime';

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
