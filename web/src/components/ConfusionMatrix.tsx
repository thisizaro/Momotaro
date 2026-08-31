import { BUCKET_LABELS } from '@/lib/format';
import type { AccuracyBlock, RootCauseBucket } from '@/types';

interface Props {
  confusion: AccuracyBlock['confusion'];
}

export function ConfusionMatrix({ confusion }: Props) {
  const predictedBuckets = Object.keys(confusion) as RootCauseBucket[];
  if (predictedBuckets.length === 0) return null;

  return (
    <div className="space-y-2.5 max-h-[160px] overflow-y-auto scrollbar-thin pr-1">
      {predictedBuckets.map((predicted) => {
        const trueCounts = confusion[predicted]?.true_bucket_counts ?? {};
        const entries = (Object.entries(trueCounts) as [RootCauseBucket, number][]).sort(
          (a, b) => b[1] - a[1],
        );
        const total = entries.reduce((sum, [, count]) => sum + count, 0);

        return (
          <div key={predicted} className="text-xs">
            <div className="flex items-center justify-between">
              <span className="font-medium text-slate-600">Predicted {BUCKET_LABELS[predicted]}</span>
              <span className="text-slate-400 tabular-nums">{total}</span>
            </div>
            <div className="flex flex-wrap gap-1 mt-1">
              {entries.map(([trueBucket, count]) => (
                <span
                  key={trueBucket}
                  className={`px-1.5 py-0.5 rounded text-[11px] ${
                    trueBucket === predicted ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-600'
                  }`}
                >
                  {count}× actually {BUCKET_LABELS[trueBucket]}
                </span>
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}
