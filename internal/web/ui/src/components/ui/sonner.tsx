import { Toaster as Sonner } from 'sonner'

// Toaster themed to the Linear dark surface. Import once per page and call
// toast() from anywhere.
export function Toaster() {
  return (
    <Sonner
      theme="dark"
      position="bottom-right"
      toastOptions={{
        style: {
          background: 'var(--popover)',
          color: 'var(--popover-foreground)',
          border: '1px solid var(--border)',
        },
      }}
    />
  )
}
