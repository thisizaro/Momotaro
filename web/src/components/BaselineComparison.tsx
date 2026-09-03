import { formatCurrencyShort } from '@/lib/format';
import { noGroundTruthReason } from '@/lib/groundTruth';
import type { BaselineComparison } from '@/types';

interface Props {
  baseline?: BaselineComparison;
  ownNetRecoveredPaise: number;
  /**
   * The active batch's `source` (BatchSummary.source, GET /v1/batches),
   * looked up by the caller from state already on hand. Only used to word
   * the explanation when `baseline` is absent; not required, since an
   * unknown source still gets an honest generic reason
   * (lib/groundTruth.ts).
   */
  source?: string;
}

export function BaselineComparisonCard({ baseline, ownNetRecoveredPaise, source }: Props) {
  if (!baseline) {
    return (
      <div className="flex items-center justify-center h-full text-sm text-slate-400 leading-relaxed text-center px-4 py-8">
        {noGroundTruthReason(source)}
      </div>
    );
  }

  const delta = ownNetRecoveredPaise - baseline.net_recovered_paise;

  return (
    <div className="space-y-3">
      <p className="text-xs text-slate-400">
        Momotaro vs. a fixed naive policy (
        <span className="font-medium text-slate-500">{baseline.policy_name}</span>), evaluated
        against the same sealed ground truth.
      </p>
      <div className="grid grid-cols-2 gap-3">
        <div className="bg-slate-50 rounded-lg p-3">
          <p className="text-xs text-slate-400 uppercase tracking-wide">Naive policy net</p>
          <p className="text-lg font-bold text-slate-700 mt-1">
            {formatCurrencyShort(baseline.net_recovered_paise)}
          </p>
          <p className="text-xs text-slate-400 mt-0.5">
            {formatCurrencyShort(baseline.gross_recovered_paise)} gross −{' '}
            {formatCurrencyShort(baseline.intervention_spend_paise)} spend
          </p>
        </div>
        <div className="bg-emerald-50 rounded-lg p-3 border border-emerald-100">
          <p className="text-xs text-emerald-600 uppercase tracking-wide">Momotaro net</p>
          <p className="text-lg font-bold text-emerald-700 mt-1">
            {formatCurrencyShort(ownNetRecoveredPaise)}
          </p>
          <p className="text-xs text-emerald-600 mt-0.5">
            {delta >= 0 ? '+' : ''}
            {formatCurrencyShort(delta)} vs. naive
          </p>
        </div>
      </div>
      <p className="text-xs text-slate-400 italic leading-relaxed">{baseline.note}</p>
    </div>
  );
}
