<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ArrowUpToLine, CirclePause, Play, Search, ScrollText, Trash2, X } from '@lucide/vue'
import { ElOption, ElSelect } from 'element-plus'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/option/style/css'
import { errorText } from './api'

const props = defineProps<{ active: boolean; connected: boolean }>()
type Entry = { id: number; time: string; type: string; payload: string; entering: boolean; highlightUntil: number }
const levels = [
  { value: 'debug', label: '调试 · debug' },
  { value: 'info', label: '信息 · info' },
  { value: 'warning', label: '警告 · warning' },
  { value: 'error', label: '错误 · error' },
]
const maxEntries = 1000
const highlightDuration = 1200
const entries = ref<Entry[]>([])
const highlightTime = ref(Date.now())
const list = ref<HTMLUListElement>()
const level = ref('all')
const query = ref('')
const paused = ref(false)
const autoScroll = ref(true)
const state = ref('等待内核连接')
const failure = ref('')
const clock = new Intl.DateTimeFormat('zh-CN', { timeStyle: 'medium', hour12: false })
const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
let sequence = 0
let timer: ReturnType<typeof setTimeout> | undefined
let highlightTimer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let disposed = false
const visible = computed(() => {
  const search = query.value.trim().toLowerCase()
  return entries.value.filter(entry => (level.value === 'all' || entry.type === level.value) &&
    entry.payload.toLowerCase().includes(search)).reverse()
})

function scrollToLatest() {
  void nextTick(() => {
    if (props.active && autoScroll.value && list.value) list.value.scrollTop = 0
  })
}

watch(() => visible.value[0]?.id, scrollToLatest)
watch(() => props.active, active => {
  if (active) scrollToLatest()
  else resetEntryEffects()
})
watch([() => props.active, () => props.connected, paused], syncConnection)
watch([level, query], resetEntryEffects)

function finishEntryAnimations() {
  for (const entry of entries.value) entry.entering = false
}

function resetEntryEffects() {
  finishEntryAnimations()
  for (const entry of entries.value) entry.highlightUntil = 0
  clearTimeout(highlightTimer)
  highlightTimer = undefined
}

// One timer tracks the next expiry, including when many entries arrive together.
function scheduleHighlights() {
  if (highlightTimer !== undefined) return
  const next = Math.min(...entries.value.map(entry => entry.highlightUntil).filter(until => until > highlightTime.value))
  if (!Number.isFinite(next)) return
  highlightTimer = setTimeout(() => {
    highlightTimer = undefined
    highlightTime.value = Date.now()
    scheduleHighlights()
  }, Math.max(0, next - Date.now()))
}

function trackScroll() {
  const element = list.value
  if (element) autoScroll.value = element.scrollTop < 24
}

function toggleAutoScroll() {
  autoScroll.value = !autoScroll.value
  if (autoScroll.value) scrollToLatest()
}

function ingest(line: string) {
  try {
    const entry = JSON.parse(line)
    if (typeof entry?.type !== 'string' || typeof entry?.payload !== 'string') return
    const now = Date.now()
    const visibleArrival = props.active && !document.hidden
    highlightTime.value = now
    entries.value.push({
      id: sequence++, time: clock.format(now), type: entry.type, payload: entry.payload,
      entering: visibleArrival && !reducedMotion.matches,
      highlightUntil: visibleArrival ? now + highlightDuration : 0,
    })
    if (entries.value.length > maxEntries) entries.value.shift()
    scheduleHighlights()
  } catch {
    // Ignore individual malformed events while keeping the stream connected.
  }
}

async function connect() {
  if (disposed || !props.active || !props.connected || document.hidden || paused.value || controller) return
  clearTimeout(timer)
  const attempt = new AbortController()
  controller = attempt
  state.value = '正在连接'
  try {
    const response = await fetch('/api/logs', { signal: attempt.signal })
    if (!response.ok || !response.body) {
      const error = await response.json().catch(() => null)
      throw new Error(error?.message ?? `无法读取日志（HTTP ${response.status}）。`)
    }
    if (attempt.signal.aborted) return
    failure.value = ''
    state.value = '实时接收'
    const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
    let buffer = ''
    try {
      while (!attempt.signal.aborted) {
        const chunk = await reader.read()
        if (chunk.done || attempt.signal.aborted) break
        buffer += chunk.value
        const lines = buffer.split('\n')
        buffer = lines.pop() ?? ''
        for (const line of lines) ingest(line)
      }
      if (!attempt.signal.aborted && buffer.trim()) ingest(buffer)
    } finally {
      reader.releaseLock()
    }
  } catch (error) {
    if (!attempt.signal.aborted) failure.value = errorText(error)
  } finally {
    if (controller === attempt) controller = undefined
    if (!attempt.signal.aborted && !disposed && props.active && props.connected && !document.hidden && !paused.value) {
      state.value = '等待重连'
      timer = setTimeout(connect, failure.value ? 2500 : 250)
    }
  }
}

function stop() {
  clearTimeout(timer)
  controller?.abort()
  controller = undefined
}

function toggle() {
  paused.value = !paused.value
}

function syncConnection() {
  if (props.active && props.connected && !document.hidden && !paused.value) {
    void connect()
    return
  }
  stop()
  failure.value = ''
  state.value = !props.connected ? '等待内核连接' : paused.value ? '已暂停' : '已停止接收'
}

onMounted(() => {
  reducedMotion.addEventListener('change', finishEntryAnimations)
  document.addEventListener('visibilitychange', syncConnection)
  syncConnection()
})
onUnmounted(() => {
  reducedMotion.removeEventListener('change', finishEntryAnimations)
  document.removeEventListener('visibilitychange', syncConnection)
  clearTimeout(highlightTimer)
  disposed = true
  stop()
})
</script>

<template>
  <section v-if="active" class="logs-view" aria-label="Mihomo 日志">
    <div class="view-toolbar logs-toolbar">
      <ElSelect v-model="level" class="log-level-select" aria-label="日志级别">
        <ElOption label="所有级别" value="all" />
        <ElOption v-for="item in levels" :key="item.value" :label="item.label" :value="item.value" />
      </ElSelect>
      <label class="search-box logs-search">
        <Search :size="16" aria-hidden="true" />
        <input v-model="query" type="search" placeholder="搜索日志内容" aria-label="搜索日志内容">
        <button v-if="query" type="button" class="search-clear" aria-label="清除日志搜索" @click="query = ''"><X :size="14" aria-hidden="true" /></button>
      </label>
      <div class="log-toolbar-actions">
        <button type="button" class="button secondary" :aria-pressed="autoScroll" @click="toggleAutoScroll">
          <ArrowUpToLine :size="16" aria-hidden="true" /><span>{{ autoScroll ? '跟随最新' : '跟随已关闭' }}</span>
        </button>
        <button type="button" class="button secondary" @click="toggle">
          <Play v-if="paused" :size="16" aria-hidden="true" /><CirclePause v-else :size="16" aria-hidden="true" />{{ paused ? '继续接收' : '暂停接收' }}
        </button>
        <button type="button" class="icon-button danger" :disabled="!entries.length" title="清空日志" aria-label="清空日志列表" @click="entries = []"><Trash2 :size="16" aria-hidden="true" /></button>
      </div>
    </div>
    <p v-if="!connected" class="inline-message" role="status">Mihomo 尚未连接，连接内核后将开始接收日志。</p>
    <p v-else-if="failure && !paused" class="inline-message error" role="alert">{{ failure }}</p>
    <div class="log-console">
      <div class="log-column-head" aria-hidden="true"><span>时间</span><span>级别</span><span>日志内容</span></div>
      <ul v-if="visible.length" ref="list" class="log-list" tabindex="0" aria-label="日志条目" @scroll.passive="trackScroll">
        <li v-for="entry in visible" :key="entry.id" class="log-line" :class="{ 'log-line--entering': entry.entering, 'log-line--latest': entry.highlightUntil > highlightTime }" :data-level="entry.type" @animationend.self="entry.entering = false">
          <time>{{ entry.time }}</time>
          <span class="log-level">{{ entry.type }}</span>
          <span class="log-message">{{ entry.payload }}</span>
        </li>
      </ul>
      <div v-else class="log-empty">
        <ScrollText :size="28" :stroke-width="1.5" aria-hidden="true" />
        <p>{{ query || level !== 'all' ? '没有匹配的日志' : !connected ? '等待内核连接' : paused ? '日志接收已暂停' : '等待 Mihomo 输出日志' }}</p>
        <span>{{ query || level !== 'all' ? '调整级别或搜索关键词后重试。' : !connected ? '请先在概览中安装或连接 Mihomo 内核。' : paused ? '继续接收后，新日志会显示在这里。' : '收到的日志会实时显示在这里。' }}</span>
      </div>
      <footer class="log-footer">
        <span class="log-state" role="status"><i :class="{ live: state === '实时接收' }" aria-hidden="true"></i>{{ state }}</span>
        <span>{{ visible.length }} 条{{ query || level !== 'all' ? `，共 ${entries.length} 条` : '' }}<span class="log-limit"> · 最多保留 {{ maxEntries }} 条</span></span>
      </footer>
    </div>
  </section>
</template>

<style scoped>
.logs-view { display: grid; gap: 16px; min-width: 0; }
.logs-toolbar { position: static; display: flex; gap: 8px; padding: 0; border: 0; border-radius: 0; background: transparent; }
.log-level-select { flex: 0 0 144px; width: 144px; }
.logs-search { flex: 1; width: auto; max-width: 400px; }
.search-clear { display: inline-grid; place-items: center; flex: none; width: 24px; height: 24px; padding: 0; border: 0; border-radius: 4px; background: transparent; color: var(--muted); }
.search-clear:hover { color: var(--ink); background: var(--hover); }
.log-toolbar-actions { display: flex; align-items: center; gap: 8px; margin-left: auto; }
.log-toolbar-actions [aria-pressed='true'] { color: var(--accent-ink); border-color: var(--live-line); background: var(--selection); }
.log-console { min-width: 0; border-block: 1px solid var(--line); border-radius: 0; overflow: hidden; background: var(--paper); }
.log-column-head { display: grid; grid-template-columns: 80px 72px minmax(0, 1fr); align-items: center; gap: 12px; padding: 12px 16px; border-bottom: 1px solid var(--line); background: var(--surface); color: var(--muted); font-size: 12px; }
.log-list { height: max(280px, calc(100dvh - 294px)); min-height: 280px; overflow: auto; margin: 0; padding: 0; border: 0; border-radius: 0; list-style: none; background: var(--paper); scrollbar-gutter: stable; }
.log-line { display: grid; grid-template-columns: 80px 72px minmax(0, 1fr); align-items: baseline; gap: 12px; padding: 11px 16px; font: 12px/1.7 var(--mono); transition: background-color 160ms ease-out, box-shadow 160ms ease-out; }
.log-line--entering { animation: log-entry-in 160ms ease-out; }
@keyframes log-entry-in {
  from { opacity: 0; transform: translateY(-4px); }
  to { opacity: 1; transform: translateY(0); }
}
@media (prefers-reduced-motion: reduce) {
  .log-line--entering { animation: none; }
  .log-line { transition: none; }
}
.log-line + .log-line { border-top: 1px solid var(--line); }
.log-line time { color: var(--muted); font-variant-numeric: tabular-nums; white-space: nowrap; }
.log-line:hover { background: var(--surface); }
.log-line--latest, .log-line--latest:hover { background: var(--selection); box-shadow: inset 3px 0 var(--accent); }
.log-message { min-width: 0; color: var(--ink); overflow-wrap: anywhere; white-space: pre-wrap; }
.log-level { justify-self: start; padding: 1px 6px; border-radius: 4px; font: 500 11px/1.7 var(--mono); color: var(--log-color, var(--muted)); background: var(--log-bg, var(--surface)); }
.log-line[data-level='debug'] { --log-color: var(--log-debug); --log-bg: var(--log-debug-bg); }
.log-line[data-level='info'] { --log-color: var(--log-info); --log-bg: var(--log-info-bg); }
.log-line[data-level='warning'] { --log-color: var(--log-warning); --log-bg: var(--log-warning-bg); }
.log-line[data-level='error'] { --log-color: var(--error); --log-bg: var(--error-bg); }
.log-empty { display: grid; place-content: center; justify-items: center; gap: 12px; min-height: max(280px, calc(100dvh - 294px)); padding: 32px; border: 0; border-radius: 0; background: var(--paper); text-align: center; color: var(--muted); }
.log-empty p { color: var(--ink); font-size: 14px; font-weight: 500; }
.log-empty > span { font-size: 12px; }
.log-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 16px; border-top: 1px solid var(--line); color: var(--muted); font-size: 12px; }
.log-state { display: inline-flex; align-items: center; gap: 6px; width: auto; margin: 0; order: 0; }
.log-state i { width: 6px; height: 6px; flex: none; border-radius: 50%; background: var(--muted); }
.log-state i.live { background: var(--success); }
@media (max-width: 1100px) {
  .logs-search { max-width: none; }
  .log-toolbar-actions { flex: 1 0 100%; justify-content: flex-end; }
}
@media (max-width: 600px) {
  .logs-view { gap: 12px; }
  .log-level-select { flex-basis: 132px; width: 132px; }
  .logs-search { min-width: 0; }
  .log-toolbar-actions { justify-content: flex-start; }
  .log-toolbar-actions .button { flex: 1; }
  .log-column-head { display: none; }
  .log-line { grid-template-columns: 80px minmax(0, 1fr); gap: 6px 12px; padding: 12px; }
  .log-message { grid-column: 1 / -1; }
  .log-list { height: max(260px, calc(100dvh - 320px)); }
  .log-empty { min-height: max(260px, calc(100dvh - 320px)); padding: 24px 12px; }
  .log-footer { padding-inline: 12px; }
  .log-limit { display: none; }
}
</style>
