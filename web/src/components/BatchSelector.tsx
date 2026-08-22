import { formatRelativeTime } from '@/lib/format';
import type { BatchSummary } from '@/types';

interface Props {
  batches: BatchSummary[];
  activeBatchId: string | null;
  onSelect: (id: string) => void;
}

export function BatchSelector({ batches, activeBatchId, onSelect }: Props) {
  if (batches.length === 0) return null;

  return (
    <div className="flex items-center gap-2 overflow-x-auto scrollbar-thin pb-1">
      {batches.map((b) => (
        <button
          key={b.batch_id}
          onClick={() => onSelect(b.batch_id)}
          className={`flex-shrink-0 px-3 py-2 rounded-lg text-xs font-medium transition-all duration-150 border ${
            b.batch_id === activeBatchId
              ? 'bg-slate-900 text-white border-slate-900'
              : 'bg-white text-slate-500 border-slate-200 hover:bg-slate-50'
          }`}
        >
          <span className="font-mono">{b.batch_id.slice(0, 8)}</span>
          <span className={`ml-2 ${b.batch_id === activeBatchId ? 'text-slate-300' : 'text-slate-400'}`}>
            {b.total_records} rec · {formatRelativeTime(b.created_at)}
          </span>
        </button>
      ))}
    </div>
  );
}
