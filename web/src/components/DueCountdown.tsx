import { useEffect, useState } from 'react';
import { formatDuration } from '@/lib/format';
import type { RecordState } from '@/types';

interface Props {
  /** RFC3339, or empty string when nothing is scheduled (docs/API_GATEWAY.md). */
  dueAt: string;
  currentState: RecordState;
  className?: string;
}

/**
 * DueCountdown renders record_state.due_at as a live relative countdown
 * ("retry in 6.4s") rather than a static timestamp, ticking once a second,
 * per Unit AA ("that is most of the value: it turns a static field into a
 * visible act of waiting").
 *
 * An empty due_at is meaningful, not missing data (docs/API_GATEWAY.md): a
 * RECORD_STATE_NUDGED record deliberately has none, since it is parked
 * waiting on ReportDelayedOutcome from the customer, nothing polls it; any
 * other state with an empty due_at has nothing left to schedule, most often
 * because it already reached a terminal state. Both render as clear,
 * intentional text, never a blank cell.
 */
export function DueCountdown({ dueAt, currentState, className }: Props) {
  // Re-renders once a second while a due_at is live, so the displayed
  // countdown keeps counting down without needing new data from the server.
  const [, tick] = useState(0);
  useEffect(() => {
    if (!dueAt) return;
    const id = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [dueAt]);

  if (!dueAt) {
    const label = currentState === 'RECORD_STATE_NUDGED' ? 'awaiting customer' : 'not scheduled';
    return <span className={`text-slate-400 italic ${className ?? ''}`}>{label}</span>;
  }

  const remainingMs = new Date(dueAt).getTime() - Date.now();
  const label = remainingMs <= 0 ? 'due now' : `in ${formatDuration(remainingMs)}`;

  return (
    <span className={`font-mono tabular-nums text-slate-600 ${className ?? ''}`}>{label}</span>
  );
}
