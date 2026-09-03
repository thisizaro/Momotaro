import { useCallback, useEffect, useRef, useState } from 'react';
import { Shield, FlaskConical, LayoutDashboard, SlidersHorizontal } from 'lucide-react';
import { api, USE_MOCK } from '@/lib/api';
import type { LiveConnectionStatus } from '@/lib/api';
import type {
  BatchReport,
  BatchSummary,
  BatchUpdate,
  InvariantsResponse,
  RecordAuditResponse,
  RecordSummary,
} from '@/types';
import { EmptyState } from '@/components/EmptyState';
import { MetricsGrid } from '@/components/MetricsGrid';
import { DonutChart } from '@/components/DonutChart';
import { RecoveryBar } from '@/components/RecoveryBar';
import { StateDistribution } from '@/components/StateDistribution';
import { TimelineView } from '@/components/TimelineView';
import { LiveFeed } from '@/components/LiveFeed';
import { RecordsTable } from '@/components/RecordsTable';
import { RecordDrawer } from '@/components/RecordDrawer';
import { BatchSelector } from '@/components/BatchSelector';
import { ErrorBanner } from '@/components/ErrorBanner';
import { RecordsTruncatedBanner } from '@/components/RecordsTruncatedBanner';
import { LlmQuotaBanner } from '@/components/LlmQuotaBanner';
import { BaselineComparisonCard } from '@/components/BaselineComparison';
import { InvariantsPanel } from '@/components/InvariantsPanel';
import { ConfusionMatrix } from '@/components/ConfusionMatrix';
import { DemoControlPanel } from '@/components/DemoControlPanel';

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

// 'connecting' is the state before the socket has had a chance to open at
// all; api.subscribeToBatch never reports it, App sets it itself right
// before subscribing. Everything after that comes straight from
// LiveConnectionStatus (lib/api.ts).
type ConnectionState = 'connecting' | LiveConnectionStatus;
type View = 'dashboard' | 'demo';

function App() {
  const [view, setView] = useState<View>('dashboard');
  const [batches, setBatches] = useState<BatchSummary[]>([]);
  const [batchesError, setBatchesError] = useState<string | null>(null);
  const [activeBatchId, setActiveBatchId] = useState<string | null>(null);
  const [report, setReport] = useState<BatchReport | null>(null);
  const [records, setRecords] = useState<RecordSummary[]>([]);
  const [recordsTotalCount, setRecordsTotalCount] = useState(0);
  const [recordsTruncated, setRecordsTruncated] = useState(false);
  const [invariants, setInvariants] = useState<InvariantsResponse | null>(null);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [updates, setUpdates] = useState<BatchUpdate[]>([]);
  const [connectionState, setConnectionState] = useState<ConnectionState>('connecting');
  const [drawerRecordId, setDrawerRecordId] = useState<string | null>(null);
  const [drawerDetail, setDrawerDetail] = useState<RecordAuditResponse | null>(null);
  const [drawerLoading, setDrawerLoading] = useState(false);
  const [drawerError, setDrawerError] = useState<string | null>(null);

  const unsubscribeRef = useRef<(() => void) | null>(null);
  const refreshTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadBatches = useCallback(async () => {
    try {
      const list = await api.getBatches();
      setBatches(list);
      setBatchesError(null);
      setActiveBatchId((prev) => prev ?? list[0]?.batch_id ?? null);
    } catch (err) {
      setBatchesError(errorMessage(err, 'Failed to load batches'));
    }
  }, []);

  // Load batch list on mount
  useEffect(() => {
    loadBatches();
  }, [loadBatches]);

  const loadBatchData = useCallback(async (batchId: string) => {
    try {
      const [rpt, recsResult, inv] = await Promise.all([
        api.getBatchReport(batchId),
        api.getBatchRecords(batchId),
        api.getBatchInvariants(batchId),
      ]);
      setReport(rpt);
      setRecords(recsResult.records);
      setRecordsTotalCount(recsResult.totalCount);
      setRecordsTruncated(recsResult.truncated);
      setInvariants(inv);
      setRefreshError(null);
    } catch (err) {
      // Keep whatever report/records are already on screen; a transient
      // refresh failure shouldn't blank out a dashboard that was working.
      setRefreshError(errorMessage(err, 'Failed to refresh batch data'));
    }
  }, []);

  // Subscribe to batch updates + poll for report/records
  useEffect(() => {
    if (!activeBatchId) return;

    setUpdates([]);
    setRefreshError(null);
    // A fresh subscribe hasn't had a chance to open yet: it's neither
    // confirmed live nor actually disconnected, so don't flash red.
    setConnectionState('connecting');

    loadBatchData(activeBatchId);

    unsubscribeRef.current = api.subscribeToBatch(
      activeBatchId,
      (update: BatchUpdate) => setUpdates((prev) => [...prev, update]),
      (status) => setConnectionState(status),
    );

    // Poll for updated report/records every 2 seconds
    refreshTimerRef.current = setInterval(() => loadBatchData(activeBatchId), 2000);

    return () => {
      unsubscribeRef.current?.();
      unsubscribeRef.current = null;
      if (refreshTimerRef.current) clearInterval(refreshTimerRef.current);
      refreshTimerRef.current = null;
      setConnectionState('connecting');
    };
  }, [activeBatchId, loadBatchData]);

  // Called by DemoControlPanel after it seeds a batch through /v1/demo/batches
  // (Unit W), so the newly seeded batch, which carries real ground truth and
  // therefore an accuracy score and a baseline comparison, is selected and
  // visible immediately rather than left for the user to find in the
  // selector themselves.
  const handleDemoBatchSeeded = useCallback(async (batchId: string) => {
    try {
      const list = await api.getBatches();
      setBatches(list);
      setBatchesError(null);
    } catch (err) {
      setBatchesError(errorMessage(err, 'Failed to load batches'));
    }
    setActiveBatchId(batchId);
    setView('dashboard');
  }, []);

  const handleSelectRecord = useCallback(async (id: string) => {
    setDrawerRecordId(id);
    setDrawerLoading(true);
    setDrawerDetail(null);
    setDrawerError(null);
    try {
      const detail = await api.getRecordDetail(id);
      setDrawerDetail(detail);
    } catch (err) {
      setDrawerError(errorMessage(err, 'Failed to load record detail'));
    } finally {
      setDrawerLoading(false);
    }
  }, []);

  const connectionLabel = !activeBatchId
    ? 'Idle'
    : connectionState === 'live'
    ? 'Streaming'
    : connectionState === 'reconnecting'
    ? 'Reconnecting...'
    : connectionState === 'complete'
    ? 'Complete'
    : connectionState === 'disconnected'
    ? 'Disconnected'
    : 'Connecting...';
  const connectionDotClass = !activeBatchId
    ? 'bg-slate-300'
    : connectionState === 'live'
    ? 'bg-emerald-500 pulse-dot'
    : connectionState === 'reconnecting'
    ? 'bg-amber-400 pulse-dot'
    : connectionState === 'complete'
    ? 'bg-emerald-500'
    : connectionState === 'disconnected'
    ? 'bg-rose-400'
    : 'bg-amber-400 pulse-dot';

  return (
    <div className="min-h-screen bg-slate-50">
      {/* Header (+ mock-mode banner, kept in the same sticky container so they stick together) */}
      <div className="sticky top-0 z-30">
        <header className="bg-white border-b border-slate-200">
          <div className="max-w-[1400px] mx-auto px-6 py-3.5 flex items-center justify-between">
            <div className="flex items-center gap-3">
              <div className="w-9 h-9 rounded-lg bg-slate-900 flex items-center justify-center">
                <Shield className="w-5 h-5 text-white" />
              </div>
              <div>
                <h1 className="text-base font-bold text-slate-900 tracking-tight">Momotaro</h1>
                <p className="text-xs text-slate-400 -mt-0.5">Payment Recovery Agent</p>
              </div>
            </div>

            <div className="flex items-center gap-3">
              <span className="flex items-center gap-1.5 text-xs text-slate-400">
                <span className={`w-1.5 h-1.5 rounded-full ${connectionDotClass}`} />
                {connectionLabel}
              </span>
              <div className="flex items-center gap-1 bg-slate-100 rounded-lg p-1">
                <button
                  onClick={() => setView('dashboard')}
                  className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium transition-all duration-150 ${
                    view === 'dashboard' ? 'bg-white text-slate-900 shadow-sm' : 'text-slate-500 hover:text-slate-700'
                  }`}
                >
                  <LayoutDashboard className="w-3.5 h-3.5" />
                  Dashboard
                </button>
                <button
                  onClick={() => setView('demo')}
                  className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium transition-all duration-150 ${
                    view === 'demo' ? 'bg-white text-slate-900 shadow-sm' : 'text-slate-500 hover:text-slate-700'
                  }`}
                >
                  <SlidersHorizontal className="w-3.5 h-3.5" />
                  Demo Controls
                </button>
              </div>
            </div>
          </div>
        </header>

        {USE_MOCK && (
          <div className="bg-amber-400 text-amber-950">
            <div className="max-w-[1400px] mx-auto px-6 py-1.5 flex items-center justify-center gap-1.5 text-xs font-semibold">
              <FlaskConical className="w-3.5 h-3.5" />
              Mock mode, sample data, not live
            </div>
          </div>
        )}
      </div>

      <main className="max-w-[1400px] mx-auto px-6 py-6 space-y-6">
        {view === 'demo' && <DemoControlPanel onBatchSeeded={handleDemoBatchSeeded} />}

        {view === 'dashboard' && (
        <>
        {batchesError && (
          <ErrorBanner message={`Couldn't load batches: ${batchesError}`} onRetry={loadBatches} />
        )}

        {/* Batch selector */}
        <BatchSelector
          batches={batches}
          activeBatchId={activeBatchId}
          onSelect={setActiveBatchId}
        />

        {refreshError && (
          <ErrorBanner
            message={`Couldn't refresh batch data: ${refreshError}`}
            onRetry={() => activeBatchId && loadBatchData(activeBatchId)}
          />
        )}

        {recordsTruncated && <RecordsTruncatedBanner loaded={records.length} total={recordsTotalCount} />}
        {report && <LlmQuotaBanner count={report.llm_quota_exhausted_count} />}

        {!activeBatchId ? (
          <EmptyState
            size="hero"
            icon={SlidersHorizontal}
            title="No batch selected yet"
            description="Seed a batch from Demo Controls to watch failures get classified, retried and recovered in real time."
            action={{ label: 'Go to Demo Controls', onClick: () => setView('demo'), icon: SlidersHorizontal }}
          />
        ) : (
        <>
        {/* Metrics */}
        {report ? (
          <MetricsGrid report={report} />
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            {[0, 1, 2, 3, 4, 5, 6].map((i) => (
              <div key={i} className="card p-5 h-[110px] animate-pulse">
                <div className="h-3 w-20 bg-slate-100 rounded mb-3" />
                <div className="h-7 w-24 bg-slate-100 rounded" />
              </div>
            ))}
          </div>
        )}

        {/* Charts row */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="card p-5">
            <h3 className="text-sm font-semibold text-slate-700 mb-4">Root Cause Distribution</h3>
            {report ? (
              <DonutChart data={report.by_root_cause} />
            ) : (
              <div className="h-[140px] animate-pulse bg-slate-50 rounded-lg" />
            )}
          </div>

          <div className="card p-5">
            <h3 className="text-sm font-semibold text-slate-700 mb-4">Recovery Progress</h3>
            {report ? (
              <RecoveryBar report={report} />
            ) : (
              <div className="h-[140px] animate-pulse bg-slate-50 rounded-lg" />
            )}
          </div>

          <div className="card p-5">
            <h3 className="text-sm font-semibold text-slate-700 mb-4">State Distribution</h3>
            {records.length > 0 ? (
              <StateDistribution records={records} />
            ) : (
              <div className="h-[140px] animate-pulse bg-slate-50 rounded-lg" />
            )}
          </div>
        </div>

        {/* Timeline: Live (scheduled) / History (what happened) toggle */}
        <div className="card p-5">
          {report ? (
            <TimelineView key={activeBatchId} records={records} onSelect={handleSelectRecord} />
          ) : (
            <div className="h-[280px] animate-pulse bg-slate-50 rounded-lg" />
          )}
        </div>

        {/* Baseline comparison + invariants */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <div className="card p-5">
            <h3 className="text-sm font-semibold text-slate-700 mb-4">Baseline Comparison</h3>
            {report ? (
              <BaselineComparisonCard
                baseline={report.baseline_comparison}
                ownNetRecoveredPaise={report.net_recovered_paise}
              />
            ) : (
              <div className="h-[140px] animate-pulse bg-slate-50 rounded-lg" />
            )}
          </div>

          <div className="card p-5">
            <h3 className="text-sm font-semibold text-slate-700 mb-4">System Invariants</h3>
            <InvariantsPanel invariants={invariants} />
          </div>
        </div>

        {/* Live feed + classification accuracy */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2">
            <LiveFeed updates={updates} connectionState={connectionState} />
          </div>
          <div className="card p-5">
            <h3 className="text-sm font-semibold text-slate-700 mb-4">Classification Accuracy</h3>
            {report ? (
              <div className="space-y-4">
                <div>
                  {report.accuracy ? (
                    <div className="flex items-baseline gap-2">
                      <span className="text-3xl font-bold text-slate-900">
                        {(report.accuracy.overall_accuracy * 100).toFixed(1)}%
                      </span>
                      <span className="text-sm text-slate-400">vs ground truth</span>
                    </div>
                  ) : (
                    <span className="text-sm text-slate-400">No ground truth for this batch</span>
                  )}
                  <p className="text-xs text-slate-400 mt-1">
                    Measures how often the LLM classifier agrees with the true root cause.
                  </p>
                </div>
                {report.accuracy && (
                  <div className="pt-2 border-t border-slate-100">
                    <p className="text-xs font-semibold text-slate-400 uppercase tracking-wide mb-2">
                      Confusion Matrix
                    </p>
                    <ConfusionMatrix confusion={report.accuracy.confusion} />
                  </div>
                )}
                <div className="space-y-2 pt-2 border-t border-slate-100">
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-slate-500">Retry attempts</span>
                    <span className="text-sm font-semibold text-slate-700 tabular-nums">
                      {report.by_intervention.ACTION_TYPE_RETRY?.attempt_count ?? 0}
                    </span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-slate-500">Nudge attempts</span>
                    <span className="text-sm font-semibold text-slate-700 tabular-nums">
                      {(report.by_intervention.ACTION_TYPE_NUDGE_METHOD_UPDATE?.attempt_count ?? 0) +
                        (report.by_intervention.ACTION_TYPE_NUDGE_REMINDER?.attempt_count ?? 0)}
                    </span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-slate-500">Escalations</span>
                    <span className="text-sm font-semibold text-slate-700 tabular-nums">
                      {report.by_intervention.ACTION_TYPE_ESCALATE?.attempt_count ?? 0}
                    </span>
                  </div>
                </div>
              </div>
            ) : (
              <div className="h-[120px] animate-pulse bg-slate-50 rounded-lg" />
            )}
          </div>
        </div>

        {/* Records table */}
        <RecordsTable records={records} onSelect={handleSelectRecord} />
        </>
        )}
        </>
        )}
      </main>

      {/* Record detail drawer */}
      <RecordDrawer
        open={drawerRecordId !== null}
        detail={drawerDetail}
        loading={drawerLoading}
        error={drawerError}
        // due_at lives on RecordSummary (the records table's own data),
        // not on the audit response the drawer otherwise renders, so it
        // is looked up from what's already on screen rather than fetched
        // again.
        dueAt={records.find((r) => r.record_id === drawerRecordId)?.due_at}
        onRetry={() => drawerRecordId && handleSelectRecord(drawerRecordId)}
        onClose={() => {
          setDrawerRecordId(null);
          setDrawerDetail(null);
          setDrawerError(null);
        }}
      />
    </div>
  );
}

export default App;
