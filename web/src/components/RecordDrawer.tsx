import { useEffect } from 'react';
import { X, Cpu } from 'lucide-react';
import {
  RECORD_TYPE_LABELS,
  STATE_COLORS,
  STATE_LABELS,
  formatCurrency,
  formatFailureCode,
  formatPaise,
  formatTime,
} from '@/lib/format';
import type { RecordAuditResponse } from '@/types';
import { ErrorBanner } from '@/components/ErrorBanner';

interface Props {
  /** Whether the drawer is showing. Without this the backdrop and panel
   *  render on first paint, blurring the page behind an empty white panel. */
  open: boolean;
  detail: RecordAuditResponse | null;
  loading: boolean;
  error?: string | null;
  onClose: () => void;
  onRetry?: () => void;
}

export function RecordDrawer({ open, detail, loading, error, onClose, onRetry }: Props) {
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
        {loading ? (
          <div className="flex items-center justify-center h-full">
            <div className="w-6 h-6 border-2 border-slate-200 border-t-slate-400 rounded-full animate-spin" />
          </div>
        ) : error ? (
          <div className="p-6 space-y-4">
            <div className="flex items-start justify-between">
              <h2 className="text-lg font-bold text-slate-900">Record Detail</h2>
              <button onClick={onClose} className="p-1.5 rounded-lg hover:bg-slate-100 transition-colors">
                <X className="w-5 h-5 text-slate-400" />
              </button>
            </div>
            <ErrorBanner message={error} onRetry={onRetry} />
          </div>
        ) : !detail ? null : (
          <div className="p-6 space-y-6">
            {/* Header */}
            <div className="flex items-start justify-between">
              <div>
                <h2 className="text-lg font-bold text-slate-900">Record Detail</h2>
                <p className="text-sm font-mono text-slate-400 mt-0.5">{detail.record.id}</p>
              </div>
              <button onClick={onClose} className="p-1.5 rounded-lg hover:bg-slate-100 transition-colors">
                <X className="w-5 h-5 text-slate-400" />
              </button>
            </div>

            {/* Key facts */}
            <div className="grid grid-cols-2 gap-3">
              <div className="bg-slate-50 rounded-lg p-3">
                <p className="text-xs text-slate-400 uppercase tracking-wide">Amount</p>
                <p className="text-lg font-bold text-slate-900 mt-1">{formatCurrency(detail.record.amount_paise)}</p>
              </div>
              <div className="bg-slate-50 rounded-lg p-3">
                <p className="text-xs text-slate-400 uppercase tracking-wide">Type</p>
                <p className="text-lg font-bold text-slate-900 mt-1">{RECORD_TYPE_LABELS[detail.record.type]}</p>
              </div>
              <div className="bg-slate-50 rounded-lg p-3">
                <p className="text-xs text-slate-400 uppercase tracking-wide">Failure Code</p>
                <p className="text-sm font-semibold text-slate-700 mt-1">{formatFailureCode(detail.record.failure_code)}</p>
              </div>
              <div className="bg-slate-50 rounded-lg p-3">
                <p className="text-xs text-slate-400 uppercase tracking-wide">Current State</p>
                <span className={`badge mt-1.5 ${STATE_COLORS[detail.current_state]}`}>
                  {STATE_LABELS[detail.current_state]}
                </span>
              </div>
            </div>

            {/* Audit Trail */}
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wide">
                  Audit Trail ({detail.entries.length})
                </h3>
                {!detail.trail_complete && (
                  <span className="text-xs text-amber-600">trail incomplete</span>
                )}
              </div>
              <div className="relative pl-5">
                <div className="absolute left-1.5 top-1 bottom-1 w-px bg-slate-200" />
                {detail.entries.map((entry, i) => (
                  <div key={i} className="relative pb-4 last:pb-0">
                    <span className="absolute -left-[14px] top-1 w-2.5 h-2.5 rounded-full bg-white border-2 border-slate-300" />
                    <div className="ml-2">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-slate-700">{STATE_LABELS[entry.to_state]}</span>
                        <span className="text-xs text-slate-300">from {STATE_LABELS[entry.from_state]}</span>
                        <span className="text-xs text-slate-300 ml-auto font-mono">{formatTime(entry.ts)}</span>
                      </div>
                      <p className="text-xs text-slate-500 mt-0.5">{entry.reason}</p>
                      {entry.rationale && (
                        <div className="flex items-start gap-2 bg-amber-50/50 border border-amber-100 rounded-lg p-2.5 mt-1.5">
                          <Cpu className="w-3.5 h-3.5 text-amber-500 mt-0.5 flex-shrink-0" />
                          <p className="text-xs text-slate-600 leading-relaxed">{entry.rationale}</p>
                        </div>
                      )}
                      {entry.attempt_number > 0 && (
                        <div className="flex items-center gap-4 text-xs text-slate-400 mt-1.5">
                          <span>Attempt #{entry.attempt_number}</span>
                          <span>Cost: <span className="font-medium text-slate-600">{formatPaise(entry.cost_paise)}</span></span>
                        </div>
                      )}
                      {entry.message_text && (
                        <div className="bg-slate-50 rounded-md p-2.5 mt-1.5">
                          <p className="text-xs text-slate-500 italic leading-relaxed">"{entry.message_text}"</p>
                        </div>
                      )}
                      <p className="text-xs text-slate-300 mt-1 font-mono">
                        {entry.actor} · source: {entry.source}
                      </p>
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
