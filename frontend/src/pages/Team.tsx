import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Check, Copy, Loader2, MailPlus, MoreHorizontal, ShieldCheck, UserMinus, UserPlus, Users, X } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { useTenant } from "@/providers/tenant"
import { formatDateTime, initials, relativeTime } from "@/lib/utils"
import type { Invite, Member, Role } from "@/lib/types"
import { Page, PageBody, PageHeader, EmptyState, RoleBadge, CopyButton } from "@/components/shared"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

export function TeamPage() {
  const { tenant, role } = useTenant()
  const { me, refresh } = useAuth()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const canAdmin = role === "owner" || role === "admin"
  const [inviteOpen, setInviteOpen] = useState(false)
  const [removing, setRemoving] = useState<Member | null>(null)

  const { data, isLoading } = useQuery({ queryKey: ["members", tenant.slug], queryFn: () => api.members(tenant.slug) })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["members", tenant.slug] })
    void queryClient.invalidateQueries({ queryKey: ["overview", tenant.slug] })
  }

  const roleMutation = useMutation({
    mutationFn: ({ member, role }: { member: Member; role: Role }) => api.updateMemberRole(tenant.slug, member.user.id, role),
    onSuccess: () => {
      toast.success("Role updated")
      invalidate()
      void refresh()
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Could not update role"),
  })

  const removeMutation = useMutation({
    mutationFn: (member: Member) => api.removeMember(tenant.slug, member.user.id),
    onSuccess: (_data, member) => {
      toast.success(`${member.user.name} removed from the workspace`)
      setRemoving(null)
      invalidate()
      if (member.user.id === me?.user.id) {
        void refresh().then(() => navigate("/"))
      }
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Could not remove member"),
  })

  const revokeMutation = useMutation({
    mutationFn: (invite: Invite) => api.revokeInvite(tenant.slug, invite.id),
    onSuccess: () => {
      toast.success("Invitation revoked")
      invalidate()
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Could not revoke invite"),
  })

  const members = data?.members ?? []
  const invites = data?.invites ?? []

  return (
    <Page>
      <PageBody>
        <PageHeader
          eyebrow="Collaboration"
          title="Team"
          description="Invite teammates and control who can view, query or administer this workspace."
          actions={
            <Button onClick={() => setInviteOpen(true)} disabled={!canAdmin}>
              <UserPlus className="size-4" /> Invite teammate
            </Button>
          }
        />

        <div className="mt-5 grid gap-4 lg:grid-cols-[1.6fr_1fr]">
          <div className="overflow-hidden rounded-xl border bg-card">
            <div className="flex items-center justify-between border-b px-5 py-4">
              <div>
                <h2 className="text-sm font-semibold">Members</h2>
                <p className="mt-0.5 text-xs text-muted-foreground">{members.length} people with access</p>
              </div>
              <Badge variant="outline" className="capitalize">{role}</Badge>
            </div>
            {isLoading ? (
              <div className="space-y-2 p-4">{Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-14" />)}</div>
            ) : (
              <div className="divide-y">
                {members.map((member) => {
                  const isSelf = member.user.id === me?.user.id
                  const canChange = canAdmin && (member.role !== "owner" || role === "owner")
                  return (
                    <div key={member.user.id} className="flex items-center gap-3 px-5 py-3.5">
                      <Avatar className="size-9">
                        {member.user.avatarUrl && <AvatarImage src={member.user.avatarUrl} />}
                        <AvatarFallback>{initials(member.user.name, member.user.email)}</AvatarFallback>
                      </Avatar>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-1.5">
                          <span className="truncate text-[13px] font-medium">{member.user.name}</span>
                          {isSelf && <Badge variant="muted" className="text-[9px]">you</Badge>}
                        </div>
                        <div className="truncate text-[11.5px] text-muted-foreground">{member.user.email}</div>
                      </div>
                      <div className="hidden text-[10.5px] text-muted-foreground md:block">joined {relativeTime(member.createdAt)}</div>
                      {canChange ? (
                        <Select value={member.role} onValueChange={(v) => roleMutation.mutate({ member, role: v as Role })} disabled={roleMutation.isPending}>
                          <SelectTrigger size="sm" className="w-[104px] text-[11px]"><SelectValue /></SelectTrigger>
                          <SelectContent>
                            <SelectItem value="admin">Admin</SelectItem>
                            <SelectItem value="member">Member</SelectItem>
                            <SelectItem value="viewer">Viewer</SelectItem>
                            {role === "owner" && <SelectItem value="owner">Owner</SelectItem>}
                          </SelectContent>
                        </Select>
                      ) : (
                        <RoleBadge role={member.role} />
                      )}
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon-sm" className="text-muted-foreground"><MoreHorizontal className="size-4" /></Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem disabled={!canAdmin || (member.role === "owner" && role !== "owner")} variant="destructive" onSelect={() => setRemoving(member)}>
                            <UserMinus className="size-4" /> Remove from workspace
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  )
                })}
              </div>
            )}
          </div>

          <div className="space-y-4">
            <div className="overflow-hidden rounded-xl border bg-card">
              <div className="border-b px-5 py-4">
                <h2 className="text-sm font-semibold">Pending invitations</h2>
                <p className="mt-0.5 text-xs text-muted-foreground">Invitations expire after 7 days</p>
              </div>
              {!canAdmin ? (
                <div className="px-5 py-6 text-[12px] text-muted-foreground">Only admins can see pending invitations.</div>
              ) : invites.length === 0 ? (
                <div className="px-5 py-6 text-center">
                  <MailPlus className="mx-auto size-5 text-muted-foreground/50" />
                  <p className="mt-2 text-[12px] text-muted-foreground">No pending invitations</p>
                </div>
              ) : (
                <div className="divide-y">
                  {invites.map((invite) => (
                    <div key={invite.id} className="flex items-center gap-2.5 px-4 py-3">
                      <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground"><MailPlus className="size-3.5" /></div>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-[12px] font-medium">{invite.email}</div>
                        <div className="text-[10px] text-muted-foreground"><span className="capitalize">{invite.role}</span> · expires {relativeTime(invite.expiresAt)}</div>
                      </div>
                      <Button variant="ghost" size="icon-xs" onClick={() => revokeMutation.mutate(invite)} title="Revoke invitation"><X className="size-3.5" /></Button>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="rounded-xl border bg-card p-5">
              <div className="flex items-center gap-2 text-sm font-semibold"><ShieldCheck className="size-4 text-primary" /> Roles</div>
              <div className="mt-3 space-y-3 text-[11.5px] leading-relaxed">
                <RoleRow role="Owner" body="Full control, including ownership transfer and workspace deletion." />
                <RoleRow role="Admin" body="Manage sources, members, invitations, API keys and settings." />
                <RoleRow role="Member" body="Run queries and create or edit saved queries." />
                <RoleRow role="Viewer" body="Run queries and view shared analyses, without editing." />
              </div>
            </div>

            <div className="rounded-xl border border-dashed bg-muted/20 p-4 text-[11.5px] leading-relaxed text-muted-foreground">
              Invite links are generated locally — copy the link and send it through your usual channel. Email delivery can be wired to your provider.
            </div>
          </div>
        </div>
      </PageBody>

      <InviteDialog open={inviteOpen} onOpenChange={setInviteOpen} onInvited={invalidate} canInviteOwner={false} />

      <AlertDialog open={Boolean(removing)} onOpenChange={(open) => !open && setRemoving(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove {removing?.user.name}?</AlertDialogTitle>
            <AlertDialogDescription>They will lose access to all data sources and saved queries in this workspace immediately.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={() => removing && removeMutation.mutate(removing)}>
              {removeMutation.isPending ? "Removing…" : "Remove member"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  )
}

function RoleRow({ role, body }: { role: string; body: string }) {
  return (
    <div className="flex gap-2">
      <Badge variant="outline" className="mt-0.5 h-fit w-14 justify-center text-[9.5px]">{role}</Badge>
      <span className="text-muted-foreground">{body}</span>
    </div>
  )
}

function InviteDialog({
  open,
  onOpenChange,
  onInvited,
  canInviteOwner,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onInvited: () => void
  canInviteOwner: boolean
}) {
  const { tenant } = useTenant()
  const [email, setEmail] = useState("")
  const [role, setRole] = useState<Role>("member")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [link, setLink] = useState<string | null>(null)
  const [invitedEmail, setInvitedEmail] = useState("")

  function reset() {
    setEmail("")
    setRole("member")
    setError(null)
    setLink(null)
    setInvitedEmail("")
    setBusy(false)
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const res = await api.invite(tenant.slug, email.trim(), role)
      setLink(res.link)
      setInvitedEmail(email.trim())
      onInvited()
      toast.success("Invitation created")
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create invitation")
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent>
        {link ? (
          <>
            <DialogHeader>
              <DialogTitle>Invitation ready</DialogTitle>
              <DialogDescription>Share this one-time link with {invitedEmail}. It expires in 7 days.</DialogDescription>
            </DialogHeader>
            <div className="mt-4 flex items-center gap-2 rounded-lg border bg-muted/40 p-2">
              <input readOnly value={link} className="min-w-0 flex-1 bg-transparent font-mono text-[10.5px] outline-none" />
              <Button type="button" size="sm" variant="outline" onClick={() => void navigator.clipboard.writeText(link).then(() => toast.success("Invite link copied"))}>
                <Copy className="size-3.5" /> Copy
              </Button>
            </div>
            <div className="mt-2 text-[11px] text-muted-foreground">Signed in as the invited email is required to accept.</div>
            <DialogFooter className="mt-5">
              <Button variant="outline" onClick={reset}>Invite another</Button>
              <Button onClick={() => onOpenChange(false)}><Check className="size-4" /> Done</Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit}>
            <DialogHeader>
              <DialogTitle>Invite a teammate</DialogTitle>
              <DialogDescription>They’ll join {tenant.name} with the role you choose.</DialogDescription>
            </DialogHeader>
            <div className="mt-5 space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="invite-email">Email</Label>
                <Input id="invite-email" autoFocus type="email" placeholder="teammate@company.com" value={email} onChange={(e) => setEmail(e.target.value)} required />
              </div>
              <div className="space-y-1.5">
                <Label>Role</Label>
                <Select value={role} onValueChange={(v) => setRole(v as Role)}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="admin">Admin — manage workspace</SelectItem>
                    <SelectItem value="member">Member — query and save</SelectItem>
                    <SelectItem value="viewer">Viewer — query read-only</SelectItem>
                    {canInviteOwner && <SelectItem value="owner">Owner</SelectItem>}
                  </SelectContent>
                </Select>
              </div>
              {error && <div className="rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-2 text-[12px] text-destructive">{error}</div>}
            </div>
            <DialogFooter className="mt-6">
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
              <Button type="submit" disabled={busy || !email.trim()}>
                {busy ? <Loader2 className="size-4 animate-spin" /> : <UserPlus className="size-4" />} {busy ? "Creating…" : "Create invite link"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
