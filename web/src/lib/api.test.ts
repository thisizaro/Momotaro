import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  DemoControlsDisabledError,
  LIVE_SOCKET_DEGRADED_AFTER_ATTEMPTS,
  LIVE_SOCKET_INITIAL_DELAY_MS,
  LIVE_SOCKET_MAX_DELAY_MS,
  collectAllRecordPages,
  connectLiveSocket,
  demoRequest,
  MAX_RECORD_PAGES,
} from '@/lib/api';
import type { BatchRecordsResponse, BatchUpdate, RecordSummary } from '@/types';

function record(id: string): RecordSummary {
  return {
    record_id: id,
    type: 'RECORD_TYPE_PAYMENT',
    amount_paise: 100,
    current_state: 'RECORD_STATE_NEW',
    bucket: 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK',
    attempt_count: 0,
    spend_paise: 0,
    due_at: '',
    first_action_at: '',
    last_action_at: '',
  };
}

/**
 * Regression test for the pagination bug (docs/INCIDENTS.md): the old
 * getBatchRecords sent no page_size and never looked at next_page_token, so
 * a 100-record batch against the real Gateway (which pages at 20) rendered
 * only the first 20. This exercises the fetch-and-follow loop against a
 * fake multi-page responder, the same shape a live Gateway or a paginating
 * mock returns, without needing either running.
 */
describe('collectAllRecordPages', () => {
  it('follows next_page_token until it is empty and returns every record', async () => {
    // Three pages: 20 + 20 + 60, matching the confirmed live-bug shape
    // (total_count 100, first page 20).
    const pages: BatchRecordsResponse[] = [
      { records: Array.from({ length: 20 }, (_, i) => record(`r${i}`)), next_page_token: '20', total_count: 100 },
      {
        records: Array.from({ length: 20 }, (_, i) => record(`r${20 + i}`)),
        next_page_token: '40',
        total_count: 100,
      },
      {
        records: Array.from({ length: 60 }, (_, i) => record(`r${40 + i}`)),
        next_page_token: '',
        total_count: 100,
      },
    ];

    const requestedTokens: string[] = [];
    const fetchPage = async (pageToken: string) => {
      requestedTokens.push(pageToken);
      return pages[requestedTokens.length - 1];
    };

    const result = await collectAllRecordPages(fetchPage);

    expect(result.records).toHaveLength(100);
    expect(new Set(result.records.map((r) => r.record_id)).size).toBe(100);
    expect(result.totalCount).toBe(100);
    expect(result.truncated).toBe(false);
    expect(requestedTokens).toEqual(['', '20', '40']);
  });

  it('returns everything in one call when the batch fits on a single page', async () => {
    const single: BatchRecordsResponse = {
      records: Array.from({ length: 5 }, (_, i) => record(`r${i}`)),
      next_page_token: '',
      total_count: 5,
    };
    let calls = 0;
    const result = await collectAllRecordPages(async () => {
      calls++;
      return single;
    });

    expect(calls).toBe(1);
    expect(result.records).toHaveLength(5);
    expect(result.truncated).toBe(false);
  });

  it('stops at the page cap and reports truncation instead of looping forever', async () => {
    // A responder that always claims there is another page: the loop must
    // still terminate, and must say so, rather than silently truncating
    // like the original bug or hanging forever.
    let calls = 0;
    const fetchPage = async (): Promise<BatchRecordsResponse> => {
      calls++;
      return { records: [record(`r${calls}`)], next_page_token: 'more', total_count: 999999 };
    };

    const result = await collectAllRecordPages(fetchPage, 5);

    expect(calls).toBe(5);
    expect(result.records).toHaveLength(5);
    expect(result.truncated).toBe(true);
    expect(result.totalCount).toBe(999999);
  });

  it('the default cap is a real, positive bound', () => {
    expect(MAX_RECORD_PAGES).toBeGreaterThan(0);
  });
});

/**
 * `/v1/demo/*` (docs/API_GATEWAY.md "Demo controls") 404s the same way any
 * unknown path does when DEMO_CONTROLS_ENABLED is false: the whole
 * namespace is unregistered, not "locked". The panel needs to tell that
 * state apart from an ordinary failure so it can show "start the stack with
 * PROFILE=demo" instead of a broken page or a generic error banner. These
 * exercise demoRequest directly against a mocked fetch, since USE_MOCK is
 * true in this test environment (no VITE_API_BASE_URL) and would otherwise
 * route every api.* demo call through mockEngine instead of this function.
 */
describe('demoRequest', () => {
  const originalFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  function fakeResponse(status: number, body: unknown): Response {
    return {
      status,
      ok: status >= 200 && status < 300,
      json: async () => body,
    } as unknown as Response;
  }

  it('throws DemoControlsDisabledError on a 404, distinct from any other failure', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(fakeResponse(404, {}));

    await expect(demoRequest('/v1/demo/scenarios')).rejects.toBeInstanceOf(DemoControlsDisabledError);
  });

  it('the disabled error names the actual fix, so the panel does not need to invent copy', () => {
    const err = new DemoControlsDisabledError();
    expect(err.message).toContain('PROFILE=demo');
  });

  it('returns the parsed body on a 2xx response', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(fakeResponse(200, { scenarios: [] }));

    const result = await demoRequest<{ scenarios: unknown[] }>('/v1/demo/scenarios');
    expect(result).toEqual({ scenarios: [] });
  });

  it('throws a plain Error carrying the server message on a non-404 failure, not DemoControlsDisabledError', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue(fakeResponse(500, { error: { code: 'INTERNAL', message: 'seed generation failed' } }));

    await expect(demoRequest('/v1/demo/batches')).rejects.toThrow('seed generation failed');

    try {
      await demoRequest('/v1/demo/batches');
      expect.fail('expected demoRequest to reject');
    } catch (err) {
      expect(err).not.toBeInstanceOf(DemoControlsDisabledError);
    }
  });
});

/**
 * Regression tests for Unit AG (docs/DEMO_READINESS.md): `subscribeToBatch`
 * used to open a WebSocket exactly once and treat every close, including a
 * clean one at the end of a finished batch, as a failure. `connectLiveSocket`
 * is the reconnect-and-status-classification core, extracted from
 * `subscribeToBatch` the same way `collectAllRecordPages` is extracted from
 * `getBatchRecords`, so it can be exercised without USE_MOCK routing every
 * call through mockEngine (no VITE_API_BASE_URL is set in this test
 * environment, so USE_MOCK is always true here).
 *
 * A fake WebSocket stands in for the browser's, since vitest's `environment:
 * 'node'` (vite.config.ts) has no real one. Each fake instance is captured
 * so a test can drive its onopen/onclose/onmessage handlers directly and
 * assert on how many sockets connectLiveSocket created.
 */
describe('connectLiveSocket', () => {
  interface CloseEventLike {
    code: number;
    wasClean: boolean;
  }

  class FakeSocket {
    onopen: (() => void) | null = null;
    onclose: ((ev: CloseEventLike) => void) | null = null;
    onerror: (() => void) | null = null;
    onmessage: ((ev: { data: string }) => void) | null = null;
    closeCalls = 0;

    constructor(
      public url: string,
      public protocols: string[],
    ) {}

    close() {
      this.closeCalls++;
      // A real browser WebSocket only fires onclose asynchronously, even
      // for a locally-initiated close; tests trigger it explicitly via
      // fireClose so the ordering (close() called, then the event arrives
      // later) matches the real race this unit fixes.
    }
  }

  let sockets: FakeSocket[];
  const createSocket = (url: string, protocols: string[]) => {
    const s = new FakeSocket(url, protocols);
    sockets.push(s);
    return s as unknown as WebSocket;
  };

  function fireOpen(s: FakeSocket) {
    s.onopen?.();
  }

  function fireClose(s: FakeSocket, code: number, wasClean: boolean) {
    s.onclose?.({ code, wasClean });
  }

  beforeEach(() => {
    sockets = [];
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('opens exactly one socket up front', () => {
    connectLiveSocket('ws://example/live', ['key'], { onUpdate: vi.fn(), createSocket });
    expect(sockets).toHaveLength(1);
    expect(sockets[0].url).toBe('ws://example/live');
    expect(sockets[0].protocols).toEqual(['key']);
  });

  it('a remote close triggers a reconnect attempt', () => {
    const onStatusChange = vi.fn();
    connectLiveSocket('ws://example/live', ['key'], { onUpdate: vi.fn(), onStatusChange, createSocket });

    fireOpen(sockets[0]);
    // Abnormal closure: no close frame from the server (dropped connection,
    // proxy idle timeout, server restart), not the batch finishing.
    fireClose(sockets[0], 1006, false);

    expect(onStatusChange).toHaveBeenCalledWith('reconnecting');
    expect(sockets).toHaveLength(1); // not yet, backoff hasn't elapsed

    vi.advanceTimersByTime(LIVE_SOCKET_INITIAL_DELAY_MS);

    expect(sockets).toHaveLength(2); // reconnected
  });

  it('a local teardown does not trigger a reconnect', () => {
    const onStatusChange = vi.fn();
    const cleanup = connectLiveSocket('ws://example/live', ['key'], { onUpdate: vi.fn(), onStatusChange, createSocket });

    fireOpen(sockets[0]);
    onStatusChange.mockClear();

    cleanup(); // caller (App.tsx unmount / batch switch) tears down first
    expect(sockets[0].closeCalls).toBe(1);

    // The real ws.close() the fake just recorded still delivers its close
    // event asynchronously, exactly like a real browser. That late event
    // must be ignored, not treated as a dropped connection.
    fireClose(sockets[0], 1006, false);

    vi.advanceTimersByTime(LIVE_SOCKET_MAX_DELAY_MS * 4);

    expect(sockets).toHaveLength(1); // no reconnect socket was opened
    expect(onStatusChange).not.toHaveBeenCalled();
  });

  it('a clean server close reports completion rather than failure, and does not reconnect', () => {
    const onStatusChange = vi.fn();
    connectLiveSocket('ws://example/live', ['key'], { onUpdate: vi.fn(), onStatusChange, createSocket });

    fireOpen(sockets[0]);
    // Gateway's liveUpdates (services/api-gateway/internal/httpapi/live.go)
    // closes with websocket.StatusNormalClosure (code 1000) when the
    // upstream StreamBatchUpdates hits io.EOF, i.e. the batch is done.
    fireClose(sockets[0], 1000, true);

    expect(onStatusChange).toHaveBeenCalledWith('complete');
    expect(onStatusChange).not.toHaveBeenCalledWith('disconnected');
    expect(onStatusChange).not.toHaveBeenCalledWith('reconnecting');

    vi.advanceTimersByTime(LIVE_SOCKET_MAX_DELAY_MS * 4);
    expect(sockets).toHaveLength(1); // nothing more will ever arrive
  });

  it('backs off exponentially and caps the delay', () => {
    const onStatusChange = vi.fn();
    connectLiveSocket('ws://example/live', ['key'], { onUpdate: vi.fn(), onStatusChange, createSocket });

    fireOpen(sockets[0]);
    fireClose(sockets[0], 1006, false);
    vi.advanceTimersByTime(LIVE_SOCKET_INITIAL_DELAY_MS - 1);
    expect(sockets).toHaveLength(1); // not yet
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(2);

    // Second failure without ever reopening: backoff should have doubled,
    // not reset.
    fireClose(sockets[1], 1006, false);
    vi.advanceTimersByTime(LIVE_SOCKET_INITIAL_DELAY_MS * 2 - 1);
    expect(sockets).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(3);

    // Keep failing until the delay would exceed the cap; it must never.
    for (let i = 0; i < 10; i++) {
      fireClose(sockets[sockets.length - 1], 1006, false);
      vi.advanceTimersByTime(LIVE_SOCKET_MAX_DELAY_MS);
    }
    // Every attempt above eventually reconnects at or under the cap, so the
    // timer never has to wait longer than LIVE_SOCKET_MAX_DELAY_MS to see
    // the next socket.
    expect(sockets.length).toBeGreaterThan(3);
  });

  it('a successful reopen resets the backoff and status back to live', () => {
    const onStatusChange = vi.fn();
    connectLiveSocket('ws://example/live', ['key'], { onUpdate: vi.fn(), onStatusChange, createSocket });

    fireOpen(sockets[0]);
    fireClose(sockets[0], 1006, false);
    vi.advanceTimersByTime(LIVE_SOCKET_INITIAL_DELAY_MS);
    expect(sockets).toHaveLength(2);

    fireOpen(sockets[1]);
    expect(onStatusChange).toHaveBeenLastCalledWith('live');

    // Fails again right away: since the attempt counter reset on open, the
    // very next backoff should be the initial delay again, not a longer one.
    fireClose(sockets[1], 1006, false);
    vi.advanceTimersByTime(LIVE_SOCKET_INITIAL_DELAY_MS - 1);
    expect(sockets).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(3);
  });

  it('reports disconnected, not reconnecting, once repeated failures show the connection is genuinely broken', () => {
    const onStatusChange = vi.fn();
    connectLiveSocket('ws://example/live', ['key'], { onUpdate: vi.fn(), onStatusChange, createSocket });

    fireOpen(sockets[0]);
    for (let i = 0; i < LIVE_SOCKET_DEGRADED_AFTER_ATTEMPTS; i++) {
      fireClose(sockets[sockets.length - 1], 1006, false);
      vi.advanceTimersByTime(LIVE_SOCKET_MAX_DELAY_MS);
    }

    // Still climbing: every close so far should have been reported amber.
    expect(onStatusChange).not.toHaveBeenCalledWith('disconnected');

    fireClose(sockets[sockets.length - 1], 1006, false);
    expect(onStatusChange).toHaveBeenCalledWith('disconnected');

    // It is still self-healing, not permanently given up: it keeps retrying.
    vi.advanceTimersByTime(LIVE_SOCKET_MAX_DELAY_MS);
    expect(sockets.length).toBeGreaterThan(LIVE_SOCKET_DEGRADED_AFTER_ATTEMPTS + 1);
  });

  it('delivers parsed messages to onUpdate', () => {
    const onUpdate = vi.fn();
    connectLiveSocket('ws://example/live', ['key'], { onUpdate, createSocket });

    fireOpen(sockets[0]);
    const update: BatchUpdate = {
      record_id: 'r1',
      from_state: 'RECORD_STATE_NEW',
      to_state: 'RECORD_STATE_SCORING',
      ts: '2026-09-02T00:00:00Z',
      recovered_delta_paise: 0,
    };
    sockets[0].onmessage?.({ data: JSON.stringify(update) });

    expect(onUpdate).toHaveBeenCalledWith(update);
  });

  it('ignores a malformed message instead of throwing', () => {
    const onUpdate = vi.fn();
    connectLiveSocket('ws://example/live', ['key'], { onUpdate, createSocket });

    fireOpen(sockets[0]);
    expect(() => sockets[0].onmessage?.({ data: 'not json' })).not.toThrow();
    expect(onUpdate).not.toHaveBeenCalled();
  });
});
