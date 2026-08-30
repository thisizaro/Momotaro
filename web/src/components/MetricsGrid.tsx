import type { LucideIcon } from 'lucide-react';
import { TrendingUp, AlertTriangle, Clock, CheckCircle2 } from 'lucide-react';
import { formatCurrencyShort, formatPercent } from '@/lib/format';
import type { BatchReport } from '@/types';

interface Props {
  report: BatchReport;
}

interface MetricCardProps {
  icon: LucideIcon;
  label: string;
  value: string;
  sublabel?: string;
  accent: string;
  iconBg: string;
}

function MetricCard({ icon: Icon, label, value, sublabel, accent, iconBg }: MetricCardProps) {
  return (
    <div className="card p-5">
      <div className="flex items-start justify-between">
        <div>
          <p className="text-xs font-medium text-slate-400 uppercase tracking-wide">{label}</p>
          <p className={`text-2xl font-bold mt-2 ${accent}`}>{value}</p>
          {sublabel && <p className="text-xs text-slate-400 mt-1">{sublabel}</p>}
        </div>
        <div className={`w-10 h-10 rounded-lg flex items-center justify-center ${iconBg}`}>
          <Icon className="w-5 h-5" />
        </div>
      </div>
    </div>
  );
}

export function MetricsGrid({ report }: Props) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
      <MetricCard
        icon={TrendingUp}
        label="Recovery Rate"
        value={formatPercent(report.recovery_rate)}
        sublabel={`${report.total_records} records in batch`}
        accent="text-emerald-600"
        iconBg="bg-emerald-50 text-emerald-600"
      />
      <MetricCard
        icon={CheckCircle2}
        label="Recovered"
        value={formatCurrencyShort(report.recovered_paise)}
        sublabel={`of ${formatCurrencyShort(report.at_risk_paise)} at risk`}
        accent="text-slate-900"
        iconBg="bg-blue-50 text-blue-600"
      />
      <MetricCard
        icon={Clock}
        label="In Flight"
        value={String(report.in_flight_count)}
        sublabel={`${report.total_records - report.in_flight_count} settled`}
        accent="text-slate-900"
        iconBg="bg-amber-50 text-amber-600"
      />
      <MetricCard
        icon={AlertTriangle}
        label="Escalated"
        value={String(report.escalated_count)}
        sublabel="need human review"
        accent="text-rose-600"
        iconBg="bg-rose-50 text-rose-600"
      />
    </div>
  );
}
