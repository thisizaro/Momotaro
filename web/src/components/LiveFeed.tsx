import { useEffect, useRef, useState } from 'react';
import { CheckCircle2, Radio, WifiOff } from 'lucide-react';
import { STATE_DOT_COLORS, STATE_LABELS, formatTime } from '@/lib/format';
import { EmptyState } from '@/components/EmptyState';
import type { LiveConnectionStatus } from '@/lib/api';
import type { BatchUpdate } from '@/types';

// 'connecting' isn't part of LiveConnectionStatus (lib/api.ts never reports
// it, App sets it before the socket has had a chance to open), but it is a
// real state this feed needs to describe honestly.
type ConnectionState = 'connecting' | LiveConnectionStatus;

interface Props {
  updates: BatchUpdate[];
  connectionState: ConnectionState;
}

/**
 * What to say when there is nothing in the feed yet, honest about *why*
 * rather than one "Waiting for events..." for every case (docs/
 * DEMO_READINESS.md Unit AF). Connecting, actually waiting on a live batch,
 * a dropped connection retrying, one that gave up retrying for now, and a
 * batch that is simply done, all mean something different.
 */
function emptyStateFor(connectionState: ConnectionState) {
  switch (connectionState) {
    case 'connecting':
      return { icon: Radio, title: 'Connecting to the live stream...' };
    case 'reconnecting':
      return { icon: Radio, title: 'Reconnecting...', description: 'The connection dropped. Events will resume as soon as it is back.' };
    case 'disconnected':
      return { icon: WifiOff, title: 'Live stream disconnected', description: 'Still retrying in the background. This batch’s numbers keep refreshing either way.' };
    case 'complete':
      return { icon: CheckCircle2, title: 'This batch already finished', description: 'No events streamed this session, there is nothing left to arrive.' };
    case 'live':
    default:
      return { icon: Radio, title: 'Waiting for the first event...' };
  }
}

export function LiveFeed({ updates, connectionState }: Props) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [autoScroll, setAutoScroll] = useState(true);

  useEffect(() => {
    if (autoScroll && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [updates, autoScroll]);

  const handleScroll = () => {
    if (!scrollRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = scrollRef.current;
    setAutoScroll(scrollHeight - scrollTop - clientHeight < 40);
  };

  const live = connectionState === 'live';

  return (
    <div className="card flex flex-col h-full">
      <div className="flex items-center justify-between px-5 py-3.5 border-b border-slate-100">
        <div className="flex items-center gap-2">
          <Radio className="w-4 h-4 text-slate-400" />
          <h3 className="text-sm font-semibold text-slate-700">Live Event Stream</h3>
        </div>
        {live && (
          <span className="flex items-center gap-1.5 text-xs text-emerald-600 font-medium">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 pulse-dot" />
            Live
          </span>
        )}
      </div>

      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto scrollbar-thin p-3 min-h-[200px] max-h-[400px]"
      >
        {updates.length === 0 ? (
          <div className="flex items-center justify-center h-full">
            <EmptyState size="inline" {...emptyStateFor(connectionState)} />
          </div>
        ) : (
          <div className="space-y-1">
            {updates.slice(-80).map((u, i) => (
              <div
                key={`${u.record_id}-${u.ts}-${i}`}
                className="flex items-center gap-2.5 px-2 py-1.5 rounded-md hover:bg-slate-50 slide-in"
              >
                <span className={`w-2 h-2 rounded-full flex-shrink-0 ${STATE_DOT_COLORS[u.to_state]}`} />
                <span className="text-xs font-mono text-slate-400 tabular-nums w-16 flex-shrink-0">
                  {formatTime(u.ts)}
                </span>
                <span className="text-xs text-slate-500 truncate flex-1 font-mono">
                  {u.record_id.slice(0, 8)}
                </span>
                <span className="text-xs text-slate-300 flex-shrink-0">{STATE_LABELS[u.from_state]}</span>
                <span className="text-xs text-slate-300 flex-shrink-0">&rarr;</span>
                <span className="text-xs font-medium text-slate-700 flex-shrink-0">
                  {STATE_LABELS[u.to_state]}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
