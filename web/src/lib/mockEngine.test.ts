import { describe, expect, it } from 'vitest';
import { MockEngine } from '@/lib/mockEngine';

/**
 * Regression test for the pagination bug (docs/INCIDENTS.md): the mock
 * engine used to hand back every record for a batch in one response with
 * next_page_token always '', which meant mock mode never exercised the
 * multi-page path the real Gateway uses (docs/API_GATEWAY.md,
 * GET /v1/batches/{batch_id}/records). It has to paginate the same way the
 * real Gateway does, or a client bug that only shows up across pages stays
 * invisible against mocks.
 */
describe('MockEngine.getBatchRecords pagination', () => {
  it('splits a batch across pages honoring page_size, and signals the last page with an empty next_page_token', async () => {
    const engine = new MockEngine();
    const batches = await engine.getBatches();
    const batch = batches[0];
    expect(batch).toBeDefined();

    // Force a small page size so a real batch is guaranteed to span
    // multiple pages regardless of how many records got seeded.
    const pageSize = 10;

    const seen = new Set<string>();
    let pageToken = '';
    let pages = 0;
    let lastTotalCount = -1;

    do {
      const res = await engine.getBatchRecords(batch.batch_id, pageSize, pageToken);
      expect(res.records.length).toBeLessThanOrEqual(pageSize);
      for (const r of res.records) {
        expect(seen.has(r.record_id)).toBe(false); // no duplicate across pages
        seen.add(r.record_id);
      }
      lastTotalCount = res.total_count;
      pageToken = res.next_page_token;
      pages++;
      expect(pages).toBeLessThan(1000); // sanity bound so a bug can't hang the test
    } while (pageToken !== '');

    expect(pages).toBeGreaterThan(1); // proves this batch actually spans multiple pages
    expect(seen.size).toBe(lastTotalCount);
    expect(seen.size).toBe(batch.total_records);
  });

  it('defaults to the same page size as the real Gateway when none is requested', async () => {
    const engine = new MockEngine();
    const batches = await engine.getBatches();
    const batch = batches[0];

    const res = await engine.getBatchRecords(batch.batch_id);

    expect(res.records.length).toBe(Math.min(20, batch.total_records));
    if (batch.total_records > 20) {
      expect(res.next_page_token).not.toBe('');
    }
  });
});
