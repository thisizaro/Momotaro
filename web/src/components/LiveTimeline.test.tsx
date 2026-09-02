// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { LiveTimeline } from '@/components/LiveTimeline';
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
    due_at: new Date(Date.now() + 60000).toISOString(),
    first_action_at: '2026-08-29T14:00:05Z',
    last_action_at: '2026-08-29T14:00:05Z',
    ...overrides,
  };
}

describe('LiveTimeline', () => {
  it('shows the empty state when nothing has a due_at, and points at History instead', () => {
    render(<LiveTimeline records={[record({ due_at: '' })]} onSelect={vi.fn()} />);
    expect(screen.getByText(/nothing pending right now/i)).toBeTruthy();
    expect(screen.getByText(/switch to history/i)).toBeTruthy();
  });

  it('excludes terminal and NUDGED records even though they are in the input, per due_at semantics', () => {
    render(
      <LiveTimeline
        records={[
          record({ record_id: 'terminal', current_state: 'RECORD_STATE_RECOVERED', due_at: '' }),
          record({ record_id: 'nudged', current_state: 'RECORD_STATE_NUDGED', due_at: '' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.getByText(/nothing pending right now/i)).toBeTruthy();
  });

  it('calls onSelect with the record id when a scheduled point is clicked', () => {
    const onSelect = vi.fn();
    render(<LiveTimeline records={[record({ record_id: 'rec-9' })]} onSelect={onSelect} />);
    fireEvent.click(screen.getByTestId('live-point'));
    expect(onSelect).toHaveBeenCalledWith('rec-9');
  });

  it('hover content names bucket, amount, and the current action/state', () => {
    render(
      <LiveTimeline
        records={[
          record({
            bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE',
            amount_paise: 20000,
            current_state: 'RECORD_STATE_RETRY_SCHEDULED',
          }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    const title = screen.getByTestId('live-point').querySelector('title');
    const text = title!.textContent ?? '';
    expect(text).toContain('Hard Decline');
    expect(text).toContain('₹200');
    expect(text).toContain('Retry Scheduled');
    expect(text).toMatch(/due (now|in)/);
  });
});
