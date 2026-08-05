import { Component, type ErrorInfo, type ReactNode } from "react";

type ErrorBoundaryProps = {
  children: ReactNode;
};

type ErrorBoundaryState = {
  error: Error | null;
};

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("JoBot UI error:", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="boot-error">
          <h1>JoBot nao conseguiu abrir a interface</h1>
          <p>{this.state.error.message}</p>
          <button type="button" onClick={() => window.location.reload()}>
            Tentar novamente
          </button>
        </div>
      );
    }

    return this.props.children;
  }
}
