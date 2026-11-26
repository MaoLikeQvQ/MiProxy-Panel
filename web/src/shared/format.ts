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
