import type {
  BatchRecordsResponse,
  BatchReport,
  BatchSubmitRecord,
  BatchSubmitResponse,
  BatchSummary,
  BatchUpdate,
  InvariantsResponse,
  ListBatchesResponse,
  RecordAuditResponse,
  SubmitRecordType,
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

const SUBMIT_RECORD_TYPES: SubmitRecordType[] = ['PAYMENT', 'MANDATE', 'CHECKOUT', 'INVOICE'];
const DEMO_FAILURE_CODES = [
  'bank_not_available',
  'insufficient_funds',
  'card_expired',
  'issuer_declined',
  'risk_threshold_breached',
];
const DEMO_AMOUNTS_PAISE = [29900, 49900, 69900, 99900, 149900, 199900, 499900];

function pick<T>(arr: T[]): T {
  return arr[Math.floor(Math.random() * arr.length)];
}

/**
 * `POST /v1/batches`'s `count` form is not yet backed (see API_GATEWAY.md),
 * so an explicit records list is built client-side and sent through the
 * live `records` form instead of attempting the unsupported shape.
 */
function generateDemoRecords(count: number): BatchSubmitRecord[] {
  return Array.from({ length: count }, () => ({
    type: pick(SUBMIT_RECORD_TYPES),
    amount_paise: pick(DEMO_AMOUNTS_PAISE),
    currency: 'INR',
    failure_code: pick(DEMO_FAILURE_CODES),
    instrument_ref: `demo_ref_${Math.random().toString(36).slice(2, 10)}`,
  }));
}

export const api = {
  async getBatches(): Promise<BatchSummary[]> {
    if (USE_MOCK) return mockEngine.getBatches();
    const res = await request<ListBatchesResponse>('/v1/batches');
    return res.batches;
  },

  async submitBatch(source: string, count: number = 80): Promise<BatchSubmitResponse> {
    if (USE_MOCK) return mockEngine.submitBatch(source, count);
    return request<BatchSubmitResponse>('/v1/batches', {
      method: 'POST',
      body: JSON.stringify({ source, records: generateDemoRecords(count) }),
    });
  },

  async getBatchReport(batch_id: string): Promise<BatchReport> {
    if (USE_MOCK) return mockEngine.getBatchReport(batch_id);
    return request<BatchReport>(`/v1/batches/${batch_id}/report`);
  },

  async getBatchRecords(batch_id: string): Promise<BatchRecordsResponse> {
    if (USE_MOCK) return mockEngine.getBatchRecords(batch_id);
    return request<BatchRecordsResponse>(`/v1/batches/${batch_id}/records`);
  },

  async getRecordDetail(record_id: string): Promise<RecordAuditResponse> {
    if (USE_MOCK) return mockEngine.getRecordDetail(record_id);
    return request<RecordAuditResponse>(`/v1/records/${record_id}/audit`);
  },

  async getBatchInvariants(batch_id: string): Promise<InvariantsResponse> {
    if (USE_MOCK) return mockEngine.getBatchInvariants(batch_id);
    return request<InvariantsResponse>(`/v1/batches/${batch_id}/invariants`);
  },

  subscribeToBatch(
    batch_id: string,
    onUpdate: (update: BatchUpdate) => void,
    onConnectionChange?: (connected: boolean) => void,
  ): () => void {
    if (USE_MOCK) {
      onConnectionChange?.(true);
      return mockEngine.subscribe(batch_id, onUpdate);
    }

    // Auth, closes gap 5 (API_GATEWAY.md): a WebSocket handshake cannot set
    // a custom header, so the API key is sent as a subprotocol instead.
    const wsUrl = `${API_BASE.replace(/^http/, 'ws')}/v1/batches/${batch_id}/live`;
    const ws = new WebSocket(wsUrl, [API_KEY]);

    ws.onopen = () => onConnectionChange?.(true);
    ws.onerror = () => onConnectionChange?.(false);
    ws.onclose = () => onConnectionChange?.(false);

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
