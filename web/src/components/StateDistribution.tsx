import { STATE_DOT_COLORS, STATE_FILL, STATE_LABELS, STATE_ORDER } from '@/lib/format';
import type { RecordState, RecordSummary } from '@/types';

interface Props {
  records: RecordSummary[];
}

export function StateDistribution({ records }: Props) {
  const counts = STATE_ORDER.reduce((acc, s) => {
    acc[s] = records.filter((r) => r.current_state === s).length;
    return acc;
  }, {} as Record<RecordState, number>);

  const total = records.length;

  return (
    <div className="space-y-3">
      <div className="flex h-2.5 rounded-full overflow-hidden">
        {STATE_ORDER.map((state) => {
          const count = counts[state];
          if (count === 0) return null;
          const pct = (count / total) * 100;
          return (
            <div
              key={state}
              className="transition-all duration-500"
              style={{ width: `${pct}%`, backgroundColor: STATE_FILL[state] }}
              title={`${STATE_LABELS[state]}: ${count}`}
            />
          );
        })}
      </div>

      <div className="grid grid-cols-3 gap-x-4 gap-y-2">
        {STATE_ORDER.filter((s) => counts[s] > 0).map((state) => (
          <div key={state} className="flex items-center gap-2">
            <span className={`w-2 h-2 rounded-full flex-shrink-0 ${STATE_DOT_COLORS[state]}`} />
            <span className="text-xs text-slate-500 truncate flex-1">{STATE_LABELS[state]}</span>
            <span className="text-xs font-semibold text-slate-700 tabular-nums">{counts[state]}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

