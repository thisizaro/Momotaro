import { useMemo } from 'react';
import { History } from 'lucide-react';
import {
  BUCKET_COLORS,
  BUCKET_LABELS,
  RECORD_TYPE_LABELS,
  STATE_FILL,
  STATE_LABELS,
  STATE_ORDER,
  formatCurrency,
  formatTime,
} from '@/lib/format';
import { formatSimulatedElapsed } from '@/lib/demoTime';
import {
  TIMELINE_AXIS_HEIGHT,
  TIMELINE_BUCKETS,
  TIMELINE_LABEL_WIDTH,
  TIMELINE_ROW_HEIGHT,
  TIMELINE_TICK_COUNT,
  amountRadius,
  clamp01,
  jitter,
} from '@/lib/timelineGeometry';
import { EmptyState } from '@/components/EmptyState';
import type { RecordState, RecordSummary, RootCauseBucket } from '@/types';

interface Props {
  records: RecordSummary[];
  onSelect: (id: string) => void;
}

/**
 * What actually happened: every record the agent has touched at least once,
 * plotted from when it was first classified (first_action_at) to the most
 * recent thing that happened to it (last_action_at). Unlike LiveTimeline,
 * this never goes empty just because a run finished, since finishing is
 * exactly what fills last_action_at in for the last records still moving
 * (docs/DEMO_READINESS.md Unit AH).
 */
export function HistoryTimeline({ records, onSelect }: Props) {
  // Anything the agent has acted on at least once. RECORD_STATE_NEW records
  // that just landed and haven't been classified yet have no
  // first_action_at (docs/API_GATEWAY.md) and are correctly absent here,
  // the same way LiveTimeline excludes terminal records from its own view.
  const acted = useMemo(() => records.filter((r) => r.first_action_at !== ''), [records]);

  const byBucket = useMemo(() => {
    const map = new Map<RootCauseBucket, RecordSummary[]>();
    for (const bucket of TIMELINE_BUCKETS) map.set(bucket, []);
    for (const r of acted) {
      map.get(r.bucket)?.push(r);
    }
    return map;
  }, [acted]);

  const statesPresent = useMemo(
    () => STATE_ORDER.filter((s) => acted.some((r) => r.current_state === s)),
    [acted],
  );

  if (acted.length === 0) {
    return (
      <EmptyState
        icon={History}
        title="Nothing has happened yet"
        description="No record has been classified yet. Once the agent acts on the first one, its history will start building here."
      />
    );
  }

  const firstTimes = acted.map((r) => new Date(r.first_action_at).getTime());
  const lastTimes = acted.map((r) => new Date(r.last_action_at || r.first_action_at).getTime());
  const rawStart = Math.min(...firstTimes);
  const rawEnd = Math.max(...lastTimes);
  const span = Math.max(rawEnd - rawStart, 1000);
  const padding = span * 0.08;
  const domainStart = rawStart - padding;
  const domainEnd = rawEnd + padding;
  const domainSpan = domainEnd - domainStart;

  const pctFor = (ms: number) => clamp01((ms - domainStart) / domainSpan) * 100;
  const maxAmountPaise = Math.max(...acted.map((r) => r.amount_paise), 1);

  const chartHeight = TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT + TIMELINE_AXIS_HEIGHT;

  // Every tick carries the real wall-clock time it falls at (bold) and the
  // simulated equivalent elapsed since the run's own start (muted,
  // underneath): docs/DEMO_READINESS.md Unit AH's "show both" requirement,
  // the honest version of the time-compression claim rather than something
  // a judge has to take on trust.
  const ticks = Array.from({ length: TIMELINE_TICK_COUNT }, (_, i) => {
    const frac = i / (TIMELINE_TICK_COUNT - 1);
    const value = domainStart + frac * domainSpan;
    const pct = frac * 100;
    return {
      pct,
      real: formatTime(new Date(value).toISOString()),
      simulated: formatSimulatedElapsed(Math.max(value - domainStart, 0)),
    };
  });

  return (
    <div>
      <div className="flex items-baseline justify-between mb-1">
        <p className="text-xs text-slate-400">
          {acted.length} record{acted.length === 1 ? '' : 's'} the agent has acted on, plotted from first
          action to most recent
        </p>
        <p className="text-xs text-slate-300">circle size = amount at risk</p>
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

            {/* Axis ticks: real time on top, simulated equivalent beneath */}
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
                <text
                  x={`${t.pct}%`}
                  y={TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT + 14}
                  fontSize={10}
                  fontWeight={600}
                  fill="#475569"
                  textAnchor={t.pct < 5 ? 'start' : t.pct > 95 ? 'end' : 'middle'}
                >
                  {t.real}
                </text>
                <text
                  x={`${t.pct}%`}
                  y={TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT + 26}
                  fontSize={9}
                  fill="#94a3b8"
                  textAnchor={t.pct < 5 ? 'start' : t.pct > 95 ? 'end' : 'middle'}
                >
                  {t.simulated}
                </text>
              </g>
            ))}

            {/* Records, row by row: a thin connector from first action to
                last, a marker at the last, sized by amount and colored by
                current state/outcome. */}
            {TIMELINE_BUCKETS.map((bucket, rowIndex) => {
              const rowRecords = byBucket.get(bucket) ?? [];
              const rowCenter = rowIndex * TIMELINE_ROW_HEIGHT + TIMELINE_ROW_HEIGHT / 2;
              const band = TIMELINE_ROW_HEIGHT / 2 - 8;
              return rowRecords.map((r) => {
                const firstMs = new Date(r.first_action_at).getTime();
                const lastMs = r.last_action_at ? new Date(r.last_action_at).getTime() : firstMs;
                const x1 = pctFor(firstMs);
                const x2 = pctFor(lastMs);
                const cy = rowCenter + jitter(r.record_id) * band;
                const radius = amountRadius(r.amount_paise, maxAmountPaise);
                const startedLabel = formatTime(r.first_action_at);
                const lastLabel = r.last_action_at ? formatTime(r.last_action_at) : startedLabel;
                const tooltip = `${BUCKET_LABELS[bucket]} · ${RECORD_TYPE_LABELS[r.type]} · ${formatCurrency(r.amount_paise)} · ${
                  STATE_LABELS[r.current_state]
                } · started ${startedLabel}${lastLabel !== startedLabel ? `, last action ${lastLabel}` : ''}`;
                return (
                  <g
                    key={r.record_id}
                    data-testid="history-point"
                    className="cursor-pointer"
                    onClick={() => onSelect(r.record_id)}
                  >
                    {x2 > x1 && (
                      <line
                        x1={`${x1}%`}
                        x2={`${x2}%`}
                        y1={cy}
                        y2={cy}
                        stroke={BUCKET_COLORS[bucket]}
                        strokeOpacity={0.35}
                        strokeWidth={2}
                      />
                    )}
                    <circle
                      cx={`${x2}%`}
                      cy={cy}
                      r={radius}
                      fill={STATE_FILL[r.current_state]}
                      fillOpacity={0.85}
                      stroke="white"
                      strokeWidth={1}
                    >
                      <title>{tooltip}</title>
                    </circle>
                  </g>
                );
              });
            })}
          </svg>
        </div>
      </div>

      {/* State legend: only the states actually present, in the shared
          fixed order, so it reads the same left-to-right vocabulary as
          StateDistribution rather than a re-sorted one. */}
      <div className="flex flex-wrap gap-x-4 gap-y-1.5 mt-3 pt-3 border-t border-slate-100">
        {statesPresent.map((state: RecordState) => (
          <div key={state} className="flex items-center gap-1.5">
            <span
              className="w-2 h-2 rounded-full flex-shrink-0"
              style={{ backgroundColor: STATE_FILL[state] }}
            />
            <span className="text-xs text-slate-500">{STATE_LABELS[state]}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
