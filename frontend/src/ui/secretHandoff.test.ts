import { expect, it } from 'vitest'
import { clearSecretHandoffs, createSecretHandoff, secretSession } from './secretHandoff'

it('consumes only the latest credential once and discards responses from a previous session', () => {
  const key = createSecretHandoff(), password = createSecretHandoff()
  key.handoff('old', 'old-secret'); key.handoff('new', 'new-secret')
  expect(key.take('old')).toBe(''); expect(key.take('new')).toBe('')
  password.handoff('user', 'password'); expect(password.take('user')).toBe('password'); expect(password.take('user')).toBe('')
  const origin = secretSession(); key.handoff('key', 'secret'); clearSecretHandoffs()
  expect(key.take('key')).toBe('')
  password.handoff('user', 'late-password', origin); expect(password.take('user')).toBe('')
  key.handoff('new', 'current-secret'); key.handoff('old', 'late-secret', origin)
  expect(key.take('new')).toBe('current-secret')
})
