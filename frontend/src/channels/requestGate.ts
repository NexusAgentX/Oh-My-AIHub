export type LatestRequestGate = {
  begin: () => number
  isCurrent: (ticket: number) => boolean
  invalidate: () => void
}

export function createLatestRequestGate(): LatestRequestGate {
  let generation = 0
  return {
    begin() {
      generation += 1
      return generation
    },
    isCurrent(ticket) {
      return ticket === generation
    },
    invalidate() {
      generation += 1
    },
  }
}
