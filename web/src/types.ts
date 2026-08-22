export type RecordType = 'payment' | 'mandate';

export type FailureCode =
  | 'insufficient_funds'
  | 'bank_timeout'
  | 'hard_decline'
  | 'risk_hold'
  | 'expired_instrument'
  | 'blocked_instrument'
  | 'rail_congestion';

export type RootCauseBucket = 'transient' | 'hard_decline' | 'risk_hold';

export type InterventionType = 'retry' | 'nudge' | 'escalate' | 'none';

export type RecordState =
  | 'New'
  | 'Scoring'
  | 'RetryScheduled'
  | 'Retrying'
  | 'NudgeScheduled'
  | 'Nudged'
  | 'Recovered'
  | 'Escalated'
  | 'ClosedUneconomic';

export type Outcome = 'success' | 'failed' | 'pending';

export type ClassificationSource = string;

export interface BatchReport {
  batch_id: string;
  total_records: number;
  in_flight_count: number;
  at_risk_amount: number;
  recovered_amount: number;
  recovery_rate: number;
  escalated_count: number;
  by_root_cause_bucket: Record<RootCauseBucket, BucketBreakdown>;
  by_intervention_type: Record<InterventionType, BucketBreakdown>;
  classification_accuracy_vs_ground_truth: number;
}

export interface BucketBreakdown {
  count: number;
  amount: number;
  recovered_amount: number;
}

export interface RecordSummary {
  id: string;
  type: RecordType;
  amount: number;
  current_state: RecordState;
  root_cause_bucket: RootCauseBucket;
}

export interface AuditEntry {
  id: string;
  record_id: string;
  ts: string;
  from_state: RecordState | null;
  to_state: RecordState;
  reason: string;
  rationale: string;
  source: ClassificationSource;
  actor: string;
}

export interface InterventionAttempt {
  id: string;
  record_id: string;
  attempt_number: number;
  action_type: InterventionType;
  executed_at: string;
  outcome: Outcome;
  cost_paise: number;
  ev_score_at_decision: number;
  p_recovery_at_decision: number;
  message_text: string;
  message_source: string;
}

export interface RecordDetail {
  id: string;
  batch_id: string;
  type: RecordType;
  amount: number;
  failure_code: FailureCode;
  current_state: RecordState;
  root_cause_bucket: RootCauseBucket;
  attempt_count: number;
  audit: AuditEntry[];
  interventions: InterventionAttempt[];
  rationale: string;
  classification_source: ClassificationSource;
}

export interface BatchUpdate {
  record_id: string;
  from_state: RecordState;
  to_state: RecordState;
  ts: string;
}

export interface ApiError {
  error: {
    code: string;
    message: string;
  };
}

export interface BatchSubmitResponse {
  batch_id: string;
}

export interface BatchSummary {
  batch_id: string;
  created_at: string;
  total_records: number;
  source: string;
}
