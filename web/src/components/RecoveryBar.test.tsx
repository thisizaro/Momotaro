// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { RecoveryBar } from '@/components/RecoveryBar';
import type { BatchReport } from '@/types';

afterEach(cleanup);

function makeReport(overrides: Partial<BatchReport> = {}): BatchReport {
  return {
    batch_id: 'b1',
    total_records: 100,
    in_flight_count: 0,
    at_risk_paise: 1000000,
    recovered_paise: 540000,
    intervention_spend_paise: 8000,
    net_recovered_paise: 532000,
    cost_per_rupee_recovered: 0.015,
    recovery_rate: 0.54,
    escalated_count: 16,
    closed_uneconomic_count: 41,
    closed_uneconomic_paise: 300000,
    processing_failure_count: 0,
    llm_quota_exhausted_count: 0,
    by_root_cause: {},
    by_intervention: {},
    generated_at: '2026-09-03T00:00:00Z',
    ...overrides,
  };
}

/**
 * The legend under the recovery progress bar used to read "In flight / lost
 * (X%)" for the unrecovered share, on a fully-settled batch with
 * in_flight_count: 0 (docs/DEMO_READINESS.md Unit AL). That is not "in
 * flight": the money it counts is either escalated or closed uneconomic,
 * both terminal, and the dashboard's own IN FLIGHT tile reads 0 on the same
 * screen, directly contradicting it. Fixed to "Not recovered", which is
 * true in every batch state rather than only while records are still
 * moving.
 */
describe('RecoveryBar', () => {
  it('labels the unrecovered share honestly, not as "in flight"', () => {
    render(<RecoveryBar report={makeReport()} />);
    expect(screen.getByText(/Not recovered/)).toBeInTheDocument();
    expect(screen.queryByText(/in flight/i)).not.toBeInTheDocument();
  });

  it('computes the unrecovered percentage from at_risk minus recovered', () => {
    render(<RecoveryBar report={makeReport({ at_risk_paise: 1000000, recovered_paise: 540000 })} />);
    // (1,000,000 - 540,000) / 1,000,000 = 46.0%
    expect(screen.getByText(/Not recovered \(46\.0%\)/)).toBeInTheDocument();
  });

  it('never crashes when at_risk_paise is zero', () => {
    expect(() =>
      render(<RecoveryBar report={makeReport({ at_risk_paise: 0, recovered_paise: 0 })} />)
    ).not.toThrow();
  });
});
