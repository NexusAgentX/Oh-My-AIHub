import { createSecretHandoff } from '../ui/secretHandoff'
const slot = createSecretHandoff()
export const handoffKeySecret = slot.handoff
export const takeKeySecret = slot.take
export const clearKeySecret = slot.clear
