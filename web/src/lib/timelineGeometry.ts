import type { RootCauseBucket } from '@/types';

// Fixed order, not derived from what's present in a given batch: an empty
// row for a bucket like HARD_DECLINE (a dead card gets a nudge, never a
// retry, so it rarely has anything waiting or acted on) is exactly the
// signal a bucket-row layout exists to show, and it only reads as a signal
// if the row is still there to be empty. Shared by the Live and History
// timelines so a bucket sits in the same row in both.
export const TIMELINE_BUCKETS: RootCauseBucket[] = [
  'ROOT_CAUSE_BUCKET_TRANSIENT_BANK',
  'ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS',
  'ROOT_CAUSE_BUCKET_HARD_DECLINE',
  'ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED',
  'ROOT_CAUSE_BUCKET_RISK_HOLD',
  'ROOT_CAUSE_BUCKET_ABANDONMENT',
  'ROOT_CAUSE_BUCKET_OVERDUE',
];

export const TIMELINE_ROW_HEIGHT = 34;
export const TIMELINE_AXIS_HEIGHT = 32;
export const TIMELINE_LABEL_WIDTH = 176;
export const TIMELINE_TICK_COUNT = 5;

/**
 * Stable pseudo-random value in [-1, 1] derived from a record id, used to
 * jitter overlapping points vertically within their row so a tight cluster
 * reads as a cloud of dots rather than one dot hiding the rest. Shared by
 * both timelines so the same record lands at the same relative offset in
 * either view.
 */
export function jitter(id: string): number {
  let h = 0;
  for (let i = 0; i < id.length; i++) {
    h = (h * 31 + id.charCodeAt(i)) | 0;
  }
  return (h % 1000) / 1000;
}

export function clamp01(n: number): number {
  return Math.min(1, Math.max(0, n));
}

/**
 * Perceptual radius for a circle mark encoding amount_paise: area (not
 * radius) scales with the value, per the usual "bubble chart" rule, so a
 * 4x bigger amount reads as roughly 2x the radius rather than 4x. Clamped
 * to a band that stays legible at 100-record density: small enough that a
 * cluster of small amounts doesn't merge into a blob, large enough that
 * the biggest amount in a batch is still clearly the biggest dot.
 */
export function amountRadius(amountPaise: number, maxAmountPaise: number): number {
  const minR = 3.5;
  const maxR = 9;
  if (maxAmountPaise <= 0) return minR;
  const frac = clamp01(amountPaise / maxAmountPaise);
  return minR + Math.sqrt(frac) * (maxR - minR);
}
