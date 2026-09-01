import { useEffect, useMemo, useState } from 'react';
import { Clock } from 'lucide-react';
import { BUCKET_COLORS, BUCKET_LABELS, RECORD_TYPE_LABELS, formatCurrency, formatDuration } from '@/lib/format';
import type { RecordSummary, RootCauseBucket } from '@/types';

interface Props {
  records: RecordSummary[];
}

// Fixed order, not derived from what's present: an empty row for a bucket
// like HARD_DECLINE (a dead card gets a nudge, never a retry, so it is
// rarely if ever waiting on anything) is exactly the signal this view
// exists to show, and it only reads as a signal if the row is still there
// to be empty.
const BUCKETS: RootCauseBucket[] = [
  'ROOT_CAUSE_BUCKET_TRANSIENT_BANK',
  'ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS',
  'ROOT_CAUSE_BUCKET_HARD_DECLINE',
  'ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED',
  'ROOT_CAUSE_BUCKET_RISK_HOLD',
  'ROOT_CAUSE_BUCKET_ABANDONMENT',
  'ROOT_CAUSE_BUCKET_OVERDUE',
];

const ROW_HEIGHT = 34;
const AXIS_HEIGHT = 24;
const LABEL_WIDTH = 176;
const TICK_COUNT = 5;

/** Stable pseudo-random value in [-1, 1] derived from a record id, used to
 *  jitter overlapping points vertically within their row so a tight cluster
 *  reads as a cloud of dots rather than one dot hiding the rest. */
function jitter(id: string): number {
  let h = 0;
  for (let i = 0; i < id.length; i++) {
    h = (h * 31 + id.charCodeAt(i)) | 0;
  }
  return (h % 1000) / 1000;
}

function clamp01(n: number): number {
  return Math.min(1, Math.max(0, n));
}

export function TimelineView({ records }: Props) {
  // Re-render once a second so the "now" marker actually moves across a
  // fixed axis, the same live-countdown idiom as DueCountdown.
  const [, tick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);

  // Pending = has a due_at. That already excludes terminal records (never
  // set) and RECORD_STATE_NUDGED (parked on the customer, never polled),
  // per docs/API_GATEWAY.md, so no separate terminal-state filter is needed.
  const pending = useMemo(() => records.filter((r) => r.due_at !== ''), [records]);

  const byBucket = useMemo(() => {
    const map = new Map<RootCauseBucket, RecordSummary[]>();
    for (const bucket of BUCKETS) map.set(bucket, []);
    for (const r of pending) {
      map.get(r.bucket)?.push(r);
    }
    return map;
  }, [pending]);

  if (pending.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-10 text-center">
        <Clock className="w-6 h-6 text-slate-300 mb-2" />
        <p className="text-sm text-slate-500">Nothing pending right now.</p>
        <p className="text-xs text-slate-400 mt-1 max-w-sm">
          Every record has either settled or is waiting on the customer. Once a retry or nudge
          is scheduled, it will show up here at its due time.
        </p>
      </div>
    );
  }

  const now = Date.now();
  const dueTimes = pending.map((r) => new Date(r.due_at).getTime());
  const rawStart = Math.min(now, ...dueTimes);
  const rawEnd = Math.max(now, ...dueTimes);
  const span = Math.max(rawEnd - rawStart, 5000);
  const padding = span * 0.08;
  const domainStart = rawStart - padding;
  const domainEnd = rawEnd + padding;
  const domainSpan = domainEnd - domainStart;

  const pctFor = (ms: number) => clamp01((ms - domainStart) / domainSpan) * 100;
  const nowPct = pctFor(now);

  const chartHeight = BUCKETS.length * ROW_HEIGHT + AXIS_HEIGHT;

  // Only future ticks get a text label; the moving "now" marker below is
  // the sole source of truth for "now" itself, so a fixed tick that happens
  // to land at or before the current time (the domain's left edge, most of
  // the time) stays unlabelled rather than showing a second, stationary
  // "now".
  const ticks = Array.from({ length: TICK_COUNT }, (_, i) => {
    const frac = i / (TICK_COUNT - 1);
    const value = domainStart + frac * domainSpan;
    const pct = frac * 100;
    const label = value > now + 500 ? `+${formatDuration(value - now)}` : null;
    return { pct, label };
  });

  return (
    <div>
      <div className="flex items-baseline justify-between mb-1">
        <p className="text-xs text-slate-400">
          {pending.length} record{pending.length === 1 ? '' : 's'} waiting on the scheduler, plotted at
          their next due time
        </p>
      </div>

      <div className="flex mt-3">
        <div className="flex-shrink-0" style={{ width: LABEL_WIDTH }}>
          {BUCKETS.map((bucket) => {
            const count = byBucket.get(bucket)?.length ?? 0;
            return (
              <div
                key={bucket}
                className="flex items-center gap-2 pr-3"
                style={{ height: ROW_HEIGHT }}
              >
                <span
                  className="w-2.5 h-2.5 rounded-full flex-shrink-0"
                  style={{ backgroundColor: BUCKET_COLORS[bucket] }}
                />
                <span className="text-xs text-slate-600 truncate flex-1">{BUCKET_LABELS[bucket]}</span>
                <span
                  className={`text-xs tabular-nums flex-shrink-0 ${count > 0 ? 'font-semibold text-slate-700' : 'text-slate-300'}`}
                >
                  {count}
                </span>
              </div>
            );
          })}
          <div style={{ height: AXIS_HEIGHT }} />
        </div>

        <div className="flex-1 relative min-w-0 overflow-hidden" style={{ height: chartHeight }}>
          <svg width="100%" height={chartHeight} className="block overflow-visible">
            {/* Row separators */}
            {BUCKETS.map((bucket, i) => (
              <line
                key={bucket}
                x1="0"
                x2="100%"
                y1={(i + 1) * ROW_HEIGHT}
                y2={(i + 1) * ROW_HEIGHT}
                stroke="#f1f5f9"
                strokeWidth={1}
              />
            ))}

            {/* Axis ticks */}
            {ticks.map((t, i) => (
              <g key={i}>
                <line
                  x1={`${t.pct}%`}
                  x2={`${t.pct}%`}
                  y1={0}
                  y2={BUCKETS.length * ROW_HEIGHT}
                  stroke="#f8fafc"
                  strokeWidth={1}
                />
                {t.label !== null && (
                  <text
                    x={`${t.pct}%`}
                    y={BUCKETS.length * ROW_HEIGHT + 16}
                    fontSize={10}
                    fill="#94a3b8"
                    textAnchor={t.pct < 5 ? 'start' : t.pct > 95 ? 'end' : 'middle'}
                  >
                    {t.label}
                  </text>
                )}
              </g>
            ))}

            {/* Points, row by row */}
            {BUCKETS.map((bucket, rowIndex) => {
              const rowRecords = byBucket.get(bucket) ?? [];
              const rowCenter = rowIndex * ROW_HEIGHT + ROW_HEIGHT / 2;
              const band = ROW_HEIGHT / 2 - 8;
              return rowRecords.map((r) => {
                const dueMs = new Date(r.due_at).getTime();
                const pct = pctFor(dueMs);
                const cy = rowCenter + jitter(r.record_id) * band;
                const remaining = dueMs - now;
                const tooltip = `${BUCKET_LABELS[bucket]} · ${RECORD_TYPE_LABELS[r.type]} · ${formatCurrency(r.amount_paise)} · ${
                  remaining <= 0 ? 'due now' : `due in ${formatDuration(remaining)}`
                }`;
                return (
                  <circle
                    key={r.record_id}
                    cx={`${pct}%`}
                    cy={cy}
                    r={4.5}
                    fill={BUCKET_COLORS[bucket]}
                    fillOpacity={0.75}
                    stroke="white"
                    strokeWidth={1}
                  >
                    <title>{tooltip}</title>
                  </circle>
                );
              });
            })}

            {/* "now" marker, moving live across the fixed axis */}
            <line
              x1={`${nowPct}%`}
              x2={`${nowPct}%`}
              y1={0}
              y2={BUCKETS.length * ROW_HEIGHT}
              stroke="#0f172a"
              strokeWidth={1.5}
              strokeDasharray="3 3"
            />
            <text
              x={`${nowPct}%`}
              y={BUCKETS.length * ROW_HEIGHT + 16}
              fontSize={10}
              fontWeight={600}
              fill="#0f172a"
              textAnchor={nowPct < 5 ? 'start' : nowPct > 95 ? 'end' : 'middle'}
            >
              now
            </text>
          </svg>
        </div>
      </div>
    </div>
  );
}
