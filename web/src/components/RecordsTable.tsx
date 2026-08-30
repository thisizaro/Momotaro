import { useState } from 'react';
import { ChevronRight, Filter } from 'lucide-react';
import { BUCKET_COLORS, BUCKET_LABELS, RECORD_TYPE_LABELS, STATE_COLORS, STATE_LABELS, formatCurrency } from '@/lib/format';
import type { RecordSummary, RootCauseBucket } from '@/types';

interface Props {
  records: RecordSummary[];
  onSelect: (id: string) => void;
}

type StateFilter = 'all' | 'in_flight' | 'terminal';

const ALL_BUCKETS: RootCauseBucket[] = [
  'ROOT_CAUSE_BUCKET_TRANSIENT_BANK',
  'ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS',
  'ROOT_CAUSE_BUCKET_HARD_DECLINE',
  'ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED',
  'ROOT_CAUSE_BUCKET_RISK_HOLD',
  'ROOT_CAUSE_BUCKET_ABANDONMENT',
  'ROOT_CAUSE_BUCKET_OVERDUE',
];

export function RecordsTable({ records, onSelect }: Props) {
  const [stateFilter, setStateFilter] = useState<StateFilter>('all');
  const [bucketFilter, setBucketFilter] = useState<RootCauseBucket | 'all'>('all');

  const filtered = records.filter((r) => {
    if (stateFilter === 'in_flight') {
      if (['RECORD_STATE_RECOVERED', 'RECORD_STATE_ESCALATED', 'RECORD_STATE_CLOSED_UNECONOMIC'].includes(r.current_state)) return false;
    } else if (stateFilter === 'terminal') {
      if (!['RECORD_STATE_RECOVERED', 'RECORD_STATE_ESCALATED', 'RECORD_STATE_CLOSED_UNECONOMIC'].includes(r.current_state)) return false;
    }
    if (bucketFilter !== 'all' && r.bucket !== bucketFilter) return false;
    return true;
  });

  return (
    <div className="card overflow-hidden">
      <div className="flex items-center justify-between px-5 py-3.5 border-b border-slate-100">
        <h3 className="text-sm font-semibold text-slate-700">Records</h3>
        <div className="flex items-center gap-2">
          <Filter className="w-3.5 h-3.5 text-slate-300" />
          <select
            value={stateFilter}
            onChange={(e) => setStateFilter(e.target.value as StateFilter)}
            className="text-xs border border-slate-200 rounded-lg px-2.5 py-1.5 bg-white text-slate-600
                       focus:outline-none focus:ring-2 focus:ring-slate-200 cursor-pointer"
          >
            <option value="all">All states</option>
            <option value="in_flight">In flight</option>
            <option value="terminal">Settled</option>
          </select>
          <select
            value={bucketFilter}
            onChange={(e) => setBucketFilter(e.target.value as RootCauseBucket | 'all')}
            className="text-xs border border-slate-200 rounded-lg px-2.5 py-1.5 bg-white text-slate-600
                       focus:outline-none focus:ring-2 focus:ring-slate-200 cursor-pointer"
          >
            <option value="all">All causes</option>
            {ALL_BUCKETS.map((bucket) => (
              <option key={bucket} value={bucket}>
                {BUCKET_LABELS[bucket]}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="overflow-x-auto scrollbar-thin">
        <table className="w-full">
          <thead>
            <tr className="border-b border-slate-100">
              <th className="text-left text-xs font-medium text-slate-400 uppercase tracking-wide px-5 py-2.5">Record ID</th>
              <th className="text-left text-xs font-medium text-slate-400 uppercase tracking-wide px-3 py-2.5">Type</th>
              <th className="text-right text-xs font-medium text-slate-400 uppercase tracking-wide px-3 py-2.5">Amount</th>
              <th className="text-left text-xs font-medium text-slate-400 uppercase tracking-wide px-3 py-2.5">Root Cause</th>
              <th className="text-left text-xs font-medium text-slate-400 uppercase tracking-wide px-3 py-2.5">State</th>
              <th className="w-8 px-3 py-2.5"></th>
            </tr>
          </thead>
          <tbody>
            {filtered.slice(0, 100).map((r) => (
              <tr
                key={r.record_id}
                onClick={() => onSelect(r.record_id)}
                className="border-b border-slate-50 hover:bg-slate-50/50 cursor-pointer transition-colors"
              >
                <td className="px-5 py-2.5">
                  <span className="text-sm font-mono text-slate-600">{r.record_id.slice(0, 8)}</span>
                </td>
                <td className="px-3 py-2.5">
                  <span className="text-xs text-slate-500">{RECORD_TYPE_LABELS[r.type]}</span>
                </td>
                <td className="px-3 py-2.5 text-right">
                  <span className="text-sm font-medium text-slate-700 tabular-nums">{formatCurrency(r.amount_paise)}</span>
                </td>
                <td className="px-3 py-2.5">
                  <span className="flex items-center gap-1.5">
                    <span className="w-2 h-2 rounded-full" style={{ backgroundColor: BUCKET_COLORS[r.bucket] }} />
                    <span className="text-xs text-slate-500">{BUCKET_LABELS[r.bucket]}</span>
                  </span>
                </td>
                <td className="px-3 py-2.5">
                  <span className={`badge ${STATE_COLORS[r.current_state]}`}>
                    {STATE_LABELS[r.current_state]}
                  </span>
                </td>
                <td className="px-3 py-2.5">
                  <ChevronRight className="w-4 h-4 text-slate-300" />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {filtered.length > 100 && (
        <div className="px-5 py-2.5 text-xs text-slate-400 border-t border-slate-100">
          Showing 100 of {filtered.length} records
        </div>
      )}
    </div>
  );
}
