import type { CoreStatus } from './api'

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

export function formatBytes(value: number | undefined, rate = false) {
  if (value === undefined) return '—'
  const base = rate ? 1000 : 1024
  const units = rate ? ['B/s', 'kB/s', 'MB/s', 'GB/s', 'TB/s'] : ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let unit = 0
  while (value >= base && unit < units.length - 1) {
    value /= base
    unit++
  }
  return `${value.toLocaleString('zh-CN', { maximumFractionDigits: unit === 0 ? 0 : 1 })} ${units[unit]}`
}
