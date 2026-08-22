import { useEffect, useRef, useState } from 'react';
import { Radio } from 'lucide-react';
import { STATE_DOT_COLORS, STATE_LABELS, formatTime } from '@/lib/format';
import type { BatchUpdate } from '@/types';

interface Props {
  updates: BatchUpdate[];
  live: boolean;
}

export function LiveFeed({ updates, live }: Props) {
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
        className="flex-1 overflow-y-auto scrollbar-thin p-3 space-y-1 min-h-[200px] max-h-[400px]"
      >
        {updates.length === 0 ? (
          <div className="flex items-center justify-center h-full text-sm text-slate-300 py-8">
            Waiting for events...
          </div>
        ) : (
          updates.slice(-80).map((u, i) => (
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
              <span className="text-xs text-slate-300 flex-shrink-0">→</span>
              <span className="text-xs font-medium text-slate-700 flex-shrink-0">
                {STATE_LABELS[u.to_state]}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
