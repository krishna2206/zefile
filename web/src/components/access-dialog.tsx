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

/**
 * AccessDialog manages who can do what at a path. Existing rules are listed with
 * editable permission checkboxes; a picker below adds people who have no rule
 * here yet. It is the human face of the ACL engine — the same rules the storage
 * layer enforces on every operation.
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

  // Grant or update a rule. An empty permission set removes the rule instead —
  // no access is the same as no rule, and it saves a separate delete.
  async function apply(subjectID: number, next: PermSet, opts: { recursive: boolean; deny: boolean; ruleID?: number }) {
    const anyLeft = GRANTABLE.some((g) => next[g.key]) || next.manage
    try {
      if (anyLeft) {
        await api.grantAccess({ subject_id: subjectID, path: entry.path, perms: next, recursive: opts.recursive, deny: opts.deny })
      } else if (opts.ruleID !== undefined) {
        await api.revokeAccess(opts.ruleID)
      }
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not update access.')
    }
  }

  async function grant() {
    if (subject === '') return
    if (!GRANTABLE.some((g) => perms[g.key])) {
      toast.error('Choose at least one permission.')
      return
    }
    setBusy(true)
    await apply(subject, perms, { recursive, deny: false })
    setSubject('')
    setPerms({ ...NONE, read: true })
    setBusy(false)
  }

  async function revoke(rule: AccessRule) {
    try {
      await api.revokeAccess(rule.id)
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not remove the rule.')
    }
  }

  const grantedIds = new Set(rules.filter((r) => r.subject_type === 'user').map((r) => r.subject_id))
  const available = users.filter((u) => !grantedIds.has(u.id))

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
                  <RuleRow
                    key={rule.id}
                    rule={rule}
                    onToggle={(key, checked) =>
                      apply(rule.subject_id, { ...rule.perms, [key]: checked }, {
                        recursive: rule.recursive,
                        deny: rule.deny,
                        ruleID: rule.id,
                      })
                    }
                    onRemove={() => revoke(rule)}
                  />
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
            ) : available.length === 0 ? (
              <p className="text-sm text-muted-foreground">Everyone already has a rule here.</p>
            ) : (
              <>
                <select
                  id="access-user"
                  value={subject}
                  onChange={(e) => setSubject(e.currentTarget.value ? Number(e.currentTarget.value) : '')}
                  className={selectClass}
                >
                  <option value="">Choose a person…</option>
                  {available.map((u) => (
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

function RuleRow({
  rule,
  onToggle,
  onRemove,
}: {
  rule: AccessRule
  onToggle: (key: keyof PermSet, checked: boolean) => void
  onRemove: () => void
}) {
  return (
    <li className="px-3 py-2">
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-sm">
          {rule.subject_name || `user #${rule.subject_id}`}
          {rule.deny && <span className="ml-1 text-destructive">(denied)</span>}
        </span>
        <span className="shrink-0 text-xs text-muted-foreground">
          {rule.recursive ? 'incl. contents' : 'this item'}
        </span>
        <Button
          variant="ghost"
          size="icon"
          className="size-7 shrink-0 text-muted-foreground hover:text-destructive"
          aria-label="Remove rule"
          onClick={onRemove}
        >
          <Trash />
        </Button>
      </div>
      <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1">
        {GRANTABLE.map((g) => (
          <label key={g.key} className="flex items-center gap-1.5 text-sm text-muted-foreground">
            <input
              type="checkbox"
              checked={rule.perms[g.key]}
              onChange={(e) => onToggle(g.key, e.currentTarget.checked)}
            />
            {g.label}
          </label>
        ))}
      </div>
    </li>
  )
}
