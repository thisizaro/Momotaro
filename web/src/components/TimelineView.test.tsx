// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { TimelineView } from '@/components/TimelineView';
import type { RecordSummary } from '@/types';

afterEach(cleanup);

function record(overrides: Partial<RecordSummary> = {}): RecordSummary {
  return {
    record_id: 'rec-1',
    type: 'RECORD_TYPE_PAYMENT',
    amount_paise: 50000,
    current_state: 'RECORD_STATE_RETRY_SCHEDULED',
    bucket: 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK',
    attempt_count: 1,
    spend_paise: 0,
    due_at: '',
    first_action_at: '',
    last_action_at: '',
    ...overrides,
  };
}

/**
 * The Live/History toggle container (docs/DEMO_READINESS.md Unit AH). The
 * headline behaviour under test: a finished run (nothing pending, but
 * history exists) must not open on the empty Live view, since that is
 * literally the bug this unit exists to fix.
 */
describe('TimelineView', () => {
  it('defaults to Live when something is pending, regardless of history', () => {
    render(
      <TimelineView
        records={[
          record({ record_id: 'pending', due_at: new Date(Date.now() + 30000).toISOString() }),
          record({ record_id: 'past', first_action_at: '2026-08-29T14:00:00Z', last_action_at: '2026-08-29T14:01:00Z' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.getByRole('tab', { name: /live/i })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: /history/i })).toHaveAttribute('aria-selected', 'false');
    expect(screen.getByText(/1 record.*waiting on the scheduler/i)).toBeTruthy();
  });

  it('defaults to History when nothing is pending but the run has history, instead of showing an empty Live panel', () => {
    render(
      <TimelineView
        records={[
          record({ record_id: 'done', current_state: 'RECORD_STATE_RECOVERED', first_action_at: '2026-08-29T14:00:00Z', last_action_at: '2026-08-29T14:01:00Z' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.getByRole('tab', { name: /history/i })).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByText(/nothing pending right now/i)).toBeNull();
    expect(screen.getByText(/1 record the agent has acted on/i)).toBeTruthy();
  });

  it('defaults to Live (its own empty state) when there is neither anything pending nor any history', () => {
    render(<TimelineView records={[]} onSelect={vi.fn()} />);
    expect(screen.getByRole('tab', { name: /live/i })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText(/nothing pending right now/i)).toBeTruthy();
  });

  it('switches views on click and shows live/history counts as badges', () => {
    render(
      <TimelineView
        records={[
          record({ record_id: 'pending', due_at: new Date(Date.now() + 30000).toISOString() }),
          record({ record_id: 'past', first_action_at: '2026-08-29T14:00:00Z', last_action_at: '2026-08-29T14:01:00Z' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    // Live is the default here (something is pending); History tab shows a
    // count of 1 even while not selected.
    expect(screen.getByRole('tab', { name: /history/i })).toHaveTextContent('1');

    fireEvent.click(screen.getByRole('tab', { name: /history/i }));
    expect(screen.getByRole('tab', { name: /history/i })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText(/1 record the agent has acted on/i)).toBeTruthy();

    fireEvent.click(screen.getByRole('tab', { name: /live/i }));
    expect(screen.getByRole('tab', { name: /live/i })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText(/1 record.*waiting on the scheduler/i)).toBeTruthy();
  });

  it('forwards onSelect through to the active view so clicking a point opens the drawer', () => {
    const onSelect = vi.fn();
    render(
      <TimelineView
        records={[record({ record_id: 'rec-77', due_at: new Date(Date.now() + 30000).toISOString() })]}
        onSelect={onSelect}
      />,
    );
    fireEvent.click(screen.getByTestId('live-point'));
    expect(onSelect).toHaveBeenCalledWith('rec-77');
  });
});
