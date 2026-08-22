import type { FailureCode, RecordState, RootCauseBucket } from '@/types';

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

export const STATE_LABELS: Record<RecordState, string> = {
  New: 'New',
  Scoring: 'Scoring',
  RetryScheduled: 'Retry Scheduled',
  Retrying: 'Retrying',
  NudgeScheduled: 'Nudge Scheduled',
  Nudged: 'Nudged',
  Recovered: 'Recovered',
  Escalated: 'Escalated',
  ClosedUneconomic: 'Closed (Uneconomic)',
};

export const STATE_COLORS: Record<RecordState, string> = {
  New: 'bg-slate-100 text-slate-600 border-slate-200',
  Scoring: 'bg-amber-50 text-amber-700 border-amber-200',
  RetryScheduled: 'bg-blue-50 text-blue-700 border-blue-200',
  Retrying: 'bg-blue-100 text-blue-800 border-blue-300',
  NudgeScheduled: 'bg-cyan-50 text-cyan-700 border-cyan-200',
  Nudged: 'bg-cyan-100 text-cyan-800 border-cyan-300',
  Recovered: 'bg-emerald-50 text-emerald-700 border-emerald-200',
  Escalated: 'bg-rose-50 text-rose-700 border-rose-200',
  ClosedUneconomic: 'bg-slate-200 text-slate-500 border-slate-300',
};

export const STATE_DOT_COLORS: Record<RecordState, string> = {
  New: 'bg-slate-400',
  Scoring: 'bg-amber-400',
  RetryScheduled: 'bg-blue-400',
  Retrying: 'bg-blue-500',
  NudgeScheduled: 'bg-cyan-400',
  Nudged: 'bg-cyan-500',
  Recovered: 'bg-emerald-500',
  Escalated: 'bg-rose-500',
  ClosedUneconomic: 'bg-slate-400',
};

export const BUCKET_LABELS: Record<RootCauseBucket, string> = {
  transient: 'Transient',
  hard_decline: 'Hard Decline',
  risk_hold: 'Risk Hold',
};

export const BUCKET_COLORS: Record<RootCauseBucket, string> = {
  transient: '#3b82f6',
  hard_decline: '#f43f5e',
  risk_hold: '#f59e0b',
};

export const FAILURE_CODE_LABELS: Record<FailureCode, string> = {
  insufficient_funds: 'Insufficient Funds',
  bank_timeout: 'Bank Timeout',
  hard_decline: 'Hard Decline',
  risk_hold: 'Risk Hold',
  expired_instrument: 'Expired Instrument',
  blocked_instrument: 'Blocked Instrument',
  rail_congestion: 'Rail Congestion',
};

export function isTerminalState(state: RecordState): boolean {
  return state === 'Recovered' || state === 'Escalated' || state === 'ClosedUneconomic';
}

export function isInFlight(state: RecordState): boolean {
  return !isTerminalState(state);
}
