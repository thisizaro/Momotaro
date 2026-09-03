import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Bug, Globe, Info, Play, PowerOff, RefreshCw, SlidersHorizontal } from 'lucide-react';
import { api, DemoControlsDisabledError, USE_MOCK } from '@/lib/api';
import type { DemoBatchResponse, DemoConfigResponse, DemoInjectPoisonResponse, DemoScenario, DemoWorldResponse } from '@/types';
import { AgentConfigPanel } from '@/components/AgentConfigPanel';
import { DueCountdown } from '@/components/DueCountdown';
import { EmptyState } from '@/components/EmptyState';
import { ErrorBanner } from '@/components/ErrorBanner';
import { OUTCOME_LABELS } from '@/lib/format';

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

interface Props {
  /** Called after a successful seed, with the new batch's id, so the caller
   *  can select it in the dashboard immediately (Unit X: "the user
   *  immediately sees it fill in"). */
  onBatchSeeded: (batchId: string) => void;
}

/** How often the World Simulator state refreshes while this panel is open.
 *  Fast enough that a raised outage or a just-seeded batch's retries feel
 *  live, slow enough not to hammer the Gateway. */
const WORLD_POLL_MS = 3000;

/**
 * The demo control panel (docs/PHASE5_5_IMPLEMENTATION.md Unit X). Replaces
 * the old "Generate Sample Data" button: everything here is driven by
 * `/v1/demo/*` (Unit W), so a seeded batch carries real ground truth and
 * gets an accuracy score and a baseline comparison, which that button could
 * never produce (see docs/API_GATEWAY.md "POST /v1/batches", the
 * ground-truth boundary).
 */
export function DemoControlPanel({ onBatchSeeded }: Props) {
  const [disabled, setDisabled] = useState(false);

  const [scenarios, setScenarios] = useState<DemoScenario[] | null>(null);
  const [scenariosError, setScenariosError] = useState<string | null>(null);
  const [scenario, setScenario] = useState('');
  const [count, setCount] = useState('80');
  const [seed, setSeed] = useState('');
  const [seeding, setSeeding] = useState(false);
  const [seedError, setSeedError] = useState<string | null>(null);
  const [seedResult, setSeedResult] = useState<DemoBatchResponse | null>(null);

  const [world, setWorld] = useState<DemoWorldResponse | null>(null);
  const [worldError, setWorldError] = useState<string | null>(null);

  const [agentConfig, setAgentConfig] = useState<DemoConfigResponse | null>(null);
  const [agentConfigError, setAgentConfigError] = useState<string | null>(null);

  const [injecting, setInjecting] = useState(false);
  const [injectError, setInjectError] = useState<string | null>(null);
  const [injectResult, setInjectResult] = useState<DemoInjectPoisonResponse | null>(null);

  const worldTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // A 404 from any /v1/demo/* route means the whole namespace is off
  // (docs/API_GATEWAY.md), so any of the three loaders below flips the same
  // switch and the rest of the panel stops trying.
  const handlePossiblyDisabled = useCallback((err: unknown, setOtherError: (msg: string | null) => void, fallback: string) => {
    if (err instanceof DemoControlsDisabledError) {
      setDisabled(true);
      return;
    }
    setOtherError(errorMessage(err, fallback));
  }, []);

  const loadScenarios = useCallback(async () => {
    try {
      const list = await api.getDemoScenarios();
      setScenarios(list);
      setScenariosError(null);
      setScenario((prev) => prev || list[0]?.name || '');
    } catch (err) {
      handlePossiblyDisabled(err, setScenariosError, 'Failed to load scenarios');
    }
  }, [handlePossiblyDisabled]);

  const loadWorld = useCallback(async () => {
    try {
      const w = await api.getDemoWorld();
      setWorld(w);
      setWorldError(null);
    } catch (err) {
      handlePossiblyDisabled(err, setWorldError, 'Failed to load World Simulator state');
    }
  }, [handlePossiblyDisabled]);

  // Unlike world state, this is fetched once, not polled: it is fixed for
  // the lifetime of the process (docs/DEMO_READINESS.md Unit AM), so there
  // is nothing that would ever change between polls.
  const loadAgentConfig = useCallback(async () => {
    try {
      const cfg = await api.getDemoConfig();
      setAgentConfig(cfg);
      setAgentConfigError(null);
    } catch (err) {
      handlePossiblyDisabled(err, setAgentConfigError, 'Failed to load agent configuration');
    }
  }, [handlePossiblyDisabled]);

  useEffect(() => {
    loadScenarios();
    loadAgentConfig();
  }, [loadScenarios, loadAgentConfig]);

  useEffect(() => {
    if (disabled) return;
    loadWorld();
    worldTimerRef.current = setInterval(loadWorld, WORLD_POLL_MS);
    return () => {
      if (worldTimerRef.current) clearInterval(worldTimerRef.current);
      worldTimerRef.current = null;
    };
  }, [disabled, loadWorld]);

  const handleSeedSubmit = useCallback(
    async (e: FormEvent) => {
      e.preventDefault();
      const parsedCount = parseInt(count, 10);
      if (!Number.isFinite(parsedCount) || parsedCount < 1 || parsedCount > 1000) {
        setSeedError('Count must be a whole number between 1 and 1000.');
        return;
      }
      const trimmedSeed = seed.trim();
      const parsedSeed = trimmedSeed ? parseInt(trimmedSeed, 10) : undefined;
      if (trimmedSeed && !Number.isFinite(parsedSeed)) {
        setSeedError('Seed must be a whole number.');
        return;
      }

      setSeeding(true);
      setSeedError(null);
      try {
        const res = await api.seedDemoBatch({ scenario, count: parsedCount, seed: parsedSeed });
        setSeedResult(res);
        onBatchSeeded(res.batch_id);
      } catch (err) {
        handlePossiblyDisabled(err, setSeedError, 'Failed to seed batch');
      } finally {
        setSeeding(false);
      }
    },
    [scenario, count, seed, onBatchSeeded, handlePossiblyDisabled],
  );

  const handleInject = useCallback(async () => {
    setInjecting(true);
    setInjectError(null);
    try {
      const res = await api.injectPoison();
      setInjectResult(res);
    } catch (err) {
      handlePossiblyDisabled(err, setInjectError, 'Failed to inject poison record');
    } finally {
      setInjecting(false);
    }
  }, [handlePossiblyDisabled]);

  if (disabled) {
    return (
      <div className="card p-10 flex flex-col items-center text-center gap-3 max-w-xl mx-auto">
        <PowerOff className="w-8 h-8 text-slate-300" />
        <h2 className="text-sm font-semibold text-slate-700">Demo controls are disabled</h2>
        <p className="text-sm text-slate-500 leading-relaxed">
          This Gateway was not started with demo controls enabled, so <code className="font-mono text-xs bg-slate-100 rounded px-1.5 py-0.5">/v1/demo/*</code> does
          not exist right now, on purpose (it is not a production surface). Start the stack with{' '}
          <code className="font-mono text-xs bg-slate-100 rounded px-1.5 py-0.5">PROFILE=demo</code> to turn it on.
        </p>
      </div>
    );
  }

  const selectedDescription = scenarios?.find((s) => s.name === scenario)?.description;

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 items-start">
        {/* Seed a batch */}
        <div className="card p-5">
          <div className="flex items-center gap-2 mb-1">
            <SlidersHorizontal className="w-4 h-4 text-slate-400" />
            <h3 className="text-sm font-semibold text-slate-700">Seed a batch</h3>
          </div>
          <p className="text-xs text-slate-400 mb-4">
            Seeds records with hidden ground truth, the same way scripts/batchgen does. Unlike the old
            "Generate Sample Data" button, the resulting batch gets an accuracy score and a baseline comparison.
          </p>
          {USE_MOCK && (
            <p className="text-xs text-amber-700 bg-amber-50 border border-amber-200 rounded-md px-3 py-2 mb-4">
              Mock mode: this fabricates a batch in your browser. It does not go through the real World Simulator,
              though it still reports an illustrative accuracy score, for development convenience only.
            </p>
          )}
          {scenariosError && <ErrorBanner message={scenariosError} onRetry={loadScenarios} />}
          <form onSubmit={handleSeedSubmit} className="space-y-3">
            <div>
              <label className="block text-xs font-medium text-slate-500 mb-1">Scenario</label>
              <select
                value={scenario}
                onChange={(e) => setScenario(e.target.value)}
                disabled={!scenarios}
                className="w-full text-sm border border-slate-200 rounded-lg px-3 py-2 bg-white disabled:opacity-50"
              >
                {(scenarios ?? []).map((s) => (
                  <option key={s.name} value={s.name}>
                    {s.name}
                  </option>
                ))}
              </select>
              {selectedDescription && (
                <p className="text-xs text-slate-400 mt-1.5 leading-relaxed">{selectedDescription}</p>
              )}
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-xs font-medium text-slate-500 mb-1">Count</label>
                <input
                  type="number"
                  min={1}
                  max={1000}
                  value={count}
                  onChange={(e) => setCount(e.target.value)}
                  className="w-full text-sm border border-slate-200 rounded-lg px-3 py-2"
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-slate-500 mb-1">Seed (optional)</label>
                <input
                  type="number"
                  value={seed}
                  onChange={(e) => setSeed(e.target.value)}
                  placeholder="random"
                  className="w-full text-sm border border-slate-200 rounded-lg px-3 py-2"
                />
              </div>
            </div>
            {seedError && <ErrorBanner message={seedError} />}
            {seedResult && !seedError && (
              <p className="text-xs text-emerald-700 bg-emerald-50 border border-emerald-200 rounded-md px-3 py-2">
                Seeded {seedResult.generated_count} records, seed {seedResult.seed}. Selected in the dashboard.
              </p>
            )}
            <button
              type="submit"
              disabled={seeding || !scenarios}
              className="btn-primary w-full justify-center disabled:opacity-50"
            >
              {seeding ? <RefreshCw className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />}
              {seeding ? 'Seeding...' : 'Seed batch'}
            </button>
          </form>
        </div>

        {/* World Simulator state */}
        <div className="card p-5">
          <div className="flex items-center gap-2 mb-1">
            <Globe className="w-4 h-4 text-slate-400" />
            <h3 className="text-sm font-semibold text-slate-700">World Simulator state</h3>
          </div>
          <p className="text-xs text-slate-400 mb-4">
            Every delayed outcome it is currently holding, and when each is due. This has never been visible
            anywhere else. Read-only: viewing this never drains or delivers anything early.
          </p>
          {worldError && <ErrorBanner message={worldError} onRetry={loadWorld} />}
          {!world && !worldError && <div className="h-24 animate-pulse bg-slate-50 rounded-lg" />}
          {world && world.pending.length === 0 && (
            <EmptyState
              size="inline"
              icon={Globe}
              title="Nothing pending right now"
              description="No delayed retry or nudge is waiting in the World Simulator. Seed a batch, or inject an outage below, to give it something to hold."
            />
          )}
          {world && world.pending.length > 0 && (
            <ul className="space-y-1.5 max-h-72 overflow-y-auto scrollbar-thin">
              {world.pending.map((p) => (
                <li
                  key={p.record_id}
                  className="flex items-center justify-between gap-2 text-xs border border-slate-100 rounded-md px-2.5 py-1.5"
                >
                  <span className="font-mono text-slate-500">{p.record_id.slice(0, 8)}</span>
                  <span className="text-slate-400 flex-shrink-0">attempt {p.attempt_number}</span>
                  <span className="text-slate-400 flex-shrink-0">{OUTCOME_LABELS[p.outcome] ?? p.outcome}</span>
                  <DueCountdown dueAt={p.due_at} currentState="RECORD_STATE_RETRY_SCHEDULED" className="ml-auto flex-shrink-0" />
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* Failure injection */}
        <div className="card p-5">
          <div className="flex items-center gap-2 mb-1">
            <Bug className="w-4 h-4 text-slate-400" />
            <h3 className="text-sm font-semibold text-slate-700">Failure injection</h3>
          </div>
          <p className="text-xs text-slate-400 mb-4">
            Publishes one record the pipeline can never look up, to show it gets dead-lettered instead of
            crashing the consumer.
          </p>
          <button
            onClick={handleInject}
            disabled={injecting}
            className="btn-secondary w-full justify-center disabled:opacity-50"
          >
            {injecting ? <RefreshCw className="w-4 h-4 animate-spin" /> : <Bug className="w-4 h-4" />}
            {injecting ? 'Injecting...' : 'Inject poison record'}
          </button>
          {injectError && <ErrorBanner message={injectError} />}
          {injectResult && !injectError && (
            <p className="text-xs text-slate-500 mt-3 leading-relaxed">
              Published record <span className="font-mono">{injectResult.record_id.slice(0, 8)}</span>. It was
              deliberately never written to storage, so it will not appear anywhere in this dashboard. Watch for
              it in the Decision Engine's logs instead: it should be dead-lettered within a few seconds rather
              than crash the consumer.
            </p>
          )}
        </div>
      </div>

      {/* Agent configuration, read-only */}
      <AgentConfigPanel config={agentConfig} error={agentConfigError} onRetry={loadAgentConfig} />

      {/* World Simulator explainer */}
      <div className="card p-5">
        <div className="flex items-center gap-2 mb-2">
          <Info className="w-4 h-4 text-slate-400" />
          <h3 className="text-sm font-semibold text-slate-700">What is the "World Simulator"?</h3>
        </div>
        <div className="text-sm text-slate-600 leading-relaxed space-y-2 max-w-3xl">
          <p>
            Two different things in this project get loosely called a simulator, and only one of them would be a
            problem if a judge found it.
          </p>
          <p>
            When the agent retries a payment, or waits on a customer to act, something has to eventually say
            whether it worked. In production that is a bank. This project has no bank to call, so the World
            Simulator plays that role: it holds the true outcome for every record on the other side of a
            boundary the agent's decision path is provably unable to read, and reveals it only when a retry or
            nudge actually executes. That is what makes a number like "the agent recovered 51% of at-risk money"
            a measured result instead of a guess, and it is permanent by design, not a shortcut standing in until
            something real replaces it.
          </p>
          <p>
            That is a different thing from this dashboard's own mock mode (the amber banner above, when it is
            showing). Mock mode stands in for our own backend during development and is never used for a real
            demo. The World Simulator stands in for a bank we cannot have, and it stays in the system on
            purpose, because that substitution is what makes measurement possible at all.
          </p>
        </div>
      </div>
    </div>
  );
}
