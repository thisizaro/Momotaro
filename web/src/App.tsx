import { useCallback, useEffect, useRef, useState } from 'react';
import { Shield, Play, RefreshCw, FlaskConical } from 'lucide-react';
import { api, USE_MOCK } from '@/lib/api';
import type {
  BatchReport,
  BatchSummary,
  BatchUpdate,
  InvariantsResponse,
  RecordAuditResponse,
  RecordSummary,
} from '@/types';
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
import { BaselineComparisonCard } from '@/components/BaselineComparison';
import { InvariantsPanel } from '@/components/InvariantsPanel';
import { ConfusionMatrix } from '@/components/ConfusionMatrix';

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

type ConnectionState = 'connecting' | 'live' | 'disconnected';

function App() {
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
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

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
      (connected) => setConnectionState(connected ? 'live' : 'disconnected'),
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

  const handleSubmitBatch = useCallback(async () => {
    setSubmitting(true);
    setSubmitError(null);
    try {
      const { batch_id } = await api.submitBatch('dashboard-generated', 80);
      const list = await api.getBatches();
      setBatches(list);
      setBatchesError(null);
      setActiveBatchId(batch_id);
    } catch (err) {
      setSubmitError(errorMessage(err, 'Failed to submit batch'));
    } finally {
      setSubmitting(false);
    }
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

  const isLive = connectionState === 'live';
  const connectionLabel = !activeBatchId
    ? 'Idle'
    : connectionState === 'live'
    ? 'Streaming'
    : connectionState === 'disconnected'
    ? 'Disconnected'
    : 'Connecting...';
  const connectionDotClass = !activeBatchId
    ? 'bg-slate-300'
    : connectionState === 'live'
    ? 'bg-emerald-500 pulse-dot'
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
              <button
                onClick={handleSubmitBatch}
                disabled={submitting}
                className="btn-primary disabled:opacity-50"
              >
                {submitting ? (
                  <RefreshCw className="w-4 h-4 animate-spin" />
                ) : (
                  <Play className="w-4 h-4" />
                )}
                {submitting ? 'Generating...' : 'Generate Sample Data'}
              </button>
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
        {submitError && (
          <ErrorBanner
            message={`Couldn't generate sample data: ${submitError}`}
            onRetry={handleSubmitBatch}
            retrying={submitting}
          />
        )}

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

        {/* Retry timeline */}
        <div className="card p-5">
          <h3 className="text-sm font-semibold text-slate-700">Retry Timeline</h3>
          {report ? (
            <TimelineView records={records} />
          ) : (
            <div className="h-[280px] animate-pulse bg-slate-50 rounded-lg mt-4" />
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
            <LiveFeed updates={updates} live={isLive} />
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
