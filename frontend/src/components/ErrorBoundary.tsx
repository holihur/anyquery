import { Component, type ErrorInfo, type ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { LogoMark } from "@/components/layout/Logo"

export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("AnyQuery UI error", error, info)
  }

  render() {
    if (this.state.error) {
      return (
        <div className="grid min-h-screen place-items-center bg-background px-6">
          <div className="max-w-md text-center">
            <div className="mx-auto mb-4 w-fit"><LogoMark className="size-12" /></div>
            <h1 className="text-xl font-semibold">Something went wrong</h1>
            <p className="mt-2 text-sm text-muted-foreground">The interface hit an unexpected error. Your data and sessions are unaffected.</p>
            <pre className="mt-4 max-h-32 overflow-auto rounded-lg border bg-muted/40 p-3 text-left font-mono text-[10.5px] text-muted-foreground">{this.state.error.message}</pre>
            <Button className="mt-5" onClick={() => window.location.reload()}>Reload AnyQuery</Button>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}
