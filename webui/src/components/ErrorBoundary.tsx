import React from 'react'
import { useToast } from './Toasts'

interface State { hasError: boolean; error?: any }

export default class ErrorBoundary extends React.Component<{ children: React.ReactNode }, State> {
  state: State = { hasError: false }

  static getDerivedStateFromError(error: any): State { return { hasError: true, error } }

  componentDidCatch(error: any, info: any) {
    // no-op here (toast via hook not available in class); we show fallback
    // In future: external logging endpoint.
    console.error('ErrorBoundary caught error', error, info)
  }

  reset = () => this.setState({ hasError: false, error: undefined })

  render() {
    if (this.state.hasError) {
      return (
        <div className="p-10 text-center space-y-4">
          <h2 className="text-lg font-semibold">Something went wrong</h2>
          <p className="text-sm text-slate-400 break-words max-w-xl mx-auto">{String(this.state.error?.message || this.state.error || 'Unknown error')}</p>
          <button onClick={this.reset} className="btn text-xs">Retry</button>
        </div>
      )
    }
    return this.props.children
  }
}

// Hook wrapper to emit toast for runtime errors inside components if needed
export function BoundaryWithToast({ children }: { children: React.ReactNode }) {
  const toast = useToast()
  return (
    <ErrorBoundary>
      <RuntimeErrorCatcher onError={(e) => toast.error(e.message || 'Unexpected error')}>
        {children}
      </RuntimeErrorCatcher>
    </ErrorBoundary>
  )
}

class RuntimeErrorCatcher extends React.Component<{ children: React.ReactNode; onError: (e: any) => void }, State> {
  state: State = { hasError: false }
  static getDerivedStateFromError(error: any): State { return { hasError: true, error } }
  componentDidCatch(error: any) { this.props.onError(error) }
  render() { return this.props.children }
}

