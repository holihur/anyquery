import { Link } from "react-router-dom"
import { Compass } from "lucide-react"
import { Button } from "@/components/ui/button"

export function NotFoundPage() {
  return (
    <div className="grid min-h-screen place-items-center bg-background px-6">
      <div className="text-center">
        <div className="mx-auto mb-5 grid size-12 place-items-center rounded-2xl border bg-card text-muted-foreground">
          <Compass className="size-5" />
        </div>
        <div className="text-[11px] font-medium tracking-[0.16em] text-muted-foreground uppercase">404</div>
        <h1 className="mt-2 text-2xl font-semibold tracking-tight">This page doesn’t exist</h1>
        <p className="mt-2 text-sm text-muted-foreground">The link may be broken, or the page moved.</p>
        <Button asChild className="mt-6"><Link to="/">Back to AnyQuery</Link></Button>
      </div>
    </div>
  )
}
