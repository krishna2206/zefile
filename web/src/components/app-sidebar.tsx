import { GearSix, House, IconContext, ShareNetwork, SignOut, Trash, UsersThree } from '@phosphor-icons/react'

import { formatSize, type Space, type User } from '@/api'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { ThemeToggle } from '@/components/theme-toggle'
import { NewButton, type CreateActions } from '@/components/create-menu'

type Section = 'files' | 'trash' | 'shared' | 'members' | 'settings'

type Props = {
  user: User
  space: Space | null
  section: Section
  create: CreateActions
  canCreate: boolean
  onHome: () => void
  onTrash: () => void
  onShared: () => void
  onMembers: () => void
  onSettings: () => void
  onSignOut: () => void
}

/**
 * Sidebar is the app's fixed left column: identity, the "New" action,
 * navigation, and the storage gauge that tells you when to stop uploading.
 */
export function Sidebar({ user, space, section, create, canCreate, onHome, onTrash, onShared, onMembers, onSettings, onSignOut }: Props) {
  const inFiles = section === 'files'
  const used = space ? Math.max(0, space.total - space.available) : 0
  const percent = space && space.total > 0 ? (used / space.total) * 100 : 0

  return (
    // Duotone icons live only here, in the sidebar; the rest of the app uses
    // Phosphor's default regular weight.
    <IconContext.Provider value={{ weight: 'duotone' }}>
    <aside className="flex w-60 shrink-0 flex-col border-r bg-card/40">
      <div className="flex h-14 items-center px-5">
        <span className="font-serif text-xl font-semibold tracking-tight">Zefile</span>
      </div>

      <div className="flex flex-col gap-1.5 px-3 pb-2">
        <NewButton actions={create} disabled={!inFiles || !canCreate} />
      </div>

      <div className="mx-3 my-4 h-px bg-border" />

      <nav className="flex flex-col gap-0.5 px-3">
        <Button
          variant={section === 'files' ? 'secondary' : 'ghost'}
          className="justify-start gap-2"
          onClick={onHome}
        >
          <House />
          My files
        </Button>
        <Button
          variant={section === 'shared' ? 'secondary' : 'ghost'}
          className="justify-start gap-2"
          onClick={onShared}
        >
          <ShareNetwork />
          Shared
        </Button>
        <Button
          variant={section === 'trash' ? 'secondary' : 'ghost'}
          className="justify-start gap-2"
          onClick={onTrash}
        >
          <Trash />
          Trash
        </Button>
        {user.is_admin && (
          <Button
            variant={section === 'members' ? 'secondary' : 'ghost'}
            className="justify-start gap-2"
            onClick={onMembers}
          >
            <UsersThree />
            Members
          </Button>
        )}
      </nav>

      <div className="mt-auto flex flex-col gap-3 p-3">
        {space && (
          <div className="space-y-2 rounded-lg border bg-background/60 p-3">
            <Progress value={percent} />
            <p className="text-xs text-muted-foreground">
              {formatSize(used)} of {formatSize(space.total)} used
            </p>
            {space.read_only && (
              <p className="text-xs font-medium text-destructive">Read-only</p>
            )}
          </div>
        )}

        <div className="flex items-center gap-2">
          <div className="grid size-8 shrink-0 place-items-center rounded-full bg-primary text-sm font-medium text-primary-foreground">
            {user.username.slice(0, 1).toUpperCase()}
          </div>
          <button
            type="button"
            onClick={onSettings}
            className={`min-w-0 flex-1 truncate rounded px-1 py-0.5 text-left text-sm font-medium hover:bg-accent ${
              section === 'settings' ? 'text-foreground' : ''
            }`}
            title="Account settings"
          >
            {user.username}
          </button>
          <ThemeToggle className="size-8" />
          <Button
            variant={section === 'settings' ? 'secondary' : 'ghost'}
            size="icon"
            className="size-8"
            aria-label="Settings"
            onClick={onSettings}
          >
            <GearSix />
          </Button>
          <Button variant="ghost" size="icon" className="size-8" aria-label="Sign out" onClick={onSignOut}>
            <SignOut />
          </Button>
        </div>
      </div>
    </aside>
    </IconContext.Provider>
  )
}
