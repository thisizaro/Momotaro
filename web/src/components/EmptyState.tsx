import type { LucideIcon } from 'lucide-react';

interface Action {
  label: string;
  onClick: () => void;
  icon?: LucideIcon;
}

interface Props {
  icon: LucideIcon;
  title: string;
  description?: string;
  action?: Action;
  /** `hero` replaces a whole section (the dashboard with no batch picked).
   *  `inline` sits inside an existing card that already has its own
   *  heading, so it stays quiet: no border of its own, no button. */
  size?: 'hero' | 'inline';
}

/**
 * A deliberate "there is nothing here yet" state, distinct from a loading
 * skeleton. Used wherever the old code let `animate-pulse` stand in for
 * "nothing will ever load" forever (docs/DEMO_READINESS.md Unit AF): the
 * main dashboard with no batch selected, the live event stream before
 * anything has streamed, and the World Simulator's pending queue.
 */
export function EmptyState({ icon: Icon, title, description, action, size = 'inline' }: Props) {
  if (size === 'hero') {
    return (
      <div className="card flex flex-col items-center text-center px-6 py-16">
        <div className="w-11 h-11 rounded-xl bg-slate-100 flex items-center justify-center mb-4">
          <Icon className="w-5 h-5 text-slate-400" />
        </div>
        <h3 className="text-base font-semibold text-slate-800">{title}</h3>
        {description && <p className="text-sm text-slate-400 mt-2 max-w-sm leading-relaxed">{description}</p>}
        {action && (
          <button onClick={action.onClick} className="btn-primary mt-5">
            {action.icon && <action.icon className="w-4 h-4" />}
            {action.label}
          </button>
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-col items-center justify-center text-center gap-1.5 py-8">
      <Icon className="w-5 h-5 text-slate-300" />
      <p className="text-sm text-slate-400">{title}</p>
      {description && <p className="text-xs text-slate-300 max-w-xs leading-relaxed">{description}</p>}
    </div>
  );
}
