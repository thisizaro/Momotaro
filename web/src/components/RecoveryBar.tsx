import { formatCurrencyShort } from '@/lib/format';
import type { BatchReport } from '@/types';

interface Props {
  report: BatchReport;
}

export function RecoveryBar({ report }: Props) {
  const recovered = report.recovered_paise;
  const atRisk = report.at_risk_paise;
  const unrecovered = atRisk - recovered;
  const recoveredPct = atRisk > 0 ? (recovered / atRisk) * 100 : 0;
  const unrecoveredPct = atRisk > 0 ? (unrecovered / atRisk) * 100 : 0;

  return (
    <div className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div>
          <span className="text-2xl font-bold text-emerald-600">{formatCurrencyShort(recovered)}</span>
          <span className="text-sm text-slate-400 ml-2">recovered</span>
        </div>
        <div className="text-sm text-slate-500">
          of <span className="font-semibold text-slate-700">{formatCurrencyShort(atRisk)}</span> at risk
        </div>
      </div>

      <div className="flex h-3 rounded-full overflow-hidden bg-slate-100">
        <div
          className="bg-gradient-to-r from-emerald-400 to-emerald-500 transition-all duration-700 ease-out"
          style={{ width: `${recoveredPct}%` }}
        />
        <div
          className="bg-slate-200 transition-all duration-700 ease-out"
          style={{ width: `${unrecoveredPct}%` }}
        />
      </div>

      <div className="flex items-center gap-4 text-xs">
        <span className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-full bg-emerald-500" />
          <span className="text-slate-600">Recovered ({recoveredPct.toFixed(1)}%)</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-full bg-slate-200" />
          <span className="text-slate-600">In flight / lost ({unrecoveredPct.toFixed(1)}%)</span>
        </span>
      </div>
    </div>
  );
}
