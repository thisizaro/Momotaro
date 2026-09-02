import { useEffect } from 'react';
import { History, X, Cpu } from 'lucide-react';
import type { ProviderHopResult, Source } from '@/types';
import {
  RECORD_TYPE_LABELS,
  STATE_COLORS,
  STATE_FILL,
  STATE_LABELS,
  formatCurrency,
  formatDuration,
  formatFailureCode,
  formatPaise,
  formatTime,
} from '@/lib/format';
import { formatSimulatedElapsed, formatSimulatedGap } from '@/lib/demoTime';
import type { RecordAuditResponse } from '@/types';
import { ErrorBanner } from '@/components/ErrorBanner';
import { EmptyState } from '@/components/EmptyState';
import { DueCountdown } from '@/components/DueCountdown';
import { DecisionTracePanel } from '@/components/DecisionTracePanel';

const HOP_RESULT_STYLE: Record<ProviderHopResult, string> = {
  ok: 'bg-emerald-50 text-emerald-700',
  error: 'bg-rose-50 text-rose-600',
  timeout: 'bg-amber-50 text-amber-700',
  rate_limited: 'bg-amber-50 text-amber-700',
  schema_invalid: 'bg-rose-50 text-rose-600',
  circuit_open: 'bg-rose-50 text-rose-600',
  deadline_exhausted: 'bg-amber-50 text-amber-700',
};

const HOP_RESULT_DOT: Record<ProviderHopResult, string> = {
  ok: 'bg-emerald-500',
  error: 'bg-rose-500',
  timeout: 'bg-amber-500',
  rate_limited: 'bg-amber-500',
  schema_invalid: 'bg-rose-500',
  circuit_open: 'bg-rose-500',
  deadline_exhausted: 'bg-amber-500',
};

/**
 * Only the two sources that vary carry a badge (docs/DEMO_READINESS.md Unit
 * AN): which rung actually answered is interesting precisely because it is
 * not the same on every entry. SOURCE_TEMPLATE_FALLBACK and
 * SOURCE_UNSPECIFIED (most entries, since most are plain state transitions
 * with no composed message at all) render as the same quiet metadata line
 * as `actor`, not a badge, so the two rare rows are the ones that stand out
 * rather than one badge shape repeated a dozen times.
 */
const SOURCE_BADGE: Partial<Record<Source, { label: string; className: string }>> = {
  SOURCE_LLM: { label: 'LLM answered', className: 'bg-amber-50 text-amber-700 border border-amber-200' },
  SOURCE_RULES_FALLBACK: { label: 'rules fallback', className: 'bg-blue-50 text-blue-700 border border-blue-200' },
};

interface Props {
  /** Whether the drawer is showing. Without this the backdrop and panel
   *  render on first paint, blurring the page behind an empty white panel. */
  open: boolean;
  detail: RecordAuditResponse | null;
  loading: boolean;
  error?: string | null;
  onClose: () => void;
  onRetry?: () => void;
  /**
   * Comes from the matching RecordSummary in the records table, not from
   * `detail`: due_at lives on reporting.v1.RecordSummary
   * (GET /v1/batches/{batch_id}/records), not on the audit trail this
   * drawer otherwise renders (GET /v1/records/{record_id}/audit). Empty
   * string, same absent-value convention as everywhere else on the wire.
   */
  dueAt?: string;
}

export function RecordDrawer({ open, detail, loading, error, onClose, onRetry, dueAt }: Props) {
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

  // Audit entries arrive oldest-first (audit_entry ORDER BY ts ASC, id ASC,
  // docs/API_GATEWAY.md), so entries[0] is the trail's own start: the
  // reference point every later entry's simulated position is measured
  // from, and the run this record's compressed-time story belongs to.
  const firstTs = detail && detail.entries.length > 0 ? new Date(detail.entries[0].ts).getTime() : 0;

  return (
    <>
      <div
        className="fixed inset-0 bg-slate-900/20 backdrop-blur-sm z-40 fade-in"
        onClick={onClose}
      />
      <div className="fixed right-0 top-0 bottom-0 w-full max-w-3xl bg-white shadow-2xl z-50 fade-in flex flex-col">
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
          <>
            {/* Sticky header: record id, amount and current state stay on
                screen while a long trail scrolls underneath, so the reader
                never loses what record they are looking at. */}
            <div
              data-testid="drawer-sticky-header"
              className="flex-shrink-0 border-b border-slate-100 bg-white px-6 py-4"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <h2 className="text-lg font-bold text-slate-900">Record Detail</h2>
                  <p className="text-sm font-mono text-slate-400 mt-0.5 truncate">{detail.record.id}</p>
                </div>
                <button
                  onClick={onClose}
                  className="p-1.5 rounded-lg hover:bg-slate-100 transition-colors flex-shrink-0"
                >
                  <X className="w-5 h-5 text-slate-400" />
                </button>
              </div>

              <div className="flex flex-wrap items-end gap-x-6 gap-y-3 mt-4">
                <div>
                  <p className="text-[10px] text-slate-400 uppercase tracking-wide">Amount</p>
                  <p className="text-xl font-bold text-slate-900 mt-0.5">{formatCurrency(detail.record.amount_paise)}</p>
                </div>
                <div>
                  <p className="text-[10px] text-slate-400 uppercase tracking-wide">Type</p>
                  <p className="text-sm font-semibold text-slate-700 mt-1">{RECORD_TYPE_LABELS[detail.record.type]}</p>
                </div>
                <div>
                  <p className="text-[10px] text-slate-400 uppercase tracking-wide">Failure Code</p>
                  <p className="text-sm font-semibold text-slate-700 mt-1">{formatFailureCode(detail.record.failure_code)}</p>
                </div>
                <div>
                  <p className="text-[10px] text-slate-400 uppercase tracking-wide">Current State</p>
                  <span className={`badge mt-1 ${STATE_COLORS[detail.current_state]}`}>
                    {STATE_LABELS[detail.current_state]}
                  </span>
                </div>
                <div>
                  <p className="text-[10px] text-slate-400 uppercase tracking-wide">Due</p>
                  <p className="text-sm mt-1">
                    <DueCountdown dueAt={dueAt ?? ''} currentState={detail.current_state} />
                  </p>
                </div>
              </div>
            </div>

            {/* Scrollable body: only the audit trail scrolls, the header above stays put. */}
            <div className="flex-1 overflow-y-auto scrollbar-thin px-6 py-5">
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wide">
                  Audit Trail ({detail.entries.length})
                </h3>
                <span
                  className={`badge ${
                    detail.trail_complete
                      ? 'bg-emerald-50 text-emerald-700 border-emerald-200'
                      : 'bg-amber-50 text-amber-700 border-amber-200'
                  }`}
                >
                  {detail.trail_complete ? 'Trail complete' : 'Trail incomplete'}
                </span>
              </div>

              {detail.entries.length === 0 ? (
                <EmptyState
                  icon={History}
                  title="No audit entries yet"
                  description="Nothing has happened to this record yet. Its trail starts the moment the agent first acts on it."
                  size="inline"
                />
              ) : (
                <div className="relative pl-6">
                  {/* The spine: one continuous line the whole trail hangs
                      off, so a dozen entries read as one journey through
                      states rather than a stack of unrelated blocks. */}
                  <div className="absolute left-[7px] top-2 bottom-2 w-0.5 bg-slate-200" />
                  {detail.entries.map((entry, i) => {
                    const entryMs = new Date(entry.ts).getTime();
                    const elapsedSinceFirst = Math.max(entryMs - firstTs, 0);
                    const positionText = formatSimulatedElapsed(elapsedSinceFirst);
                    const gapRealMs =
                      i > 0 ? Math.max(entryMs - new Date(detail.entries[i - 1].ts).getTime(), 0) : 0;
                    const sourceBadge = SOURCE_BADGE[entry.source];
                    // The Scoring entry and the entry right after it (often
                    // Nudge Scheduled) frequently carry the same rationale
                    // verbatim, since the Decision Engine writes it once at
                    // the point of scoring rather than re-deriving it per
                    // hop. Showing the identical sentence in the same amber
                    // box twice in a row costs vertical space for no new
                    // information, so suppress only an exact repeat of the
                    // immediately previous entry's rationale, never one that
                    // differs, and never a repeat further back in the trail
                    // (docs/DEMO_READINESS.md Unit AO).
                    const previousRationale = i > 0 ? detail.entries[i - 1].rationale : '';
                    const showRationale = entry.rationale !== '' && entry.rationale !== previousRationale;

                    return (
                      <div key={i} data-testid="audit-entry" className="relative pb-5 last:pb-0">
                        {/* State-coloured node: the same STATE_FILL palette
                            DonutChart and the historical timeline use, so a
                            step's colour means the same thing everywhere in
                            the product. */}
                        <span
                          className="absolute left-0 top-2 w-3.5 h-3.5 rounded-full border-2 border-white shadow-sm"
                          style={{ backgroundColor: STATE_FILL[entry.to_state] }}
                        />
                        <div className="card ml-2 p-3.5">
                          <div className="flex items-start justify-between gap-3">
                            <div className="min-w-0">
                              <div className="flex items-center gap-2 flex-wrap">
                                <span className="text-sm font-semibold text-slate-700">
                                  {STATE_LABELS[entry.to_state]}
                                </span>
                                <span className="text-xs text-slate-300">from {STATE_LABELS[entry.from_state]}</span>
                              </div>
                              <p className="text-xs text-slate-500 mt-0.5">{entry.reason}</p>
                            </div>
                            <span className="text-xs text-slate-400 font-mono tabular-nums flex-shrink-0 mt-0.5">
                              {formatTime(entry.ts)}
                            </span>
                          </div>

                          {/* The compressed-time story: what this instant
                              represents in the 7-day recovery window (same
                              phrasing as the historical timeline's axis, so
                              the two surfaces agree), and, once there is a
                              previous entry to compare against, the gap
                              since it in both real and simulated terms,
                              which is the number that actually helps when
                              reading a trail compressed 300000x. */}
                          {i === 0 ? (
                            <p className="text-[11px] text-slate-400 mt-1.5 tabular-nums">{positionText}</p>
                          ) : (
                            <p data-testid="entry-gap" className="text-[11px] text-slate-400 mt-1.5 tabular-nums">
                              {positionText}
                              <span className="text-slate-300"> · </span>
                              <span className="text-slate-500 font-medium">
                                +{formatDuration(gapRealMs)} real
                              </span>
                              {', '}
                              +{formatSimulatedGap(gapRealMs)} simulated
                            </p>
                          )}

                          <DecisionTracePanel trace={entry.decision_trace} atRiskPaise={detail.record.amount_paise} />

                          {showRationale && (
                            <div className="flex items-start gap-2 bg-amber-50/50 border border-amber-100 rounded-lg p-2.5 mt-2">
                              <Cpu className="w-3.5 h-3.5 text-amber-500 mt-0.5 flex-shrink-0" />
                              <p className="text-xs text-slate-600 leading-relaxed">{entry.rationale}</p>
                            </div>
                          )}

                          {entry.hops.length > 0 && (
                            <div className="flex flex-wrap gap-1.5 mt-2">
                              {entry.hops.map((hop, hi) => (
                                <span
                                  key={hi}
                                  className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium ${HOP_RESULT_STYLE[hop.result]}`}
                                >
                                  <span className={`w-1.5 h-1.5 rounded-full ${HOP_RESULT_DOT[hop.result]}`} />
                                  {hop.provider}: {hop.result}
                                </span>
                              ))}
                            </div>
                          )}

                          {entry.attempt_number > 0 && (
                            <div className="flex items-center gap-4 text-xs text-slate-400 mt-2">
                              <span>Attempt #{entry.attempt_number}</span>
                              <span>Cost: <span className="font-medium text-slate-600">{formatPaise(entry.cost_paise)}</span></span>
                            </div>
                          )}

                          {/* The composed message: one of two things a
                              judge actually reads here, so it gets a full
                              width block of its own rather than a cramped
                              caption. */}
                          {entry.message_text && (
                            <div className="bg-slate-50 border border-slate-100 rounded-lg p-3.5 mt-2">
                              <p className="text-sm text-slate-600 italic leading-relaxed break-words">
                                "{entry.message_text}"
                              </p>
                            </div>
                          )}

                          {/* Repetitive metadata, demoted: actor is almost
                              always "system", and most entries never set a
                              source at all (SOURCE_UNSPECIFIED). Both stay
                              legible but quiet, so the entries that do carry
                              a genuinely different source stand out via the
                              badge above instead. */}
                          <div className="flex items-center gap-2 mt-2.5">
                            <span className="text-[10px] text-slate-300 font-mono">{entry.actor}</span>
                            {sourceBadge ? (
                              <span
                                data-testid="source-badge"
                                className={`text-[10px] font-medium px-1.5 py-0.5 rounded-full ${sourceBadge.className}`}
                              >
                                {sourceBadge.label}
                              </span>
                            ) : (
                              <span className="text-[10px] text-slate-300 font-mono">source: {entry.source}</span>
                            )}
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          </>
        )}
      </div>
    </>
  );
}
