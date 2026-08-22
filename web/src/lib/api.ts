import type {
  BatchReport,
  BatchSummary,
  BatchSubmitResponse,
  BatchUpdate,
  RecordDetail,
  RecordSummary,
} from '@/types';
import { mockEngine } from '@/lib/mockEngine';

const API_KEY = import.meta.env.VITE_API_KEY ?? 'momotaro-demo-key';
const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '';

const USE_MOCK = !API_BASE || import.meta.env.VITE_USE_MOCK === 'true';

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': API_KEY,
      ...(options?.headers ?? {}),
    },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: { code: 'UNKNOWN', message: res.statusText } }));
    throw new Error(body.error?.message ?? `Request failed: ${res.status}`);
  }
  return res.json();
}

export const api = {
  async getBatches(): Promise<BatchSummary[]> {
    if (USE_MOCK) return mockEngine.getBatches();
    return request<BatchSummary[]>('/v1/batches');
  },

  async submitBatch(count: number = 80): Promise<BatchSubmitResponse> {
    if (USE_MOCK) return mockEngine.submitBatch(count);
    return request<BatchSubmitResponse>('/v1/batches', {
      method: 'POST',
      body: JSON.stringify({ count }),
    });
  },

  async getBatchReport(batch_id: string): Promise<BatchReport> {
    if (USE_MOCK) return mockEngine.getBatchReport(batch_id);
    return request<BatchReport>(`/v1/batches/${batch_id}/report`);
  },

  async getBatchRecords(batch_id: string): Promise<RecordSummary[]> {
    if (USE_MOCK) return mockEngine.getBatchRecords(batch_id);
    return request<RecordSummary[]>(`/v1/batches/${batch_id}/records`);
  },

  async getRecordDetail(record_id: string): Promise<RecordDetail> {
    if (USE_MOCK) return mockEngine.getRecordDetail(record_id);
    return request<RecordDetail>(`/v1/records/${record_id}/audit`);
  },

  subscribeToBatch(batch_id: string, onUpdate: (update: BatchUpdate) => void): () => void {
    if (USE_MOCK) {
      return mockEngine.subscribe(batch_id, onUpdate);
    }

    const wsUrl = `${API_BASE.replace(/^http/, 'ws')}/v1/batches/${batch_id}/live`;
    const ws = new WebSocket(wsUrl, [API_KEY]);

    ws.onmessage = (event) => {
      try {
        const update = JSON.parse(event.data) as BatchUpdate;
        onUpdate(update);
      } catch {
        // ignore malformed messages
      }
    };

    return () => ws.close();
  },
};
