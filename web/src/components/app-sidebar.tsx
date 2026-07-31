import { CaretUpDown, GearSix, House, IconContext, ShareNetwork, SignOut, Trash, UsersThree } from '@phosphor-icons/react'

import { formatSize, type Space, type User } from '@/api'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { ThemeToggle } from '@/components/theme-toggle'
import { NewButton, type CreateActions } from '@/components/create-menu'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

type Section = 'files' | 'trash' | 'shared' | 'members' | 'settings'

type Props = {
  user: User
  space: Space | null
  section: Section
  create: CreateActions
  canCreate: boolean
  version: string
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
export function Sidebar({ user, space, section, create, canCreate, version, onHome, onTrash, onShared, onMembers, onSettings, onSignOut }: Props) {
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

        <div className="flex flex-col gap-1.5">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                className="flex w-full items-center gap-2 rounded-lg border bg-background/60 px-2 py-1.5 text-left outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring/50"
              >
                <div className="grid size-7 shrink-0 place-items-center rounded-full bg-primary text-xs font-medium text-primary-foreground">
                  {user.username.slice(0, 1).toUpperCase()}
                </div>
                <div className="min-w-0 flex-1 leading-tight">
                  <p className="truncate text-sm font-medium">{user.username}</p>
                  <p className="truncate text-[11px] text-muted-foreground">
                    {user.email || (user.is_admin ? 'Administrator' : 'Member')}
                  </p>
                </div>
                <CaretUpDown className="size-4 shrink-0 text-muted-foreground" />
              </button>
            </DropdownMenuTrigger>

            <DropdownMenuContent
              side="top"
              align="start"
              sideOffset={8}
              className="w-(--radix-dropdown-menu-trigger-width)"
            >
              <div className="flex items-center gap-2 px-2 py-1.5">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{user.username}</p>
                  <p className="truncate text-xs text-muted-foreground">
                    {user.email || (user.is_admin ? 'Administrator' : 'Member')}
                  </p>
                </div>
                <ThemeToggle className="size-8 shrink-0" />
              </div>

              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={onSettings}>
                <GearSix />
                Settings
              </DropdownMenuItem>
              {user.is_admin && (
                <DropdownMenuItem onSelect={onMembers}>
                  <UsersThree />
                  Members
                </DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={onSignOut}>
                <SignOut />
                Log out
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          {version && (
            <p className="text-center text-xs text-muted-foreground">Version {version}</p>
          )}
        </div>
      </div>
    </aside>
    </IconContext.Provider>
  )
}
