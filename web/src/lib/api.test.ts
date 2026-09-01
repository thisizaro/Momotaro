import { describe, expect, it } from 'vitest';
import { collectAllRecordPages, MAX_RECORD_PAGES } from '@/lib/api';
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
