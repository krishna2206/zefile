import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

/** cn merges class lists, letting a later Tailwind utility win over an earlier
 *  one that sets the same property — which is what makes component overrides
 *  through a `className` prop behave the way a caller expects. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
