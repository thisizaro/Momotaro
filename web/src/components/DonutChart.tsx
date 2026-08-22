import { useMemo } from 'react';
import { BUCKET_COLORS, BUCKET_LABELS, formatCurrencyShort } from '@/lib/format';
import type { BucketBreakdown, RootCauseBucket } from '@/types';

interface Props {
  data: Record<RootCauseBucket, BucketBreakdown>;
}

const BUCKETS: RootCauseBucket[] = ['transient', 'hard_decline', 'risk_hold'];

export function DonutChart({ data }: Props) {
  const total = BUCKETS.reduce((sum, b) => sum + data[b].count, 0);

  const segments = useMemo(() => {
    let offset = 0;
    return BUCKETS.map((bucket) => {
      const count = data[bucket].count;
      const pct = total > 0 ? count / total : 0;
      const dash = pct * CIRCUMFERENCE;
      const seg = {
        bucket,
        count,
        pct,
        dashArray: `${dash} ${CIRCUMFERENCE - dash}`,
        dashOffset: -offset,
      };
      offset += dash;
      return seg;
    });
  }, [data, total]);

  return (
    <div className="flex items-center gap-6">
      <div className="relative flex-shrink-0">
        <svg width="140" height="140" viewBox="0 0 140 140" className="-rotate-90">
          <circle
            cx="70" cy="70" r="56"
            fill="none" stroke="#f1f5f9" strokeWidth="16"
          />
          {segments.map((seg) => (
            <circle
              key={seg.bucket}
              cx="70" cy="70" r="56"
              fill="none"
              stroke={BUCKET_COLORS[seg.bucket]}
              strokeWidth="16"
              strokeDasharray={seg.dashArray}
              strokeDashoffset={seg.dashOffset}
              className="transition-all duration-500"
            />
          ))}
        </svg>
        <div className="absolute inset-0 flex flex-col items-center justify-center">
          <span className="text-2xl font-bold text-slate-900">{total}</span>
          <span className="text-xs text-slate-400 font-medium">Records</span>
        </div>
      </div>

      <div className="flex-1 space-y-3">
        {segments.map((seg) => (
          <div key={seg.bucket} className="flex items-center gap-2.5">
            <span
              className="w-3 h-3 rounded-full flex-shrink-0"
              style={{ backgroundColor: BUCKET_COLORS[seg.bucket] }}
            />
            <span className="text-sm text-slate-600 flex-1">{BUCKET_LABELS[seg.bucket]}</span>
            <span className="text-sm font-semibold text-slate-900 tabular-nums">{seg.count}</span>
            <span className="text-xs text-slate-400 tabular-nums w-10 text-right">
              {(seg.pct * 100).toFixed(0)}%
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

const CIRCUMFERENCE = 2 * Math.PI * 56;
