// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { HistoryTimeline } from '@/components/HistoryTimeline';
import type { RecordSummary } from '@/types';

afterEach(cleanup);

function record(overrides: Partial<RecordSummary> = {}): RecordSummary {
  return {
    record_id: 'rec-1',
    type: 'RECORD_TYPE_PAYMENT',
    amount_paise: 50000,
    current_state: 'RECORD_STATE_RECOVERED',
    bucket: 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK',
    attempt_count: 1,
    spend_paise: 50,
    due_at: '',
    first_action_at: '2026-08-29T14:00:05Z',
    last_action_at: '2026-08-29T14:03:11Z',
    ...overrides,
  };
}

/**
 * The History view of the Live/History timeline toggle (docs/DEMO_READINESS.md
 * Unit AH): what actually happened across a run, built from first_action_at/
 * last_action_at rather than the future-only due_at.
 */
describe('HistoryTimeline', () => {
  it('renders the empty state when no record has a first_action_at yet', () => {
    const onSelect = vi.fn();
    render(
      <HistoryTimeline
        records={[record({ record_id: 'r1', current_state: 'RECORD_STATE_NEW', first_action_at: '', last_action_at: '' })]}
        onSelect={onSelect}
      />,
    );
    expect(screen.getByText(/nothing has happened yet/i)).toBeTruthy();
    expect(screen.queryAllByTestId('history-point')).toHaveLength(0);
  });

  it('excludes records with no first_action_at even when other records have history', () => {
    const onSelect = vi.fn();
    render(
      <HistoryTimeline
        records={[
          record({ record_id: 'acted', first_action_at: '2026-08-29T14:00:05Z', last_action_at: '2026-08-29T14:03:11Z' }),
          record({ record_id: 'fresh', current_state: 'RECORD_STATE_NEW', first_action_at: '', last_action_at: '' }),
        ]}
        onSelect={onSelect}
      />,
    );
    expect(screen.getAllByTestId('history-point')).toHaveLength(1);
    expect(screen.getByText(/1 record the agent has acted on/i)).toBeTruthy();
  });

  it('calls onSelect with the record id when a point is clicked', () => {
    const onSelect = vi.fn();
    render(<HistoryTimeline records={[record({ record_id: 'rec-42' })]} onSelect={onSelect} />);
    fireEvent.click(screen.getByTestId('history-point'));
    expect(onSelect).toHaveBeenCalledWith('rec-42');
  });

  it('hover content (the point title) names bucket, amount, state and both action times', () => {
    render(
      <HistoryTimeline
        records={[
          record({
            record_id: 'rec-1',
            bucket: 'ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS',
            amount_paise: 123456,
            current_state: 'RECORD_STATE_ESCALATED',
            first_action_at: '2026-08-29T14:00:05Z',
            last_action_at: '2026-08-29T14:03:11Z',
          }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    const title = screen.getByTestId('history-point').querySelector('title');
    expect(title).not.toBeNull();
    const text = title!.textContent ?? '';
    expect(text).toContain('Insufficient Funds');
    expect(text).toContain('₹1,235');
    expect(text).toContain('Escalated');
    expect(text).toContain('started');
    expect(text).toContain('last action');
  });

  it('collapses the tooltip to a single time when first and last action coincide', () => {
    render(
      <HistoryTimeline
        records={[record({ first_action_at: '2026-08-29T14:00:05Z', last_action_at: '2026-08-29T14:00:05Z' })]}
        onSelect={vi.fn()}
      />,
    );
    const text = screen.getByTestId('history-point').querySelector('title')!.textContent ?? '';
    expect(text).not.toContain('last action');
  });

  it('renders a larger marker for a larger amount, all else equal', () => {
    render(
      <HistoryTimeline
        records={[
          record({ record_id: 'small', amount_paise: 1000, bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE' }),
          record({ record_id: 'big', amount_paise: 500000, bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    const points = screen.getAllByTestId('history-point');
    const radii = points.map((g) => Number(g.querySelector('circle')!.getAttribute('r')));
    expect(radii[1]).toBeGreaterThan(radii[0]);
  });

  it('renders the real wall-clock time and the simulated recovery-window equivalent on the axis', () => {
    const { container } = render(
      <HistoryTimeline
        records={[record({ first_action_at: '2026-08-29T14:00:05Z', last_action_at: '2026-08-29T14:03:11Z' })]}
        onSelect={vi.fn()}
      />,
    );
    const axisText = Array.from(container.querySelectorAll('text')).map((t) => t.textContent ?? '');
    // Real wall-clock ticks read like "14:00:07" (HH:MM:SS, hour12: false).
    expect(axisText.some((t) => /^\d{2}:\d{2}:\d{2}$/.test(t))).toBe(true);
    // The simulated equivalent is framed against the 7-day recovery window.
    expect(axisText.some((t) => /recovery window/.test(t))).toBe(true);
  });

  it('shows only the states actually present in the legend, not all nine', () => {
    render(
      <HistoryTimeline
        records={[record({ record_id: 'r1', current_state: 'RECORD_STATE_RECOVERED' })]}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.getByText('Recovered')).toBeTruthy();
    expect(screen.queryByText('Closed (Uneconomic)')).toBeNull();
    expect(screen.queryByText('Escalated')).toBeNull();
  });
});
