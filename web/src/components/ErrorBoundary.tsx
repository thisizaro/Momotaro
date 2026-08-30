import { Component, type ErrorInfo, type ReactNode } from 'react';
import { AlertTriangle } from 'lucide-react';

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * Catches render-time crashes anywhere in the tree below it. A try/catch in
 * an event handler or effect cannot stop a throw during render from
 * unmounting the whole app, this is the only mechanism that can.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Unhandled error in dashboard render tree:', error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="min-h-screen bg-slate-50 flex items-center justify-center p-6">
          <div className="card max-w-md w-full p-6 text-center space-y-4">
            <div className="w-12 h-12 rounded-full bg-rose-50 flex items-center justify-center mx-auto">
              <AlertTriangle className="w-6 h-6 text-rose-500" />
            </div>
            <div>
              <h2 className="text-base font-bold text-slate-900">Something went wrong</h2>
              <p className="text-sm text-slate-500 mt-1">
                The dashboard hit an unexpected error and could not continue rendering.
              </p>
            </div>
            <button onClick={() => window.location.reload()} className="btn-primary mx-auto">
              Reload dashboard
            </button>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}
