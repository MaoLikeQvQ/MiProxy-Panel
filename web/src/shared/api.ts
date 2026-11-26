export type Snapshot = { content: string; revision: string; exists: boolean; externalConfig?: boolean }
export type RuntimeRule = {
  index: number
  type: string
  payload: string
  proxy: string
  extra?: { disabled: boolean; hitCount: number }
}
export type RuntimeGroup = { name: string; type: string; now: string; all: string[] }
export type CoreStatus = { connected: boolean; version?: string }
export type CoreInstallation = CoreStatus & {
  installed: boolean; binary?: string; platform: string; managed: boolean; autoStart: boolean
  busy: boolean; phase: string; detail?: string; disabledReason?: string
  downloaded?: number; total?: number; speed?: number
  selectedId?: string; source?: string; external?: boolean
}

export type CoreCandidate = {
  id: string; source: string; binary?: string; config?: string; controller?: string
  running: boolean; connected: boolean; version?: string; reason?: string; selected?: boolean
}
export type CoreDiscovery = { candidates: CoreCandidate[]; warnings: string[]; selectedId?: string }

export async function request<T>(path: string, payload?: unknown, method = payload === undefined ? 'GET' : 'POST', signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, {
    method,
    signal,
    ...(payload === undefined ? {} : {
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),
  })
  const result = await response.json().catch(() => null)
  if (!response.ok) {
    const error = new Error(result?.message ?? `请求失败（HTTP ${response.status}）`)
    throw Object.assign(error, { detail: result?.detail, status: response.status })
  }
  if (result === null) throw new Error('服务返回了无效响应。')
  return result as T
}

export function errorText(error: unknown) {
  if (!(error instanceof Error)) return '操作失败，请重试。'
  const detail = 'detail' in error && typeof error.detail === 'string' ? error.detail : ''
  return [error.message, detail].filter(Boolean).join(' ')
}
