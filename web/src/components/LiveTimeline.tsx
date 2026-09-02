import { useEffect, useMemo, useState } from 'react';
import { Clock } from 'lucide-react';
import { BUCKET_COLORS, BUCKET_LABELS, RECORD_TYPE_LABELS, STATE_LABELS, formatCurrency, formatDuration } from '@/lib/format';
import {
  TIMELINE_AXIS_HEIGHT,
  TIMELINE_BUCKETS,
  TIMELINE_LABEL_WIDTH,
  TIMELINE_ROW_HEIGHT,
  TIMELINE_TICK_COUNT,
  clamp01,
  jitter,
} from '@/lib/timelineGeometry';
import { EmptyState } from '@/components/EmptyState';
import type { RecordSummary, RootCauseBucket } from '@/types';

interface Props {
  records: RecordSummary[];
  onSelect: (id: string) => void;
}

/**
 * What's scheduled: every record the Decision Engine's scheduler is
 * currently waiting on, plotted at its next due time. Empties the instant a
 * run finishes, which is exactly what the History toggle
 * (docs/DEMO_READINESS.md Unit AH) exists to cover.
 */
export function LiveTimeline({ records, onSelect }: Props) {
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
    for (const bucket of TIMELINE_BUCKETS) map.set(bucket, []);
    for (const r of pending) {
      map.get(r.bucket)?.push(r);
    }
    return map;
  }, [pending]);

  if (pending.length === 0) {
    return (
      <EmptyState
        icon={Clock}
        title="Nothing pending right now"
        description="Every record has either settled or is waiting on the customer. Once a retry or nudge is scheduled, it will show up here at its due time. Switch to History to see what already happened."
      />
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

  const chartHeight = TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT + TIMELINE_AXIS_HEIGHT;

  // Only future ticks get a text label; the moving "now" marker below is
  // the sole source of truth for "now" itself, so a fixed tick that happens
  // to land at or before the current time (the domain's left edge, most of
  // the time) stays unlabelled rather than showing a second, stationary
  // "now".
  const ticks = Array.from({ length: TIMELINE_TICK_COUNT }, (_, i) => {
    const frac = i / (TIMELINE_TICK_COUNT - 1);
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
        <div className="flex-shrink-0" style={{ width: TIMELINE_LABEL_WIDTH }}>
          {TIMELINE_BUCKETS.map((bucket) => {
            const count = byBucket.get(bucket)?.length ?? 0;
            return (
              <div
                key={bucket}
                className="flex items-center gap-2 pr-3"
                style={{ height: TIMELINE_ROW_HEIGHT }}
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
          <div style={{ height: TIMELINE_AXIS_HEIGHT }} />
        </div>

        <div className="flex-1 relative min-w-0 overflow-hidden" style={{ height: chartHeight }}>
          <svg width="100%" height={chartHeight} className="block overflow-visible">
            {/* Row separators */}
            {TIMELINE_BUCKETS.map((bucket, i) => (
              <line
                key={bucket}
                x1="0"
                x2="100%"
                y1={(i + 1) * TIMELINE_ROW_HEIGHT}
                y2={(i + 1) * TIMELINE_ROW_HEIGHT}
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
                  y2={TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT}
                  stroke="#f8fafc"
                  strokeWidth={1}
                />
                {t.label !== null && (
                  <text
                    x={`${t.pct}%`}
                    y={TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT + 16}
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
            {TIMELINE_BUCKETS.map((bucket, rowIndex) => {
              const rowRecords = byBucket.get(bucket) ?? [];
              const rowCenter = rowIndex * TIMELINE_ROW_HEIGHT + TIMELINE_ROW_HEIGHT / 2;
              const band = TIMELINE_ROW_HEIGHT / 2 - 8;
              return rowRecords.map((r) => {
                const dueMs = new Date(r.due_at).getTime();
                const pct = pctFor(dueMs);
                const cy = rowCenter + jitter(r.record_id) * band;
                const remaining = dueMs - now;
                const tooltip = `${BUCKET_LABELS[bucket]} · ${RECORD_TYPE_LABELS[r.type]} · ${formatCurrency(r.amount_paise)} · ${STATE_LABELS[r.current_state]} · ${
                  remaining <= 0 ? 'due now' : `due in ${formatDuration(remaining)}`
                }`;
                return (
                  <circle
                    key={r.record_id}
                    data-testid="live-point"
                    cx={`${pct}%`}
                    cy={cy}
                    r={4.5}
                    fill={BUCKET_COLORS[bucket]}
                    fillOpacity={0.75}
                    stroke="white"
                    strokeWidth={1}
                    className="cursor-pointer"
                    onClick={() => onSelect(r.record_id)}
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
              y2={TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT}
              stroke="#0f172a"
              strokeWidth={1.5}
              strokeDasharray="3 3"
            />
            <text
              x={`${nowPct}%`}
              y={TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT + 16}
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
