import { useEffect, useState } from 'react';
import type { RecordState } from '@/types';

interface Props {
  /** RFC3339, or empty string when nothing is scheduled (docs/API_GATEWAY.md). */
  dueAt: string;
  currentState: RecordState;
  className?: string;
}

/**
 * formatRemaining renders a countdown resolution appropriate to how far out
 * the wait is: sub-second precision only matters when the wait itself is
 * short (a demo-scaled retry a few seconds out), so it fades out once the
 * wait is long enough that a viewer is reading minutes/hours/days, not
 * counting seconds.
 */
function formatRemaining(ms: number): string {
  const totalSeconds = ms / 1000;
  if (totalSeconds < 60) return `${totalSeconds.toFixed(1)}s`;
  const totalMinutes = Math.floor(totalSeconds / 60);
  if (totalMinutes < 60) return `${totalMinutes}m ${Math.floor(totalSeconds % 60)}s`;
  const totalHours = Math.floor(totalMinutes / 60);
  if (totalHours < 24) return `${totalHours}h ${totalMinutes % 60}m`;
  const totalDays = Math.floor(totalHours / 24);
  return `${totalDays}d ${totalHours % 24}h`;
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
  const label = remainingMs <= 0 ? 'due now' : `in ${formatRemaining(remainingMs)}`;

  return (
    <span className={`font-mono tabular-nums text-slate-600 ${className ?? ''}`}>{label}</span>
  );
}
