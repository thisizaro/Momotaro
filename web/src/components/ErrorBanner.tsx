import { AlertCircle, RefreshCw } from 'lucide-react';

interface Props {
  message: string;
  onRetry?: () => void;
  retrying?: boolean;
}

export function ErrorBanner({ message, onRetry, retrying }: Props) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3">
      <AlertCircle className="w-4 h-4 text-rose-500 flex-shrink-0" />
      <p className="text-sm text-rose-700 flex-1">{message}</p>
      {onRetry && (
        <button
          onClick={onRetry}
          disabled={retrying}
          className="flex items-center gap-1.5 text-xs font-medium text-rose-700 hover:text-rose-900 disabled:opacity-50 transition-colors"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${retrying ? 'animate-spin' : ''}`} />
          Retry
        </button>
      )}
    </div>
  );
}
