import { Info } from 'lucide-react';

interface Props {
  count: number;
}

/**
 * Surfaces BatchReport.llm_quota_exhausted_count (docs/API_GATEWAY.md,
 * docs/DEMO_READINESS.md Unit AI): records whose classification wanted a
 * live model call and did not get one, either because Groq's free tier or
 * the classifier's own breaker said no, or because LLM_SAMPLE_RATE's
 * ceiling was already spent for this batch. Every one of those records
 * still resolved to a real answer from the deterministic rules table, so
 * this reports a normal operating condition on a free tier, not a fault:
 * a quiet slate note when there is something to say, nothing at all when
 * there is not. No red, no amber, no icon that reads as a warning.
 */
export function LlmQuotaBanner({ count }: Props) {
  if (count <= 0) return null;

  const noun = count === 1 ? 'record' : 'records';

  return (
    <div className="flex items-center gap-3 rounded-lg border border-slate-200 bg-slate-50 px-4 py-3">
      <Info className="w-4 h-4 text-slate-400 flex-shrink-0" />
      <p className="text-sm text-slate-600 flex-1">
        LLM quota exhausted, {count.toLocaleString('en-IN')} {noun} fell back to deterministic rules.
      </p>
    </div>
  );
}
