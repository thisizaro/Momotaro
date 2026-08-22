import type {
  AuditEntry,
  BatchReport,
  BatchSummary,
  BatchUpdate,
  BucketBreakdown,
  FailureCode,
  InterventionAttempt,
  InterventionType,
  Outcome,
  RecordDetail,
  RecordState,
  RecordSummary,
  RecordType,
  RootCauseBucket,
} from '@/types';

type Listener = (update: BatchUpdate) => void;

interface GroundTruth {
  true_bucket: RootCauseBucket;
  recovery_probability: number;
  recovers_on: 'retry' | 'nudge' | 'never';
}

interface InternalRecord {
  id: string;
  batch_id: string;
  type: RecordType;
  amount: number;
  failure_code: FailureCode;
  current_state: RecordState;
  root_cause_bucket: RootCauseBucket;
  attempt_count: number;
  rationale: string;
  classification_source: string;
  classification_correct: boolean;
  audit: AuditEntry[];
  interventions: InterventionAttempt[];
  ground_truth: GroundTruth;
  due_at: number | null;
  processed: boolean;
}

interface InternalBatch {
  id: string;
  created_at: string;
  total_records: number;
  source: string;
  record_ids: string[];
}

const FAILURE_CODES: FailureCode[] = [
  'insufficient_funds',
  'bank_timeout',
  'hard_decline',
  'risk_hold',
  'expired_instrument',
  'blocked_instrument',
  'rail_congestion',
];

const FAILURE_TO_BUCKET: Record<FailureCode, RootCauseBucket> = {
  insufficient_funds: 'transient',
  bank_timeout: 'transient',
  rail_congestion: 'transient',
  hard_decline: 'hard_decline',
  expired_instrument: 'hard_decline',
  blocked_instrument: 'hard_decline',
  risk_hold: 'risk_hold',
};

const BUCKET_LABELS: Record<RootCauseBucket, string> = {
  transient: 'Transient',
  hard_decline: 'Hard Decline',
  risk_hold: 'Risk Hold',
};

const RATIONALES: Record<RootCauseBucket, string[]> = {
  transient: [
    'Insufficient funds detected, but the instrument is valid. Scheduling a retry for the next salary-credit window when balance is likely replenished.',
    'Bank timeout occurred during the transaction. This is a transient rail issue — retrying with a short backoff.',
    'Rail congestion on the payment network. The failure is not customer-side; a retry in the next window should succeed.',
  ],
  hard_decline: [
    'The card is expired. No retry can succeed — the customer must update their payment method. Sending a nudge with a method-update link.',
    'Instrument is blocked by the issuing bank. Retrying will only burn an attempt. A nudge prompting method update is the only viable path.',
    'Hard decline from the issuer. The instrument is no longer usable. Nudging the customer to update their payment method.',
  ],
  risk_hold: [
    'Risk hold placed by the fraud engine. Automatic retries must not circumvent a risk decision. Escalating to a human reviewer.',
    'Payment blocked by risk controls. This requires manual review — escalating immediately, no auto-retry attempted.',
  ],
};

const NUDGE_MESSAGES: Record<RootCauseBucket, string[]> = {
  transient: [
    'Namaste! Aapka autopay payment fail ho gaya due to a temporary issue. Hum automatically retry karenge. Koi action zaroori nahi.',
    'Hi! Your autopay had a temporary failure. We will retry automatically. No action needed from your end.',
  ],
  hard_decline: [
    'Namaste! Aapka card expire ho gaya hai. Please apne payment method ko update karein: momotaro.link/update',
    'Hi! Your saved card was declined. Please update your payment method to continue your subscription: momotaro.link/update',
  ],
  risk_hold: [
    'Your payment is under review by our security team. Our team will contact you shortly. No action is needed right now.',
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

function randomAmount(): number {
  const tiers = [299, 499, 699, 999, 1499, 1999, 2499, 4999, 9999];
  return pick(tiers) * 100;
}

function randomType(): RecordType {
  return Math.random() > 0.4 ? 'mandate' : 'payment';
}

function classify(
  failure_code: FailureCode,
  ground_truth: GroundTruth,
): { bucket: RootCauseBucket; rationale: string; source: string; correct: boolean } {
  // 85% chance the classifier gets it right
  const correct = Math.random() < 0.85;
  const true_bucket = ground_truth.true_bucket;
  const bucket = correct ? true_bucket : pick(['transient', 'hard_decline', 'risk_hold'] as RootCauseBucket[]);

  // Source: 70% LLM, 15% secondary LLM, 15% rules fallback
  const sourceRoll = Math.random();
  let source: string;
  if (sourceRoll < 0.7) {
    source = 'llm:claude';
  } else if (sourceRoll < 0.85) {
    source = 'llm:openai';
  } else {
    source = 'rules_fallback';
  }

  return {
    bucket,
    rationale: pick(RATIONALES[true_bucket]),
    source,
    correct: bucket === true_bucket,
  };
}

function generateGroundTruth(failure_code: FailureCode): GroundTruth {
  const true_bucket = FAILURE_TO_BUCKET[failure_code];

  if (true_bucket === 'risk_hold') {
    return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
  }

  if (true_bucket === 'transient') {
    // 80% recoverable on retry
    if (Math.random() < 0.8) {
      return { true_bucket, recovery_probability: 0.75 + Math.random() * 0.15, recovers_on: 'retry' };
    }
    return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
  }

  // hard_decline: 15% recoverable on nudge (method update)
  if (Math.random() < 0.15) {
    return { true_bucket, recovery_probability: 0.1 + Math.random() * 0.1, recovers_on: 'nudge' };
  }
  return { true_bucket, recovery_probability: 0, recovers_on: 'never' };
}

function decideAction(
  bucket: RootCauseBucket,
  attempt_count: number,
  ground_truth: GroundTruth,
): { action: InterventionType; delay: number } {
  if (bucket === 'risk_hold') {
    return { action: 'escalate', delay: 500 };
  }

  if (bucket === 'hard_decline') {
    if (attempt_count === 0) {
      return { action: 'nudge', delay: 800 };
    }
    // After one nudge attempt, escalate
    return { action: 'escalate', delay: 500 };
  }

  // transient
  if (attempt_count >= 3) {
    return { action: 'escalate', delay: 500 };
  }
  return { action: 'retry', delay: 600 + attempt_count * 400 };
}

function rollOutcome(ground_truth: GroundTruth, action: InterventionType): Outcome {
  if (action === 'escalate') return 'failed';
  if (action === 'none') return 'failed';

  if (ground_truth.recovers_on === 'never') return 'failed';

  if (action === 'retry' && ground_truth.recovers_on === 'retry') {
    return Math.random() < ground_truth.recovery_probability ? 'success' : 'failed';
  }

  if (action === 'nudge' && ground_truth.recovers_on === 'nudge') {
    return Math.random() < ground_truth.recovery_probability ? 'success' : 'failed';
  }

  // Wrong action for this bucket
  return Math.random() < 0.05 ? 'success' : 'failed';
}

function actionToState(action: InterventionType): RecordState {
  if (action === 'retry') return 'RetryScheduled';
  if (action === 'nudge') return 'NudgeScheduled';
  if (action === 'escalate') return 'Escalated';
  return 'ClosedUneconomic';
}

function actionCostPaise(action: InterventionType): number {
  if (action === 'retry') return 20;
  if (action === 'nudge') return 35;
  return 0;
}

function evScore(action: InterventionType, attempt: number): number {
  const cost = actionCostPaise(action) / 100;
  const baseP = action === 'retry' ? 0.7 - attempt * 0.15 : action === 'nudge' ? 0.15 : 0;
  return Math.max(0, baseP - cost / 100);
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
    // Seed with one completed batch for immediate viewing
    this.seedCompletedBatch();
  }

  private seedCompletedBatch() {
    const batch_id = 'demo-' + uuid().slice(0, 8);
    const count = 72;
    const record_ids: string[] = [];
    const created_at = new Date(Date.now() - 1000 * 60 * 15).toISOString();

    for (let i = 0; i < count; i++) {
      const id = uuid();
      record_ids.push(id);
      const failure_code = pick(FAILURE_CODES);
      const ground_truth = generateGroundTruth(failure_code);
      const classification = classify(failure_code, ground_truth);
      const type = randomType();
      const amount = randomAmount();

      // Simulate a fully processed record
      let current_state: RecordState = 'New';
      const audit: AuditEntry[] = [];
      const interventions: InterventionAttempt[] = [];
      let attempt_count = 0;
      let ts = Date.now() - 1000 * 60 * 14;

      const addAudit = (
        from: RecordState | null,
        to: RecordState,
        reason: string,
        rationale: string,
        source: string,
      ) => {
        audit.push({
          id: uuid(),
          record_id: id,
          ts: new Date(ts).toISOString(),
          from_state: from,
          to_state: to,
          reason,
          rationale,
          source,
          actor: 'decision-engine',
        });
        current_state = to;
        ts += Math.floor(Math.random() * 3000) + 1000;
      };

      addAudit(null, 'New', 'Record ingested from batch', '', 'ingestion');
      addAudit('New', 'Scoring', 'Classification complete', classification.rationale, classification.source);

      // Simulate the recovery path
      let recovered = false;
      const maxAttempts = 3;

      while (attempt_count < maxAttempts && !recovered && current_state as RecordState !== 'Escalated' && current_state as RecordState !== 'ClosedUneconomic') {
        const { action } = decideAction(classification.bucket, attempt_count, ground_truth);

        if (action === 'escalate') {
          addAudit(current_state, 'Escalated', 'Escalated to human review', 'Retry budget exhausted or risk hold detected', 'decision-engine');
          break;
        }

        const scheduledState = actionToState(action);
        addAudit(current_state, scheduledState, `${action} scheduled`, '', 'decision-engine');

        attempt_count++;
        const outcome = rollOutcome(ground_truth, action);
        const cost_paise = actionCostPaise(action);
        const ev = evScore(action, attempt_count);
        const p_recovery = action === 'retry' ? 0.7 - (attempt_count - 1) * 0.15 : 0.15;

        const intervention: InterventionAttempt = {
          id: uuid(),
          record_id: id,
          attempt_number: attempt_count,
          action_type: action,
          executed_at: new Date(ts).toISOString(),
          outcome,
          cost_paise,
          ev_score_at_decision: ev,
          p_recovery_at_decision: p_recovery,
          message_text: action === 'nudge' ? pick(NUDGE_MESSAGES[classification.bucket]) : '',
          message_source: action === 'nudge' ? (classification.source === 'rules_fallback' ? 'template' : 'llm:claude') : '',
        };
        interventions.push(intervention);

        if (action === 'retry') {
          addAudit(scheduledState, 'Retrying', `Retry attempt ${attempt_count} executing`, '', 'executor');
        } else if (action === 'nudge') {
          addAudit(scheduledState, 'Nudged', `Nudge attempt ${attempt_count} sent`, '', 'executor');
        }

        if (outcome === 'success') {
          addAudit(current_state, 'Recovered', `${action} succeeded`, 'Payment recovered successfully', 'executor');
          recovered = true;
        } else {
          if (attempt_count >= maxAttempts) {
            addAudit(current_state, 'Escalated', 'Retry budget exhausted', 'Maximum attempts reached without recovery', 'decision-engine');
          } else if (action === 'nudge' && attempt_count >= 2) {
            addAudit(current_state, 'Escalated', 'Contact cap reached', 'Maximum nudge attempts reached', 'decision-engine');
          } else {
            addAudit(current_state, 'Scoring', `${action} failed, re-scoring`, 'Re-evaluating with updated probability', 'decision-engine');
          }
        }
      }

      if (!recovered && current_state as RecordState !== 'Escalated') {
        // Check if closed uneconomic
        if (Math.random() < 0.1) {
          addAudit(current_state, 'ClosedUneconomic', 'No positive EV action available', 'Remaining actions have negative expected value', 'decision-engine');
        }
      }

      this.records.set(id, {
        id,
        batch_id,
        type,
        amount,
        failure_code,
        current_state,
        root_cause_bucket: classification.bucket,
        attempt_count,
        rationale: classification.rationale,
        classification_source: classification.source,
        classification_correct: classification.correct,
        audit,
        interventions,
        ground_truth,
        due_at: null,
        processed: true,
      });
    }

    this.batches.set(batch_id, {
      id: batch_id,
      created_at,
      total_records: count,
      source: 'demo-seed',
      record_ids,
    });
  }

  private emit(batch_id: string, update: BatchUpdate) {
    const set = this.listeners.get(batch_id);
    if (set) {
      set.forEach((l) => l(update));
    }
  }

  private addAudit(
    record: InternalRecord,
    from: RecordState | null,
    to: RecordState,
    reason: string,
    rationale: string,
    source: string,
  ) {
    record.audit.push({
      id: uuid(),
      record_id: record.id,
      ts: nowISO(),
      from_state: from,
      to_state: to,
      reason,
      rationale,
      source,
      actor: 'decision-engine',
    });
    record.current_state = to;
  }

  private processRecord(record: InternalRecord, batch_id: string) {
    if (record.processed) return;

    const step = () => {
      if (record.processed) return;
      if (record.current_state === 'Recovered' || record.current_state === 'Escalated' || record.current_state === 'ClosedUneconomic') {
        record.processed = true;
        return;
      }

      if (record.current_state === 'New') {
        // Classify
        const classification = classify(record.failure_code, record.ground_truth);
        record.root_cause_bucket = classification.bucket;
        record.rationale = classification.rationale;
        record.classification_source = classification.source;
        record.classification_correct = classification.correct;

        this.addAudit(record, 'New', 'Scoring', 'Classification complete', classification.rationale, classification.source);
        this.emit(batch_id, { record_id: record.id, from_state: 'New', to_state: 'Scoring', ts: nowISO() });
        this.timers.set(record.id, setTimeout(step, 300 + Math.random() * 400));
        return;
      }

      if (record.current_state === 'Scoring') {
        const { action, delay } = decideAction(record.root_cause_bucket, record.attempt_count, record.ground_truth);

        if (action === 'escalate') {
          this.addAudit(record, 'Scoring', 'Escalated', 'Escalated to human review', 'Risk hold or budget exhausted', 'decision-engine');
          this.emit(batch_id, { record_id: record.id, from_state: 'Scoring', to_state: 'Escalated', ts: nowISO() });
          record.processed = true;
          return;
        }

        if (action === 'none' || evScore(action, record.attempt_count) <= 0) {
          this.addAudit(record, 'Scoring', 'ClosedUneconomic', 'No positive EV action available', 'Remaining actions have negative expected value', 'decision-engine');
          this.emit(batch_id, { record_id: record.id, from_state: 'Scoring', to_state: 'ClosedUneconomic', ts: nowISO() });
          record.processed = true;
          return;
        }

        const scheduledState = actionToState(action);
        this.addAudit(record, 'Scoring', scheduledState, `${action} scheduled`, '', 'decision-engine');
        this.emit(batch_id, { record_id: record.id, from_state: 'Scoring', to_state: scheduledState, ts: nowISO() });
        this.timers.set(record.id, setTimeout(step, delay));
        return;
      }

      if (record.current_state === 'RetryScheduled') {
        record.attempt_count++;
        this.addAudit(record, 'RetryScheduled', 'Retrying', `Retry attempt ${record.attempt_count} executing`, '', 'executor');
        this.emit(batch_id, { record_id: record.id, from_state: 'RetryScheduled', to_state: 'Retrying', ts: nowISO() });

        const outcome = rollOutcome(record.ground_truth, 'retry');
        const cost_paise = actionCostPaise('retry');
        const ev = evScore('retry', record.attempt_count);
        const p_recovery = 0.7 - (record.attempt_count - 1) * 0.15;

        record.interventions.push({
          id: uuid(),
          record_id: record.id,
          attempt_number: record.attempt_count,
          action_type: 'retry',
          executed_at: nowISO(),
          outcome,
          cost_paise,
          ev_score_at_decision: ev,
          p_recovery_at_decision: p_recovery,
          message_text: '',
          message_source: '',
        });

        this.timers.set(record.id, setTimeout(() => {
          if (outcome === 'success') {
            this.addAudit(record, 'Retrying', 'Recovered', 'Retry succeeded', 'Payment recovered successfully', 'executor');
            this.emit(batch_id, { record_id: record.id, from_state: 'Retrying', to_state: 'Recovered', ts: nowISO() });
            record.processed = true;
          } else {
            if (record.attempt_count >= 3) {
              this.addAudit(record, 'Retrying', 'Escalated', 'Retry budget exhausted', 'Maximum attempts reached without recovery', 'decision-engine');
              this.emit(batch_id, { record_id: record.id, from_state: 'Retrying', to_state: 'Escalated', ts: nowISO() });
              record.processed = true;
            } else {
              this.addAudit(record, 'Retrying', 'Scoring', 'Retry failed, re-scoring', 'Re-evaluating with updated probability', 'decision-engine');
              this.emit(batch_id, { record_id: record.id, from_state: 'Retrying', to_state: 'Scoring', ts: nowISO() });
              this.timers.set(record.id, setTimeout(step, 400 + Math.random() * 600));
            }
          }
        }, 500 + Math.random() * 800));
        return;
      }

      if (record.current_state === 'NudgeScheduled') {
        record.attempt_count++;
        this.addAudit(record, 'NudgeScheduled', 'Nudged', `Nudge attempt ${record.attempt_count} sent`, '', 'executor');
        this.emit(batch_id, { record_id: record.id, from_state: 'NudgeScheduled', to_state: 'Nudged', ts: nowISO() });

        const outcome = rollOutcome(record.ground_truth, 'nudge');
        const cost_paise = actionCostPaise('nudge');
        const ev = evScore('nudge', record.attempt_count);
        const p_recovery = 0.15;
        const msgSource = record.classification_source === 'rules_fallback' ? 'template' : 'llm:claude';

        record.interventions.push({
          id: uuid(),
          record_id: record.id,
          attempt_number: record.attempt_count,
          action_type: 'nudge',
          executed_at: nowISO(),
          outcome,
          cost_paise,
          ev_score_at_decision: ev,
          p_recovery_at_decision: p_recovery,
          message_text: pick(NUDGE_MESSAGES[record.root_cause_bucket]),
          message_source: msgSource,
        });

        this.timers.set(record.id, setTimeout(() => {
          if (outcome === 'success') {
            this.addAudit(record, 'Nudged', 'Recovered', 'Nudge succeeded', 'Customer completed payment after nudge', 'executor');
            this.emit(batch_id, { record_id: record.id, from_state: 'Nudged', to_state: 'Recovered', ts: nowISO() });
            record.processed = true;
          } else {
            if (record.attempt_count >= 2) {
              this.addAudit(record, 'Nudged', 'Escalated', 'Contact cap reached', 'Maximum nudge attempts reached', 'decision-engine');
              this.emit(batch_id, { record_id: record.id, from_state: 'Nudged', to_state: 'Escalated', ts: nowISO() });
              record.processed = true;
            } else {
              this.addAudit(record, 'Nudged', 'Scoring', 'Nudge failed, re-scoring', 'Re-evaluating with updated probability', 'decision-engine');
              this.emit(batch_id, { record_id: record.id, from_state: 'Nudged', to_state: 'Scoring', ts: nowISO() });
              this.timers.set(record.id, setTimeout(step, 400 + Math.random() * 600));
            }
          }
        }, 800 + Math.random() * 1200));
        return;
      }
    };

    // Stagger initial processing
    this.timers.set(record.id, setTimeout(step, Math.random() * 2000));
  }

  async submitBatch(count: number = 80): Promise<{ batch_id: string }> {
    const batch_id = uuid();
    const record_ids: string[] = [];
    const created_at = nowISO();

    for (let i = 0; i < count; i++) {
      const id = uuid();
      record_ids.push(id);
      const failure_code = pick(FAILURE_CODES);
      const ground_truth = generateGroundTruth(failure_code);

      this.records.set(id, {
        id,
        batch_id,
        type: randomType(),
        amount: randomAmount(),
        failure_code,
        current_state: 'New',
        root_cause_bucket: FAILURE_TO_BUCKET[failure_code],
        attempt_count: 0,
        rationale: '',
        classification_source: '',
        classification_correct: false,
        audit: [],
        interventions: [],
        ground_truth,
        due_at: null,
        processed: false,
      });
    }

    this.batches.set(batch_id, {
      id: batch_id,
      created_at,
      total_records: count,
      source: 'batch-submit',
      record_ids,
    });

    // Start processing each record
    record_ids.forEach((id) => {
      const record = this.records.get(id)!;
      this.processRecord(record, batch_id);
    });

    return { batch_id };
  }

  async getBatchReport(batch_id: string): Promise<BatchReport> {
    const batch = this.batches.get(batch_id);
    if (!batch) throw new Error('Batch not found');

    const records = batch.record_ids.map((id) => this.records.get(id)!).filter(Boolean);

    const total_records = records.length;
    const in_flight_count = records.filter(
      (r) => !['Recovered', 'Escalated', 'ClosedUneconomic'].includes(r.current_state),
    ).length;
    const at_risk_amount = records.reduce((sum, r) => sum + r.amount, 0);
    const recovered_amount = records
      .filter((r) => r.current_state === 'Recovered')
      .reduce((sum, r) => sum + r.amount, 0);
    const escalated_count = records.filter((r) => r.current_state === 'Escalated').length;
    const recovery_rate = total_records > 0 ? records.filter((r) => r.current_state === 'Recovered').length / total_records : 0;

    const by_root_cause_bucket: Record<RootCauseBucket, BucketBreakdown> = {
      transient: { count: 0, amount: 0, recovered_amount: 0 },
      hard_decline: { count: 0, amount: 0, recovered_amount: 0 },
      risk_hold: { count: 0, amount: 0, recovered_amount: 0 },
    };

    const by_intervention_type: Record<InterventionType, BucketBreakdown> = {
      retry: { count: 0, amount: 0, recovered_amount: 0 },
      nudge: { count: 0, amount: 0, recovered_amount: 0 },
      escalate: { count: 0, amount: 0, recovered_amount: 0 },
      none: { count: 0, amount: 0, recovered_amount: 0 },
    };

    for (const r of records) {
      const bucket = by_root_cause_bucket[r.root_cause_bucket];
      bucket.count++;
      bucket.amount += r.amount;
      if (r.current_state === 'Recovered') bucket.recovered_amount += r.amount;

      for (const iv of r.interventions) {
        const ivb = by_intervention_type[iv.action_type];
        ivb.count++;
        ivb.amount += iv.cost_paise;
        if (r.current_state === 'Recovered' && iv.cost_paise > 0) ivb.recovered_amount += r.amount / Math.max(1, r.interventions.length);
      }
    }

    const correct = records.filter((r) => r.classification_correct).length;
    const classified = records.filter((r) => r.classification_source !== '').length;
    const classification_accuracy_vs_ground_truth = classified > 0 ? correct / classified : 0;

    return {
      batch_id,
      total_records,
      in_flight_count,
      at_risk_amount,
      recovered_amount,
      recovery_rate,
      escalated_count,
      by_root_cause_bucket,
      by_intervention_type,
      classification_accuracy_vs_ground_truth,
    };
  }

  async getBatchRecords(batch_id: string): Promise<RecordSummary[]> {
    const batch = this.batches.get(batch_id);
    if (!batch) throw new Error('Batch not found');

    return batch.record_ids
      .map((id) => this.records.get(id)!)
      .filter(Boolean)
      .map((r) => ({
        id: r.id,
        type: r.type,
        amount: r.amount,
        current_state: r.current_state,
        root_cause_bucket: r.root_cause_bucket,
      }));
  }

  async getRecordDetail(record_id: string): Promise<RecordDetail> {
    const r = this.records.get(record_id);
    if (!r) throw new Error('Record not found');

    return {
      id: r.id,
      batch_id: r.batch_id,
      type: r.type,
      amount: r.amount,
      failure_code: r.failure_code,
      current_state: r.current_state,
      root_cause_bucket: r.root_cause_bucket,
      attempt_count: r.attempt_count,
      audit: r.audit,
      interventions: r.interventions,
      rationale: r.rationale,
      classification_source: r.classification_source,
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
