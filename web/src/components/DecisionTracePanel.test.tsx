// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { DecisionTracePanel } from '@/components/DecisionTracePanel';
import type { DecisionTrace } from '@/types';

// vitest's config here does not set `test.globals`, so @testing-library/react
// cannot auto-detect a global `afterEach` to register its own cleanup;
// without this, each render in this file would pile up in the same jsdom
// document and later assertions would match stale nodes from earlier tests.
afterEach(cleanup);

/**
 * The "why not the alternatives" panel (docs/DEMO_READINESS.md Unit S).
 * Always visible under a decision entry, no click required, so these tests
 * check what renders on first paint, not after any interaction.
 */
describe('DecisionTracePanel', () => {
  it('renders nothing at all when the entry has no trace', () => {
    const { container } = render(<DecisionTracePanel trace={undefined} atRiskPaise={68363} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders candidates sorted by EV descending, in rupees, with p_recovery as a percentage', () => {
    const trace: DecisionTrace = {
      candidates: [
        { action: 'ACTION_TYPE_RETRY', ev_paise: -625, cost_paise: 625, p_recovery: 0 },
        { action: 'ACTION_TYPE_NUDGE_METHOD_UPDATE', ev_paise: -29, cost_paise: 29, p_recovery: 0 },
        { action: 'ACTION_TYPE_NUDGE_REMINDER', ev_paise: 870.76, cost_paise: 35, p_recovery: 0.12 },
      ],
    };
    render(<DecisionTracePanel trace={trace} atRiskPaise={68363} />);

    const rows = screen.getAllByTestId('candidate-row');
    expect(rows).toHaveLength(3);
    // Winner (highest positive EV) sorts first, not first-in-wire-order.
    expect(rows[0]).toHaveTextContent('₹8.71');
    expect(rows[0]).toHaveTextContent('12%');
    expect(rows[1]).toHaveTextContent('₹0.29');
    expect(rows[2]).toHaveTextContent('₹6.25');
    // cost_paise is real money, formatted the same way.
    expect(rows[0]).toHaveTextContent('₹0.35');
  });

  it('marks the chosen action by value, not by row position', () => {
    // A tie: both candidates have EV 100, first-in-list wins per BestOf's
    // own tie-break rule, which is ACTION_TYPE_RETRY here even though
    // sorting for display could put either one first.
    const trace: DecisionTrace = {
      candidates: [
        { action: 'ACTION_TYPE_RETRY', ev_paise: 100, cost_paise: 10, p_recovery: 0.5 },
        { action: 'ACTION_TYPE_NUDGE_REMINDER', ev_paise: 100, cost_paise: 20, p_recovery: 0.4 },
      ],
    };
    render(<DecisionTracePanel trace={trace} atRiskPaise={10000} />);

    const winnerRows = screen.getAllByTestId('candidate-row').filter(
      (row) => row.querySelector('[data-testid="winner-mark"]') !== null,
    );
    expect(winnerRows).toHaveLength(1);
    expect(winnerRows[0]).toHaveTextContent('Retry');
  });

  it('marks no action as chosen when every candidate has zero or negative EV', () => {
    const trace: DecisionTrace = {
      candidates: [
        { action: 'ACTION_TYPE_RETRY', ev_paise: -10, cost_paise: 10, p_recovery: 0 },
        { action: 'ACTION_TYPE_NUDGE_METHOD_UPDATE', ev_paise: 0, cost_paise: 5, p_recovery: 0 },
      ],
    };
    render(<DecisionTracePanel trace={trace} atRiskPaise={10000} />);
    expect(screen.queryByTestId('winner-mark')).toBeNull();
  });

  it('renders blocked actions in their own section, never inside the candidate ranking', () => {
    const trace: DecisionTrace = {
      candidates: [{ action: 'ACTION_TYPE_NUDGE_REMINDER', ev_paise: 870.76, cost_paise: 35, p_recovery: 0.12 }],
      blocked: {
        ACTION_TYPE_RETRY: 'retry budget exhausted: 3 of 3 attempts used',
      },
    };
    render(<DecisionTracePanel trace={trace} atRiskPaise={68363} />);

    expect(screen.getByText(/blocked by guardrails/i)).toBeTruthy();
    expect(screen.getByText(/retry budget exhausted: 3 of 3 attempts used/)).toBeTruthy();
    // The blocked action's own label appears in the blocked section, but
    // never as a row inside the ranked candidate list.
    const candidateRows = screen.getAllByTestId('candidate-row');
    expect(candidateRows).toHaveLength(1);
    for (const row of candidateRows) {
      expect(row).not.toHaveTextContent('retry budget exhausted');
    }
  });

  it('renders a blocked-only trace with no candidates section at all', () => {
    const trace: DecisionTrace = {
      blocked: { ACTION_TYPE_NUDGE_REMINDER: 'contact cooldown active: last contact 73ms ago, cooldown is 288ms' },
    };
    render(<DecisionTracePanel trace={trace} atRiskPaise={68363} />);
    expect(screen.queryAllByTestId('candidate-row')).toHaveLength(0);
    expect(screen.getByText(/blocked by guardrails/i)).toBeTruthy();
  });

  it('shows the amount at risk converted to rupees via the shared formatter', () => {
    const trace: DecisionTrace = {
      candidates: [{ action: 'ACTION_TYPE_RETRY', ev_paise: 1, cost_paise: 1, p_recovery: 1 }],
    };
    render(<DecisionTracePanel trace={trace} atRiskPaise={68363} />);
    expect(screen.getByText('₹683.63')).toBeTruthy();
  });

  it('gives negative EV a rose color and positive EV an emerald color', () => {
    const trace: DecisionTrace = {
      candidates: [
        { action: 'ACTION_TYPE_RETRY', ev_paise: -625, cost_paise: 625, p_recovery: 0 },
        { action: 'ACTION_TYPE_NUDGE_REMINDER', ev_paise: 870.76, cost_paise: 35, p_recovery: 0.12 },
      ],
    };
    render(<DecisionTracePanel trace={trace} atRiskPaise={68363} />);
    const rows = screen.getAllByTestId('candidate-row');
    const negativeEv = rows.find((r) => r.textContent?.includes('6.25'));
    const positiveEv = rows.find((r) => r.textContent?.includes('8.71'));
    expect(negativeEv?.querySelector('.text-rose-600, .text-rose-700')).toBeTruthy();
    expect(positiveEv?.querySelector('.text-emerald-600, .text-emerald-700')).toBeTruthy();
  });

  it('lets a long guardrail reason wrap rather than overflow the panel', () => {
    const longReason =
      'contact cooldown active: last contact 73.169551123456789ms ago and the configured cooldown window for this channel is 288ms, so no further outbound contact is permitted until it elapses';
    const trace: DecisionTrace = {
      blocked: { ACTION_TYPE_NUDGE_REMINDER: longReason },
    };
    render(<DecisionTracePanel trace={trace} atRiskPaise={68363} />);
    const reasonEl = screen.getByText(longReason);
    expect(reasonEl.className).toMatch(/break-words|whitespace-normal/);
  });
});
