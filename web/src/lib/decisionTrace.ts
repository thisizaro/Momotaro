import type { DecisionTraceCandidate } from '@/types';

/**
 * Replicates economics.BestOf exactly
 * (services/decision-engine/internal/economics/score.go): the highest
 * ev_paise that is also strictly greater than zero, ties resolved to
 * whichever candidate comes first in the array as given (the guardrail
 * order the wire delivers, not sorted). Returns undefined when nothing
 * clears zero, which is the real ClosedUneconomic case: candidates were
 * scored and none was worth doing.
 *
 * Deliberately does not parse `entry.reason` to find the winner.
 * docs/API_GATEWAY.md says plainly that `reason` names the winner in prose
 * for a human, but is not a machine contract.
 */
export function pickWinningCandidate(
  candidates: DecisionTraceCandidate[] | undefined,
): DecisionTraceCandidate | undefined {
  if (!candidates || candidates.length === 0) return undefined;

  let best: DecisionTraceCandidate | undefined;
  for (const candidate of candidates) {
    if (candidate.ev_paise <= 0) continue;
    if (!best || candidate.ev_paise > best.ev_paise) {
      best = candidate;
    }
  }
  return best;
}

/**
 * For display only: highest ev_paise first, so the winner naturally reads
 * as the top row. Never used to determine the winner itself (see
 * pickWinningCandidate above); sorting first would silently change tie
 * resolution.
 */
export function sortCandidatesByEv(candidates: DecisionTraceCandidate[]): DecisionTraceCandidate[] {
  return [...candidates].sort((a, b) => b.ev_paise - a.ev_paise);
}
