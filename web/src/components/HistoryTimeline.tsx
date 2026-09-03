import { useMemo, useState } from 'react';
import { FilterX, History, Search } from 'lucide-react';
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
  TIMELINE_ROW_HEIGHT,
  TIMELINE_SUB_ROW_HEIGHT,
  TIMELINE_TICK_COUNT,
  amountRadius,
  amountRadiusCompact,
  clamp01,
  jitter,
} from '@/lib/timelineGeometry';
import { EmptyState } from '@/components/EmptyState';
import type { RecordState, RecordSummary, RootCauseBucket } from '@/types';

interface Props {
  records: RecordSummary[];
  onSelect: (id: string) => void;
}

type ViewMode = 'compact' | 'gantt';

// Neutral connector colour (kept from Unit AO, docs/DEMO_READINESS.md):
// bucket identity is already carried by the row grouping and its label, so
// the connector no longer repeats it. Leaves the state colour on the marker
// as the only meaningful hue in the plot, in both view modes.
const CONNECTOR_COLOR = '#cbd5e1';
const CONNECTOR_COLOR_HOVER = '#64748b';

function sortRecords(records: RecordSummary[]): RecordSummary[] {
  return [...records].sort((a, b) => {
    const diff = new Date(a.first_action_at).getTime() - new Date(b.first_action_at).getTime();
    return diff !== 0 ? diff : a.record_id.localeCompare(b.record_id);
  });
}

/**
 * Whether a record matches a search query: on its id (substring, so a short
 * prefix like the records table's `f43f0a35` works) or on its amount in
 * rupees (substring on the digits only, so "1235" finds a ₹1,235 record
 * without the reader having to type the currency symbol or a comma). Amount
 * is the display unit, not paise, because that is what a person reads off
 * the chart and the drawer; matching on raw paise would ask them to do
 * arithmetic first.
 */
function matchesQuery(r: RecordSummary, trimmedQuery: string, queryDigits: string): boolean {
  if (trimmedQuery === '') return true;
  if (r.record_id.toLowerCase().includes(trimmedQuery)) return true;
  if (queryDigits === '') return false;
  const rupees = String(Math.round(r.amount_paise / 100));
  return rupees.includes(queryDigits);
}

/**
 * What actually happened: every record the agent has touched at least once,
 * plotted from when it was first classified (first_action_at) to the most
 * recent thing that happened to it (last_action_at). Unlike LiveTimeline,
 * this never goes empty just because a run finished, since finishing is
 * exactly what fills last_action_at in for the last records still moving
 * (docs/DEMO_READINESS.md Unit AH).
 *
 * Unit AP (docs/DEMO_READINESS.md), reversing part of Unit AO after direct
 * user review: "too much scrolling and so gapped... the initial view of the
 * last one was better, it gave a better idea in one view". Unit AO's
 * per-record Gantt layout (one thin sub-row per record) gave per-record
 * legibility nobody asked for, at the cost of the whole-batch read at a
 * glance that this panel exists for. So the compact view Unit AH originally
 * shipped, one fixed-height row per bucket with jittered points, is the
 * default again: `view` starts at 'compact' and every bucket's row is
 * TIMELINE_ROW_HEIGHT regardless of how many records land in it, so the
 * chart's height never depends on batch size and never needs to scroll at
 * typical density (80-100 records across 7 buckets).
 *
 * What Unit AO added is kept, not reverted: the neutral connector colour
 * (state, not bucket, is the one meaningful hue), the caption contrast fix,
 * click-a-bucket-to-isolate, click-a-legend-outcome-to-filter (composing
 * with each other), hover-to-highlight, and the filter chips with a "clear
 * filters" affordance. Unit AO's per-record layout itself is not gone
 * either: it is the 'gantt' view, reached through the "Per-record" toggle
 * next to "Compact", exactly the opt-in the user asked for ("clicking on a
 * specific entry will open the record drawer and there will be an option to
 * see the gantt chart"). Both views share the same filter/search state, so
 * switching views never loses a reader's place.
 *
 * Unit AP also adds search, the other thing the user asked for directly
 * ("or search a specific entry"): a record id or amount narrows the view to
 * the match, through the same isolate mechanism the bucket and outcome
 * filters already use, so a match is unambiguous and a non-match is an
 * honest empty state rather than a silently blank chart.
 */
export function HistoryTimeline({ records, onSelect }: Props) {
  const [bucketFilter, setBucketFilter] = useState<RootCauseBucket | null>(null);
  const [stateFilter, setStateFilter] = useState<RecordState | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [view, setView] = useState<ViewMode>('compact');
  const [query, setQuery] = useState('');

  // Anything the agent has acted on at least once. RECORD_STATE_NEW records
  // that just landed and haven't been classified yet have no
  // first_action_at (docs/API_GATEWAY.md) and are correctly absent here,
  // the same way LiveTimeline excludes terminal records from its own view.
  const acted = useMemo(() => records.filter((r) => r.first_action_at !== ''), [records]);

  // The legend always lists every state present anywhere in the run,
  // regardless of the current filters, so it stays a stable reference
  // rather than shifting under the reader as they filter, and so a state
  // absent from an isolated bucket (or a search match) is still one click
  // away.
  const statesPresent = useMemo(
    () => STATE_ORDER.filter((s) => acted.some((r) => r.current_state === s)),
    [acted],
  );

  const trimmedQuery = query.trim().toLowerCase();
  const queryDigits = trimmedQuery.replace(/[^0-9]/g, '');
  const searchActive = trimmedQuery !== '';

  const searchScoped = useMemo(
    () => (searchActive ? acted.filter((r) => matchesQuery(r, trimmedQuery, queryDigits)) : acted),
    [acted, searchActive, trimmedQuery, queryDigits],
  );

  // Bucket counts and drawn records honour search and the outcome filter
  // (so a collapsed bucket's count reflects both), independent of which
  // bucket is isolated.
  const stateScoped = useMemo(
    () => (stateFilter ? searchScoped.filter((r) => r.current_state === stateFilter) : searchScoped),
    [searchScoped, stateFilter],
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
  // set, otherwise every bucket's records (matching search/outcome either
  // way).
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
    setQuery('');
  };

  // Domain and marker scale come from the full unfiltered run, not the
  // current filter: filtering narrows which records draw, it never
  // rescales the axis or shrinks what "biggest amount" means, so a judge
  // never loses their bearings switching filters or views.
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

  const compact = view === 'compact';

  // Band heights: in Compact, every bucket's row is a fixed
  // TIMELINE_ROW_HEIGHT regardless of record count or filter, which is what
  // makes "no scrolling at typical batch size" a structural guarantee
  // rather than a hope. In Per-record (Gantt), the isolated bucket (or
  // every bucket, when none is isolated) gets one sub-row per record and
  // every other bucket collapses to a single compact row, matching Unit
  // AO's original behaviour.
  const bandHeights = TIMELINE_BUCKETS.map((bucket) => {
    if (compact) return TIMELINE_ROW_HEIGHT;
    const expanded = !bucketFilter || bucketFilter === bucket;
    const count = byBucket.get(bucket)?.length ?? 0;
    return expanded ? Math.max(count, 1) * TIMELINE_SUB_ROW_HEIGHT : TIMELINE_SUB_ROW_HEIGHT;
  });
  const bandGap = compact ? 0 : TIMELINE_BUCKET_GAP;

  const bandOffsets: number[] = [];
  let cursor = 0;
  TIMELINE_BUCKETS.forEach((_, i) => {
    bandOffsets.push(cursor);
    cursor += bandHeights[i] + (i < TIMELINE_BUCKETS.length - 1 ? bandGap : 0);
  });
  const contentHeight = cursor;

  const totalActed = acted.length;
  const visibleCount = visibleRecords.length;
  const filtersActive = bucketFilter !== null || stateFilter !== null || searchActive;

  const noMatchDescription = searchActive
    ? `No acted-on record's id or amount matches "${query.trim()}". Clear the search to see everything again.`
    : 'Nothing acted-on falls under this combination of bucket and outcome. Clear the filter above to see everything again.';

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

      <div className="flex items-center justify-between gap-3 mt-2">
        <div className="relative flex-shrink-0">
          <Search className="w-3.5 h-3.5 text-slate-300 absolute left-2 top-1/2 -translate-y-1/2 pointer-events-none" />
          <input
            type="text"
            data-testid="timeline-search-input"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search id or amount"
            className="text-xs border border-slate-200 rounded-lg pl-7 pr-2.5 py-1 w-44 focus:outline-none focus:ring-1 focus:ring-slate-300"
          />
        </div>

        <div className="inline-flex items-center gap-0.5 bg-slate-100 rounded-lg p-0.5" role="tablist" aria-label="Timeline detail">
          <button
            type="button"
            role="tab"
            aria-selected={compact}
            data-testid="view-toggle-compact"
            title="One row per bucket, everything visible at once"
            onClick={() => setView('compact')}
            className={`px-2 py-1 text-[11px] font-medium rounded-md transition-colors ${
              compact ? 'bg-white text-slate-900 shadow-sm' : 'text-slate-500 hover:text-slate-700'
            }`}
          >
            Compact
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={!compact}
            data-testid="view-toggle-gantt"
            title="One row per record (Gantt-style), for following a single record's journey"
            onClick={() => setView('gantt')}
            className={`px-2 py-1 text-[11px] font-medium rounded-md transition-colors ${
              !compact ? 'bg-white text-slate-900 shadow-sm' : 'text-slate-500 hover:text-slate-700'
            }`}
          >
            Per-record
          </button>
        </div>
      </div>

      {filtersActive && (
        <div className="flex flex-wrap items-center gap-2 mt-2">
          {searchActive && (
            <button
              type="button"
              data-testid="active-filter-search"
              onClick={() => setQuery('')}
              className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-slate-800 text-white text-[11px] font-medium hover:bg-slate-700 transition-colors"
            >
              &quot;{query.trim()}&quot;
              <span aria-hidden="true">×</span>
            </button>
          )}
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
          icon={searchActive ? Search : FilterX}
          title="No records match this filter"
          description={noMatchDescription}
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
                <svg
                  data-testid="history-svg"
                  width="100%"
                  height={contentHeight}
                  className="block overflow-visible"
                >
                  {/* Band separators */}
                  {TIMELINE_BUCKETS.slice(0, -1).map((bucket, i) => (
                    <line
                      key={bucket}
                      x1="0"
                      x2="100%"
                      y1={bandOffsets[i] + bandHeights[i] + bandGap / 2}
                      y2={bandOffsets[i] + bandHeights[i] + bandGap / 2}
                      stroke="#f1f5f9"
                      strokeWidth={1}
                    />
                  ))}

                  {/* Records: in Compact, every record in a bucket shares
                      that bucket's one row, jittered vertically so a dense
                      bucket reads as a cloud of dots rather than a single
                      overlapping stack. In Per-record (Gantt), each record
                      gets its own sub-row instead. Either way: a neutral
                      connector from first action to last, a marker at the
                      last action, sized by amount, coloured by current
                      state/outcome. Only the isolated bucket (or every
                      bucket, when none is isolated) draws its records; a
                      collapsed bucket draws nothing but its label row. */}
                  {TIMELINE_BUCKETS.map((bucket, i) => {
                    const expanded = !bucketFilter || bucketFilter === bucket;
                    if (!expanded) return null;
                    const bandTop = bandOffsets[i];
                    const bandHeight = bandHeights[i];
                    const rawRecords = byBucket.get(bucket) ?? [];
                    const rowRecords = compact ? rawRecords : sortRecords(rawRecords);
                    return rowRecords.map((r, rowIdx) => {
                      const firstMs = new Date(r.first_action_at).getTime();
                      const lastMs = r.last_action_at ? new Date(r.last_action_at).getTime() : firstMs;
                      const x1 = pctFor(firstMs);
                      const x2 = pctFor(lastMs);
                      const cy = compact
                        ? bandTop + bandHeight / 2 + jitter(r.record_id) * (bandHeight / 2 - 8)
                        : bandTop + rowIdx * TIMELINE_SUB_ROW_HEIGHT + TIMELINE_SUB_ROW_HEIGHT / 2;
                      const radius = compact
                        ? amountRadiusCompact(r.amount_paise, maxAmountPaise)
                        : amountRadius(r.amount_paise, maxAmountPaise);
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
              inside it, so the time reference stays on screen if a Gantt
              view is tall enough to scroll. */}
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
