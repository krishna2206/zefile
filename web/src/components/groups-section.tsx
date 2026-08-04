import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { UsersThree } from '@phosphor-icons/react'

import { api, ApiError, type Group, type UserSummary } from '@/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

/**
 * GroupsSection manages named sets of users. Access granted to a group reaches
 * everyone in it, so groups keep permissions manageable as the number of people
 * and folders grows.
 */
export function GroupsSection({ users }: { users: UserSummary[] }) {
  const [groups, setGroups] = useState<Group[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [managing, setManaging] = useState<Group | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setGroups((await api.listGroups()).groups)
    } catch {
      toast.error('Could not load groups.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function remove(group: Group) {
    try {
      await api.deleteGroup(group.id)
      toast.success(`Group “${group.name}” removed`)
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not remove this group.')
    }
  }

  const members = users.filter((u) => !u.is_admin && !u.disabled)

  return (
    <section className="space-y-2">
      <div className="mb-2 flex items-center gap-3 px-1">
        <h2 className="text-base font-semibold text-foreground">Groups</h2>
        <Button size="sm" variant="outline" className="ml-auto gap-2" onClick={() => setCreating(true)}>
          <UsersThree />
          New group
        </Button>
      </div>

      {loading ? (
        <p className="px-1 text-sm text-muted-foreground">Loading…</p>
      ) : groups.length === 0 ? (
        <p className="px-1 text-sm text-muted-foreground">
          No groups yet. Create one to grant a whole team access at once.
        </p>
      ) : (
        <div className="overflow-hidden rounded-lg border">
          {groups.map((group) => (
            <div
              key={group.id}
              className="flex h-14 items-center gap-3 border-b border-border/60 px-4 last:border-b-0 hover:bg-accent/40"
            >
              <UsersThree className="size-5 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm">{group.name}</p>
                <p className="truncate text-xs text-muted-foreground">
                  {group.member_count} member{group.member_count === 1 ? '' : 's'}
                </p>
              </div>
              <Button variant="ghost" size="sm" className="shrink-0" onClick={() => setManaging(group)}>
                Members
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="shrink-0 text-muted-foreground hover:text-destructive"
                onClick={() => remove(group)}
              >
                Delete
              </Button>
            </div>
          ))}
        </div>
      )}

      {creating && (
        <CreateGroupDialog
          onClose={() => setCreating(false)}
          onCreated={() => {
            void load()
          }}
        />
      )}
      {managing && (
        <GroupMembersDialog
          group={managing}
          users={members}
          onClose={() => setManaging(null)}
          onChanged={() => {
            void load()
          }}
        />
      )}
    </section>
  )
}

function CreateGroupDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  async function create(event: FormEvent) {
    event.preventDefault()
    if (!name.trim()) {
      setErr('Choose a name.')
      return
    }
    setBusy(true)
    setErr('')
    try {
      await api.createGroup(name.trim())
      toast.success('Group created')
      onCreated()
      onClose()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'Could not create the group.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New group</DialogTitle>
        </DialogHeader>
        <form onSubmit={create} className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label htmlFor="group-name">Name</Label>
            <Input
              id="group-name"
              autoFocus
              placeholder="e.g. Design team"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
            />
            {err && <p className="text-sm text-destructive">{err}</p>}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? 'Creating…' : 'Create group'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function GroupMembersDialog({
  group,
  users,
  onClose,
  onChanged,
}: {
  group: Group
  users: UserSummary[]
  onClose: () => void
  onChanged: () => void
}) {
  const [memberIds, setMemberIds] = useState<Set<number>>(() => new Set())
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setMemberIds(new Set((await api.groupMembers(group.id)).member_ids))
    } catch {
      toast.error('Could not load members.')
    } finally {
      setLoading(false)
    }
  }, [group.id])

  useEffect(() => {
    void load()
  }, [load])

  async function toggle(user: UserSummary, member: boolean) {
    // Optimistic: reflect the change at once, roll back on failure.
    setMemberIds((cur) => {
      const next = new Set(cur)
      if (member) next.add(user.id)
      else next.delete(user.id)
      return next
    })
    try {
      if (member) await api.addGroupMember(group.id, user.id)
      else await api.removeGroupMember(group.id, user.id)
      onChanged()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not update membership.')
      await load()
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Members of “{group.name}”</DialogTitle>
        </DialogHeader>
        <div className="py-2">
          {loading ? (
            <p className="text-sm text-muted-foreground">Loading…</p>
          ) : users.length === 0 ? (
            <p className="text-sm text-muted-foreground">No accounts to add yet.</p>
          ) : (
            <ul className="max-h-72 space-y-1 overflow-auto">
              {users.map((user) => (
                <li key={user.id}>
                  <label className="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm hover:bg-accent">
                    <input
                      type="checkbox"
                      checked={memberIds.has(user.id)}
                      onChange={(e) => toggle(user, e.currentTarget.checked)}
                    />
                    {user.username}
                  </label>
                </li>
              ))}
            </ul>
          )}
        </div>
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
