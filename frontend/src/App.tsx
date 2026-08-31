import { useEffect, useState } from 'react'

type HealthResponse = {
  service: string
  status: string
}

export function App() {
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [error, setError] = useState(false)

  useEffect(() => {
    fetch('/api/health')
      .then((response) => {
        if (!response.ok) throw new Error('Backend request failed')
        return response.json() as Promise<HealthResponse>
      })
      .then(setHealth)
      .catch(() => setError(true))
  }, [])

  return (
    <main className="shell">
      <section className="card">
        <p className="eyebrow">NexusAgentX</p>
        <h1>Oh-My-AIHub</h1>
        <p className="intro">
          React and Go are connected. This clean foundation is ready for the
          first AI workflow.
        </p>
        <div className="status" aria-live="polite">
          <span className={`dot ${health ? 'online' : error ? 'offline' : ''}`} />
          {health
            ? `${health.service}: ${health.status}`
            : error
              ? 'Backend unavailable'
              : 'Checking backend…'}
        </div>
      </section>
    </main>
  )
}
