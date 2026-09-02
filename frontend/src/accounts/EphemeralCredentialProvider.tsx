import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { useLocation } from 'react-router-dom'
import type { CreatedCredential } from '../api/contracts'
import { shouldClearCredential } from './ephemeralCredential'

type EphemeralCredentialContextValue = {
  credential: CreatedCredential | null
  setCredential: (credential: CreatedCredential) => void
  clearCredential: () => void
}

const EphemeralCredentialContext =
  createContext<EphemeralCredentialContextValue | null>(null)

export function EphemeralCredentialProvider({
  children,
}: {
  children: ReactNode
}) {
  const [credential, setCredentialState] =
    useState<CreatedCredential | null>(null)
  const location = useLocation()
  const previousPath = useRef(location.pathname)

  useEffect(() => {
    if (shouldClearCredential(previousPath.current, location.pathname)) {
      setCredentialState(null)
    }
    previousPath.current = location.pathname
  }, [location.pathname])

  const value = useMemo(
    () => ({
      credential,
      setCredential: setCredentialState,
      clearCredential: () => setCredentialState(null),
    }),
    [credential],
  )

  return (
    <EphemeralCredentialContext.Provider value={value}>
      {children}
    </EphemeralCredentialContext.Provider>
  )
}

export function useEphemeralCredential() {
  const value = useContext(EphemeralCredentialContext)
  if (!value) {
    throw new Error(
      'useEphemeralCredential must be used inside EphemeralCredentialProvider',
    )
  }
  return value
}
