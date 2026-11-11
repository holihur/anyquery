import { useEffect, useState } from "react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { useTenant } from "@/providers/tenant"
import type { SavedQuery } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { ErrorNotice } from "@/components/shared"

export function SaveQueryDialog({
  open,
  onOpenChange,
  sql,
  editing,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  sql: string
  editing?: SavedQuery | null
  onSaved: (query: SavedQuery) => void
}) {
  const { tenant } = useTenant()
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [visibility, setVisibility] = useState<"team" | "private">("team")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    setName(editing?.name ?? "")
    setDescription(editing?.description ?? "")
    setVisibility(editing?.visibility ?? "team")
    setError(null)
  }, [open, editing])

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setBusy(true)
    setError(null)
    try {
      const payload = { name: name.trim(), description: description.trim(), sql, visibility }
      const saved = editing
        ? await api.updateSavedQuery(tenant.slug, editing.id, payload)
        : await api.createSavedQuery(tenant.slug, payload)
      toast.success(editing ? "Query updated" : "Query saved to the workspace")
      onSaved(saved)
      onOpenChange(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save query")
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{editing ? "Update saved query" : "Save query"}</DialogTitle>
            <DialogDescription>
              {editing ? "Changes are shared with teammates immediately." : "Give this query a name so your team can find and reuse it."}
            </DialogDescription>
          </DialogHeader>
          <div className="mt-5 space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="query-name">Name</Label>
              <Input id="query-name" autoFocus placeholder="e.g. Revenue by category" value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="query-description">Description <span className="font-normal text-muted-foreground">(optional)</span></Label>
              <Textarea id="query-description" placeholder="What does this query answer?" className="min-h-16" value={description} onChange={(e) => setDescription(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label>Visibility</Label>
              <Select value={visibility} onValueChange={(v) => setVisibility(v as "team" | "private")}>
                <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="team">Team — visible to all workspace members</SelectItem>
                  <SelectItem value="private">Private — only you and admins</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {error && <ErrorNotice message={error} />}
          </div>
          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={busy || !name.trim()}>{busy ? "Saving…" : editing ? "Update query" : "Save query"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
