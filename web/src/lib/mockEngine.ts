import type {
  AccuracyBlock,
  ActionType,
  AuditEntry,
  BaselineComparison,
  BatchRecordsResponse,
  BatchReport,
  BatchSubmitResponse,
  BatchSummary,
  BatchUpdate,
  InterventionBreakdown,
  InvariantsResponse,
  Outcome,
  ProviderHop,
  RecordAuditResponse,
  RecordState,
  RecordSummary,
  RecordType,
  RootCauseBreakdown,
  RootCauseBucket,
  Source,
} from '@/types';

type Listener = (update: BatchUpdate) => void;

interface GroundTruth {
  true_bucket: RootCauseBucket;
  recovery_probability: number;
  recovers_on: 'retry' | 'nudge' | 'never';
}

interface InternalIntervention {
  action: ActionType;
  cost_paise: number;
  amount_paise: number;
  outcome: Outcome;
}

interface InternalRecord {
  id: string;
  batch_id: string;
  type: RecordType;
  amount_paise: number;
  failure_code: string;
  current_state: RecordState;
  bucket: RootCauseBucket;
  attempt_count: number;
  rationale: string;
  classification_source: Source;
  classification_correct: boolean;
  entries: AuditEntry[];
  interventions: InternalIntervention[];
  ground_truth: GroundTruth;
  /** Whether a naive "retry 3x, nudge once, no economics" policy would have
   *  recovered this record, decided once at creation (like ground_truth)
   *  so baseline_comparison stays stable across repeated report polls
   *  instead of re-rolling every 2 seconds. */
  naive_recovered: boolean;
  processed: boolean;
  created_at: string;
  /**
   * RFC3339 while a scheduled timer (RETRY_SCHEDULED/NUDGE_SCHEDULED) is
   * pending, '' otherwise, mirroring due_at's real wire representation
   * (docs/API_GATEWAY.md) so the mock engine exercises the same countdown
   * UI the real backend drives.
   */
  due_at: string;
}

interface InternalBatch {
  id: string;
  created_at: string;
  total_records: number;
  source: string;
  record_ids: string[];
  /** Mirrors the real system: only scripts/batchgen-seeded batches carry
   *  GROUND_TRUTH. A submitted batch reports like real production traffic
   *  (see API_GATEWAY.md's ground-truth boundary). */
  has_ground_truth: boolean;
}

const ALL_BUCKETS: RootCauseBucket[] = [
  'ROOT_CAUSE_BUCKET_TRANSIENT_BANK',
  'ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS',
  'ROOT_CAUSE_BUCKET_HARD_DECLINE',
  'ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED',
  'ROOT_CAUSE_BUCKET_RISK_HOLD',
  'ROOT_CAUSE_BUCKET_ABANDONMENT',
  'ROOT_CAUSE_BUCKET_OVERDUE',
];

const FAILURE_CODES = [
  'bank_not_available',
  'bank_timeout',
  'insufficient_funds',
  'card_expired',
  'issuer_declined',
  'mandate_not_authenticated',
  'risk_threshold_breached',
  'checkout_abandoned',
  'invoice_overdue',
];

const FAILURE_TO_BUCKET: Record<string, RootCauseBucket> = {
  bank_not_available: 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK',
  bank_timeout: 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK',
  insufficient_funds: 'ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS',
  card_expired: 'ROOT_CAUSE_BUCKET_HARD_DECLINE',
  issuer_declined: 'ROOT_CAUSE_BUCKET_HARD_DECLINE',
  mandate_not_authenticated: 'ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED',
  risk_threshold_breached: 'ROOT_CAUSE_BUCKET_RISK_HOLD',
  checkout_abandoned: 'ROOT_CAUSE_BUCKET_ABANDONMENT',
  invoice_overdue: 'ROOT_CAUSE_BUCKET_OVERDUE',
};

const RATIONALES: Record<RootCauseBucket, string[]> = {
  ROOT_CAUSE_BUCKET_TRANSIENT_BANK: [
    'Bank timeout occurred during the transaction. This is a transient rail issue, so retrying with a short backoff.',
    'Rail congestion on the payment network. The failure is not customer-side; a retry in the next window should succeed.',
  ],
  ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS: [
    'Insufficient funds detected, but the instrument is valid. Scheduling a retry for the next salary-credit window when balance is likely replenished.',
  ],
  ROOT_CAUSE_BUCKET_HARD_DECLINE: [
    'The card is expired. No retry can succeed, the customer must update their payment method. Sending a nudge with a method-update link.',
    'Hard decline from the issuer. The instrument is no longer usable. Nudging the customer to update their payment method.',
  ],
  ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED: [
    'Mandate is not authenticated. The customer must complete authentication before this can succeed. Nudging with an authentication link.',
  ],
  ROOT_CAUSE_BUCKET_RISK_HOLD: [
    'Risk hold placed by the fraud engine. Automatic retries must not circumvent a risk decision. Escalating to a human reviewer.',
  ],
  ROOT_CAUSE_BUCKET_ABANDONMENT: [
    'Checkout was abandoned before completion. Sending a reminder nudge to resume the purchase.',
  ],
  ROOT_CAUSE_BUCKET_OVERDUE: [
    'Invoice is overdue with no failure signal from the rail. Sending a payment reminder.',
  ],
};

const NUDGE_MESSAGES: Partial<Record<RootCauseBucket, string[]>> = {
  ROOT_CAUSE_BUCKET_HARD_DECLINE: [
    'Namaste! Aapka card expire ho gaya hai. Please apne payment method ko update karein: momotaro.link/update',
    'Hi! Your saved card was declined. Please update your payment method to continue your subscription: momotaro.link/update',
  ],
  ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED: [
    'Hi! Your mandate needs authentication before we can proceed. Please complete it here: momotaro.link/authenticate',
  ],
  ROOT_CAUSE_BUCKET_ABANDONMENT: [
    'Hi! Aapne checkout complete nahi kiya. Yahan se resume karein: momotaro.link/resume',
  ],
  ROOT_CAUSE_BUCKET_OVERDUE: [
    'Hi! Your invoice is overdue. Please complete payment here: momotaro.link/pay',
  ],
};

function uuid(): string {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === 'x' ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

function pick<T>(arr: T[]): T {
  return arr[Math.floor(Math.random() * arr.length)];
}

function randomAmountPaise(): number {
  const tiers = [299, 499, 699, 999, 1499, 1999, 2499, 4999, 9999];
  return pick(tiers) * 100;
}

function randomType(): RecordType {
  const roll = Math.random();
  if (roll < 0.4) return 'RECORD_TYPE_PAYMENT';
  if (roll < 0.7) return 'RECORD_TYPE_MANDATE';
  if (roll < 0.85) return 'RECORD_TYPE_CHECKOUT';
  return 'RECORD_TYPE_INVOICE';
}

function classify(
  failure_code: string,
  ground_truth: GroundTruth,
): { bucket: RootCauseBucket; rationale: string; source: Source; correct: boolean } {
  // 85% chance the classifier gets it right
  const correct = Math.random() < 0.85;
  const true_bucket = ground_truth.true_bucket;
  const bucket = correct ? true_bucket : pick(ALL_BUCKETS);

  // 80% LLM, 20% rules fallback
  const source: Source = Math.random() < 0.8 ? 'SOURCE_LLM' : 'SOURCE_RULES_FALLBACK';

  return {
    bucket,
    rationale: pick(RATIONALES[true_bucket]),
    source,
    correct: bucket === true_bucket,
  };
}

function generateGroundTruth(failure_code: string): GroundTruth {
  const true_bucket = FAILURE_TO_BUCKET[failure_code] ?? pick(ALL_BUCKETS);

  switch (true_bucket) {
    case 'ROOT_CAUSE_BUCKET_RISK_HOLD':
      return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
    case 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK':
      if (Math.random() < 0.8) {
        return { true_bucket, recovery_probability: 0.75 + Math.random() * 0.15, recovers_on: 'retry' };
      }
      return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
    case 'ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS':
      if (Math.random() < 0.7) {
        return { true_bucket, recovery_probability: 0.6 + Math.random() * 0.2, recovers_on: 'retry' };
      }
      return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
    case 'ROOT_CAUSE_BUCKET_HARD_DECLINE':
      if (Math.random() < 0.15) {
        return { true_bucket, recovery_probability: 0.1 + Math.random() * 0.1, recovers_on: 'nudge' };
      }
      return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
    case 'ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED':
      if (Math.random() < 0.4) {
        return { true_bucket, recovery_probability: 0.3 + Math.random() * 0.2, recovers_on: 'nudge' };
      }
      return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
    case 'ROOT_CAUSE_BUCKET_ABANDONMENT':
      if (Math.random() < 0.25) {
        return { true_bucket, recovery_probability: 0.15 + Math.random() * 0.15, recovers_on: 'nudge' };
      }
      return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
    case 'ROOT_CAUSE_BUCKET_OVERDUE':
      if (Math.random() < 0.5) {
        return { true_bucket, recovery_probability: 0.4 + Math.random() * 0.2, recovers_on: 'nudge' };
      }
      return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
    default:
      return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
  }
}

function nudgeActionFor(bucket: RootCauseBucket): ActionType {
  return bucket === 'ROOT_CAUSE_BUCKET_HARD_DECLINE' || bucket === 'ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED'
    ? 'ACTION_TYPE_NUDGE_METHOD_UPDATE'
    : 'ACTION_TYPE_NUDGE_REMINDER';
}

/**
 * Cause-aware retry timing, the behaviour Unit AB exists to make visible:
 * a transient rail issue is retried almost immediately, but a retry against
 * an empty account is scheduled for a simulated salary-credit window instead
 * of burning an attempt right away. Clustering the insufficient-funds delays
 * into a narrow late band (rather than spreading them across the whole
 * range) is what makes them read as one shared window on the timeline
 * instead of a handful of unrelated waits.
 */
function retryDelayMs(bucket: RootCauseBucket, attempt_count: number): number {
  if (bucket === 'ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS') {
    return 45000 + attempt_count * 4000 + Math.random() * 15000;
  }
  return 600 + attempt_count * 300 + Math.random() * 900;
}

function decideAction(
  bucket: RootCauseBucket,
  attempt_count: number,
): { action: ActionType; delay: number } {
  if (bucket === 'ROOT_CAUSE_BUCKET_RISK_HOLD') {
    return { action: 'ACTION_TYPE_ESCALATE', delay: 500 };
  }

  if (
    bucket === 'ROOT_CAUSE_BUCKET_HARD_DECLINE' ||
    bucket === 'ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED' ||
    bucket === 'ROOT_CAUSE_BUCKET_ABANDONMENT' ||
    bucket === 'ROOT_CAUSE_BUCKET_OVERDUE'
  ) {
    if (attempt_count === 0) {
      return { action: nudgeActionFor(bucket), delay: 800 };
    }
    return { action: 'ACTION_TYPE_ESCALATE', delay: 500 };
  }

  // transient bank / insufficient funds
  if (attempt_count >= 3) {
    return { action: 'ACTION_TYPE_ESCALATE', delay: 500 };
  }
  return { action: 'ACTION_TYPE_RETRY', delay: retryDelayMs(bucket, attempt_count) };
}

function isRetry(action: ActionType): boolean {
  return action === 'ACTION_TYPE_RETRY';
}

function isNudge(action: ActionType): boolean {
  return action === 'ACTION_TYPE_NUDGE_METHOD_UPDATE' || action === 'ACTION_TYPE_NUDGE_REMINDER';
}

function rollOutcome(ground_truth: GroundTruth, action: ActionType): Outcome {
  if (action === 'ACTION_TYPE_ESCALATE' || action === 'ACTION_TYPE_NONE') return 'OUTCOME_FAILURE';
  if (ground_truth.recovers_on === 'never') return 'OUTCOME_FAILURE';

  if (isRetry(action) && ground_truth.recovers_on === 'retry') {
    return Math.random() < ground_truth.recovery_probability ? 'OUTCOME_SUCCESS' : 'OUTCOME_FAILURE';
  }

  if (isNudge(action) && ground_truth.recovers_on === 'nudge') {
    return Math.random() < ground_truth.recovery_probability ? 'OUTCOME_SUCCESS' : 'OUTCOME_FAILURE';
  }

  // Wrong action for this bucket
  return Math.random() < 0.05 ? 'OUTCOME_SUCCESS' : 'OUTCOME_FAILURE';
}

function scheduledStateFor(action: ActionType): RecordState {
  if (isRetry(action)) return 'RECORD_STATE_RETRY_SCHEDULED';
  if (isNudge(action)) return 'RECORD_STATE_NUDGE_SCHEDULED';
  if (action === 'ACTION_TYPE_ESCALATE') return 'RECORD_STATE_ESCALATED';
  return 'RECORD_STATE_CLOSED_UNECONOMIC';
}

function actionCostPaise(action: ActionType): number {
  if (action === 'ACTION_TYPE_RETRY') return 20;
  if (action === 'ACTION_TYPE_NUDGE_METHOD_UPDATE') return 35;
  if (action === 'ACTION_TYPE_NUDGE_REMINDER') return 25;
  return 0;
}

function classificationHops(source: Source): ProviderHop[] {
  return source === 'SOURCE_LLM' ? [{ provider: 'groq', result: 'ok' }] : [];
}

function nowISO(): string {
  return new Date().toISOString();
}

export class MockEngine {
  private batches = new Map<string, InternalBatch>();
  private records = new Map<string, InternalRecord>();
  private listeners = new Map<string, Set<Listener>>();
  private timers = new Map<string, ReturnType<typeof setTimeout>>();

  constructor() {
    // Seed with one completed, ground-truth-bearing batch for immediate viewing.
    this.seedCompletedBatch();
  }

  // Appends one audit entry and advances current_state. Non-classification
  // transitions reuse the record's last-known classification `source` since
  // AuditEntry.source is documented as the closed LLM/rules/template
  // vocabulary with no "system"/"executor" member for mechanical steps,
  // see the ambiguity callout in the PR summary.
  private addEntry(
    record: InternalRecord,
    from: RecordState,
    to: RecordState,
    reason: string,
    rationale: string,
    actor: string,
    opts?: { attempt_number?: number; cost_paise?: number; message_text?: string; hops?: ProviderHop[]; source?: Source },
  ) {
    record.entries.push({
      ts: nowISO(),
      from_state: from,
      to_state: to,
      reason,
      rationale,
      source: opts?.source ?? record.classification_source,
      actor,
      attempt_number: opts?.attempt_number ?? 0,
      cost_paise: opts?.cost_paise ?? 0,
      message_text: opts?.message_text ?? '',
      hops: opts?.hops ?? [],
    });
    record.current_state = to;
  }

  private buildRecord(id: string, batch_id: string, created_at: string): InternalRecord {
    const failure_code = pick(FAILURE_CODES);
    const ground_truth = generateGroundTruth(failure_code);
    // A naive policy retries every record 3x and nudges every record once,
    // so it always attempts whichever channel could recover this record.
    const naive_recovered = ground_truth.recovers_on !== 'never' && Math.random() < ground_truth.recovery_probability;
    return {
      id,
      batch_id,
      type: randomType(),
      amount_paise: randomAmountPaise(),
      failure_code,
      current_state: 'RECORD_STATE_NEW',
      bucket: ground_truth.true_bucket,
      attempt_count: 0,
      rationale: '',
      classification_source: 'SOURCE_LLM',
      classification_correct: false,
      entries: [],
      interventions: [],
      ground_truth,
      naive_recovered,
      processed: false,
      created_at,
      due_at: '',
    };
  }

  private seedCompletedBatch() {
    const batch_id = 'demo-' + uuid().slice(0, 8);
    const count = 72;
    const record_ids: string[] = [];
    const created_at = new Date(Date.now() - 1000 * 60 * 15).toISOString();

    for (let i = 0; i < count; i++) {
      const id = uuid();
      record_ids.push(id);
      const record = this.buildRecord(id, batch_id, created_at);

      const classification = classify(record.failure_code, record.ground_truth);
      record.bucket = classification.bucket;
      record.rationale = classification.rationale;
      record.classification_source = classification.source;
      record.classification_correct = classification.correct;

      this.addEntry(
        record,
        'RECORD_STATE_NEW',
        'RECORD_STATE_SCORING',
        'classified',
        classification.rationale,
        'system',
        { hops: classificationHops(classification.source) },
      );

      let recovered = false;
      const maxAttempts = 3;

      while (
        record.attempt_count < maxAttempts &&
        !recovered &&
        record.current_state !== 'RECORD_STATE_ESCALATED' &&
        record.current_state !== 'RECORD_STATE_CLOSED_UNECONOMIC'
      ) {
        const { action } = decideAction(record.bucket, record.attempt_count);

        if (action === 'ACTION_TYPE_ESCALATE') {
          this.addEntry(
            record,
            record.current_state,
            'RECORD_STATE_ESCALATED',
            'escalated',
            'Retry budget exhausted or risk hold detected',
            'decision-engine',
          );
          break;
        }

        const scheduledState = scheduledStateFor(action);
        this.addEntry(record, record.current_state, scheduledState, `${action} scheduled`, '', 'decision-engine');

        record.attempt_count++;
        const outcome = rollOutcome(record.ground_truth, action);
        const cost_paise = actionCostPaise(action);
        const message_text = isNudge(action) ? pick(NUDGE_MESSAGES[record.bucket] ?? ['']) : '';

        record.interventions.push({ action, cost_paise, amount_paise: record.amount_paise, outcome });

        const executingState: RecordState = isRetry(action) ? 'RECORD_STATE_RETRYING' : 'RECORD_STATE_NUDGED';
        this.addEntry(
          record,
          scheduledState,
          executingState,
          `${action} executed`,
          '',
          'executor',
          { attempt_number: record.attempt_count, cost_paise, message_text },
        );

        if (outcome === 'OUTCOME_SUCCESS') {
          this.addEntry(
            record,
            record.current_state,
            'RECORD_STATE_RECOVERED',
            'recovered',
            'Payment recovered successfully',
            'executor',
          );
          recovered = true;
        } else if (record.attempt_count >= maxAttempts || (isNudge(action) && record.attempt_count >= 2)) {
          this.addEntry(
            record,
            record.current_state,
            'RECORD_STATE_ESCALATED',
            'budget exhausted',
            'Maximum attempts reached without recovery',
            'decision-engine',
          );
        } else {
          this.addEntry(
            record,
            record.current_state,
            'RECORD_STATE_SCORING',
            're-scoring',
            'Re-evaluating with updated probability',
            'decision-engine',
          );
        }
      }

      if (!recovered && record.current_state !== 'RECORD_STATE_ESCALATED' && Math.random() < 0.1) {
        this.addEntry(
          record,
          record.current_state,
          'RECORD_STATE_CLOSED_UNECONOMIC',
          'closed uneconomic',
          'Remaining actions have negative expected value',
          'decision-engine',
        );
      }

      record.processed = true;
      this.records.set(id, record);
    }

    const pendingCount = this.seedPendingTimelineRecords(batch_id, created_at, record_ids);

    this.batches.set(batch_id, {
      id: batch_id,
      created_at,
      total_records: count + pendingCount,
      source: 'demo-seed',
      record_ids,
      has_ground_truth: true,
    });
  }

  /**
   * The batch above resolves every record to a terminal state synchronously,
   * which is right for a "completed batch" but leaves nothing pending for
   * TimelineView to plot the moment the dashboard loads. This adds a handful
   * of records deliberately left mid-wait, at each end of the cause-aware
   * scheduling policy described in docs/PHASE5_5_IMPLEMENTATION.md Unit AB:
   * transient-bank retries due almost immediately, insufficient-funds
   * retries clustered around a later simulated salary window, and (by
   * omission) nothing for hard-decline, since a dead card gets a nudge, not
   * a retry. Returns how many records it added, so the caller can keep
   * total_records accurate.
   */
  private seedPendingTimelineRecords(batch_id: string, created_at: string, record_ids: string[]): number {
    const now = Date.now();
    const plan: { failure_code: string; due_at_ms: number }[] = [
      ...Array.from({ length: 7 }, () => ({
        failure_code: pick(['bank_not_available', 'bank_timeout']),
        due_at_ms: now + 1500 + Math.random() * 13000,
      })),
      ...Array.from({ length: 8 }, () => ({
        failure_code: 'insufficient_funds',
        due_at_ms: now + 45000 + Math.random() * 30000,
      })),
    ];

    for (const { failure_code, due_at_ms } of plan) {
      const id = uuid();
      const record = this.buildRecord(id, batch_id, created_at);
      record.failure_code = failure_code;
      record.ground_truth = generateGroundTruth(failure_code);
      // Classifier noise is deliberately skipped here: these records exist
      // to demonstrate the scheduling policy's shape, and a misclassified
      // one landing under the wrong bucket row would blur exactly the
      // pattern this view is for.
      record.bucket = record.ground_truth.true_bucket;
      record.rationale = pick(RATIONALES[record.bucket]);
      record.classification_source = Math.random() < 0.8 ? 'SOURCE_LLM' : 'SOURCE_RULES_FALLBACK';
      record.classification_correct = true;

      this.addEntry(
        record,
        'RECORD_STATE_NEW',
        'RECORD_STATE_SCORING',
        'classified',
        record.rationale,
        'system',
        { hops: classificationHops(record.classification_source) },
      );
      this.addEntry(
        record,
        'RECORD_STATE_SCORING',
        'RECORD_STATE_RETRY_SCHEDULED',
        'ACTION_TYPE_RETRY scheduled',
        '',
        'decision-engine',
      );
      record.due_at = new Date(due_at_ms).toISOString();

      this.records.set(id, record);
      record_ids.push(id);
    }

    return plan.length;
  }

  private emit(batch_id: string, update: BatchUpdate) {
    const set = this.listeners.get(batch_id);
    if (set) {
      set.forEach((l) => l(update));
    }
  }

  private processRecord(record: InternalRecord, batch_id: string) {
    if (record.processed) return;

    const step = () => {
      if (record.processed) return;
      if (
        record.current_state === 'RECORD_STATE_RECOVERED' ||
        record.current_state === 'RECORD_STATE_ESCALATED' ||
        record.current_state === 'RECORD_STATE_CLOSED_UNECONOMIC'
      ) {
        record.processed = true;
        return;
      }

      if (record.current_state === 'RECORD_STATE_NEW') {
        const classification = classify(record.failure_code, record.ground_truth);
        record.bucket = classification.bucket;
        record.rationale = classification.rationale;
        record.classification_source = classification.source;
        record.classification_correct = classification.correct;

        const from = record.current_state;
        this.addEntry(record, from, 'RECORD_STATE_SCORING', 'classified', classification.rationale, 'system', {
          hops: classificationHops(classification.source),
        });
        this.emit(batch_id, { record_id: record.id, from_state: from, to_state: 'RECORD_STATE_SCORING', ts: nowISO(), recovered_delta_paise: 0 });
        this.timers.set(record.id, setTimeout(step, 300 + Math.random() * 400));
        return;
      }

      if (record.current_state === 'RECORD_STATE_SCORING') {
        const { action, delay } = decideAction(record.bucket, record.attempt_count);
        const from = record.current_state;

        if (action === 'ACTION_TYPE_ESCALATE') {
          this.addEntry(record, from, 'RECORD_STATE_ESCALATED', 'escalated', 'Risk hold or budget exhausted', 'decision-engine');
          this.emit(batch_id, { record_id: record.id, from_state: from, to_state: 'RECORD_STATE_ESCALATED', ts: nowISO(), recovered_delta_paise: 0 });
          record.processed = true;
          return;
        }

        const scheduledState = scheduledStateFor(action);
        record.due_at = new Date(Date.now() + delay).toISOString();
        this.addEntry(record, from, scheduledState, `${action} scheduled`, '', 'decision-engine');
        this.emit(batch_id, { record_id: record.id, from_state: from, to_state: scheduledState, ts: nowISO(), recovered_delta_paise: 0 });
        this.timers.set(record.id, setTimeout(() => this.executeAttempt(record, batch_id, action), delay));
        return;
      }
    };

    // Stagger initial processing
    this.timers.set(record.id, setTimeout(step, Math.random() * 2000));
  }

  private executeAttempt(record: InternalRecord, batch_id: string, action: ActionType) {
    if (record.processed) return;

    const scheduledState = record.current_state;
    // The scheduler's wait is over now that the attempt is executing;
    // RETRYING/NUDGED never carry a due_at (see the InternalRecord field
    // comment and docs/API_GATEWAY.md).
    record.due_at = '';
    record.attempt_count++;
    const cost_paise = actionCostPaise(action);
    const message_text = isNudge(action) ? pick(NUDGE_MESSAGES[record.bucket] ?? ['']) : '';
    const executingState: RecordState = isRetry(action) ? 'RECORD_STATE_RETRYING' : 'RECORD_STATE_NUDGED';

    this.addEntry(record, scheduledState, executingState, `${action} executed`, '', 'executor', {
      attempt_number: record.attempt_count,
      cost_paise,
      message_text,
    });
    this.emit(batch_id, { record_id: record.id, from_state: scheduledState, to_state: executingState, ts: nowISO(), recovered_delta_paise: 0 });

    const outcome = rollOutcome(record.ground_truth, action);
    record.interventions.push({ action, cost_paise, amount_paise: record.amount_paise, outcome });

    const settleDelay = isRetry(action) ? 500 + Math.random() * 800 : 800 + Math.random() * 1200;
    this.timers.set(
      record.id,
      setTimeout(() => {
        const from = record.current_state;
        if (outcome === 'OUTCOME_SUCCESS') {
          this.addEntry(record, from, 'RECORD_STATE_RECOVERED', 'recovered', 'Payment recovered successfully', 'executor');
          this.emit(batch_id, { record_id: record.id, from_state: from, to_state: 'RECORD_STATE_RECOVERED', ts: nowISO(), recovered_delta_paise: record.amount_paise });
          record.processed = true;
          return;
        }

        const budgetExhausted = record.attempt_count >= 3 || (isNudge(action) && record.attempt_count >= 2);
        if (budgetExhausted) {
          this.addEntry(record, from, 'RECORD_STATE_ESCALATED', 'budget exhausted', 'Maximum attempts reached without recovery', 'decision-engine');
          this.emit(batch_id, { record_id: record.id, from_state: from, to_state: 'RECORD_STATE_ESCALATED', ts: nowISO(), recovered_delta_paise: 0 });
          record.processed = true;
        } else {
          this.addEntry(record, from, 'RECORD_STATE_SCORING', 're-scoring', 'Re-evaluating with updated probability', 'decision-engine');
          this.emit(batch_id, { record_id: record.id, from_state: from, to_state: 'RECORD_STATE_SCORING', ts: nowISO(), recovered_delta_paise: 0 });
          this.timers.set(record.id, setTimeout(() => this.processRecord(record, batch_id), 400 + Math.random() * 600));
        }
      }, settleDelay),
    );
  }

  async submitBatch(source: string, count: number = 80): Promise<BatchSubmitResponse> {
    const batch_id = uuid();
    const record_ids: string[] = [];
    const created_at = nowISO();

    for (let i = 0; i < count; i++) {
      const id = uuid();
      record_ids.push(id);
      this.records.set(id, this.buildRecord(id, batch_id, created_at));
    }

    this.batches.set(batch_id, {
      id: batch_id,
      created_at,
      total_records: count,
      source,
      record_ids,
      has_ground_truth: false,
    });

    record_ids.forEach((id) => {
      const record = this.records.get(id)!;
      this.processRecord(record, batch_id);
    });

    return { batch_id, accepted_count: count, rejected: {} };
  }

  async getBatchReport(batch_id: string): Promise<BatchReport> {
    const batch = this.batches.get(batch_id);
    if (!batch) throw new Error('Batch not found');

    const records = batch.record_ids.map((id) => this.records.get(id)!).filter(Boolean);

    const total_records = records.length;
    const in_flight_count = records.filter(
      (r) => !['RECORD_STATE_RECOVERED', 'RECORD_STATE_ESCALATED', 'RECORD_STATE_CLOSED_UNECONOMIC'].includes(r.current_state),
    ).length;
    const at_risk_paise = records.reduce((sum, r) => sum + r.amount_paise, 0);
    const recovered_paise = records
      .filter((r) => r.current_state === 'RECORD_STATE_RECOVERED')
      .reduce((sum, r) => sum + r.amount_paise, 0);
    const escalated_count = records.filter((r) => r.current_state === 'RECORD_STATE_ESCALATED').length;
    const closed_uneconomic_records = records.filter((r) => r.current_state === 'RECORD_STATE_CLOSED_UNECONOMIC');
    const closed_uneconomic_count = closed_uneconomic_records.length;
    const closed_uneconomic_paise = closed_uneconomic_records.reduce((sum, r) => sum + r.amount_paise, 0);
    const intervention_spend_paise = records.reduce(
      (sum, r) => sum + r.interventions.reduce((s, iv) => s + iv.cost_paise, 0),
      0,
    );
    const net_recovered_paise = recovered_paise - intervention_spend_paise;
    const cost_per_rupee_recovered = recovered_paise > 0 ? intervention_spend_paise / recovered_paise : 0;
    const recovery_rate = at_risk_paise > 0 ? recovered_paise / at_risk_paise : 0;

    const by_root_cause: Partial<Record<RootCauseBucket, RootCauseBreakdown>> = {};
    for (const bucket of ALL_BUCKETS) {
      const inBucket = records.filter((r) => r.bucket === bucket);
      if (inBucket.length === 0) continue;
      const bucketAtRisk = inBucket.reduce((sum, r) => sum + r.amount_paise, 0);
      const bucketRecovered = inBucket
        .filter((r) => r.current_state === 'RECORD_STATE_RECOVERED')
        .reduce((sum, r) => sum + r.amount_paise, 0);
      by_root_cause[bucket] = {
        record_count: inBucket.length,
        at_risk_paise: bucketAtRisk,
        recovered_paise: bucketRecovered,
        recovery_rate: bucketAtRisk > 0 ? bucketRecovered / bucketAtRisk : 0,
      };
    }

    const by_intervention: Partial<Record<ActionType, InterventionBreakdown>> = {};
    for (const r of records) {
      for (const iv of r.interventions) {
        const existing: InterventionBreakdown = by_intervention[iv.action] ?? {
          attempt_count: 0,
          success_count: 0,
          spend_paise: 0,
          recovered_paise: 0,
          success_rate: 0,
        };
        existing.attempt_count++;
        existing.spend_paise += iv.cost_paise;
        if (iv.outcome === 'OUTCOME_SUCCESS') {
          existing.success_count++;
          existing.recovered_paise += iv.amount_paise;
        }
        existing.success_rate = existing.attempt_count > 0 ? existing.success_count / existing.attempt_count : 0;
        by_intervention[iv.action] = existing;
      }
    }

    let accuracy: AccuracyBlock | undefined;
    if (batch.has_ground_truth) {
      const correct = records.filter((r) => r.classification_correct).length;
      const by_bucket: Partial<Record<RootCauseBucket, number>> = {};
      const confusion: AccuracyBlock['confusion'] = {};

      for (const bucket of ALL_BUCKETS) {
        const trueInBucket = records.filter((r) => r.ground_truth.true_bucket === bucket);
        if (trueInBucket.length === 0) continue;
        const correctInBucket = trueInBucket.filter((r) => r.classification_correct).length;
        by_bucket[bucket] = correctInBucket / trueInBucket.length;
      }

      for (const r of records) {
        const entry: { true_bucket_counts: Partial<Record<RootCauseBucket, number>> } = confusion[r.bucket] ?? {
          true_bucket_counts: {},
        };
        entry.true_bucket_counts[r.ground_truth.true_bucket] = (entry.true_bucket_counts[r.ground_truth.true_bucket] ?? 0) + 1;
        confusion[r.bucket] = entry;
      }

      accuracy = {
        scored_records: total_records,
        overall_accuracy: total_records > 0 ? correct / total_records : 0,
        by_bucket,
        confusion,
      };
    }

    let baseline_comparison: BaselineComparison | undefined;
    if (batch.has_ground_truth) {
      // A fixed naive policy: retry every record up to 3x, nudge every
      // record once, no economics gating. naive_recovered was decided once
      // per record at creation, so this stays stable across repeated polls.
      const naiveRetryCost = actionCostPaise('ACTION_TYPE_RETRY') * 3;
      const naiveNudgeCost = actionCostPaise('ACTION_TYPE_NUDGE_REMINDER');
      const naiveSpendPerRecord = naiveRetryCost + naiveNudgeCost;
      const naiveGrossRecoveredPaise = records
        .filter((r) => r.naive_recovered)
        .reduce((sum, r) => sum + r.amount_paise, 0);
      const naiveSpendPaise = naiveSpendPerRecord * total_records;

      baseline_comparison = {
        policy_name: 'naive_retry3_nudge1',
        gross_recovered_paise: naiveGrossRecoveredPaise,
        intervention_spend_paise: naiveSpendPaise,
        net_recovered_paise: naiveGrossRecoveredPaise - naiveSpendPaise,
        note: 'Evaluated analytically against the same sealed ground truth using a fixed naive policy (retry every record up to 3x, nudge every record once, no economics). Measures this policy against our modelled world, not real money.',
      };
    }

    return {
      batch_id,
      total_records,
      in_flight_count,
      at_risk_paise,
      recovered_paise,
      intervention_spend_paise,
      net_recovered_paise,
      cost_per_rupee_recovered,
      recovery_rate,
      escalated_count,
      closed_uneconomic_count,
      closed_uneconomic_paise,
      processing_failure_count: 0,
      by_root_cause,
      by_intervention,
      accuracy,
      baseline_comparison,
      generated_at: nowISO(),
    };
  }

  async getBatchRecords(batch_id: string): Promise<BatchRecordsResponse> {
    const batch = this.batches.get(batch_id);
    if (!batch) throw new Error('Batch not found');

    const records: RecordSummary[] = batch.record_ids
      .map((id) => this.records.get(id)!)
      .filter(Boolean)
      .map((r) => ({
        record_id: r.id,
        type: r.type,
        amount_paise: r.amount_paise,
        current_state: r.current_state,
        bucket: r.bucket,
        attempt_count: r.attempt_count,
        spend_paise: r.interventions.reduce((sum, iv) => sum + iv.cost_paise, 0),
        due_at: r.due_at,
      }));

    return { records, next_page_token: '', total_count: records.length };
  }

  async getRecordDetail(record_id: string): Promise<RecordAuditResponse> {
    const r = this.records.get(record_id);
    if (!r) throw new Error('Record not found');

    return {
      record: {
        id: r.id,
        batch_id: r.batch_id,
        type: r.type,
        amount_paise: r.amount_paise,
        currency: 'INR',
        failure_code: r.failure_code,
        created_at: r.created_at,
        instrument_ref: `demo_ref_${r.id.slice(0, 8)}`,
      },
      current_state: r.current_state,
      trail_complete: r.processed,
      entries: r.entries,
    };
  }

  async getBatchInvariants(batch_id: string): Promise<InvariantsResponse> {
    const batch = this.batches.get(batch_id);
    if (!batch) throw new Error('Batch not found');

    // The mock engine's state machine only ever writes transitions it
    // itself defines, so it has nothing to violate; always-zero is the
    // honest mock answer, same as the real system in the healthy case.
    return {
      stopping_rule_violations: 0,
      incomplete_audit_trails: 0,
      impossible_transitions: 0,
      records_checked: batch.total_records,
      examples: {},
    };
  }

  async getBatches(): Promise<BatchSummary[]> {
    return Array.from(this.batches.values())
      .map((b) => ({
        batch_id: b.id,
        created_at: b.created_at,
        total_records: b.total_records,
        source: b.source,
      }))
      .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());
  }

  subscribe(batch_id: string, listener: Listener): () => void {
    if (!this.listeners.has(batch_id)) {
      this.listeners.set(batch_id, new Set());
    }
    this.listeners.get(batch_id)!.add(listener);
    return () => {
      this.listeners.get(batch_id)?.delete(listener);
    };
  }

  isBatchComplete(batch_id: string): boolean {
    const batch = this.batches.get(batch_id);
    if (!batch) return false;
    return batch.record_ids.every((id) => {
      const r = this.records.get(id);
      return r?.processed;
    });
  }
}

export const mockEngine = new MockEngine();
