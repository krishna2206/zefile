import type { CSSProperties, ReactNode } from 'react'

import styles from './layout.module.css'

/** Space is the layout scale. Components pick a rung rather than a pixel
 *  count, which is what keeps spacing consistent without a review catching it. */
export type Space = 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8

type Common = {
  children?: ReactNode
  gap?: Space
  className?: string
  style?: CSSProperties
}

function withGap(gap: Space | undefined, style: CSSProperties | undefined): CSSProperties | undefined {
  if (gap === undefined) return style
  return { ...style, ['--gap' as string]: `var(--space-${gap})` }
}

function classes(...names: (string | false | undefined)[]): string {
  return names.filter(Boolean).join(' ')
}

/** Stack arranges children in a column. */
export function Stack({ children, gap, className, style, fill }: Common & { fill?: boolean }) {
  return (
    <div className={classes(styles.stack, fill && styles.fill, className)} style={withGap(gap, style)}>
      {children}
    </div>
  )
}

/** Row arranges children horizontally, centred on their shared baseline. */
export function Row({ children, gap, className, style, wrap }: Common & { wrap?: boolean }) {
  return (
    <div className={classes(styles.row, wrap && styles.wrap, className)} style={withGap(gap, style)}>
      {children}
    </div>
  )
}

/** Center places one thing in the middle of the height it is given. */
export function Center({ children, className }: { children?: ReactNode; className?: string }) {
  return <div className={classes(styles.center, className)}>{children}</div>
}

/** Fill takes the remaining space in a flex parent, optionally scrolling. */
export function Fill({ children, className, scroll }: Common & { scroll?: boolean }) {
  return <div className={classes(styles.fill, scroll && styles.scroll, className)}>{children}</div>
}

/** Spacer pushes everything after it to the far end of a Row. */
export function Spacer() {
  return <div className={styles.spacer} />
}

/** truncate is exported as a class because it applies to elements this module
 *  does not own — a Material Text, say. */
export const truncate = styles.truncate
