/**
 * Copy shared between the Classification Accuracy panel (App.tsx) and
 * BaselineComparisonCard for a batch with no GROUND_TRUTH row
 * (docs/API_GATEWAY.md: `accuracy` and `baseline_comparison` are both
 * absent, never present as null or zeroed, when the batch has none, "a
 * missing key means no answer key exists, distinct from a real zero").
 * Kept as one function so the two panels always say the same thing rather
 * than drifting into two slightly different explanations of the same fact.
 *
 * `source` is the batch's own `source` column, already exposed on the wire
 * by `GET /v1/batches` (`BatchSummary.source`, docs/API_GATEWAY.md) with no
 * contract change needed for this: `App.tsx` already holds the batch list
 * it came from, so the active batch's source is looked up from state
 * already on hand rather than fetched again.
 *
 * `"webhook"` is the always-on rolling batch every production webhook event
 * attaches to (services/ingestion/internal/server/store.go's
 * `rollingBatchSource`, `docs/DEMO_READINESS.md` Unit AJ's own CLI posts
 * here). Anything else without ground truth (a `count`-submitted batch, an
 * unrecognised source) still gets an honest, generic reason rather than
 * silently assuming which case it is: the one fact that is always true is
 * "no sealed ground truth", so that is the one thing every branch below
 * leads with.
 */
export function noGroundTruthReason(source: string | undefined): string {
  if (source === 'webhook') {
    return (
      'No ground truth for this batch: it is live production traffic, ' +
      'arrived through the webhook API the same way real events would. ' +
      'Production traffic is never sealed with a hidden answer key. That ' +
      'is exactly why a seeded batch exists alongside it: seed one from ' +
      'Demo Controls to see accuracy and the baseline comparison scored.'
    );
  }
  return (
    'No ground truth for this batch: it was not seeded with a hidden ' +
    'answer key. Seed a batch from Demo Controls to see accuracy and the ' +
    'baseline comparison scored.'
  );
}
