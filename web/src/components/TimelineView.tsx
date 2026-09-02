import { useState } from 'react';
import { LiveTimeline } from '@/components/LiveTimeline';
import { HistoryTimeline } from '@/components/HistoryTimeline';
import type { RecordSummary } from '@/types';

interface Props {
  records: RecordSummary[];
  onSelect: (id: string) => void;
}

type Mode = 'live' | 'history';

function pendingCount(records: RecordSummary[]): number {
  return records.filter((r) => r.due_at !== '').length;
}

function historyCount(records: RecordSummary[]): number {
  return records.filter((r) => r.first_action_at !== '').length;
}

/**
 * Live shows what's scheduled (today's due_at-driven view). History shows
 * what actually happened across the run, using first_action_at/
 * last_action_at (docs/API_GATEWAY.md). Two problems drove this split
 * (docs/DEMO_READINESS.md Unit AH): Live alone goes empty the instant a run
 * finishes, exactly when someone wants to study it, and neither view
 * previously showed real timing at all, only a future due_at.
 *
 * The initial mode is chosen once, from the records this component first
 * mounts with: if nothing is pending but something has already happened,
 * open on History rather than showing "nothing pending" on a finished run.
 * A caller that wants this re-evaluated for a new batch should remount with
 * a fresh `key` (App.tsx keys this on the active batch id), the same reason
 * a lazy useState initializer alone would otherwise go stale across a batch
 * switch.
 */
export function TimelineView({ records, onSelect }: Props) {
  const [mode, setMode] = useState<Mode>(() => (pendingCount(records) === 0 && historyCount(records) > 0 ? 'history' : 'live'));

  const pending = pendingCount(records);
  const history = historyCount(records);

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <h3 className="text-sm font-semibold text-slate-700">Timeline</h3>
        <div className="inline-flex items-center gap-0.5 bg-slate-100 rounded-lg p-0.5" role="tablist" aria-label="Timeline view">
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'live'}
            onClick={() => setMode('live')}
            className={`px-2.5 py-1 text-xs font-medium rounded-md transition-colors ${
              mode === 'live' ? 'bg-white text-slate-900 shadow-sm' : 'text-slate-500 hover:text-slate-700'
            }`}
          >
            Live <span className="tabular-nums">{pending}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'history'}
            onClick={() => setMode('history')}
            className={`px-2.5 py-1 text-xs font-medium rounded-md transition-colors ${
              mode === 'history' ? 'bg-white text-slate-900 shadow-sm' : 'text-slate-500 hover:text-slate-700'
            }`}
          >
            History <span className="tabular-nums">{history}</span>
          </button>
        </div>
      </div>

      {mode === 'live' ? (
        <LiveTimeline records={records} onSelect={onSelect} />
      ) : (
        <HistoryTimeline records={records} onSelect={onSelect} />
      )}
    </div>
  );
}
