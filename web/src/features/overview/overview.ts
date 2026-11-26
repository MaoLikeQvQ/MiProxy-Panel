import type { CoreStatus } from '../../shared/api'

export type Counters = {
  active: number
  downloaded: number
  uploaded: number
  sampledAt: number
}

export type ConnectionHistory = {
  revision: string
  events: { id: number; connected: boolean; time: number }[]
  storageWarning?: string
}

export type Overview = CoreStatus & {
  controller: string; counters?: Counters; memory?: number; history?: ConnectionHistory
  traffic: { samples: TrafficSample[]; rate?: TrafficSample }
}
export type TrafficSample = { time: number; upload: number; download: number; startsSegment: boolean }
export const overviewInterval = 3_000
export const trafficWindow = 120_000
