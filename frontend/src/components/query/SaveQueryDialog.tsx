import { useEffect, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
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
  const queryClient = useQueryClient()
  const { data: tenantInfo } = useQuery({
    queryKey: ["tenant", tenant.slug],
    queryFn: () => api.tenant(tenant.slug),
    enabled: open,
  })
  const savedCount = tenantInfo?.usage?.savedQueries ?? 0
  const savedLimit = tenantInfo?.limits?.maxSavedQueries ?? 100
  const atLimit = !editing && savedCount >= savedLimit
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
    if (atLimit) {
      setError(`This workspace has reached its limit of ${savedLimit} saved queries. Delete an older query to free a slot.`)
      return
    }
    setBusy(true)
    setError(null)
    try {
      const payload = { name: name.trim(), description: description.trim(), sql, visibility }
      const saved = editing
        ? await api.updateSavedQuery(tenant.slug, editing.id, payload)
        : await api.createSavedQuery(tenant.slug, payload)
      void queryClient.invalidateQueries({ queryKey: ["saved-queries", tenant.slug] })
      void queryClient.invalidateQueries({ queryKey: ["tenant", tenant.slug] })
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
            <div className="flex items-center justify-between rounded-lg bg-muted/35 px-3 py-2 text-[11px]">
              <span className="text-muted-foreground">Workspace library</span>
              <span className={atLimit ? "font-medium text-warning-foreground dark:text-warning" : "font-medium"}>{savedCount} / {savedLimit}</span>
            </div>
            {atLimit && (
              <div className="text-[11px] leading-relaxed text-warning-foreground dark:text-warning">
                The library is full. Delete an older saved query to free a slot.
              </div>
            )}
            {error && <ErrorNotice message={error} />}
          </div>
          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={busy || !name.trim() || atLimit}>{busy ? "Saving…" : editing ? "Update query" : "Save query"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
