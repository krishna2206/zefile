import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Trash, UsersThree } from '@phosphor-icons/react'

import {
  api,
  ApiError,
  type AccessRule,
  type Entry,
  type Group,
  type PermSet,
  type UserSummary,
} from '@/api'
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

type SubjectType = 'user' | 'group'

/**
 * AccessDialog manages who can do what at a path. Existing rules are listed with
 * editable permission checkboxes; a picker below grants access to a user or a
 * group that has no rule here yet. It is the human face of the ACL engine — the
 * same rules the storage layer enforces on every operation.
 */
export function AccessDialog({ entry, onClose }: { entry: Entry; onClose: () => void }) {
  const [rules, setRules] = useState<AccessRule[]>([])
  const [users, setUsers] = useState<UserSummary[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [loading, setLoading] = useState(true)

  // The chosen grantee, encoded as "type:id" so one select can offer both.
  const [subject, setSubject] = useState<string>('')
  const [perms, setPerms] = useState<PermSet>({ ...NONE, read: true })
  const [recursive, setRecursive] = useState(entry.is_dir)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [access, people, teams] = await Promise.all([
        api.listAccess(entry.path),
        api.listUsers(),
        api.listGroups(),
      ])
      setRules(access.rules)
      setUsers(people.users.filter((u) => !u.is_admin && !u.disabled))
      setGroups(teams.groups)
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
  async function apply(
    subjectType: SubjectType,
    subjectID: number,
    next: PermSet,
    opts: { recursive: boolean; deny: boolean; ruleID?: number },
  ) {
    const anyLeft = GRANTABLE.some((g) => next[g.key]) || next.manage
    try {
      if (anyLeft) {
        await api.grantAccess({
          subject_type: subjectType,
          subject_id: subjectID,
          path: entry.path,
          perms: next,
          recursive: opts.recursive,
          deny: opts.deny,
        })
      } else if (opts.ruleID !== undefined) {
        await api.revokeAccess(opts.ruleID)
      }
      await load()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : 'Could not update access.')
    }
  }

  async function grant() {
    if (!subject) return
    if (!GRANTABLE.some((g) => perms[g.key])) {
      toast.error('Choose at least one permission.')
      return
    }
    const [type, id] = subject.split(':')
    setBusy(true)
    await apply(type as SubjectType, Number(id), perms, { recursive, deny: false })
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

  const granted = new Set(rules.map((r) => `${r.subject_type}:${r.subject_id}`))
  const availableUsers = users.filter((u) => !granted.has(`user:${u.id}`))
  const availableGroups = groups.filter((g) => !granted.has(`group:${g.id}`))
  const nothingToGrant = availableUsers.length === 0 && availableGroups.length === 0
  const noSubjectsAtAll = users.length === 0 && groups.length === 0

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
                      apply(rule.subject_type as SubjectType, rule.subject_id, { ...rule.perms, [key]: checked }, {
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
            <Label htmlFor="access-subject">Grant access</Label>
            {noSubjectsAtAll ? (
              <p className="text-sm text-muted-foreground">
                No accounts or groups to grant to yet. Invite someone or create a group from Members first.
              </p>
            ) : nothingToGrant ? (
              <p className="text-sm text-muted-foreground">Everyone already has a rule here.</p>
            ) : (
              <>
                <select
                  id="access-subject"
                  value={subject}
                  onChange={(e) => setSubject(e.currentTarget.value)}
                  className={selectClass}
                >
                  <option value="">Choose a person or group…</option>
                  {availableGroups.length > 0 && (
                    <optgroup label="Groups">
                      {availableGroups.map((g) => (
                        <option key={`group:${g.id}`} value={`group:${g.id}`}>
                          {g.name}
                        </option>
                      ))}
                    </optgroup>
                  )}
                  {availableUsers.length > 0 && (
                    <optgroup label="People">
                      {availableUsers.map((u) => (
                        <option key={`user:${u.id}`} value={`user:${u.id}`}>
                          {u.username}
                        </option>
                      ))}
                    </optgroup>
                  )}
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

                <Button type="button" className="mt-2 w-full" disabled={busy || !subject} onClick={grant}>
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
  const isGroup = rule.subject_type === 'group'
  return (
    <li className="px-3 py-2">
      <div className="flex items-center gap-2">
        {isGroup && <UsersThree className="size-4 shrink-0 text-muted-foreground" />}
        <span className="min-w-0 flex-1 truncate text-sm">
          {rule.subject_name || `${rule.subject_type} #${rule.subject_id}`}
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
