import { afterEach, describe, expect, it, vi } from 'vitest';
import { DemoControlsDisabledError, collectAllRecordPages, demoRequest, MAX_RECORD_PAGES } from '@/lib/api';
import type { BatchRecordsResponse, RecordSummary } from '@/types';

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
