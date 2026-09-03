import type { ReactNode } from 'react';
import { Lock } from 'lucide-react';
import type { DemoConfigResponse } from '@/types';
import { formatDuration, formatPercent } from '@/lib/format';
import { ErrorBanner } from '@/components/ErrorBanner';

interface Props {
  config: DemoConfigResponse | null;
  error: string | null;
  onRetry: () => void;
}

/**
 * The number of environment variables `.env.example` lists today, verified
 * by hand with `grep -cE '^[A-Z_][A-Z0-9_]*=' .env.example` rather than
 * trusted from an earlier count written elsewhere (docs/DEMO_READINESS.md
 * Unit AM). None of them are adjustable once the process is running: this
 * panel shows the subset worth knowing, not a full dump, and says so once,
 * plainly, rather than implying completeness.
 */
const ENV_VAR_COUNT = 60;

interface ConfigRowProps {
  label: string;
  value: string;
}

function ConfigRow({ label, value }: ConfigRowProps) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1.5">
      <span className="text-sm text-slate-500">{label}</span>
      <span className="font-mono text-sm text-slate-700 text-right">{value}</span>
    </div>
  );
}

interface ConfigGroupProps {
  title: string;
  children: ReactNode;
}

function ConfigGroup({ title, children }: ConfigGroupProps) {
  return (
    <div>
      <h4 className="text-xs font-semibold text-slate-500 mb-1">{title}</h4>
      <div className="divide-y divide-slate-100">{children}</div>
    </div>
  );
}

/**
 * What the agent is bounded by (docs/DEMO_READINESS.md Unit AM), on the
 * Demo Controls page: guardrails and LLM routing, exactly as the Decision
 * Engine loaded and validated them at startup, proxied through
 * GET /v1/demo/config rather than read anywhere else. Read-only by
 * construction, not merely by convention: nothing on this card can be
 * edited, and it says so, once, rather than leaving a viewer to assume the
 * numbers below are live controls.
 */
export function AgentConfigPanel({ config, error, onRetry }: Props) {
  return (
    <div className="card p-5">
      <div className="flex items-center gap-2 mb-1">
        <Lock className="w-4 h-4 text-slate-400" />
        <h3 className="text-sm font-semibold text-slate-700">Agent configuration</h3>
        <span className="badge bg-slate-50 text-slate-500 border-slate-200 ml-1">Fixed at startup</span>
      </div>
      <p className="text-xs text-slate-400 mb-4">
        What the agent is bounded by right now. These values were loaded once, when the Decision Engine
        started, and cannot be changed from this dashboard.
      </p>

      {error && <ErrorBanner message={error} onRetry={onRetry} />}

      {!config && !error && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
          <div className="h-24 animate-pulse bg-slate-50 rounded-lg" />
          <div className="h-24 animate-pulse bg-slate-50 rounded-lg" />
          <div className="h-24 animate-pulse bg-slate-50 rounded-lg" />
        </div>
      )}

      {config && !error && (
        <>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-x-6 gap-y-4">
            <ConfigGroup title="Time compression">
              <ConfigRow label="Simulated speed" value={`${(config.demo_time_scale ?? 0).toLocaleString('en-US')}x real time`} />
              <ConfigRow label="Contact cooldown" value={formatDuration(config.contact_cooldown_ms ?? 0)} />
              <ConfigRow label="Recovery window" value={formatDuration((config.recovery_window_seconds ?? 0) * 1000)} />
            </ConfigGroup>

            <ConfigGroup title="Retry and contact limits">
              <ConfigRow label="Max retries" value={`${config.max_retries ?? 0} attempts`} />
              <ConfigRow label="Max contacts" value={`${config.max_contacts ?? 0} contacts`} />
              <ConfigRow
                label="Bank downtime hold"
                value={formatDuration((config.downtime_max_unresolved_hold_seconds ?? 0) * 1000)}
              />
            </ConfigGroup>

            <ConfigGroup title="LLM routing">
              <ConfigRow label="Sample rate ceiling" value={formatPercent(config.llm_sample_rate ?? 0, 0)} />
              <ConfigRow label="Route confidence floor" value={formatPercent(config.route_confidence_threshold ?? 0, 0)} />
              <ConfigRow label="Classify confidence floor" value={formatPercent(config.classify_confidence_threshold ?? 0, 0)} />
              <ConfigRow label="Nudge length cap" value={`${config.nudge_max_chars ?? 0} chars`} />
            </ConfigGroup>
          </div>

          <p className="text-xs text-slate-400 mt-4 pt-4 border-t border-slate-100 leading-relaxed">
            This deployment has {ENV_VAR_COUNT} environment variables in total, and none of them are
            adjustable at runtime. What is shown above is the behavioral subset worth knowing, timings,
            limits and routing thresholds, not a full list of every one of them.
          </p>
        </>
      )}
    </div>
  );
}
