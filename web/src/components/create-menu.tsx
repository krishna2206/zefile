import { CaretDown, FileArrowUp, FolderPlus, FolderSimplePlus, LinkSimple, Plus } from '@phosphor-icons/react'

import { Button } from '@/components/ui/button'
import { ContextMenuItem } from '@/components/ui/context-menu'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

/** CreateActions are the three ways to add content to the current folder. The
 *  same set backs the sidebar's "New" button and the empty-area right-click
 *  menu, so both offer exactly the same choices. */
export type CreateActions = {
  newFolder: () => void
  importFiles: () => void
  importFolder: () => void
  fetchUrl: () => void
}

const items = (a: CreateActions) => [
  { key: 'folder', Icon: FolderPlus, label: 'New folder', run: a.newFolder },
  { key: 'files', Icon: FileArrowUp, label: 'Import files', run: a.importFiles },
  { key: 'import-folder', Icon: FolderSimplePlus, label: 'Import folder', run: a.importFolder },
  { key: 'fetch-url', Icon: LinkSimple, label: 'Download from URL', run: a.fetchUrl },
]

/** NewButton is the sidebar's primary action: a dropdown that gathers creating a
 *  folder and importing files or a folder under one "New". */
export function NewButton({ actions, disabled }: { actions: CreateActions; disabled?: boolean }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button className="justify-start gap-2" disabled={disabled}>
          <Plus />
          New
          <CaretDown className="ml-auto size-4 opacity-70" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-52">
        {items(actions).map(({ key, Icon, label, run }) => (
          <DropdownMenuItem key={key} onSelect={run}>
            <Icon />
            {label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** CreateContextItems renders the same three actions as context-menu entries,
 *  for the right-click menu on an empty area of the file list. */
export function CreateContextItems({ actions }: { actions: CreateActions }) {
  return (
    <>
      {items(actions).map(({ key, Icon, label, run }) => (
        <ContextMenuItem key={key} onSelect={run}>
          <Icon />
          {label}
        </ContextMenuItem>
      ))}
    </>
  )
}
