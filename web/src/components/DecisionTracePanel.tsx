import { Check } from 'lucide-react';
import type { ActionType, DecisionTrace, DecisionTraceCandidate } from '@/types';
import { ACTION_TYPE_LABELS, formatPaise, formatPercent } from '@/lib/format';
import { pickWinningCandidate, sortCandidatesByEv } from '@/lib/decisionTrace';

interface Props {
  /** Undefined on every entry except the one that actually left Scoring
   *  (docs/API_GATEWAY.md). Renders nothing at all in that case, not an
   *  empty box. */
  trace: DecisionTrace | undefined;
  /** The record's amount_paise, i.e. what is at risk on this decision. */
  atRiskPaise: number;
}

function actionLabel(action: string): string {
  return ACTION_TYPE_LABELS[action as ActionType] ?? action;
}

/** `+₹8.71`, `-₹6.25`, or `₹0.00`: sign placed before the currency symbol,
 *  the rupee conversion itself always going through the shared formatter,
 *  never a raw `/ 100` here. */
function formatSignedPaise(paise: number): string {
  if (paise > 0) return `+${formatPaise(paise)}`;
  if (paise < 0) return `-${formatPaise(Math.abs(paise))}`;
  return formatPaise(0);
}

function evColor(paise: number): string {
  if (paise > 0) return 'text-emerald-700';
  if (paise < 0) return 'text-rose-600';
  return 'text-slate-500';
}

function CandidateRow({ candidate, isWinner }: { candidate: DecisionTraceCandidate; isWinner: boolean }) {
  return (
    <li
      data-testid="candidate-row"
      className="flex items-center gap-2 px-3 py-1.5 text-xs"
    >
      <span className="w-3.5 shrink-0 flex justify-center">
        {isWinner && <Check data-testid="winner-mark" className="w-3.5 h-3.5 text-emerald-600" />}
      </span>
      <span className={`flex-1 min-w-0 truncate ${isWinner ? 'font-semibold text-slate-800' : 'text-slate-500'}`}>
        {actionLabel(candidate.action)}
      </span>
      <span className={`w-16 text-right tabular-nums font-medium ${evColor(candidate.ev_paise)}`}>
        {formatSignedPaise(candidate.ev_paise)}
      </span>
      <span className="w-12 text-right tabular-nums text-slate-400">
        p {formatPercent(candidate.p_recovery, 0)}
      </span>
      <span className="w-16 text-right tabular-nums text-slate-400">
        cost {formatPaise(candidate.cost_paise)}
      </span>
    </li>
  );
}

/**
 * "Why this action", the every-money-action-explainable artifact
 * (docs/PRD.md section 0, docs/DEMO_READINESS.md Unit S): every candidate
 * Decision Engine priced, ranked, with the guardrail-blocked actions kept
 * in their own section rather than mixed into the ranking, since the
 * stopping rules are the compliance story and read as a distinct thing.
 * Always visible under the decision entry, no click required.
 */
export function DecisionTracePanel({ trace, atRiskPaise }: Props) {
  if (!trace) return null;

  const candidates = trace.candidates ?? [];
  const blockedEntries = Object.entries(trace.blocked ?? {});
  if (candidates.length === 0 && blockedEntries.length === 0) return null;

  const winner = pickWinningCandidate(candidates);
  const ranked = sortCandidatesByEv(candidates);

  return (
    <div className="rounded-lg border border-slate-200 bg-white mt-1.5 overflow-hidden">
      <div className="flex items-center justify-between px-3 py-1.5 bg-slate-50 border-b border-slate-200">
        <span className="text-[11px] font-semibold text-slate-500">Why this action</span>
        <span className="text-[11px] text-slate-400">
          at risk <span className="font-medium text-slate-600">{formatPaise(atRiskPaise)}</span>
        </span>
      </div>

      {ranked.length > 0 && (
        <ul className="divide-y divide-slate-100">
          {ranked.map((candidate) => (
            <CandidateRow key={candidate.action} candidate={candidate} isWinner={candidate === winner} />
          ))}
        </ul>
      )}

      {blockedEntries.length > 0 && (
        <div className={`px-3 py-1.5 bg-rose-50/40 ${ranked.length > 0 ? 'border-t border-slate-200' : ''}`}>
          <p className="text-[10px] font-semibold text-rose-700 tracking-wide mb-1">Blocked by guardrails</p>
          <ul className="space-y-1">
            {blockedEntries.map(([action, reason]) => (
              <li key={action} className="grid grid-cols-[minmax(0,7rem)_1fr] gap-2 text-xs">
                <span className="text-slate-600 font-medium truncate">{actionLabel(action)}</span>
                <span className="text-slate-500 break-words">{reason}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
