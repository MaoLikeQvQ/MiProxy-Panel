<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { Check, Gauge, LoaderCircle, Search, X } from '@lucide/vue'
import { errorText, request } from '../../shared/api'
import type { RuntimeGroup } from '../../shared/api'
import { nodeDelays } from './nodeDelays'
import type { DelayDisplay } from './nodeDelays'

const props = defineProps<{
  group: RuntimeGroup
  disabled: boolean
  measure?: boolean
  switchingName?: string
  failedName?: string
  selectionError?: string
}>()
const emit = defineEmits<{ select: [name: string] }>()
type Delay = { delay: number } | { status: 'timeout' | 'unreachable' }
const filter = ref('')
const sort = ref('default')
const delays = nodeDelays
const pending = ref(new Set<string>())
const measuring = ref(false)
const measurementError = ref('')
const completed = ref(0)
const total = ref(0)
let controller: AbortController | undefined

const visibleNodes = computed(() => {
  const query = filter.value.trim().toLowerCase()
  const nodes = props.group.all.filter(node => node.toLowerCase().includes(query))
  if (sort.value === 'name') nodes.sort((a, b) => a.localeCompare(b, 'zh-CN'))
  if (sort.value === 'delay') nodes.sort((a, b) => (delays.value.get(a)?.value ?? Infinity) - (delays.value.get(b)?.value ?? Infinity))
  return nodes
})

function stopMeasurement() {
  controller?.abort()
  controller = undefined
  pending.value = new Set()
  measuring.value = false
}

watch(() => props.group.name, () => {
  stopMeasurement()
  filter.value = ''
  measurementError.value = ''
  completed.value = total.value = 0
})
onUnmounted(stopMeasurement)

function displayDelay(result: Delay): DelayDisplay {
  if ('status' in result) return { label: result.status === 'timeout' ? '超时' : '不可达', tone: 'failed' }
  const { delay } = result
  const tone = delay < 300 ? 'fast' : delay < 800 ? 'medium' : 'slow'
  return { label: `${delay} ms`, value: delay, tone }
}

async function measureNodes(names: string[]) {
  if (measuring.value || props.disabled || !names.length) return
  const batch = new AbortController()
  controller = batch
  measuring.value = true
  measurementError.value = ''
  const nodes = [...new Set(names)]
  pending.value = new Set(nodes)
  completed.value = 0
  total.value = nodes.length
  let next = 0
  async function worker() {
    while (next < nodes.length && !batch.signal.aborted) {
      const name = nodes[next++]
      try {
        const result = await request<Delay>('/api/nodes/delay', { name }, 'POST', batch.signal)
        if (!batch.signal.aborted) delays.value.set(name, displayDelay(result))
      } catch (error) {
        if (batch.signal.aborted) return
        delays.value.set(name, { label: '失败', tone: 'failed' })
        measurementError.value ||= errorText(error)
      } finally {
        if (!batch.signal.aborted) {
          pending.value.delete(name)
          completed.value++
        }
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(4, nodes.length) }, worker))
  if (controller === batch) {
    measuring.value = false
    controller = undefined
  }
}
</script>

<template>
  <div class="nodes" :class="{ 'nodes--measured': measure }">
    <div class="node-tools">
      <label v-if="measure || group.all.length > 8" class="search-box node-search">
        <Search :size="16" aria-hidden="true" />
        <input v-model="filter" type="search" placeholder="搜索节点" aria-label="搜索节点">
        <button v-if="filter" type="button" class="search-clear" aria-label="清除节点搜索" @click="filter = ''"><X :size="14" /></button>
      </label>
      <h3 v-else>组内节点</h3>
      <div v-if="measure" class="node-tools-actions">
        <select v-model="sort" aria-label="节点排序"><option value="default">默认排序</option><option value="delay">延迟优先</option><option value="name">名称排序</option></select>
        <button type="button" class="button secondary" :disabled="disabled || measuring || !group.all.length" @click="measureNodes(group.all)">
          <LoaderCircle v-if="measuring" :size="16" class="spinner" aria-hidden="true" />
          <Gauge v-else :size="16" aria-hidden="true" />{{ measuring ? '测速中' : '整组测速' }}
        </button>
      </div>
    </div>
    <div v-if="measure && total > 1" class="measurement-progress" role="status">
      <span>{{ measuring ? '正在测速' : '测速完成' }}</span><span>{{ completed }} / {{ total }}</span>
      <progress :value="completed" :max="total" aria-label="整组测速进度"></progress>
    </div>
    <p v-if="measure && measurementError" class="node-measure-error" role="alert">部分节点测速失败，可重新运行整组测速。{{ measurementError }}</p>
    <p v-if="!visibleNodes.length" class="empty-state">{{ filter ? '没有匹配的节点' : '该组没有可用成员' }}</p>
    <template v-else>
      <div class="node-grid" :class="{ 'node-grid--measured': measure }">
        <div v-for="name in visibleNodes" :key="name" class="node-entry" :class="{ 'node-entry--selected': group.now === name, 'node-entry--failed': failedName === name && selectionError }">
          <component :is="group.type === 'Selector' ? 'button' : 'div'" :type="group.type === 'Selector' ? 'button' : undefined" class="node"
            :aria-pressed="group.type === 'Selector' ? group.now === name : undefined"
            :aria-label="group.type === 'Selector' ? `${name}${group.now === name ? '，当前使用' : '，切换到此节点'}` : undefined"
            :class="{ selected: group.now === name }" :disabled="group.type === 'Selector' && disabled ? true : undefined"
            @click="group.type === 'Selector' && emit('select', name)">
            <span class="node-card-heading">
              <span class="node-name" :title="name">{{ name }}</span>
              <LoaderCircle v-if="switchingName === name" :size="16" class="spinner" aria-label="切换中" />
              <Check v-else-if="group.now === name" :size="16" aria-hidden="true" />
            </span>
            <span v-if="measure && (pending.has(name) || delays.has(name))" class="node-delay-value"
              :data-tone="pending.has(name) ? undefined : delays.get(name)?.tone">
              {{ pending.has(name) ? '测速中' : delays.get(name)?.label }}
            </span>
          </component>
          <p v-if="measure && failedName === name && selectionError" class="node-selection-error" role="alert">切换失败。{{ selectionError }} <button type="button" :disabled="disabled" @click="emit('select', name)">重试</button></p>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.nodes { min-width: 0; }
.node-tools { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; margin: 12px 0; }
.node-tools h3 { margin: 0; color: var(--heading); font-size: 14px; font-weight: 600; }
.nodes--measured .node-tools { margin: 20px 0 12px; }
.node-search { flex: 1; min-width: 150px; max-width: 280px; }
.node-tools-actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; }
.node-tools-actions select { min-height: 36px; font-size: 12px; }
.search-clear { display: inline-grid; place-items: center; flex: none; width: 24px; height: 24px; padding: 0; border: 0; border-radius: 4px; background: transparent; color: var(--muted); }
.search-clear:hover { background: var(--hover); color: var(--heading); }
.measurement-progress { display: flex; align-items: center; gap: 8px; margin: 0 0 12px; color: var(--muted); font-size: 12px; font-variant-numeric: tabular-nums; }
.measurement-progress progress { width: 100px; height: 4px; border: 0; border-radius: 4px; overflow: hidden; accent-color: var(--accent); }
.measurement-progress progress::-webkit-progress-bar { background: var(--line); }
.measurement-progress progress::-webkit-progress-value { background: var(--accent); }
.node-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 148px), 1fr)); gap: 8px; max-width: 100%; }
.node-grid--measured { grid-template-columns: repeat(auto-fill, minmax(min(100%, 220px), 1fr)); gap: 12px; }
.node-entry { min-width: 0; max-width: 100%; }
.node { display: flex; align-items: center; justify-content: space-between; gap: 8px; width: 100%; min-height: 36px; padding: 8px 12px; border: 1px solid var(--line); border-radius: 6px; background: var(--paper); color: var(--ink); text-align: left; }
button.node:hover:not(:disabled) { border-color: var(--control); background: var(--surface); }
.node.selected, button.node.selected:hover:not(:disabled) { color: var(--accent-ink); background: var(--selection); border-color: var(--accent); }
.node-card-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; min-width: 0; width: 100%; }
.node-card-heading > svg { flex: none; margin-top: 2px; color: var(--accent); }
.node-name { min-width: 0; overflow-wrap: anywhere; }
.nodes--measured .node { align-items: center; min-height: 44px; height: 100%; padding: 10px 12px; gap: 12px; font-size: 13px; line-height: 1.5; }
.nodes--measured .node-card-heading { flex: 1; width: auto; align-items: center; }
.nodes--measured .node-card-heading > svg { margin-top: 0; }
.nodes--measured .node-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.nodes--measured .node-delay-value { flex: none; white-space: nowrap; text-align: right; }
.nodes--measured .node:disabled { opacity: 1; }
.node-entry--failed .node { height: auto; border-color: var(--error); }
.node-delay-value { color: var(--muted); font: 12px/1.5 var(--mono); font-variant-numeric: tabular-nums; }
.node-delay-value[data-tone='fast'] { color: var(--success); }
.node-delay-value[data-tone='medium'] { color: var(--draft); }
.node-delay-value[data-tone='slow'], .node-delay-value[data-tone='failed'] { color: var(--error); }
.node-selection-error { margin-top: 8px; color: var(--error); font-size: 12px; overflow-wrap: anywhere; }
.node-selection-error button { padding: 0; border: 0; background: transparent; color: inherit; font: inherit; text-decoration: underline; text-underline-offset: 3px; }
.node-measure-error { margin: 0 0 12px; color: var(--error); font-size: 12px; overflow-wrap: anywhere; }
@media (max-width: 1100px) {
  .node-search { max-width: none; }
  .node-tools-actions { margin-left: auto; }
}
@media (max-width: 768px) {
  .node-search { flex-basis: 100%; }
  .node-tools-actions { width: 100%; }
  .node-tools-actions select { flex: 1; min-height: 44px; }
  .node-tools-actions .button { min-height: 44px; }
}
</style>
