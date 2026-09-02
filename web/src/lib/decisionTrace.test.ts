import { describe, expect, it } from 'vitest';
import { pickWinningCandidate, sortCandidatesByEv } from '@/lib/decisionTrace';
import type { DecisionTraceCandidate } from '@/types';

/**
 * pickWinningCandidate must replicate economics.BestOf's exact rule
 * (services/decision-engine/internal/economics/score.go) rather than
 * parsing the entry's freeform `reason` string: highest ev_paise strictly
 * greater than zero, ties resolved to whichever comes first in the
 * server-supplied (unsorted) candidates array. docs/API_GATEWAY.md is
 * explicit that `reason` is not a machine contract.
 */
describe('pickWinningCandidate', () => {
  it('picks the highest positive EV candidate', () => {
    const candidates: DecisionTraceCandidate[] = [
      { action: 'ACTION_TYPE_RETRY', ev_paise: -625, cost_paise: 625, p_recovery: 0 },
      { action: 'ACTION_TYPE_NUDGE_METHOD_UPDATE', ev_paise: -29, cost_paise: 29, p_recovery: 0 },
      { action: 'ACTION_TYPE_NUDGE_REMINDER', ev_paise: 870.76, cost_paise: 35, p_recovery: 0.12 },
    ];
    expect(pickWinningCandidate(candidates)?.action).toBe('ACTION_TYPE_NUDGE_REMINDER');
  });

  it('picks nothing when every candidate has zero or negative EV, even though candidates exist', () => {
    // This is the ClosedUneconomic case: guardrails permitted actions, all
    // were scored, none was worth doing.
    const candidates: DecisionTraceCandidate[] = [
      { action: 'ACTION_TYPE_RETRY', ev_paise: -625, cost_paise: 625, p_recovery: 0 },
      { action: 'ACTION_TYPE_NUDGE_METHOD_UPDATE', ev_paise: 0, cost_paise: 29, p_recovery: 0 },
    ];
    expect(pickWinningCandidate(candidates)).toBeUndefined();
  });

  it('breaks ties by first-in-list order, not by which sorts first', () => {
    const candidates: DecisionTraceCandidate[] = [
      { action: 'ACTION_TYPE_RETRY', ev_paise: 100, cost_paise: 10, p_recovery: 0.5 },
      { action: 'ACTION_TYPE_NUDGE_REMINDER', ev_paise: 100, cost_paise: 20, p_recovery: 0.4 },
    ];
    expect(pickWinningCandidate(candidates)?.action).toBe('ACTION_TYPE_RETRY');
  });

  it('returns undefined for an empty or missing candidate list', () => {
    expect(pickWinningCandidate([])).toBeUndefined();
    expect(pickWinningCandidate(undefined)).toBeUndefined();
  });

  it('is not fooled by candidates arriving already sorted', () => {
    // If a future change to the wire order ever sorted candidates first,
    // the winner must still be identical, since it is defined by the value,
    // not by position after sorting.
    const candidates: DecisionTraceCandidate[] = [
      { action: 'ACTION_TYPE_NUDGE_REMINDER', ev_paise: 870.76, cost_paise: 35, p_recovery: 0.12 },
      { action: 'ACTION_TYPE_RETRY', ev_paise: -625, cost_paise: 625, p_recovery: 0 },
    ];
    expect(pickWinningCandidate(candidates)?.action).toBe('ACTION_TYPE_NUDGE_REMINDER');
  });
});

describe('sortCandidatesByEv', () => {
  it('sorts descending by ev_paise, independent of input order', () => {
    const candidates: DecisionTraceCandidate[] = [
      { action: 'ACTION_TYPE_RETRY', ev_paise: -625, cost_paise: 625, p_recovery: 0 },
      { action: 'ACTION_TYPE_NUDGE_METHOD_UPDATE', ev_paise: -29, cost_paise: 29, p_recovery: 0 },
      { action: 'ACTION_TYPE_NUDGE_REMINDER', ev_paise: 870.76, cost_paise: 35, p_recovery: 0.12 },
    ];
    const sorted = sortCandidatesByEv(candidates);
    expect(sorted.map((c) => c.action)).toEqual([
      'ACTION_TYPE_NUDGE_REMINDER',
      'ACTION_TYPE_NUDGE_METHOD_UPDATE',
      'ACTION_TYPE_RETRY',
    ]);
  });

  it('does not mutate the input array', () => {
    const candidates: DecisionTraceCandidate[] = [
      { action: 'ACTION_TYPE_RETRY', ev_paise: 1, cost_paise: 1, p_recovery: 1 },
      { action: 'ACTION_TYPE_NUDGE_REMINDER', ev_paise: 2, cost_paise: 1, p_recovery: 1 },
    ];
    const original = [...candidates];
    sortCandidatesByEv(candidates);
    expect(candidates).toEqual(original);
  });
});
