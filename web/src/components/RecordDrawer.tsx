import { useEffect } from 'react';
import { X, Cpu, MessageSquare, ArrowUpCircle, Clock } from 'lucide-react';
import {
  BUCKET_COLORS,
  BUCKET_LABELS,
  FAILURE_CODE_LABELS,
  STATE_COLORS,
  STATE_LABELS,
  formatCurrency,
  formatPaise,
  formatTime,
} from '@/lib/format';
import type { InterventionType, RecordDetail } from '@/types';

interface Props {
  /** Whether the drawer is showing. Without this the backdrop and panel
   *  render on first paint, blurring the page behind an empty white panel. */
  open: boolean;
  detail: RecordDetail | null;
  loading: boolean;
  onClose: () => void;
}

const ACTION_ICONS: Record<InterventionType, typeof Cpu> = {
  retry: Clock,
  nudge: MessageSquare,
  escalate: ArrowUpCircle,
  none: Cpu,
};

const ACTION_LABELS: Record<InterventionType, string> = {
  retry: 'Retry',
  nudge: 'Nudge',
  escalate: 'Escalate',
  none: 'None',
};

const OUTCOME_COLORS: Record<string, string> = {
  success: 'text-emerald-600 bg-emerald-50',
  failed: 'text-rose-600 bg-rose-50',
  pending: 'text-amber-600 bg-amber-50',
};

export function RecordDrawer({ open, detail, loading, onClose }: Props) {
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [open, onClose]);

  // Nothing rendered when closed. Declared after the hook so hook order
  // stays stable across renders.
  if (!open) return null;

  return (
    <>
      <div
        className="fixed inset-0 bg-slate-900/20 backdrop-blur-sm z-40 fade-in"
        onClick={onClose}
      />
      <div className="fixed right-0 top-0 bottom-0 w-full max-w-lg bg-white shadow-2xl z-50 overflow-y-auto scrollbar-thin fade-in">
        {loading || !detail ? (
          <div className="flex items-center justify-center h-full">
            <div className="w-6 h-6 border-2 border-slate-200 border-t-slate-400 rounded-full animate-spin" />
          </div>
        ) : (
          <div className="p-6 space-y-6">
            {/* Header */}
            <div className="flex items-start justify-between">
              <div>
                <h2 className="text-lg font-bold text-slate-900">Record Detail</h2>
                <p className="text-sm font-mono text-slate-400 mt-0.5">{detail.id}</p>
              </div>
              <button onClick={onClose} className="p-1.5 rounded-lg hover:bg-slate-100 transition-colors">
                <X className="w-5 h-5 text-slate-400" />
              </button>
            </div>

            {/* Key facts */}
            <div className="grid grid-cols-2 gap-3">
              <div className="bg-slate-50 rounded-lg p-3">
                <p className="text-xs text-slate-400 uppercase tracking-wide">Amount</p>
                <p className="text-lg font-bold text-slate-900 mt-1">{formatCurrency(detail.amount)}</p>
              </div>
              <div className="bg-slate-50 rounded-lg p-3">
                <p className="text-xs text-slate-400 uppercase tracking-wide">Type</p>
                <p className="text-lg font-bold text-slate-900 mt-1 capitalize">{detail.type}</p>
              </div>
              <div className="bg-slate-50 rounded-lg p-3">
                <p className="text-xs text-slate-400 uppercase tracking-wide">Failure Code</p>
                <p className="text-sm font-semibold text-slate-700 mt-1">{FAILURE_CODE_LABELS[detail.failure_code]}</p>
              </div>
              <div className="bg-slate-50 rounded-lg p-3">
                <p className="text-xs text-slate-400 uppercase tracking-wide">Current State</p>
                <span className={`badge mt-1.5 ${STATE_COLORS[detail.current_state]}`}>
                  {STATE_LABELS[detail.current_state]}
                </span>
              </div>
            </div>

            {/* Classification */}
            <div className="space-y-2">
              <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wide">Classification</h3>
              <div className="flex items-center gap-2">
                <span className="w-3 h-3 rounded-full" style={{ backgroundColor: BUCKET_COLORS[detail.root_cause_bucket] }} />
                <span className="text-sm font-medium text-slate-700">{BUCKET_LABELS[detail.root_cause_bucket]}</span>
                <span className="text-xs text-slate-400 ml-auto font-mono">{detail.classification_source}</span>
              </div>
              {detail.rationale && (
                <div className="flex items-start gap-2 bg-amber-50/50 border border-amber-100 rounded-lg p-3">
                  <Cpu className="w-4 h-4 text-amber-500 mt-0.5 flex-shrink-0" />
                  <p className="text-sm text-slate-600 leading-relaxed">{detail.rationale}</p>
                </div>
              )}
            </div>

            {/* Interventions */}
            <div className="space-y-3">
              <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wide">
                Intervention Attempts ({detail.interventions.length})
              </h3>
              {detail.interventions.length === 0 ? (
                <p className="text-sm text-slate-300">No interventions executed yet.</p>
              ) : (
                <div className="space-y-2">
                  {detail.interventions.map((iv) => {
                    const Icon = ACTION_ICONS[iv.action_type];
                    return (
                      <div key={iv.id} className="border border-slate-200 rounded-lg p-3 space-y-2">
                        <div className="flex items-center gap-2">
                          <div className="w-7 h-7 rounded-lg bg-slate-100 flex items-center justify-center">
                            <Icon className="w-3.5 h-3.5 text-slate-500" />
                          </div>
                          <span className="text-sm font-medium text-slate-700">
                            {ACTION_LABELS[iv.action_type]} #{iv.attempt_number}
                          </span>
                          <span className={`text-xs font-medium px-2 py-0.5 rounded-full ml-auto ${OUTCOME_COLORS[iv.outcome]}`}>
                            {iv.outcome}
                          </span>
                        </div>
                        <div className="flex items-center gap-4 text-xs text-slate-400">
                          <span>Cost: <span className="font-medium text-slate-600">{formatPaise(iv.cost_paise)}</span></span>
                          <span>P(recovery): <span className="font-medium text-slate-600">{(iv.p_recovery_at_decision * 100).toFixed(0)}%</span></span>
                          <span>EV: <span className="font-medium text-slate-600">{iv.ev_score_at_decision.toFixed(3)}</span></span>
                          <span className="ml-auto font-mono">{formatTime(iv.executed_at)}</span>
                        </div>
                        {iv.message_text && (
                          <div className="bg-slate-50 rounded-md p-2.5 mt-1">
                            <p className="text-xs text-slate-500 italic leading-relaxed">"{iv.message_text}"</p>
                            <p className="text-xs text-slate-300 mt-1 font-mono">source: {iv.message_source}</p>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Audit Trail */}
            <div className="space-y-3">
              <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wide">Audit Trail</h3>
              <div className="relative pl-5">
                <div className="absolute left-1.5 top-1 bottom-1 w-px bg-slate-200" />
                {detail.audit.map((entry, i) => (
                  <div key={entry.id} className="relative pb-4 last:pb-0">
                    <span className="absolute -left-[14px] top-1 w-2.5 h-2.5 rounded-full bg-white border-2 border-slate-300" />
                    <div className="ml-2">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-slate-700">{entry.to_state}</span>
                        {entry.from_state && (
                          <span className="text-xs text-slate-300">from {entry.from_state}</span>
                        )}
                        <span className="text-xs text-slate-300 ml-auto font-mono">{formatTime(entry.ts)}</span>
                      </div>
                      <p className="text-xs text-slate-500 mt-0.5">{entry.reason}</p>
                      {entry.rationale && (
                        <p className="text-xs text-slate-400 mt-1 italic leading-relaxed">{entry.rationale}</p>
                      )}
                      <p className="text-xs text-slate-300 mt-1 font-mono">source: {entry.source}</p>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
    </>
  );
}
