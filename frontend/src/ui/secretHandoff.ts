let session = 0
const clearers = new Set<() => void>()
export const secretSession = () => session
export function clearSecretHandoffs() { session++; for (const clear of clearers) clear() }

/** One bounded in-memory slot per credential kind, including late-response protection. */
export function createSecretHandoff() {
  let pending: { id: string; secret: string } | undefined
  const clear = () => { pending = undefined }
  clearers.add(clear)
  return {
    clear,
    handoff: (id: string, secret: string, origin = session) => { if (origin === session) pending = { id, secret } },
    take: (id: string) => { const secret = pending?.id === id ? pending.secret : ''; clear(); return secret },
  }
}
