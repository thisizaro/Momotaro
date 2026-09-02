import type { ActionType, Outcome, RecordState, RecordType, RootCauseBucket } from '@/types';
import { TERMINAL_RECORD_STATES } from '@/types';

export function formatCurrency(paise: number): string {
  const rupees = paise / 100;
  return new Intl.NumberFormat('en-IN', {
    style: 'currency',
    currency: 'INR',
    maximumFractionDigits: 0,
  }).format(rupees);
}

export function formatCurrencyShort(paise: number): string {
  const rupees = paise / 100;
  if (rupees >= 10000000) return `₹${(rupees / 10000000).toFixed(1)}Cr`;
  if (rupees >= 100000) return `₹${(rupees / 100000).toFixed(1)}L`;
  if (rupees >= 1000) return `₹${(rupees / 1000).toFixed(0)}K`;
  return `₹${rupees.toFixed(0)}`;
}

export function formatPaise(paise: number): string {
  return `₹${(paise / 100).toFixed(2)}`;
}

export function formatPercent(value: number, decimals: number = 1): string {
  return `${(value * 100).toFixed(decimals)}%`;
}

export function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('en-US', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  });
}

export function formatRelativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

/**
 * Renders a positive duration at a resolution appropriate to its size:
 * sub-second precision only matters when the duration itself is short (a
 * demo-scaled wait a few seconds out), so it fades out once the duration is
 * long enough that a reader is counting minutes/hours/days, not seconds.
 * Shared by DueCountdown (a live per-record countdown) and TimelineView (axis
 * tick labels), so both read the same "in 6.4s" / "in 2m 3s" vocabulary.
 */
export function formatDuration(ms: number): string {
  const totalSeconds = Math.max(ms, 0) / 1000;
  if (totalSeconds < 60) return `${totalSeconds.toFixed(1)}s`;
  const totalMinutes = Math.floor(totalSeconds / 60);
  if (totalMinutes < 60) return `${totalMinutes}m ${Math.floor(totalSeconds % 60)}s`;
  const totalHours = Math.floor(totalMinutes / 60);
  if (totalHours < 24) return `${totalHours}h ${totalMinutes % 60}m`;
  const totalDays = Math.floor(totalHours / 24);
  return `${totalDays}d ${totalHours % 24}h`;
}

export const RECORD_TYPE_LABELS: Record<RecordType, string> = {
  RECORD_TYPE_PAYMENT: 'Payment',
  RECORD_TYPE_MANDATE: 'Mandate',
  RECORD_TYPE_CHECKOUT: 'Checkout',
  RECORD_TYPE_INVOICE: 'Invoice',
};

/**
 * Fixed display order for the nine record states, used everywhere a
 * per-state legend or breakdown needs one so a state sits in the same
 * position across StateDistribution, the historical timeline and anywhere
 * else it appears: new/scoring first, the two scheduled/in-flight pairs
 * next, then the three terminal outcomes.
 */
export const STATE_ORDER: RecordState[] = [
  'RECORD_STATE_NEW',
  'RECORD_STATE_SCORING',
  'RECORD_STATE_RETRY_SCHEDULED',
  'RECORD_STATE_RETRYING',
  'RECORD_STATE_NUDGE_SCHEDULED',
  'RECORD_STATE_NUDGED',
  'RECORD_STATE_RECOVERED',
  'RECORD_STATE_ESCALATED',
  'RECORD_STATE_CLOSED_UNECONOMIC',
];

export const STATE_LABELS: Record<RecordState, string> = {
  RECORD_STATE_NEW: 'New',
  RECORD_STATE_SCORING: 'Scoring',
  RECORD_STATE_RETRY_SCHEDULED: 'Retry Scheduled',
  RECORD_STATE_RETRYING: 'Retrying',
  RECORD_STATE_NUDGE_SCHEDULED: 'Nudge Scheduled',
  RECORD_STATE_NUDGED: 'Nudged',
  RECORD_STATE_RECOVERED: 'Recovered',
  RECORD_STATE_ESCALATED: 'Escalated',
  RECORD_STATE_CLOSED_UNECONOMIC: 'Closed (Uneconomic)',
};

export const STATE_COLORS: Record<RecordState, string> = {
  RECORD_STATE_NEW: 'bg-slate-100 text-slate-600 border-slate-200',
  RECORD_STATE_SCORING: 'bg-amber-50 text-amber-700 border-amber-200',
  RECORD_STATE_RETRY_SCHEDULED: 'bg-blue-50 text-blue-700 border-blue-200',
  RECORD_STATE_RETRYING: 'bg-blue-100 text-blue-800 border-blue-300',
  RECORD_STATE_NUDGE_SCHEDULED: 'bg-cyan-50 text-cyan-700 border-cyan-200',
  RECORD_STATE_NUDGED: 'bg-cyan-100 text-cyan-800 border-cyan-300',
  RECORD_STATE_RECOVERED: 'bg-emerald-50 text-emerald-700 border-emerald-200',
  RECORD_STATE_ESCALATED: 'bg-rose-50 text-rose-700 border-rose-200',
  RECORD_STATE_CLOSED_UNECONOMIC: 'bg-slate-200 text-slate-500 border-slate-300',
};

/**
 * Hex fills for SVG/CSS contexts that can't take a Tailwind class (an
 * inline `fill` attribute, a `background-color` set from JS). Same nine
 * states as STATE_DOT_COLORS above, same colors, just as raw hex so
 * StateDistribution's bar segments and the historical timeline's outcome
 * markers read as the same visual language rather than two palettes that
 * happen to be close.
 */
export const STATE_FILL: Record<RecordState, string> = {
  RECORD_STATE_NEW: '#cbd5e1',
  RECORD_STATE_SCORING: '#f59e0b',
  RECORD_STATE_RETRY_SCHEDULED: '#60a5fa',
  RECORD_STATE_RETRYING: '#3b82f6',
  RECORD_STATE_NUDGE_SCHEDULED: '#22d3ee',
  RECORD_STATE_NUDGED: '#06b6d4',
  RECORD_STATE_RECOVERED: '#10b981',
  RECORD_STATE_ESCALATED: '#f43f5e',
  RECORD_STATE_CLOSED_UNECONOMIC: '#94a3b8',
};

export const STATE_DOT_COLORS: Record<RecordState, string> = {
  RECORD_STATE_NEW: 'bg-slate-400',
  RECORD_STATE_SCORING: 'bg-amber-400',
  RECORD_STATE_RETRY_SCHEDULED: 'bg-blue-400',
  RECORD_STATE_RETRYING: 'bg-blue-500',
  RECORD_STATE_NUDGE_SCHEDULED: 'bg-cyan-400',
  RECORD_STATE_NUDGED: 'bg-cyan-500',
  RECORD_STATE_RECOVERED: 'bg-emerald-500',
  RECORD_STATE_ESCALATED: 'bg-rose-500',
  RECORD_STATE_CLOSED_UNECONOMIC: 'bg-slate-400',
};

export const BUCKET_LABELS: Record<RootCauseBucket, string> = {
  ROOT_CAUSE_BUCKET_TRANSIENT_BANK: 'Transient (Bank)',
  ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS: 'Insufficient Funds',
  ROOT_CAUSE_BUCKET_HARD_DECLINE: 'Hard Decline',
  ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED: 'User Action Needed',
  ROOT_CAUSE_BUCKET_RISK_HOLD: 'Risk Hold',
  ROOT_CAUSE_BUCKET_ABANDONMENT: 'Abandonment',
  ROOT_CAUSE_BUCKET_OVERDUE: 'Overdue',
};

export const BUCKET_COLORS: Record<RootCauseBucket, string> = {
  ROOT_CAUSE_BUCKET_TRANSIENT_BANK: '#3b82f6',
  ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS: '#f97316',
  ROOT_CAUSE_BUCKET_HARD_DECLINE: '#f43f5e',
  ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED: '#f59e0b',
  ROOT_CAUSE_BUCKET_RISK_HOLD: '#8b5cf6',
  ROOT_CAUSE_BUCKET_ABANDONMENT: '#94a3b8',
  ROOT_CAUSE_BUCKET_OVERDUE: '#14b8a6',
};

export const ACTION_TYPE_LABELS: Record<ActionType, string> = {
  ACTION_TYPE_RETRY: 'Retry',
  ACTION_TYPE_NUDGE_METHOD_UPDATE: 'Nudge (Update Method)',
  ACTION_TYPE_NUDGE_REMINDER: 'Nudge (Reminder)',
  ACTION_TYPE_ESCALATE: 'Escalate',
  ACTION_TYPE_NONE: 'None',
};

export const OUTCOME_LABELS: Record<Outcome, string> = {
  OUTCOME_SUCCESS: 'success',
  OUTCOME_FAILURE: 'failed',
  OUTCOME_PENDING: 'pending',
};

export const OUTCOME_COLORS: Record<Outcome, string> = {
  OUTCOME_SUCCESS: 'text-emerald-600 bg-emerald-50',
  OUTCOME_FAILURE: 'text-rose-600 bg-rose-50',
  OUTCOME_PENDING: 'text-amber-600 bg-amber-50',
};

/**
 * `failure_code` is an open string set by the upstream rail (Wire
 * conventions 3), not a closed enum. This table is best-effort for known
 * codes; formatFailureCode() below falls back for anything else rather than
 * rendering blank.
 */
const FAILURE_CODE_LABELS: Record<string, string> = {
  bank_not_available: 'Bank Not Available',
  insufficient_funds: 'Insufficient Funds',
  card_expired: 'Card Expired',
  issuer_declined: 'Issuer Declined',
  risk_threshold_breached: 'Risk Threshold Breached',
};

export function formatFailureCode(code: string): string {
  return FAILURE_CODE_LABELS[code] ?? code.replace(/_/g, ' ');
}

export function isTerminalState(state: RecordState): boolean {
  return TERMINAL_RECORD_STATES.includes(state);
}

export function isInFlight(state: RecordState): boolean {
  return !isTerminalState(state);
}
