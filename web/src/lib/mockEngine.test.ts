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

/**
 * Mock implementations of `/v1/demo/*` (docs/API_GATEWAY.md, Phase 5.5 Unit
 * W/X), required by web/AGENTS.md's "if you add an endpoint, add it to both
 * api.ts and mockEngine.ts" rule so the demo control panel works with no
 * backend running.
 */
describe('MockEngine demo controls', () => {
  it('getDemoScenarios lists the four presets with non-empty descriptions', async () => {
    const engine = new MockEngine();
    const scenarios = await engine.getDemoScenarios();

    expect(scenarios.map((s) => s.name)).toEqual(['normal', 'bank-outage', 'salary-day', 'dead-cards']);
    for (const s of scenarios) {
      expect(s.description.length).toBeGreaterThan(0);
    }
  });

  it(
    'seedDemoBatch concentrates the requested scenario\'s failure code and, unlike the deleted ' +
      '"Generate Sample Data" button, carries ground truth (accuracy and baseline comparison present)',
    async () => {
      const engine = new MockEngine();
      const res = await engine.seedDemoBatch({ scenario: 'bank-outage', count: 20, seed: 7 });

      expect(res.generated_count).toBe(20);
      expect(res.seed).toBe(7);

      const { records } = await engine.getBatchRecords(res.batch_id, 100);
      expect(records).toHaveLength(20);
      // Checked immediately after seeding, before any of the staggered
      // classification timers in processRecord have had a chance to fire,
      // so every record still shows the bucket it was seeded with.
      for (const r of records) {
        expect(r.bucket).toBe('ROOT_CAUSE_BUCKET_TRANSIENT_BANK');
      }

      const report = await engine.getBatchReport(res.batch_id);
      expect(report.accuracy).toBeDefined();
      expect(report.baseline_comparison).toBeDefined();
    },
  );

  it('seedDemoBatch picks a random seed when none is given, and always echoes a nonzero one back', async () => {
    const engine = new MockEngine();
    const res = await engine.seedDemoBatch({ scenario: 'normal', count: 3 });

    expect(res.seed).toBeGreaterThan(0);
  });

  it('seedDemoBatch defaults an empty scenario to "normal" rather than throwing', async () => {
    const engine = new MockEngine();
    const res = await engine.seedDemoBatch({ scenario: '', count: 3 });

    expect(res.generated_count).toBe(3);
  });

  it('getDemoWorld reports the constructor-seeded pending records, sorted soonest-due first', async () => {
    // A fresh engine's seedCompletedBatch synchronously leaves a handful of
    // records mid-wait (RETRY_SCHEDULED with due_at set) precisely so
    // TimelineView has something to plot on load; getDemoWorld should
    // surface those same records without needing any timers to fire.
    const engine = new MockEngine();
    const world = await engine.getDemoWorld();

    expect(world.pending.length).toBeGreaterThan(0);
    const dueTimes = world.pending.map((p) => new Date(p.due_at).getTime());
    expect(dueTimes).toEqual([...dueTimes].sort((a, b) => a - b));
    for (const p of world.pending) {
      expect(p.attempt_number).toBeGreaterThan(0);
      expect(['OUTCOME_SUCCESS', 'OUTCOME_FAILURE']).toContain(p.outcome);
    }
  });

  it('injectPoison returns fresh ids that are not tracked anywhere, mirroring the real route never writing them to storage', async () => {
    const engine = new MockEngine();
    const res = await engine.injectPoison();

    expect(res.record_id).toBeTruthy();
    expect(res.batch_id).toBeTruthy();
    expect(res.record_id).not.toBe(res.batch_id);
    await expect(engine.getRecordDetail(res.record_id)).rejects.toThrow();
  });
});
