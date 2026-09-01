// Mirrors ../docs/API_GATEWAY.md exactly. That document is FROZEN: every
// field name, enum spelling and endpoint shape here must match it verbatim.

export type RecordType =
  | 'RECORD_TYPE_PAYMENT'
  | 'RECORD_TYPE_MANDATE'
  | 'RECORD_TYPE_CHECKOUT'
  | 'RECORD_TYPE_INVOICE';

export type RootCauseBucket =
  | 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK'
  | 'ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS'
  | 'ROOT_CAUSE_BUCKET_HARD_DECLINE'
  | 'ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED'
  | 'ROOT_CAUSE_BUCKET_RISK_HOLD'
  | 'ROOT_CAUSE_BUCKET_ABANDONMENT'
  | 'ROOT_CAUSE_BUCKET_OVERDUE';

export type ActionType =
  | 'ACTION_TYPE_RETRY'
  | 'ACTION_TYPE_NUDGE_METHOD_UPDATE'
  | 'ACTION_TYPE_NUDGE_REMINDER'
  | 'ACTION_TYPE_ESCALATE'
  | 'ACTION_TYPE_NONE';

export type RecordState =
  | 'RECORD_STATE_NEW'
  | 'RECORD_STATE_SCORING'
  | 'RECORD_STATE_RETRY_SCHEDULED'
  | 'RECORD_STATE_RETRYING'
  | 'RECORD_STATE_NUDGE_SCHEDULED'
  | 'RECORD_STATE_NUDGED'
  | 'RECORD_STATE_RECOVERED'
  | 'RECORD_STATE_ESCALATED'
  | 'RECORD_STATE_CLOSED_UNECONOMIC';

export const TERMINAL_RECORD_STATES: readonly RecordState[] = [
  'RECORD_STATE_RECOVERED',
  'RECORD_STATE_ESCALATED',
  'RECORD_STATE_CLOSED_UNECONOMIC',
];

export type Outcome = 'OUTCOME_SUCCESS' | 'OUTCOME_FAILURE' | 'OUTCOME_PENDING';

export type Source = 'SOURCE_LLM' | 'SOURCE_RULES_FALLBACK' | 'SOURCE_TEMPLATE_FALLBACK';

/** Closed set of strings (not a proto enum) per Wire conventions / Closed vocabularies. */
export type ProviderHopResult =
  | 'ok'
  | 'error'
  | 'timeout'
  | 'rate_limited'
  | 'schema_invalid'
  | 'circuit_open'
  | 'deadline_exhausted';

/**
 * Open string, not a closed enum (Wire conventions 3): whatever the upstream
 * rail returned. A lookup over this must fall back gracefully, never render
 * blank on a miss.
 */
export type FailureCode = string;

/**
 * The Gateway's own short spelling used only in webhook / batch-submit
 * request bodies, distinct from the proto `RecordType` enum used everywhere
 * else on the wire.
 */
export type SubmitRecordType = 'PAYMENT' | 'MANDATE' | 'CHECKOUT' | 'INVOICE';

export interface ProviderHop {
  provider: string;
  result: ProviderHopResult;
}

export interface RootCauseBreakdown {
  record_count: number;
  at_risk_paise: number;
  recovered_paise: number;
  recovery_rate: number;
}

export interface InterventionBreakdown {
  attempt_count: number;
  success_count: number;
  spend_paise: number;
  recovered_paise: number;
  success_rate: number;
}

export interface AccuracyBlock {
  scored_records: number;
  overall_accuracy: number;
  by_bucket: Partial<Record<RootCauseBucket, number>>;
  confusion: Partial<Record<RootCauseBucket, { true_bucket_counts: Partial<Record<RootCauseBucket, number>> }>>;
}

export interface BaselineComparison {
  policy_name: string;
  gross_recovered_paise: number;
  intervention_spend_paise: number;
  net_recovered_paise: number;
  note: string;
}

export interface BatchReport {
  batch_id: string;
  total_records: number;
  in_flight_count: number;
  at_risk_paise: number;
  recovered_paise: number;
  intervention_spend_paise: number;
  net_recovered_paise: number;
  cost_per_rupee_recovered: number;
  recovery_rate: number;
  escalated_count: number;
  closed_uneconomic_count: number;
  closed_uneconomic_paise: number;
  processing_failure_count: number;
  by_root_cause: Partial<Record<RootCauseBucket, RootCauseBreakdown>>;
  by_intervention: Partial<Record<ActionType, InterventionBreakdown>>;
  /** Absent (not null/zeroed) when the batch has no GROUND_TRUTH. */
  accuracy?: AccuracyBlock;
  /** Absent (not null/zeroed) when the batch has no GROUND_TRUTH. */
  baseline_comparison?: BaselineComparison;
  generated_at: string;
}

export interface RecordSummary {
  record_id: string;
  type: RecordType;
  amount_paise: number;
  current_state: RecordState;
  bucket: RootCauseBucket;
  attempt_count: number;
  spend_paise: number;
  /**
   * RFC3339 when the Decision Engine's scheduler is waiting on this record
   * (RETRY_SCHEDULED or NUDGE_SCHEDULED), empty string otherwise, always
   * present (never omitted, never null; docs/API_GATEWAY.md). A
   * RECORD_STATE_NUDGED record deliberately has none, it waits on the
   * customer, not the scheduler; a terminal record has none because there
   * is nothing left to schedule. Tell those two apart by current_state,
   * not by due_at alone.
   */
  due_at: string;
}

export interface BatchRecordsResponse {
  records: RecordSummary[];
  next_page_token: string;
  total_count: number;
}

export interface AuditEntry {
  ts: string;
  from_state: RecordState;
  to_state: RecordState;
  reason: string;
  rationale: string;
  source: Source;
  actor: string;
  attempt_number: number;
  cost_paise: number;
  message_text: string;
  hops: ProviderHop[];
}

export interface AuditRecordInfo {
  id: string;
  batch_id: string;
  type: RecordType;
  amount_paise: number;
  currency: string;
  failure_code: FailureCode;
  created_at: string;
  instrument_ref: string;
}

export interface RecordAuditResponse {
  record: AuditRecordInfo;
  current_state: RecordState;
  trail_complete: boolean;
  entries: AuditEntry[];
}

export interface BatchUpdate {
  record_id: string;
  from_state: RecordState;
  to_state: RecordState;
  ts: string;
  recovered_delta_paise: number;
}

export interface ApiError {
  error: {
    code: string;
    message: string;
  };
}

export interface BatchSubmitRecord {
  type: SubmitRecordType;
  amount_paise: number;
  currency?: string;
  failure_code: string;
  instrument_ref: string;
}

export interface BatchSubmitRequest {
  source: string;
  records: BatchSubmitRecord[];
}

export interface BatchSubmitResponse {
  batch_id: string;
  accepted_count: number;
  rejected: Record<string, string>;
}

export interface BatchSummary {
  batch_id: string;
  created_at: string;
  total_records: number;
  source: string;
}

export interface ListBatchesResponse {
  batches: BatchSummary[];
}

/** Mirrors audit.v1.VerifyInvariantsResponse. Every count must be zero;
 *  a non-zero count is a bug being surfaced, never a business outcome. */
export interface InvariantsResponse {
  stopping_rule_violations: number;
  incomplete_audit_trails: number;
  impossible_transitions: number;
  records_checked: number;
  examples: Record<string, unknown>;
}

// `/v1/demo/*` (docs/API_GATEWAY.md "Demo controls"). Phase 5.5 Unit W/X.
// Every route here exists only when the Gateway runs with
// DEMO_CONTROLS_ENABLED=true; when it does not, all four 404 identically,
// which is what DemoControlsDisabledError (src/lib/api.ts) detects.

export interface DemoScenario {
  name: string;
  description: string;
}

export interface DemoScenariosResponse {
  scenarios: DemoScenario[];
}

export interface DemoBatchRequest {
  /** One of GET /v1/demo/scenarios' names; empty/omitted defaults to "normal". */
  scenario?: string;
  /** Required, 1 to 1000. */
  count: number;
  /** Optional; 0 or omitted picks one, always echoed back in the response. */
  seed?: number;
}

export interface DemoBatchResponse {
  batch_id: string;
  generated_count: number;
  seed: number;
}

export interface DemoWorldPendingEntry {
  record_id: string;
  attempt_number: number;
  /** The already-rolled answer waiting to be delivered at due_at. */
  outcome: Outcome;
  due_at: string;
}

export interface DemoWorldResponse {
  pending: DemoWorldPendingEntry[];
}

export interface DemoInjectPoisonResponse {
  record_id: string;
  batch_id: string;
}
