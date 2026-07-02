import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

// cn merges conditional class names and de-duplicates conflicting Tailwind
// utilities (shadcn convention).
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}
