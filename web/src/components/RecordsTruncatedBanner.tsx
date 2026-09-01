import { AlertTriangle } from 'lucide-react';

interface Props {
  loaded: number;
  total: number;
}

/**
 * Shown when getBatchRecords (src/lib/api.ts) hits its pagination safety
 * cap before reaching the batch's last page. Charts and the table below
 * only ever see what was loaded, so this says so explicitly rather than
 * rendering a partial record set as if it were the whole batch, which is
 * the bug this cap exists to avoid repeating (docs/INCIDENTS.md).
 */
export function RecordsTruncatedBanner({ loaded, total }: Props) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3">
      <AlertTriangle className="w-4 h-4 text-amber-600 flex-shrink-0" />
      <p className="text-sm text-amber-800 flex-1">
        Loaded {loaded.toLocaleString('en-IN')} of {total.toLocaleString('en-IN')} records before hitting the
        pagination safety cap. Charts and the table below reflect only these records. The summary metrics above
        still cover the full batch.
      </p>
    </div>
  );
}
