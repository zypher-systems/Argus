import { Component, type ReactNode } from "react";
import { Link } from "react-router-dom";

export class PageErrorBoundary extends Component<
  { children: ReactNode; resetKey: string },
  { error: Error | null }
> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidUpdate(prevProps: { resetKey: string }) {
    if (prevProps.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null });
    }
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="py-16 text-center">
        <p className="text-sm text-muted-foreground">this page failed to render</p>
        <Link to="/" className="mt-3 inline-block text-sm text-primary hover:underline">
          back to dashboard
        </Link>
      </div>
    );
  }
}
