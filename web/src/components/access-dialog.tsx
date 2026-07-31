import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Trash } from '@phosphor-icons/react'

import { api, ApiError, type AccessRule, type Entry, type PermSet, type UserSummary } from '@/api'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'

const selectClass =
  'flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]'

// The permissions offered in the interface. Manage is left out: changing who can
// do what stays an administrator's job, not something delegated through this box.
const GRANTABLE: { key: keyof PermSet; label: string }[] = [
  { key: 'read', label: 'Read' },
  { key: 'write', label: 'Write' },
  { key: 'delete', label: 'Delete' },
  { key: 'share', label: 'Share' },
]

const NONE: PermSet = { read: false, write: false, delete: false, share: false, manage: false }

/** permLabel renders a rule's permissions as a short, readable list. */
function permLabel(perms: PermSet): string {
  const on = GRANTABLE.filter((g) => perms[g.key]).map((g) => g.label.toLowerCase())
  if (perms.manage) on.push('manage')
  return on.length ? on.join(', ') : 'none'
}

/**
 * AccessDialog manages who can do what at a path. An administrator picks a user,
 * chooses permissions, and grants them; existing rules are listed with a way to
 * remove each. It is the human face of the ACL engine — the same rules the
 * storage layer enforces on every operation.
 */
export function AccessDialog({ entry, onClose }: { entry: Entry; onClose: () => void }) {
  const [rules, setRules] = useState<AccessRule[]>([])
  const [users, setUsers] = useState<UserSummary[]>([])
  const [loading, setLoading] = useState(true)

  const [subject, setSubject] = useState<number | ''>('')
  const [perms, setPerms] = useState<PermSet>({ ...NONE, read: true })
  const [recursive, setRecursive] = useState(entry.is_dir)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [access, people] = await Promise.all([api.listAccess(entry.path), api.listUsers()])
      setRules(access.rules)
      // Only non-admins are worth listing: an admin already has every right
      // everywhere, so a rule for one would do nothing.
      setUsers(people.users.filter((u) => !u.is_admin && !u.disabled))
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not load access.')
    } finally {
      setLoading(false)
    }
  }, [entry.path])

  useEffect(() => {
    void load()
  }, [load])

  async function grant() {
    if (subject === '') return
    const chosen = GRANTABLE.some((g) => perms[g.key])
    if (!chosen) {
      toast.error('Choose at least one permission.')
      return
    }
    setBusy(true)
    try {
      await api.grantAccess({ subject_id: subject, path: entry.path, perms, recursive })
      toast.success('Access granted')
      setSubject('')
      setPerms({ ...NONE, read: true })
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not grant access.')
    } finally {
      setBusy(false)
    }
  }

  async function revoke(rule: AccessRule) {
    try {
      await api.revokeAccess(rule.id)
      toast.success('Rule removed')
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not remove the rule.')
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Access to “{entry.name}”</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="space-y-2">
            <Label>Who has access</Label>
            {loading ? (
              <p className="text-sm text-muted-foreground">Loading…</p>
            ) : rules.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No rules here yet. Everyone but administrators is denied until you grant access.
              </p>
            ) : (
              <ul className="divide-y rounded-md border">
                {rules.map((rule) => (
                  <li key={rule.id} className="flex items-center gap-3 px-3 py-2">
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm">
                        {rule.subject_name || `user #${rule.subject_id}`}
                        {rule.deny && <span className="ml-1 text-destructive">(denied)</span>}
                      </p>
                      <p className="truncate text-xs text-muted-foreground">
                        {permLabel(rule.perms)}
                        {rule.recursive ? ' · applies to contents' : ' · this item only'}
                      </p>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
                      aria-label="Remove rule"
                      onClick={() => revoke(rule)}
                    >
                      <Trash />
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="space-y-2 border-t pt-4">
            <Label htmlFor="access-user">Grant access</Label>
            {users.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No accounts to grant to yet. Invite someone from Members first.
              </p>
            ) : (
              <>
                <select
                  id="access-user"
                  value={subject}
                  onChange={(e) => setSubject(e.currentTarget.value ? Number(e.currentTarget.value) : '')}
                  className={selectClass}
                >
                  <option value="">Choose a person…</option>
                  {users.map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.username}
                    </option>
                  ))}
                </select>

                <div className="flex flex-wrap gap-x-4 gap-y-1.5 pt-1">
                  {GRANTABLE.map((g) => (
                    <label key={g.key} className="flex items-center gap-1.5 text-sm">
                      <input
                        type="checkbox"
                        checked={perms[g.key]}
                        onChange={(e) => setPerms((p) => ({ ...p, [g.key]: e.currentTarget.checked }))}
                      />
                      {g.label}
                    </label>
                  ))}
                </div>

                {entry.is_dir && (
                  <label className="flex items-center gap-1.5 pt-1 text-sm">
                    <input
                      type="checkbox"
                      checked={recursive}
                      onChange={(e) => setRecursive(e.currentTarget.checked)}
                    />
                    Apply to everything inside this folder
                  </label>
                )}

                <Button type="button" className="mt-2 w-full" disabled={busy || subject === ''} onClick={grant}>
                  {busy ? 'Granting…' : 'Grant access'}
                </Button>
              </>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
