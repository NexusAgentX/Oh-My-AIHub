import { createSecretHandoff } from '../ui/secretHandoff'
const slot = createSecretHandoff()
export const handoffAdminSecret = slot.handoff
export const takeAdminSecret = slot.take
