import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

/**
 * cn merges Tailwind class strings, letting a caller's class win over a
 * component's default for the same property. Every component in this package
 * takes a `class` prop and passes it through here.
 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// The three helpers below are what shadcn-svelte's generated components expect
// from their `utils` alias. They exist because Bits UI components accept both
// a `child` snippet and `children`, and a wrapper usually wants to forbid one.
export type WithoutChild<T> = T extends { child?: unknown } ? Omit<T, "child"> : T
export type WithoutChildren<T> = T extends { children?: unknown } ? Omit<T, "children"> : T
export type WithoutChildrenOrChild<T> = WithoutChildren<WithoutChild<T>>
export type WithElementRef<T, U extends HTMLElement = HTMLElement> = T & {
  ref?: U | null
}
