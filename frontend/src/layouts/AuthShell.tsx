import type { ReactNode } from 'react'
import { Brand } from './Brand'

export function AuthShell({ children }: { children: ReactNode }) {
  return (
    <main className="auth-shell">
      <section className="auth-card">
        <Brand />
        {children}
      </section>
    </main>
  )
}
