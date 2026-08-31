import { AlertTriangle, ShieldCheck } from 'lucide-react';
import type { InvariantsResponse } from '@/types';

interface Props {
  invariants: InvariantsResponse | null;
}

export function InvariantsPanel({ invariants }: Props) {
  if (!invariants) {
    return <div className="h-[140px] animate-pulse bg-slate-50 rounded-lg" />;
  }

  const healthy =
    invariants.stopping_rule_violations === 0 &&
    invariants.incomplete_audit_trails === 0 &&
    invariants.impossible_transitions === 0;

  return (
    <div
      className={`rounded-lg border p-4 ${healthy ? 'border-emerald-200 bg-emerald-50' : 'border-rose-300 bg-rose-50'}`}
    >
      <div className="flex items-center gap-2 mb-4">
        {healthy ? (
          <ShieldCheck className="w-4 h-4 text-emerald-600" />
        ) : (
          <AlertTriangle className="w-4 h-4 text-rose-600" />
        )}
        <span className={`text-sm font-semibold ${healthy ? 'text-emerald-700' : 'text-rose-700'}`}>
          {healthy ? 'All invariants holding' : 'Invariant violation detected'}
        </span>
      </div>
      <div className="grid grid-cols-3 gap-3 text-center">
        <div>
          <p
            className={`text-lg font-bold tabular-nums ${invariants.stopping_rule_violations > 0 ? 'text-rose-600' : 'text-slate-900'}`}
          >
            {invariants.stopping_rule_violations}
          </p>
          <p className="text-xs text-slate-500 mt-0.5">Stopping-rule violations</p>
        </div>
        <div>
          <p
            className={`text-lg font-bold tabular-nums ${invariants.incomplete_audit_trails > 0 ? 'text-rose-600' : 'text-slate-900'}`}
          >
            {invariants.incomplete_audit_trails}
          </p>
          <p className="text-xs text-slate-500 mt-0.5">Incomplete audit trails</p>
        </div>
        <div>
          <p
            className={`text-lg font-bold tabular-nums ${invariants.impossible_transitions > 0 ? 'text-rose-600' : 'text-slate-900'}`}
          >
            {invariants.impossible_transitions}
          </p>
          <p className="text-xs text-slate-500 mt-0.5">Impossible transitions</p>
        </div>
      </div>
      <p className="text-xs text-slate-400 mt-3">{invariants.records_checked} records checked</p>
    </div>
  );
}
