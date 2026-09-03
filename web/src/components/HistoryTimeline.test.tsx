// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { HistoryTimeline } from '@/components/HistoryTimeline';
import { BUCKET_COLORS } from '@/lib/format';
import { TIMELINE_BUCKETS, TIMELINE_ROW_HEIGHT } from '@/lib/timelineGeometry';
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
    const { container } = render(
      <HistoryTimeline
        records={[
          record({ record_id: 'small', amount_paise: 1000, bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE' }),
          record({ record_id: 'big', amount_paise: 500000, bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    const radiusOf = (id: string) =>
      Number(container.querySelector(`[data-record-id="${id}"] circle`)!.getAttribute('r'));
    expect(radiusOf('big')).toBeGreaterThan(radiusOf('small'));
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

/**
 * Unit AP (docs/DEMO_READINESS.md), after direct user review of Unit AO's
 * per-record Gantt: "too much scrolling and so gapped... the initial view of
 * the last one was better, it gave a better idea in one view". The compact,
 * one-row-per-bucket layout Unit AH originally shipped is the default again.
 * What Unit AO added stays: the neutral connector colour, the caption
 * contrast fix, click-to-isolate a bucket, click-to-filter an outcome,
 * hover-to-highlight, and the filter chips. What changes is that these now
 * apply to a fixed-height, always-fits, jittered single row per bucket
 * rather than a per-record sub-row band, and the per-record view (Unit AO's
 * layout) survives as an opt-in "Per-record" mode, not the default.
 */
describe('HistoryTimeline: compact view is the default (Unit AP)', () => {
  it('opens on the Compact view, not Per-record', () => {
    render(<HistoryTimeline records={[record()]} onSelect={vi.fn()} />);
    expect(screen.getByTestId('view-toggle-compact')).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByTestId('view-toggle-gantt')).toHaveAttribute('aria-selected', 'false');
  });

  it('gives every bucket exactly one row, at a fixed height that does not grow with record count', () => {
    const many = Array.from({ length: 90 }, (_, i) =>
      record({
        record_id: `rec-${i}`,
        bucket: TIMELINE_BUCKETS[i % TIMELINE_BUCKETS.length],
        first_action_at: '2026-08-29T14:00:00Z',
        last_action_at: '2026-08-29T14:01:00Z',
      }),
    );
    const { container } = render(<HistoryTimeline records={many} onSelect={vi.fn()} />);

    expect(screen.getAllByTestId('bucket-row')).toHaveLength(TIMELINE_BUCKETS.length);
    // Every one of the 90 records still draws a point: density is handled by
    // jitter and opacity, not by hiding records or growing the chart.
    expect(screen.getAllByTestId('history-point')).toHaveLength(90);

    const recordSvg = container.querySelector('svg[data-testid="history-svg"]')!;
    // Fixed height: one row per bucket, nothing scales with the 90 records.
    expect(Number(recordSvg.getAttribute('height'))).toBe(TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT);
  });

  it('needs no scrolling at typical batch density (80-100 records across 7 buckets)', () => {
    const many = Array.from({ length: 96 }, (_, i) =>
      record({
        record_id: `rec-${i}`,
        bucket: TIMELINE_BUCKETS[i % TIMELINE_BUCKETS.length],
      }),
    );
    render(<HistoryTimeline records={many} onSelect={vi.fn()} />);
    const body = screen.getByTestId('history-scroll-body');
    const recordSvg = body.querySelector('svg[data-testid="history-svg"]')!;
    const contentHeight = Number(recordSvg.getAttribute('height'));
    // The scrollable wrapper caps at TIMELINE_MAX_BODY_HEIGHT (480); a
    // compact view's content must sit well under that regardless of count.
    expect(contentHeight).toBeLessThan(300);
  });

  it('draws the connector in a neutral slate, not the bucket colour, so the marker colour carries the meaning', () => {
    render(
      <HistoryTimeline
        records={[
          record({
            bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE',
            first_action_at: '2026-08-29T14:00:00Z',
            last_action_at: '2026-08-29T14:10:00Z',
          }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    const line = screen.getByTestId('history-connector');
    expect(line.getAttribute('stroke')).not.toBe(BUCKET_COLORS.ROOT_CAUSE_BUCKET_HARD_DECLINE);
    expect(line.getAttribute('stroke')?.toLowerCase()).toBe('#cbd5e1');
  });

  it('renders the amount-at-risk caption with readable contrast, not near-invisible grey', () => {
    render(<HistoryTimeline records={[record()]} onSelect={vi.fn()} />);
    const caption = screen.getByText('circle size = amount at risk');
    expect(caption.className).not.toMatch(/text-slate-300/);
  });

  it('isolates a bucket on click and restores the full view on a second click', () => {
    render(
      <HistoryTimeline
        records={[
          record({ record_id: 'a', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT' }),
          record({ record_id: 'b', bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.getAllByTestId('history-point')).toHaveLength(2);

    const abandonmentRow = screen.getByText('Abandonment').closest('button')!;
    fireEvent.click(abandonmentRow);
    expect(screen.getAllByTestId('history-point')).toHaveLength(1);
    expect(screen.getByTestId('active-filter-bucket')).toHaveTextContent('Abandonment');

    fireEvent.click(abandonmentRow);
    expect(screen.getAllByTestId('history-point')).toHaveLength(2);
    expect(screen.queryByTestId('active-filter-bucket')).toBeNull();
  });

  it('filters by outcome via the legend, and composes with an active bucket filter', () => {
    render(
      <HistoryTimeline
        records={[
          record({ record_id: 'a', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT', current_state: 'RECORD_STATE_RECOVERED' }),
          record({ record_id: 'b', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT', current_state: 'RECORD_STATE_ESCALATED' }),
          record({ record_id: 'c', bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE', current_state: 'RECORD_STATE_RECOVERED' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByText('Recovered').closest('button')!);
    expect(screen.getAllByTestId('history-point')).toHaveLength(2);

    fireEvent.click(screen.getByText('Abandonment').closest('button')!);
    expect(screen.getAllByTestId('history-point')).toHaveLength(1);

    fireEvent.click(screen.getByTestId('clear-filters'));
    expect(screen.getAllByTestId('history-point')).toHaveLength(3);
  });

  it('shows an honest empty state, not a blank panel, when the combined filters match nothing', () => {
    render(
      <HistoryTimeline
        records={[
          record({ record_id: 'a', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT', current_state: 'RECORD_STATE_RECOVERED' }),
          record({ record_id: 'b', bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE', current_state: 'RECORD_STATE_ESCALATED' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByText('Abandonment').closest('button')!);
    fireEvent.click(screen.getByText('Escalated').closest('button')!);
    expect(screen.queryAllByTestId('history-point')).toHaveLength(0);
    expect(screen.getByText(/no records match/i)).toBeTruthy();
    // An obvious way back stays visible even though the chart is empty.
    expect(screen.getByTestId('clear-filters')).toBeTruthy();
  });

  it('highlights the hovered record and dims the others', () => {
    render(
      <HistoryTimeline
        records={[
          record({ record_id: 'a', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT' }),
          record({ record_id: 'b', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    const points = screen.getAllByTestId('history-point');
    fireEvent.mouseEnter(points[0]);
    const circleA = points[0].querySelector('circle')!;
    const circleB = points[1].querySelector('circle')!;
    expect(Number(circleA.getAttribute('fill-opacity'))).toBeGreaterThan(Number(circleB.getAttribute('fill-opacity')));

    fireEvent.mouseLeave(points[0]);
    expect(circleA.getAttribute('fill-opacity')).toBe(circleB.getAttribute('fill-opacity'));
  });

  it('still calls onSelect when a marker is clicked, even while hovering', () => {
    const onSelect = vi.fn();
    render(
      <HistoryTimeline
        records={[record({ record_id: 'rec-99', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT' })]}
        onSelect={onSelect}
      />,
    );
    const point = screen.getByTestId('history-point');
    fireEvent.mouseEnter(point);
    fireEvent.click(point);
    expect(onSelect).toHaveBeenCalledWith('rec-99');
  });
});

/**
 * The per-record Gantt layout Unit AO built is not gone, it is opt-in: Unit
 * AP's "Per-record" toggle switches HistoryTimeline into exactly that
 * layout, one sub-row per record, a capped and internally-scrolling record
 * area for a dense bucket. Reached deliberately, never shown first.
 */
describe('HistoryTimeline: per-record Gantt view is opt-in (Unit AP)', () => {
  it('switches into a per-record sub-row layout when Per-record is selected', () => {
    render(
      <HistoryTimeline
        records={[
          record({ record_id: 'a', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT', first_action_at: '2026-08-29T14:00:00Z', last_action_at: '2026-08-29T14:10:00Z' }),
          record({ record_id: 'b', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT', first_action_at: '2026-08-29T14:01:00Z', last_action_at: '2026-08-29T14:09:00Z' }),
          record({ record_id: 'c', bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT', first_action_at: '2026-08-29T14:02:00Z', last_action_at: '2026-08-29T14:08:00Z' }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId('view-toggle-gantt'));
    expect(screen.getByTestId('view-toggle-gantt')).toHaveAttribute('aria-selected', 'true');

    const points = screen.getAllByTestId('history-point');
    expect(points).toHaveLength(3);
    const cys = points.map((g) => g.querySelector('circle')!.getAttribute('cy'));
    expect(new Set(cys).size).toBe(3);
  });

  it('caps the record area height and scrolls internally rather than growing unboundedly', () => {
    const many = Array.from({ length: 40 }, (_, i) =>
      record({
        record_id: `r${i}`,
        bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT',
        first_action_at: '2026-08-29T14:00:00Z',
        last_action_at: '2026-08-29T14:01:00Z',
      }),
    );
    render(<HistoryTimeline records={many} onSelect={vi.fn()} />);
    fireEvent.click(screen.getByTestId('view-toggle-gantt'));
    const body = screen.getByTestId('history-scroll-body');
    expect(body.style.maxHeight).not.toBe('');
    expect(body.style.overflowY).toBe('auto');
  });

  it('returns to the fixed-height compact layout when Compact is clicked again', () => {
    const many = Array.from({ length: 40 }, (_, i) =>
      record({
        record_id: `r${i}`,
        bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT',
      }),
    );
    const { container } = render(<HistoryTimeline records={many} onSelect={vi.fn()} />);
    fireEvent.click(screen.getByTestId('view-toggle-gantt'));
    fireEvent.click(screen.getByTestId('view-toggle-compact'));
    expect(screen.getByTestId('view-toggle-compact')).toHaveAttribute('aria-selected', 'true');
    const recordSvg = container.querySelector('svg[data-testid="history-svg"]')!;
    expect(Number(recordSvg.getAttribute('height'))).toBe(TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT);
  });
});

/**
 * Search (Unit AP): the user asked for it directly ("we could have added...
 * or search a specific entry"). Matches a record's id (substring, so a short
 * prefix like the table's `f43f0a35` works) or its amount in rupees
 * (substring on the digits, so "1235" finds a ₹1,235 record). Narrows the
 * view to the match, the same isolate mechanism bucket/outcome filters
 * already use, and is honest when nothing matches.
 */
describe('HistoryTimeline: search (Unit AP)', () => {
  const searchRecords: RecordSummary[] = [
    record({ record_id: 'f43f0a35', amount_paise: 123456, bucket: 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK' }),
    record({ record_id: '9c11bd02', amount_paise: 999900, bucket: 'ROOT_CAUSE_BUCKET_HARD_DECLINE' }),
    record({ record_id: 'aa22cc33', amount_paise: 50000, bucket: 'ROOT_CAUSE_BUCKET_ABANDONMENT' }),
  ];

  it('matches on a record id prefix and narrows the view to it', () => {
    render(<HistoryTimeline records={searchRecords} onSelect={vi.fn()} />);
    fireEvent.change(screen.getByTestId('timeline-search-input'), { target: { value: 'f43f' } });
    expect(screen.getAllByTestId('history-point')).toHaveLength(1);
    expect(screen.getByTestId('history-point')).toHaveAttribute('data-record-id', 'f43f0a35');
    expect(screen.getByTestId('active-filter-search')).toHaveTextContent('f43f');
  });

  it('matches on amount (rupees) as well as id', () => {
    render(<HistoryTimeline records={searchRecords} onSelect={vi.fn()} />);
    // amount_paise 123456 -> ₹1,235, so "1235" should find it by amount alone.
    fireEvent.change(screen.getByTestId('timeline-search-input'), { target: { value: '1235' } });
    expect(screen.getAllByTestId('history-point')).toHaveLength(1);
    expect(screen.getByTestId('history-point')).toHaveAttribute('data-record-id', 'f43f0a35');
  });

  it('composes with an active bucket filter', () => {
    render(<HistoryTimeline records={searchRecords} onSelect={vi.fn()} />);
    fireEvent.change(screen.getByTestId('timeline-search-input'), { target: { value: 'a' } });
    // "a" alone would match more than one id; narrow further with a bucket isolate.
    fireEvent.click(screen.getByText('Abandonment').closest('button')!);
    expect(screen.getAllByTestId('history-point')).toHaveLength(1);
    expect(screen.getByTestId('history-point')).toHaveAttribute('data-record-id', 'aa22cc33');
  });

  it('shows an honest empty state naming the query when nothing matches, and clears via the chip', () => {
    render(<HistoryTimeline records={searchRecords} onSelect={vi.fn()} />);
    fireEvent.change(screen.getByTestId('timeline-search-input'), { target: { value: 'zzzz' } });
    expect(screen.queryAllByTestId('history-point')).toHaveLength(0);
    expect(screen.getByText(/no records match/i)).toBeTruthy();
    expect(screen.getByText(/matches "zzzz"/)).toBeTruthy();

    fireEvent.click(screen.getByTestId('active-filter-search'));
    expect((screen.getByTestId('timeline-search-input') as HTMLInputElement).value).toBe('');
    expect(screen.getAllByTestId('history-point')).toHaveLength(3);
  });

  it('clears via "Clear filters, show everything" alongside bucket/outcome filters', () => {
    render(<HistoryTimeline records={searchRecords} onSelect={vi.fn()} />);
    fireEvent.change(screen.getByTestId('timeline-search-input'), { target: { value: 'f43f' } });
    expect(screen.getAllByTestId('history-point')).toHaveLength(1);
    fireEvent.click(screen.getByTestId('clear-filters'));
    expect((screen.getByTestId('timeline-search-input') as HTMLInputElement).value).toBe('');
    expect(screen.getAllByTestId('history-point')).toHaveLength(3);
  });
});
