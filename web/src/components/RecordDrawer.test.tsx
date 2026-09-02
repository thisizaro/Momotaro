// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { RecordDrawer } from '@/components/RecordDrawer';
import { formatDuration } from '@/lib/format';
import { formatSimulatedElapsed, formatSimulatedGap } from '@/lib/demoTime';
import type { AuditEntry, RecordAuditResponse } from '@/types';

afterEach(cleanup);

function makeEntry(overrides: Partial<AuditEntry>): AuditEntry {
  return {
    ts: '2026-08-29T14:00:05.000Z',
    from_state: 'RECORD_STATE_NEW',
    to_state: 'RECORD_STATE_SCORING',
    reason: 'classified',
    rationale: '',
    source: 'SOURCE_UNSPECIFIED',
    actor: 'system',
    attempt_number: 0,
    cost_paise: 0,
    message_text: '',
    hops: [],
    ...overrides,
  };
}

function makeDetail(entries: AuditEntry[], overrides: Partial<RecordAuditResponse> = {}): RecordAuditResponse {
  return {
    record: {
      id: 'rec_ab12cd34',
      batch_id: 'batch_1',
      type: 'RECORD_TYPE_PAYMENT',
      amount_paise: 24727,
      currency: 'INR',
      failure_code: 'bank_not_available',
      created_at: '2026-08-29T14:00:00.000Z',
      instrument_ref: 'card_ref_1',
    },
    current_state: 'RECORD_STATE_RETRYING',
    trail_complete: true,
    entries,
    ...overrides,
  };
}

// Every test in here uses one closing structure: open, detail, loading and
// error stay at their defaults unless a test says otherwise.
const baseProps = {
  open: true,
  loading: false,
  error: null,
  onClose: () => {},
};

describe('RecordDrawer', () => {
  it('renders nothing when closed, even with a detail already loaded', () => {
    const { container } = render(
      <RecordDrawer {...baseProps} open={false} detail={makeDetail([makeEntry({})])} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('keeps the loading spinner working', () => {
    render(<RecordDrawer {...baseProps} detail={null} loading />);
    expect(screen.queryByText('Record Detail')).toBeNull();
  });

  it('keeps the error state working, with retry wired', () => {
    const onRetry = vi.fn();
    render(<RecordDrawer {...baseProps} detail={null} error="boom" onRetry={onRetry} />);
    expect(screen.getByText('boom')).toBeTruthy();
  });

  it('shows the record id, amount and current state in the sticky header', () => {
    render(<RecordDrawer {...baseProps} detail={makeDetail([makeEntry({})])} />);
    const header = screen.getByTestId('drawer-sticky-header');
    expect(header).toHaveTextContent('rec_ab12cd34');
    expect(header).toHaveTextContent('₹247');
    expect(header).toHaveTextContent('Retrying');
  });

  // "No trace" edge case: a trail with zero entries must not crash reading
  // entries[-1] or entries[0], and reads as an honest empty state rather
  // than a spine with nothing on it.
  it('renders a trail with zero entries without crashing, and says so', () => {
    render(<RecordDrawer {...baseProps} detail={makeDetail([])} />);
    expect(screen.getByText(/audit trail \(0\)/i)).toBeTruthy();
    expect(screen.queryAllByTestId('audit-entry')).toHaveLength(0);
    expect(screen.queryByTestId('entry-gap')).toBeNull();
  });

  // Single-entry edge case: there is no previous entry to gap against, so
  // no gap chip renders, only the absolute simulated position (which is
  // "0 min into the window" for the trail's own first entry).
  it('renders a single-entry trail with a position but no gap', () => {
    render(<RecordDrawer {...baseProps} detail={makeDetail([makeEntry({ ts: '2026-08-29T14:00:05.000Z' })])} />);
    const entries = screen.getAllByTestId('audit-entry');
    expect(entries).toHaveLength(1);
    expect(entries[0]).toHaveTextContent(formatSimulatedElapsed(0));
    expect(screen.queryByTestId('entry-gap')).toBeNull();
  });

  it('shows the real clock time on every entry, unchanged in substance', () => {
    render(<RecordDrawer {...baseProps} detail={makeDetail([makeEntry({ ts: '2026-08-29T14:00:05.000Z' })])} />);
    const entries = screen.getAllByTestId('audit-entry');
    // formatTime renders in the runner's local timezone; check the entry
    // carries *a* real time string with the expected shape rather than
    // hardcoding a timezone-dependent hour.
    expect(entries[0].textContent).toMatch(/\d{2}:\d{2}:\d{2}/);
  });

  it('computes the elapsed gap between two entries from their real timestamps, both real and simulated', () => {
    // Chosen to match demoTime.test.ts's own hand-checked case: 1 real
    // second of gap is 300000 simulated seconds, 3.4722 days, which
    // formatSimulatedGap renders as "3.5 days".
    const t0 = '2026-08-29T14:00:05.000Z';
    const t1 = '2026-08-29T14:00:06.000Z'; // +1000ms real
    const detail = makeDetail([makeEntry({ ts: t0 }), makeEntry({ ts: t1, from_state: 'RECORD_STATE_SCORING', to_state: 'RECORD_STATE_RETRY_SCHEDULED' })]);
    render(<RecordDrawer {...baseProps} detail={detail} />);

    const gapChips = screen.getAllByTestId('entry-gap');
    // Only the second entry has a previous entry to gap against.
    expect(gapChips).toHaveLength(1);
    expect(gapChips[0]).toHaveTextContent(`+${formatDuration(1000)} real`);
    expect(gapChips[0]).toHaveTextContent(`+${formatSimulatedGap(1000)} simulated`);
  });

  it("positions each entry against the trail's own first timestamp, not against the previous entry", () => {
    const t0 = '2026-08-29T14:00:00.000Z';
    const t1 = '2026-08-29T14:00:01.000Z'; // +1000ms from t0
    const t2 = '2026-08-29T14:00:05.000Z'; // +4000ms from t1, +5000ms from t0
    const detail = makeDetail([
      makeEntry({ ts: t0 }),
      makeEntry({ ts: t1, from_state: 'RECORD_STATE_SCORING', to_state: 'RECORD_STATE_RETRY_SCHEDULED' }),
      makeEntry({ ts: t2, from_state: 'RECORD_STATE_RETRY_SCHEDULED', to_state: 'RECORD_STATE_RETRYING' }),
    ]);
    render(<RecordDrawer {...baseProps} detail={detail} />);

    const entries = screen.getAllByTestId('audit-entry');
    // The third entry's position is elapsed-since-t0 (5000ms real). A buggy
    // implementation measuring from the previous entry instead would show
    // formatSimulatedElapsed(4000) here, a different, wrong day count.
    expect(entries[2]).toHaveTextContent(formatSimulatedElapsed(5000));
    expect(entries[2]).not.toHaveTextContent(formatSimulatedElapsed(4000));
  });

  it('gives the genuinely varying sources a visible marker and leaves the routine one quiet', () => {
    const detail = makeDetail([
      makeEntry({ source: 'SOURCE_UNSPECIFIED' }),
      makeEntry({
        ts: '2026-08-29T14:00:06.000Z',
        from_state: 'RECORD_STATE_SCORING',
        to_state: 'RECORD_STATE_RETRY_SCHEDULED',
        source: 'SOURCE_LLM',
      }),
      makeEntry({
        ts: '2026-08-29T14:00:07.000Z',
        from_state: 'RECORD_STATE_RETRY_SCHEDULED',
        to_state: 'RECORD_STATE_RETRYING',
        source: 'SOURCE_RULES_FALLBACK',
      }),
    ]);
    render(<RecordDrawer {...baseProps} detail={detail} />);

    const badges = screen.getAllByTestId('source-badge');
    expect(badges).toHaveLength(2);
    expect(badges.map((b) => b.textContent)).toEqual(
      expect.arrayContaining([expect.stringContaining('LLM'), expect.stringContaining('rules')]),
    );
    // The routine source is still readable, just not badged.
    const entries = screen.getAllByTestId('audit-entry');
    expect(entries[0]).toHaveTextContent(/SOURCE_UNSPECIFIED|system/i);
  });

  it('still renders the composed message and the decision trace panel for the entry that carries them', () => {
    const detail = makeDetail([
      makeEntry({
        message_text: 'Aapka payment fail ho gaya, please retry karein.',
        decision_trace: { candidates: [{ action: 'ACTION_TYPE_RETRY', ev_paise: 100, cost_paise: 10, p_recovery: 0.5 }] },
      }),
    ]);
    render(<RecordDrawer {...baseProps} detail={detail} />);
    expect(screen.getByText(/Aapka payment fail ho gaya/)).toBeTruthy();
    expect(screen.getByText('Why this action')).toBeTruthy();
  });
});
