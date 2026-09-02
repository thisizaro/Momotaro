import { useMemo, useState } from 'react';
import { FilterX, History } from 'lucide-react';
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
  TIMELINE_BUCKET_GAP,
  TIMELINE_BUCKETS,
  TIMELINE_LABEL_WIDTH,
  TIMELINE_MAX_BODY_HEIGHT,
  TIMELINE_SUB_ROW_HEIGHT,
  TIMELINE_TICK_COUNT,
  amountRadius,
  clamp01,
} from '@/lib/timelineGeometry';
import { EmptyState } from '@/components/EmptyState';
import type { RecordState, RecordSummary, RootCauseBucket } from '@/types';

interface Props {
  records: RecordSummary[];
  onSelect: (id: string) => void;
}

// Neutral connector colour (Unit AO, docs/DEMO_READINESS.md): bucket
// identity is already carried by the row grouping and its label, so the
// connector no longer repeats it. Leaves the state colour on the marker as
// the only meaningful hue in the plot.
const CONNECTOR_COLOR = '#cbd5e1';
const CONNECTOR_COLOR_HOVER = '#64748b';

function sortRecords(records: RecordSummary[]): RecordSummary[] {
  return [...records].sort((a, b) => {
    const diff = new Date(a.first_action_at).getTime() - new Date(b.first_action_at).getTime();
    return diff !== 0 ? diff : a.record_id.localeCompare(b.record_id);
  });
}

/**
 * What actually happened: every record the agent has touched at least once,
 * plotted from when it was first classified (first_action_at) to the most
 * recent thing that happened to it (last_action_at). Unlike LiveTimeline,
 * this never goes empty just because a run finished, since finishing is
 * exactly what fills last_action_at in for the last records still moving
 * (docs/DEMO_READINESS.md Unit AH).
 *
 * Unit AO gave every record its own sub-row within its bucket band (rather
 * than one shared row per bucket) so a dense bucket's connector lines no
 * longer merge into a solid band, and layered click-to-filter on top:
 * clicking a bucket row isolates it (the other buckets collapse to a
 * one-line summary rather than disappearing, so switching focus stays a
 * single click), clicking an outcome in the legend filters to it, and both
 * compose. Filter state is local and resets whenever this component
 * remounts, which TimelineView already does on a batch switch (keyed on
 * the batch id) and for free every time the Live/History toggle swaps
 * component type.
 */
export function HistoryTimeline({ records, onSelect }: Props) {
  const [bucketFilter, setBucketFilter] = useState<RootCauseBucket | null>(null);
  const [stateFilter, setStateFilter] = useState<RecordState | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);

  // Anything the agent has acted on at least once. RECORD_STATE_NEW records
  // that just landed and haven't been classified yet have no
  // first_action_at (docs/API_GATEWAY.md) and are correctly absent here,
  // the same way LiveTimeline excludes terminal records from its own view.
  const acted = useMemo(() => records.filter((r) => r.first_action_at !== ''), [records]);

  // The legend always lists every state present anywhere in the run,
  // regardless of the current filters, so it stays a stable reference
  // rather than shifting under the reader as they filter, and so a state
  // absent from an isolated bucket is still one click away (which is also
  // how a filtered-empty result becomes reachable at all).
  const statesPresent = useMemo(
    () => STATE_ORDER.filter((s) => acted.some((r) => r.current_state === s)),
    [acted],
  );

  // Bucket counts and drawn records both honour the outcome filter (so a
  // collapsed bucket's count reflects it too), independent of which bucket
  // is isolated.
  const stateScoped = useMemo(
    () => (stateFilter ? acted.filter((r) => r.current_state === stateFilter) : acted),
    [acted, stateFilter],
  );

  const byBucket = useMemo(() => {
    const map = new Map<RootCauseBucket, RecordSummary[]>();
    for (const bucket of TIMELINE_BUCKETS) map.set(bucket, []);
    for (const r of stateScoped) {
      map.get(r.bucket)?.push(r);
    }
    return map;
  }, [stateScoped]);

  // What's actually drawn: every record in the isolated bucket if one is
  // set, otherwise every bucket's records (matching the outcome filter
  // either way).
  const visibleRecords = useMemo(() => {
    if (bucketFilter) return byBucket.get(bucketFilter) ?? [];
    return stateScoped;
  }, [bucketFilter, byBucket, stateScoped]);

  if (acted.length === 0) {
    return (
      <EmptyState
        icon={History}
        title="Nothing has happened yet"
        description="No record has been classified yet. Once the agent acts on the first one, its history will start building here."
      />
    );
  }

  const clearFilters = () => {
    setBucketFilter(null);
    setStateFilter(null);
  };

  // Domain and marker scale come from the full unfiltered run, not the
  // current filter: filtering narrows which records draw, it never
  // rescales the axis or shrinks what "biggest amount" means, so a judge
  // never loses their bearings switching filters.
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

  // Band heights: the isolated bucket (or every bucket, when none is
  // isolated) gets one sub-row per record; every other bucket collapses to
  // a single compact row, just enough to stay visible and clickable. This
  // is what keeps "isolate a bucket" from being a one-way door: the other
  // six buckets never leave the label column, they just get out of the way.
  const bandHeights = TIMELINE_BUCKETS.map((bucket) => {
    const expanded = !bucketFilter || bucketFilter === bucket;
    const count = byBucket.get(bucket)?.length ?? 0;
    return expanded ? Math.max(count, 1) * TIMELINE_SUB_ROW_HEIGHT : TIMELINE_SUB_ROW_HEIGHT;
  });

  const bandOffsets: number[] = [];
  let cursor = 0;
  TIMELINE_BUCKETS.forEach((_, i) => {
    bandOffsets.push(cursor);
    cursor += bandHeights[i] + (i < TIMELINE_BUCKETS.length - 1 ? TIMELINE_BUCKET_GAP : 0);
  });
  const contentHeight = cursor;

  const totalActed = acted.length;
  const visibleCount = visibleRecords.length;
  const filtersActive = bucketFilter !== null || stateFilter !== null;

  return (
    <div>
      <div className="flex items-baseline justify-between mb-1">
        <p className="text-xs text-slate-400">
          {visibleCount === totalActed
            ? `${totalActed} record${totalActed === 1 ? '' : 's'} the agent has acted on, plotted from first action to most recent`
            : `${visibleCount} of ${totalActed} record${totalActed === 1 ? '' : 's'} shown, plotted from first action to most recent`}
        </p>
        <p className="text-xs text-slate-500">circle size = amount at risk</p>
      </div>

      {filtersActive && (
        <div className="flex flex-wrap items-center gap-2 mt-1.5">
          {bucketFilter && (
            <button
              type="button"
              data-testid="active-filter-bucket"
              onClick={() => setBucketFilter(null)}
              className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-slate-800 text-white text-[11px] font-medium hover:bg-slate-700 transition-colors"
            >
              {BUCKET_LABELS[bucketFilter]}
              <span aria-hidden="true">×</span>
            </button>
          )}
          {stateFilter && (
            <button
              type="button"
              data-testid="active-filter-state"
              onClick={() => setStateFilter(null)}
              className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-slate-800 text-white text-[11px] font-medium hover:bg-slate-700 transition-colors"
            >
              {STATE_LABELS[stateFilter]}
              <span aria-hidden="true">×</span>
            </button>
          )}
          <button
            type="button"
            data-testid="clear-filters"
            onClick={clearFilters}
            className="text-[11px] text-slate-400 hover:text-slate-600 underline underline-offset-2 transition-colors"
          >
            Clear filters, show everything
          </button>
        </div>
      )}

      {visibleRecords.length === 0 ? (
        <EmptyState
          icon={FilterX}
          title="No records match this filter"
          description="Nothing acted-on falls under this combination of bucket and outcome. Clear the filter above to see everything again."
          size="inline"
        />
      ) : (
        <>
          <div className="flex mt-3">
            <div
              data-testid="history-scroll-body"
              className="flex w-full scrollbar-thin"
              style={{ maxHeight: TIMELINE_MAX_BODY_HEIGHT, overflowY: 'auto', overflowX: 'hidden' }}
            >
              <div className="flex-shrink-0" style={{ width: TIMELINE_LABEL_WIDTH }}>
                {TIMELINE_BUCKETS.map((bucket, i) => {
                  const count = byBucket.get(bucket)?.length ?? 0;
                  const isActive = bucketFilter === bucket;
                  const isCollapsed = bucketFilter !== null && !isActive;
                  return (
                    <button
                      key={bucket}
                      type="button"
                      data-testid="bucket-row"
                      data-active={isActive}
                      aria-pressed={isActive}
                      title={isActive ? `Showing only ${BUCKET_LABELS[bucket]}. Click to restore all buckets.` : `Click to show only ${BUCKET_LABELS[bucket]}.`}
                      onClick={() => setBucketFilter((cur) => (cur === bucket ? null : bucket))}
                      className={`w-full flex items-center gap-2 pr-3 text-left rounded-md transition-colors ${
                        isActive ? 'bg-slate-100' : 'hover:bg-slate-50'
                      } ${isCollapsed ? 'opacity-50' : ''}`}
                      style={{ height: bandHeights[i] }}
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
                    </button>
                  );
                })}
              </div>

              <div className="flex-1 relative min-w-0" style={{ height: contentHeight }}>
                <svg width="100%" height={contentHeight} className="block overflow-visible">
                  {/* Band separators */}
                  {TIMELINE_BUCKETS.slice(0, -1).map((bucket, i) => (
                    <line
                      key={bucket}
                      x1="0"
                      x2="100%"
                      y1={bandOffsets[i] + bandHeights[i] + TIMELINE_BUCKET_GAP / 2}
                      y2={bandOffsets[i] + bandHeights[i] + TIMELINE_BUCKET_GAP / 2}
                      stroke="#f1f5f9"
                      strokeWidth={1}
                    />
                  ))}

                  {/* Records, one sub-row per record within its bucket band:
                      a neutral connector from first action to last (Unit AO:
                      no longer bucket-coloured, so it never competes with
                      the state colour on the marker), a marker at the last
                      action, sized by amount, coloured by current
                      state/outcome. Only the isolated bucket (or every
                      bucket, when none is isolated) draws its records; a
                      collapsed bucket draws nothing but its label row. */}
                  {TIMELINE_BUCKETS.map((bucket, i) => {
                    const expanded = !bucketFilter || bucketFilter === bucket;
                    if (!expanded) return null;
                    const rowRecords = sortRecords(byBucket.get(bucket) ?? []);
                    const bandTop = bandOffsets[i];
                    return rowRecords.map((r, rowIdx) => {
                      const firstMs = new Date(r.first_action_at).getTime();
                      const lastMs = r.last_action_at ? new Date(r.last_action_at).getTime() : firstMs;
                      const x1 = pctFor(firstMs);
                      const x2 = pctFor(lastMs);
                      const cy = bandTop + rowIdx * TIMELINE_SUB_ROW_HEIGHT + TIMELINE_SUB_ROW_HEIGHT / 2;
                      const radius = amountRadius(r.amount_paise, maxAmountPaise);
                      const isHovered = hoveredId === r.record_id;
                      const isDimmed = hoveredId !== null && !isHovered;
                      const startedLabel = formatTime(r.first_action_at);
                      const lastLabel = r.last_action_at ? formatTime(r.last_action_at) : startedLabel;
                      const tooltip = `${BUCKET_LABELS[bucket]} · ${RECORD_TYPE_LABELS[r.type]} · ${formatCurrency(r.amount_paise)} · ${
                        STATE_LABELS[r.current_state]
                      } · started ${startedLabel}${lastLabel !== startedLabel ? `, last action ${lastLabel}` : ''}`;
                      return (
                        <g
                          key={r.record_id}
                          data-testid="history-point"
                          data-record-id={r.record_id}
                          className="cursor-pointer"
                          onClick={() => onSelect(r.record_id)}
                          onMouseEnter={() => setHoveredId(r.record_id)}
                          onMouseLeave={() => setHoveredId((cur) => (cur === r.record_id ? null : cur))}
                        >
                          {x2 > x1 && (
                            <line
                              data-testid="history-connector"
                              x1={`${x1}%`}
                              x2={`${x2}%`}
                              y1={cy}
                              y2={cy}
                              stroke={isHovered ? CONNECTOR_COLOR_HOVER : CONNECTOR_COLOR}
                              strokeOpacity={isDimmed ? 0.15 : isHovered ? 1 : 0.7}
                              strokeWidth={isHovered ? 2 : 1.5}
                            />
                          )}
                          <circle
                            cx={`${x2}%`}
                            cy={cy}
                            r={radius}
                            fill={STATE_FILL[r.current_state]}
                            fillOpacity={isDimmed ? 0.2 : isHovered ? 1 : 0.85}
                            stroke={isHovered ? '#0f172a' : 'white'}
                            strokeWidth={isHovered ? 1.5 : 1}
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
          </div>

          {/* Axis: pinned below the scrollable record area rather than
              inside it, so the time reference stays on screen while a tall
              unfiltered view scrolls. */}
          <div className="flex">
            <div className="flex-shrink-0" style={{ width: TIMELINE_LABEL_WIDTH }} />
            <div className="flex-1 relative min-w-0 overflow-hidden" style={{ height: TIMELINE_AXIS_HEIGHT }}>
              <svg width="100%" height={TIMELINE_AXIS_HEIGHT} className="block overflow-visible">
                {ticks.map((t, i) => (
                  <g key={i}>
                    <line x1={`${t.pct}%`} x2={`${t.pct}%`} y1={0} y2={4} stroke="#e2e8f0" strokeWidth={1} />
                    <text
                      x={`${t.pct}%`}
                      y={16}
                      fontSize={10}
                      fontWeight={600}
                      fill="#475569"
                      textAnchor={t.pct < 5 ? 'start' : t.pct > 95 ? 'end' : 'middle'}
                    >
                      {t.real}
                    </text>
                    <text
                      x={`${t.pct}%`}
                      y={28}
                      fontSize={9}
                      fill="#94a3b8"
                      textAnchor={t.pct < 5 ? 'start' : t.pct > 95 ? 'end' : 'middle'}
                    >
                      {t.simulated}
                    </text>
                  </g>
                ))}
              </svg>
            </div>
          </div>
        </>
      )}

      {/* Outcome legend: only the states actually present, in the shared
          fixed order, so it reads the same left-to-right vocabulary as
          StateDistribution rather than a re-sorted one. Doubles as a
          filter: click a state to isolate it, click again to restore. */}
      <div className="flex flex-wrap gap-x-1.5 gap-y-1.5 mt-3 pt-3 border-t border-slate-100">
        {statesPresent.map((state: RecordState) => {
          const isActive = stateFilter === state;
          return (
            <button
              key={state}
              type="button"
              data-testid="state-legend-item"
              data-active={isActive}
              aria-pressed={isActive}
              onClick={() => setStateFilter((cur) => (cur === state ? null : state))}
              className={`flex items-center gap-1.5 rounded-full px-2 py-1 transition-colors ${
                isActive ? 'bg-slate-100 ring-1 ring-slate-300' : 'hover:bg-slate-50'
              }`}
            >
              <span
                className="w-2 h-2 rounded-full flex-shrink-0"
                style={{ backgroundColor: STATE_FILL[state] }}
              />
              <span className="text-xs text-slate-500">{STATE_LABELS[state]}</span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
