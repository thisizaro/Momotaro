import { describe, expect, it } from 'vitest';
import { noGroundTruthReason } from './groundTruth';

describe('noGroundTruthReason', () => {
  it('names the webhook source explicitly for the rolling production batch', () => {
    const text = noGroundTruthReason('webhook');
    expect(text.toLowerCase()).toContain('webhook');
    expect(text.toLowerCase()).toContain('answer key');
    // Two-mode story: the explanation should point at the seeded batch too.
    expect(text.toLowerCase()).toContain('demo controls');
  });

  it('gives a generic but still honest reason for any other unseeded source', () => {
    const text = noGroundTruthReason('dashboard-generated');
    expect(text.toLowerCase()).toContain('ground truth');
    expect(text.toLowerCase()).toContain('demo controls');
  });

  it('handles an absent source the same way as an unrecognised one', () => {
    const text = noGroundTruthReason(undefined);
    expect(text.length).toBeGreaterThan(0);
    expect(text.toLowerCase()).toContain('ground truth');
  });

  it('never contains an em dash', () => {
    const emDash = String.fromCharCode(0x2014); // banned repo-wide; written this way so no literal em dash sits in this file's own source
    for (const source of ['webhook', 'dashboard-generated', undefined, 'synthetic-demo']) {
      expect(noGroundTruthReason(source)).not.toContain(emDash);
    }
  });

  it('reads as a quiet statement of method, not an alarm: no exclamation marks', () => {
    expect(noGroundTruthReason('webhook')).not.toContain('!');
    expect(noGroundTruthReason(undefined)).not.toContain('!');
  });
});
