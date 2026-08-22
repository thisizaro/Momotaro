import { useCallback, useEffect, useRef, useState } from 'react';
import { Shield, Play, RefreshCw } from 'lucide-react';
import { api } from '@/lib/api';
import type {
  BatchReport,
  BatchSummary,
  BatchUpdate,
  RecordDetail,
  RecordSummary,
} from '@/types';
import { MetricsGrid } from '@/components/MetricsGrid';
import { DonutChart } from '@/components/DonutChart';
import { RecoveryBar } from '@/components/RecoveryBar';
import { StateDistribution } from '@/components/StateDistribution';
import { LiveFeed } from '@/components/LiveFeed';
import { RecordsTable } from '@/components/RecordsTable';
import { RecordDrawer } from '@/components/RecordDrawer';
import { BatchSelector } from '@/components/BatchSelector';

function App() {
  const [batches, setBatches] = useState<BatchSummary[]>([]);
  const [activeBatchId, setActiveBatchId] = useState<string | null>(null);
  const [report, setReport] = useState<BatchReport | null>(null);
  const [records, setRecords] = useState<RecordSummary[]>([]);
  const [updates, setUpdates] = useState<BatchUpdate[]>([]);
  const [live, setLive] = useState(false);
  const [drawerRecordId, setDrawerRecordId] = useState<string | null>(null);
  const [drawerDetail, setDrawerDetail] = useState<RecordDetail | null>(null);
  const [drawerLoading, setDrawerLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const unsubscribeRef = useRef<(() => void) | null>(null);
  const refreshTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Load batch list on mount
  useEffect(() => {
    api.getBatches().then((list) => {
      setBatches(list);
      if (list.length > 0 && !activeBatchId) {
        setActiveBatchId(list[0].batch_id);
      }
    });
  }, []);

  // Subscribe to batch updates + poll for report/records
  useEffect(() => {
    if (!activeBatchId) return;

    setUpdates([]);
    setLive(true);

    // Initial fetch
    const loadBatchData = async () => {
      const [rpt, recs] = await Promise.all([
        api.getBatchReport(activeBatchId),
        api.getBatchRecords(activeBatchId),
      ]);
      setReport(rpt);
      setRecords(recs);
    };
    loadBatchData();

    // Subscribe to live updates
    unsubscribeRef.current = api.subscribeToBatch(activeBatchId, (update: BatchUpdate) => {
      setUpdates((prev) => [...prev, update]);
    });

    // Poll for updated report/records every 2 seconds
    refreshTimerRef.current = setInterval(loadBatchData, 2000);

    return () => {
      unsubscribeRef.current?.();
      unsubscribeRef.current = null;
      if (refreshTimerRef.current) clearInterval(refreshTimerRef.current);
      refreshTimerRef.current = null;
      setLive(false);
    };
  }, [activeBatchId]);

  const handleSubmitBatch = useCallback(async () => {
    setSubmitting(true);
    try {
      const { batch_id } = await api.submitBatch(80);
      const list = await api.getBatches();
      setBatches(list);
      setActiveBatchId(batch_id);
    } finally {
      setSubmitting(false);
    }
  }, []);

  const handleSelectRecord = useCallback(async (id: string) => {
    setDrawerRecordId(id);
    setDrawerLoading(true);
    setDrawerDetail(null);
    try {
      const detail = await api.getRecordDetail(id);
      setDrawerDetail(detail);
    } finally {
      setDrawerLoading(false);
    }
  }, []);

  return (
    <div className="min-h-screen bg-slate-50">
      {/* Header */}
      <header className="bg-white border-b border-slate-200 sticky top-0 z-30">
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
              <span className={`w-1.5 h-1.5 rounded-full ${live ? 'bg-emerald-500 pulse-dot' : 'bg-slate-300'}`} />
              {live ? 'Streaming' : 'Idle'}
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
              {submitting ? 'Submitting...' : 'Submit Batch'}
            </button>
          </div>
        </div>
      </header>

      <main className="max-w-[1400px] mx-auto px-6 py-6 space-y-6">
        {/* Batch selector */}
        <BatchSelector
          batches={batches}
          activeBatchId={activeBatchId}
          onSelect={setActiveBatchId}
        />

        {/* Metrics */}
        {report ? (
          <MetricsGrid report={report} />
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            {[0, 1, 2, 3].map((i) => (
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
              <DonutChart data={report.by_root_cause_bucket} />
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

        {/* Live feed + classification accuracy */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2">
            <LiveFeed updates={updates} live={live} />
          </div>
          <div className="card p-5">
            <h3 className="text-sm font-semibold text-slate-700 mb-4">Classification Accuracy</h3>
            {report ? (
              <div className="space-y-4">
                <div>
                  <div className="flex items-baseline gap-2">
                    <span className="text-3xl font-bold text-slate-900">
                      {(report.classification_accuracy_vs_ground_truth * 100).toFixed(1)}%
                    </span>
                    <span className="text-sm text-slate-400">vs ground truth</span>
                  </div>
                  <p className="text-xs text-slate-400 mt-1">
                    Measures how often the LLM classifier agrees with the true root cause.
                  </p>
                </div>
                <div className="space-y-2 pt-2 border-t border-slate-100">
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-slate-500">Retry attempts</span>
                    <span className="text-sm font-semibold text-slate-700 tabular-nums">
                      {report.by_intervention_type.retry.count}
                    </span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-slate-500">Nudge attempts</span>
                    <span className="text-sm font-semibold text-slate-700 tabular-nums">
                      {report.by_intervention_type.nudge.count}
                    </span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-slate-500">Escalations</span>
                    <span className="text-sm font-semibold text-slate-700 tabular-nums">
                      {report.by_intervention_type.escalate.count}
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
        onClose={() => {
          setDrawerRecordId(null);
          setDrawerDetail(null);
        }}
      />
    </div>
  );
}

export default App;
