<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef } from 'vue'
import { Activity, ArrowDownToLine, ArrowUpFromLine, Cable, ChevronLeft, ChevronRight, Cpu, History, RefreshCw } from '@lucide/vue'
import { request } from '../../shared/api'
import type { CoreStatus } from '../../shared/api'
import { overviewInterval } from './overview'
import { formatBytes } from '../../shared/format'
import type { ConnectionHistory, Overview, TrafficSample } from './overview'
import TrafficChart from './TrafficChart.vue'
import CoreManager from '../core/CoreManager.vue'

const props = defineProps<{ active: boolean; dark: boolean }>()
const emit = defineEmits<{ status: [CoreStatus]; configured: [] }>()
type ConnectionState = 'connected' | 'disconnected' | 'unavailable'
const states = {
  connected: '已连接',
  disconnected: '未连接',
  unavailable: '无法连接面板',
}
const clock = new Intl.DateTimeFormat('zh-CN', { timeStyle: 'medium', hour12: false })
const eventClock = new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

const current = ref<Overview>()
const state = ref<ConnectionState>()
const checking = ref(false)
const sampledAt = ref<number>()
const rate = ref<TrafficSample>()
const traffic = ref<TrafficSample[]>([])
const connectionHistory = shallowRef<ConnectionHistory>()
const historyPage = ref(1)
const shortDesktop = window.matchMedia('(min-width: 1000px) and (max-height: 700px)')
const historyPageSize = ref(shortDesktop.matches ? 1 : 2)
const historyPageCount = computed(() => Math.max(1, Math.ceil((connectionHistory.value?.events.length ?? 0) / historyPageSize.value)))
const currentHistoryPage = computed(() => Math.min(historyPage.value, historyPageCount.value))
const visibleEvents = computed(() => {
  const start = (currentHistoryPage.value - 1) * historyPageSize.value
  return connectionHistory.value?.events.slice(start, start + historyPageSize.value) ?? []
})
function resizeHistory() {
  historyPageSize.value = shortDesktop.matches ? 1 : 2
  historyPage.value = 1
}
let pending: AbortController | undefined
let timer: number | undefined

const online = computed(() => state.value === 'connected')
const counters = computed(() => current.value?.counters)
const metrics = computed(() => [
  { label: '活跃连接', value: counters.value?.active.toLocaleString('zh-CN'), icon: Cable },
  { label: '内存占用', value: formatBytes(current.value?.memory), icon: Cpu },
  { label: '累计下载', value: formatBytes(counters.value?.downloaded), icon: ArrowDownToLine },
  { label: '累计上传', value: formatBytes(counters.value?.uploaded), icon: ArrowUpFromLine },
])
const dataStatus = computed(() => {
  if (state.value === 'unavailable') return '无法读取面板状态，正在重试'
  if (state.value === 'disconnected') return '尚未连接 Mihomo，可在内核管理中选择。'
  if (!sampledAt.value) return '正在读取 Mihomo 状态…'
  const status = !counters.value || current.value?.memory === undefined ? '部分指标暂不可用 · ' : '更新于 '
  return `${status}${clock.format(sampledAt.value)}`
})

async function sample() {
  if (document.hidden || pending) return
  window.clearTimeout(timer)
  const attempt = new AbortController()
  pending = attempt
  checking.value = true
  const start = performance.now()
  const deadline = window.setTimeout(() => attempt.abort(), 6500)
  try {
    const revision = connectionHistory.value?.revision
    const url = revision ? `/api/overview?historyRevision=${encodeURIComponent(revision)}` : '/api/overview'
    const result = await request<Overview>(url, undefined, 'GET', attempt.signal)
    if (pending !== attempt) return
    const time = Date.now()
    current.value = result
    sampledAt.value = time
    state.value = result.connected ? 'connected' : 'disconnected'
    if (result.history) connectionHistory.value = result.history
    traffic.value = result.traffic.samples
    rate.value = result.traffic.rate
    emit('status', { connected: result.connected, version: result.version })
  } catch {
    if (pending !== attempt) return
    const time = Date.now()
    current.value = undefined
    sampledAt.value = time
    state.value = 'unavailable'
    rate.value = undefined
    emit('status', { connected: false })
  } finally {
    window.clearTimeout(deadline)
    if (pending === attempt) {
      pending = undefined
      checking.value = false
      timer = window.setTimeout(sample, Math.max(500, overviewInterval - (performance.now() - start)))
    }
  }
}

function pause() {
  window.clearTimeout(timer)
  pending?.abort()
  pending = undefined
  checking.value = false
}

function syncSampling() {
  if (!document.hidden) void sample()
  else pause()
}

onMounted(() => {
  shortDesktop.addEventListener('change', resizeHistory)
  document.addEventListener('visibilitychange', syncSampling)
  syncSampling()
})
onUnmounted(() => {
  shortDesktop.removeEventListener('change', resizeHistory)
  document.removeEventListener('visibilitychange', syncSampling)
  pause()
})
defineExpose({ sample })
</script>

<template>
  <div class="overview">
    <div class="overview-update">
      <span class="overview-sampling"><i :class="{ online }" aria-hidden="true"></i>{{ dataStatus }}</span>
      <button class="button secondary" type="button" :disabled="checking" @click="sample">
        <RefreshCw :size="16" :class="{ spinner: checking }" aria-hidden="true" />刷新状态
      </button>
    </div>

    <dl class="overview-metrics" aria-label="Mihomo 运行指标">
      <div v-for="metric in metrics" :key="metric.label" class="overview-card metric">
        <dt><component :is="metric.icon" :size="16" aria-hidden="true" />{{ metric.label }}</dt>
        <dd>{{ metric.value ?? '—' }}</dd>
      </div>
    </dl>

    <div class="overview-grid">
      <section class="overview-card traffic-panel" aria-labelledby="traffic-title">
        <header class="overview-card-title">
          <Activity :size="17" aria-hidden="true" />
          <h2 id="traffic-title">实时流量</h2>
          <span class="overview-note">最近 2 分钟</span>
        </header>
        <dl class="traffic-legend">
          <div><dt><i class="download-line" aria-hidden="true"></i>下载速率</dt><dd>{{ formatBytes(rate?.download, true) }}</dd></div>
          <div><dt><i class="upload-line" aria-hidden="true"></i>上传速率</dt><dd>{{ formatBytes(rate?.upload, true) }}</dd></div>
        </dl>
        <TrafficChart :samples="traffic" :now="sampledAt ?? 0" :active="active" :dark="dark" />
        <p v-if="!traffic.length" class="overview-note">{{ online && counters ? '正在采集速率…' : '等待可用的流量数据' }}</p>
      </section>

      <div class="overview-side">
        <section class="overview-card overview-details" aria-labelledby="core-title">
          <header class="overview-card-title">
            <Cpu :size="16" aria-hidden="true" />
            <h2 id="core-title">Mihomo 内核</h2>
            <span class="core-state" :class="{ online, offline: state && !online }" role="status">
              <i aria-hidden="true"></i>{{ state ? states[state] : '检查中' }}
            </span>
          </header>
          <CoreManager :connected="online" :controller="current?.controller" @connected="sample" @configured="emit('configured')" />
        </section>

        <section v-memo="[connectionHistory, currentHistoryPage, historyPageSize, state === 'unavailable']" class="overview-card overview-details history-panel" aria-labelledby="history-title">
          <header class="overview-card-title">
            <History :size="16" aria-hidden="true" />
            <h2 id="history-title">连接波动记录</h2>
          </header>
          <p v-if="connectionHistory?.storageWarning" class="overview-note" role="status">{{ connectionHistory.storageWarning }}</p>
          <ol v-if="visibleEvents.length" class="connection-events" aria-labelledby="history-title">
            <li v-for="event in visibleEvents" :key="event.id">
              <span class="event-indicator" :class="{ restored: event.connected }" aria-hidden="true"></span>
              <span>{{ event.connected ? 'Mihomo 控制接口已恢复' : 'Mihomo 控制接口不可达' }}</span>
              <time :datetime="new Date(event.time).toISOString()">{{ eventClock.format(event.time) }}</time>
            </li>
          </ol>
          <p v-else class="events-empty">{{ connectionHistory ? '暂无断开或恢复记录' : state === 'unavailable' ? '暂时无法读取波动记录' : '正在读取波动记录…' }}</p>
          <nav v-if="historyPageCount > 1" class="history-pagination" aria-label="连接记录分页">
            <span aria-live="polite">第 {{ currentHistoryPage }} / {{ historyPageCount }} 页</span>
            <button class="icon-button" type="button" aria-label="上一页连接记录" :disabled="currentHistoryPage === 1" @click="historyPage = currentHistoryPage - 1"><ChevronLeft :size="16" aria-hidden="true" /></button>
            <button class="icon-button" type="button" aria-label="下一页连接记录" :disabled="currentHistoryPage === historyPageCount" @click="historyPage = currentHistoryPage + 1"><ChevronRight :size="16" aria-hidden="true" /></button>
          </nav>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.overview { display: grid; gap: 16px; min-width: 0; }
.overview-update { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; color: var(--muted); font-size: 12px; }
.overview-sampling { display: inline-flex; align-items: center; gap: 8px; }
.overview-sampling i { width: 6px; height: 6px; flex: none; border-radius: 50%; background: var(--muted); }
.overview-sampling i.online { background: var(--success); }
.overview-metrics, .traffic-legend { margin: 0; }
.overview-card { min-width: 0; border: 1px solid var(--line); border-radius: 6px; background: var(--paper); }
.overview-metrics { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); overflow: hidden; border: 1px solid var(--line); border-radius: 6px; background: var(--paper); }
.metric { padding: 16px 20px; border: 0; border-radius: 0; }
.metric + .metric { border-left: 1px solid var(--line); }
.metric dt, .traffic-legend dt { display: flex; align-items: center; gap: 8px; color: var(--muted); font-size: 12px; }
.metric dt svg { color: var(--muted); }
.metric dd, .traffic-legend dd { margin: 0; font-variant-numeric: tabular-nums; }
.metric dd { margin-top: 8px; color: var(--heading); font-size: clamp(20px, 2.2vw, 26px); font-weight: 650; letter-spacing: -.025em; line-height: 1.3; overflow-wrap: anywhere; }
.overview-grid { display: grid; grid-template-columns: minmax(0, 1fr); align-items: start; gap: 16px; }
.overview-side { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); align-items: stretch; gap: 20px; min-width: 0; }
.traffic-panel, .overview-details { display: grid; grid-template-columns: minmax(0, 1fr); align-content: start; gap: 16px; padding: 20px; }
.overview-card-title { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; color: var(--muted); }
.overview-card-title > svg { flex: none; }
.overview-card-title h2 { color: var(--heading); font-size: 16px; font-weight: 600; }
.overview-card-title .overview-note, .core-state { margin-left: auto; }
.overview-note { color: var(--muted); font-size: 12px; }
.traffic-legend { display: flex; gap: 40px; flex-wrap: wrap; }
.traffic-legend dd { margin-top: 6px; color: var(--heading); font-size: 22px; font-weight: 600; letter-spacing: -.02em; }
.traffic-legend i { width: 16px; border-top: 2px solid var(--accent); }
.traffic-legend i.upload-line { border-top: 2px dashed var(--success); }
.core-state { display: inline-flex; gap: 6px; align-items: center; padding: 3px 8px; border-radius: 4px; background: var(--surface); color: var(--muted); font-size: 12px; }
.core-state.online { color: var(--success); background: var(--success-bg); }
.core-state.offline { color: var(--error); background: var(--error-bg); }
.core-state i, .event-indicator { width: 6px; height: 6px; flex: none; border-radius: 50%; background: currentColor; }
.connection-events { list-style: none; padding: 0; margin: 0; }
.connection-events li { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 6px 10px; align-items: center; padding-block: 12px; font-size: 13px; }
.connection-events li + li { border-top: 1px solid var(--line); }
.event-indicator { color: var(--error); }
.event-indicator.restored { color: var(--success); }
.connection-events time { grid-column: 2; color: var(--muted); font-size: 12px; font-variant-numeric: tabular-nums; }
.events-empty { display: grid; place-content: center; min-height: 148px; padding: 24px; color: var(--muted); font-size: 13px; text-align: center; }
.history-pagination { display: flex; align-items: center; gap: 4px; color: var(--muted); font-size: 12px; }
.history-pagination > span { margin-right: auto; }
.history-pagination .icon-button { width: 28px; height: 28px; }
@media (min-width: 1000px) {
  .overview { flex: 1; min-height: 0; grid-template-rows: auto auto minmax(0, 1fr); gap: 12px; }
  .metric { padding: 12px 16px; }
  .metric dd { margin-top: 4px; font-size: 24px; }
  .overview-grid { min-height: 0; grid-template-columns: minmax(0, 1fr) 340px; grid-template-rows: minmax(0, 1fr); align-items: stretch; gap: 12px; }
  .overview-side { min-height: 0; grid-template-columns: minmax(0, 1fr); grid-template-rows: auto minmax(min-content, 1fr); gap: 12px; }
  .traffic-panel, .overview-details { padding: 12px; gap: 8px; }
  .traffic-panel { min-height: 0; grid-template-rows: auto auto minmax(140px, 1fr); }
  .traffic-panel:has(> p) { grid-template-rows: auto auto minmax(140px, 1fr) auto; }
  .traffic-panel :deep(canvas) { height: 100%; min-height: 140px; }
  .traffic-legend { gap: 28px; }
  .traffic-legend > div { display: flex; align-items: baseline; gap: 12px; }
  .traffic-legend dd { margin-top: 0; font-size: 20px; }
  .history-panel { align-content: start; }
  .connection-events li { padding-block: 6px; gap: 2px 8px; font-size: 12px; }
  .events-empty { min-height: 64px; padding: 12px; }
}
@media (max-width: 1100px) {
  .metric { padding: 16px; }
}
@media (min-width: 1000px) and (max-height: 700px) {
  .overview, .overview-grid, .overview-side { gap: 8px; }
  .overview-update .button { min-height: 28px; padding: 3px 10px; font-size: 12px; }
  .metric { padding: 8px 12px; }
  .metric dd { font-size: 22px; }
}
@media (min-width: 601px) and (max-width: 999px) {
  .overview-side { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 600px) {
  .overview, .overview-grid, .overview-side { gap: 16px; }
  .overview-metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .metric { padding: 16px; }
  .metric:nth-child(3) { border-left: 0; }
  .metric:nth-child(n + 3) { border-top: 1px solid var(--line); }
  .overview-details, .traffic-panel { padding: 16px; gap: 16px; }
  .overview-side { grid-template-columns: minmax(0, 1fr); }
  .traffic-legend { gap: 24px; }
  .traffic-legend dd { font-size: 20px; }
}
</style>
