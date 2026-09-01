import type {
  BatchRecordsResponse,
  BatchReport,
  BatchSummary,
  BatchUpdate,
  DemoBatchRequest,
  DemoBatchResponse,
  DemoInjectPoisonResponse,
  DemoScenario,
  DemoScenariosResponse,
  DemoWorldResponse,
  InvariantsResponse,
  ListBatchesResponse,
  RecordAuditResponse,
  RecordSummary,
} from '@/types';
import { mockEngine } from '@/lib/mockEngine';

const API_KEY = import.meta.env.VITE_API_KEY ?? 'momotaro-demo-key';
const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '';

export const USE_MOCK = !API_BASE || import.meta.env.VITE_USE_MOCK === 'true';

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

/**
 * Thrown by `demoRequest` when a `/v1/demo/*` route comes back `404`. Per
 * docs/API_GATEWAY.md, every one of these routes exists only when the
 * Gateway is started with `DEMO_CONTROLS_ENABLED=true`; when it is not, they
 * are not registered at all, so a 404 here is never "wrong URL", it means
 * the whole namespace is off. A named error class lets the panel tell that
 * state apart from a real failure (bad request, server error, network
 * problem) instead of guessing from a status code or message text.
 */
export class DemoControlsDisabledError extends Error {
  constructor() {
    super('Demo controls are disabled. Start the stack with PROFILE=demo.');
    this.name = 'DemoControlsDisabledError';
  }
}

// Exported (like collectAllRecordPages below) so its 404-vs-other-failure
// branching can be exercised directly against a mocked fetch, without
// needing USE_MOCK false and a real network call.
export async function demoRequest<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': API_KEY,
      ...(options?.headers ?? {}),
    },
  });
  if (res.status === 404) {
    throw new DemoControlsDisabledError();
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: { code: 'UNKNOWN', message: res.statusText } }));
    throw new Error(body.error?.message ?? `Request failed: ${res.status}`);
  }
  return res.json();
}

/**
 * `GET /v1/batches/{id}/records` paginates (docs/API_GATEWAY.md); its
 * default page is small (20 on the live Gateway), so a batch of any real
 * size needs several requests. Requesting a larger page here cuts the
 * number of round trips. docs/API_GATEWAY.md documents `page_size` as an
 * optional param but does not publish the Gateway's own maximum, so this
 * is a conservative value rather than a documented ceiling; it has been
 * checked against the live Gateway and comes back unclamped.
 */
export const RECORDS_PAGE_SIZE = 100;

/**
 * Guards against unbounded fetching for a batch too large to page through
 * reasonably. At RECORDS_PAGE_SIZE that is up to 5,000 records before
 * collectAllRecordPages stops and reports `truncated: true` instead of
 * either hanging or silently dropping the rest, which is the bug this
 * whole fetch loop exists to fix.
 */
export const MAX_RECORD_PAGES = 50;

export interface FetchAllRecordsResult {
  records: RecordSummary[];
  totalCount: number;
  truncated: boolean;
}

/**
 * Fetches every page of a paginated records response by following
 * `next_page_token` until the server sends back an empty one, meaning
 * there is no more (docs/API_GATEWAY.md). `fetchPage` is injected so this
 * loop can be exercised with a fake multi-page responder in tests, rather
 * than only through a real network call or the mock engine.
 *
 * Bounded by `maxPages`: an unbounded caller could otherwise hang or make
 * an unreasonable number of requests against a very large batch. Hitting
 * the cap is reported via `truncated`, not swallowed, so callers can tell
 * the user rather than silently showing a partial list as if it were
 * everything, which is exactly the bug being fixed here.
 */
export async function collectAllRecordPages(
  fetchPage: (pageToken: string) => Promise<BatchRecordsResponse>,
  maxPages: number = MAX_RECORD_PAGES,
): Promise<FetchAllRecordsResult> {
  const records: RecordSummary[] = [];
  let pageToken = '';
  let totalCount = 0;
  let truncated = false;
  let pagesFetched = 0;

  for (;;) {
    const page = await fetchPage(pageToken);
    records.push(...page.records);
    totalCount = page.total_count;
    pagesFetched++;

    if (!page.next_page_token) break;

    if (pagesFetched >= maxPages) {
      truncated = true;
      break;
    }
    pageToken = page.next_page_token;
  }

  return { records, totalCount, truncated };
}

function recordsPath(batch_id: string, page_token: string): string {
  const params = new URLSearchParams({ page_size: String(RECORDS_PAGE_SIZE) });
  if (page_token) params.set('page_token', page_token);
  return `/v1/batches/${batch_id}/records?${params.toString()}`;
}

export const api = {
  async getBatches(): Promise<BatchSummary[]> {
    if (USE_MOCK) return mockEngine.getBatches();
    const res = await request<ListBatchesResponse>('/v1/batches');
    return res.batches;
  },

  async getBatchReport(batch_id: string): Promise<BatchReport> {
    if (USE_MOCK) return mockEngine.getBatchReport(batch_id);
    return request<BatchReport>(`/v1/batches/${batch_id}/report`);
  },

  async getBatchRecords(batch_id: string): Promise<FetchAllRecordsResult> {
    if (USE_MOCK) {
      return collectAllRecordPages((pageToken) =>
        mockEngine.getBatchRecords(batch_id, RECORDS_PAGE_SIZE, pageToken),
      );
    }
    return collectAllRecordPages((pageToken) =>
      request<BatchRecordsResponse>(recordsPath(batch_id, pageToken)),
    );
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

  // `/v1/demo/*` (docs/API_GATEWAY.md "Demo controls"). All four go through
  // demoRequest so a disabled Gateway namespace surfaces as
  // DemoControlsDisabledError instead of a generic failure.

  async getDemoScenarios(): Promise<DemoScenario[]> {
    if (USE_MOCK) return mockEngine.getDemoScenarios();
    const res = await demoRequest<DemoScenariosResponse>('/v1/demo/scenarios');
    return res.scenarios;
  },

  async seedDemoBatch(req: DemoBatchRequest): Promise<DemoBatchResponse> {
    if (USE_MOCK) return mockEngine.seedDemoBatch(req);
    return demoRequest<DemoBatchResponse>('/v1/demo/batches', {
      method: 'POST',
      body: JSON.stringify(req),
    });
  },

  async getDemoWorld(): Promise<DemoWorldResponse> {
    if (USE_MOCK) return mockEngine.getDemoWorld();
    return demoRequest<DemoWorldResponse>('/v1/demo/world');
  },

  async injectPoison(): Promise<DemoInjectPoisonResponse> {
    if (USE_MOCK) return mockEngine.injectPoison();
    return demoRequest<DemoInjectPoisonResponse>('/v1/demo/inject-poison', { method: 'POST' });
  },
};
