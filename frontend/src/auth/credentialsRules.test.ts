import { describe, expect, it } from 'vitest'
import { passwordProblem, passwordRuleText, usernameProblem, usernameRuleText } from './credentialsRules'

describe('credentialsRules', () => {
  it.each([
    ['admin', null],
    ['founder_01', null],
    ['a.b-c_d', null],
    ['Admin', usernameRuleText],
    ['管理员', usernameRuleText],
    ['ab', usernameRuleText],
    ['-admin', usernameRuleText],
    ['a'.repeat(33), usernameRuleText],
  ])('username %j -> %j', (username, expected) => {
    expect(usernameProblem(username)).toBe(expected)
  })

  it.each([
    ['Short1a', passwordRuleText],
    ['Abcdef12', null],
    ['abcdef12', passwordRuleText],
    ['ABCDEF12', passwordRuleText],
    ['Abcdefgh', passwordRuleText],
    ['A1' + 'a'.repeat(127), passwordRuleText],
  ])('password %j -> %j', (password, expected) => {
    expect(passwordProblem(password)).toBe(expected)
  })
})
