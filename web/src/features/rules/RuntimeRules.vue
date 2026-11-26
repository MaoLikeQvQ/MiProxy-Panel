<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { ChevronDown, ChevronRight, CircleDot, GitBranch, GripVertical, LoaderCircle, Pencil, Plus, RefreshCw, Search, Trash2 } from '@lucide/vue'
import { errorText, request } from '../../shared/api'
import type { RuntimeGroup, RuntimeRule } from '../../shared/api'
import { useRuleDrag } from './useRuleDrag'
import GroupNodes from '../groups/GroupNodes.vue'
import type { RuleProviderEntry } from '../configuration/yaml'

const props = defineProps<{
  applying: boolean; revision?: number; reorder?: (from: RuntimeRule, to: RuntimeRule) => Promise<boolean>
  configured?: string[] | null; collections?: RuleProviderEntry[]; draft?: boolean; externalConfig?: boolean
  canAdd?: boolean; canUndo?: boolean; canEdit?: (rule: RuntimeRule) => boolean; refreshConfig?: () => Promise<void>
}>()
const emit = defineEmits<{ add: []; edit: [rule: RuntimeRule]; remove: [rule: RuntimeRule]; undo: [] }>()
const rules = ref<RuntimeRule[]>([])
const groups = ref<RuntimeGroup[]>([])
const query = ref('')
const expanded = ref<number | null>(null)
const loading = ref(true)
const pendingRule = ref<number | null>(null)
const switching = ref(false)
const loadError = ref('')
const groupError = ref('')
const operationError = ref('')
const announcement = ref('')
const expandedCollection = ref<number | null>(null)
const usingConfiguration = computed(() => props.draft || Boolean(loadError.value))
const configuredRules = computed<RuntimeRule[]>(() => (props.configured ?? []).map((text, index) => {
  const parts = text.split(',')
  const type = parts[0].toUpperCase()
  const proxyIndex = parts.at(-1)?.toLowerCase() === 'no-resolve' ? parts.length - 2 : parts.length - 1
  return {
    index, type: type === 'SUB-RULE' ? 'SubRules' : type === 'RULE-SET' ? 'RuleSet' : type,
    payload: type === 'MATCH' ? '' : parts.slice(1, proxyIndex).join(','),
    proxy: parts[proxyIndex] ?? '',
  }
}))
const shownRules = computed(() => usingConfiguration.value ? configuredRules.value : rules.value)
function collectionFor(rule: RuntimeRule) {
  return props.collections?.find(item => item.name === (rule.type === 'SubRules' ? rule.proxy : rule.payload))
}
const groupsByName = computed(() => new Map(groups.value.map(group => [group.name, group])))
const matches = computed(() => {
  const text = query.value.trim().toLowerCase()
  return shownRules.value.filter(rule => !text || [rule.type, rule.payload, rule.proxy, groupsByName.value.get(rule.proxy)?.now]
    .some(field => field?.toLowerCase().includes(text)))
})
const visible = computed(() => matches.value.map(rule => ({ rule, group: rule.type === 'SubRules' ? undefined : groupsByName.value.get(rule.proxy), collection: collectionFor(rule) })))
const list = ref<HTMLElement>()
const reordering = ref(false)
let pendingFocus: number | undefined
let refreshVersion = 0
function focusRule(index: number) {
  pendingFocus = index
  if (loading.value) return
  const handle = list.value?.querySelector<HTMLElement>(`[data-rule-index="${index}"] [data-rule-handle]`)
  if (handle) {
    handle.focus({ preventScroll: true })
    handle.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    pendingFocus = undefined
  }
}
defineExpose({ focusRule })
const busy = computed(() => loading.value || props.applying || pendingRule.value !== null || switching.value || reordering.value)
const { draggedIndex, dropIndex, saving, start, key } = useRuleDrag(list, reorder, () => busy.value || !props.reorder || !!loadError.value)

watch(query, () => { expanded.value = null; if (list.value) list.value.scrollTop = 0 })
watch(() => props.revision, () => { void refresh() })

async function reorder(fromIndex: number, toIndex: number) {
  const from = shownRules.value.find(rule => rule.index === fromIndex)
  const to = shownRules.value.find(rule => rule.index === toIndex)
  if (!from || !to || !props.reorder || busy.value) return false
  reordering.value = true
  operationError.value = announcement.value = ''
  expanded.value = null
  try {
    const moved = await props.reorder(from, to)
    return moved
  } catch (error) {
    operationError.value = errorText(error)
    return false
  } finally {
    reordering.value = false
  }
}

async function refreshAll() {
  await props.refreshConfig?.()
  await refresh()
}

async function refresh() {
  const version = ++refreshVersion
  const scrollTop = list.value?.scrollTop ?? 0
  const previousQuery = query.value
  loading.value = true
  loadError.value = groupError.value = operationError.value = ''
  const [active, proxies] = await Promise.allSettled([
    request<RuntimeRule[]>('/api/rules'),
    request<RuntimeGroup[]>('/api/groups'),
  ])
  if (version !== refreshVersion) return
  if (active.status === 'fulfilled') {
    rules.value = active.value
    expanded.value = null
    expandedCollection.value = null
  } else {
    loadError.value = errorText(active.reason)
  }
  if (proxies.status === 'fulfilled') groups.value = proxies.value
  else groupError.value = errorText(proxies.reason)
  loading.value = false
  await nextTick()
  if (version !== refreshVersion) return
  if (list.value) list.value.scrollTop = previousQuery === query.value ? scrollTop : 0
  if (pendingFocus !== undefined) focusRule(pendingFocus)
}

function toggleGroup(index: number) {
  expanded.value = expanded.value === index ? null : index
  operationError.value = ''
}

async function toggleRule(rule: RuntimeRule) {
  if (!rule.extra || busy.value || loadError.value) return
  pendingRule.value = rule.index
  operationError.value = announcement.value = ''
  try {
    const { index, type, payload, proxy } = rule
    const result = await request<{ disabled: boolean }>('/api/rules/state', { index, type, payload, proxy, disabled: !rule.extra.disabled })
    rule.extra.disabled = result.disabled
    announcement.value = `规则 ${index + 1} 已${result.disabled ? '临时停用' : '启用'}。`
  } catch (error) {
    operationError.value = errorText(error)
  } finally {
    pendingRule.value = null
  }
}

async function select(group: RuntimeGroup, name: string) {
  if (busy.value || group.now === name || groupError.value) return
  switching.value = true
  operationError.value = announcement.value = ''
  try {
    await request('/api/groups', { group: group.name, name })
    group.now = name
    announcement.value = `${group.name} 已切换到 ${name}。`
  } catch (error) {
    operationError.value = errorText(error)
  } finally {
    switching.value = false
  }
}

onMounted(refresh)
</script>

<template>
  <section class="runtime-rules" aria-label="规则列表">
    <div class="view-toolbar runtime-rules-toolbar">
      <label class="search-box">
        <Search :size="15" aria-hidden="true" />
        <input v-model="query" type="search" placeholder="搜索规则、策略或节点" aria-label="搜索规则">
      </label>
      <span class="entry-count">{{ query.trim() ? `${matches.length} / ${shownRules.length}` : shownRules.length }} 条规则</span>
      <div class="runtime-rule-actions">
      <button type="button" class="button secondary icon-control" :disabled="busy || saving"
        title="刷新规则" aria-label="刷新规则" @click="refreshAll">
        <RefreshCw :size="15" :class="{ spinner: loading }" aria-hidden="true" />
      </button>
      <button type="button" class="button primary" :disabled="!canAdd || applying" @click="emit('add')"><Plus :size="15" aria-hidden="true" />添加规则</button>
      </div>
    </div>
    <p v-if="operationError || loadError || groupError" class="inline-message error" role="alert">{{ operationError || loadError || groupError }}</p>
    <p v-if="announcement && !operationError" class="inline-message success" role="status">{{ announcement }}</p>
    <p v-if="canUndo" class="inline-message" role="status">规则已删除。<button type="button" class="button quiet compact" :disabled="busy" @click="emit('undo')">撤销删除</button></p>
    <p v-if="draft" class="inline-message" role="status">{{ externalConfig ? '当前修改仅保存在草稿中，请导出后在原应用应用。' : '以下为配置内容，尚未成功重载到内核。' }}</p>
    <div v-if="loading && !shownRules.length" class="empty-state runtime-rules-empty" role="status">正在读取规则…</div>
    <div v-else-if="!visible.length && !loadError" class="empty-state runtime-rules-empty">{{ query ? '没有匹配的规则。' : '还没有分流规则。' }}</div>
    <ol v-else ref="list" class="runtime-rule-list" aria-label="可排序规则">
      <li v-for="{ rule, group, collection } in visible" :key="rule.index" class="runtime-rule-card" :data-rule-index="rule.index"
        :class="{ 'rule-disabled': rule.extra?.disabled, 'rule-dragged': draggedIndex === rule.index, 'rule-drop-target': dropIndex === rule.index && draggedIndex !== rule.index }"
        :aria-busy="pendingRule === rule.index || reordering">
        <div class="runtime-rule-summary" :class="{ 'without-handle': !props.reorder }">
          <button v-if="props.reorder" type="button" class="rule-drag-handle" data-rule-handle
            :disabled="busy || saving || !!loadError" :aria-label="`拖动排序规则 ${rule.index + 1}，或用上下方向键移动`"
            title="拖动排序，或用上下方向键移动" @pointerdown="start($event, rule.index)" @keydown="key($event, rule.index)">
            <GripVertical :size="16" aria-hidden="true" />
          </button>
          <div class="runtime-rule-heading">
            <span class="runtime-rule-index" :aria-label="'规则 ' + (rule.index + 1)">{{ rule.index + 1 }}</span>
            <span class="runtime-rule-type">{{ rule.type === 'SubRules' ? '订阅规则组' : rule.type }}</span>
            <strong v-if="rule.type === 'SubRules'">{{ rule.proxy }}</strong><strong v-else-if="rule.payload">{{ rule.payload }}</strong><strong v-else class="rule-fallback">{{ rule.type.toUpperCase() === 'MATCH' ? '所有未匹配流量' : '—' }}</strong>
            <span v-if="rule.extra?.hitCount" class="runtime-rule-hits">命中 {{ rule.extra.hitCount }}</span>
          </div>
          <div class="runtime-rule-controls">
            <button v-if="collection?.rules?.length" type="button" class="rule-policy rule-group-policy" :aria-expanded="expandedCollection === rule.index"
              @click="expandedCollection = expandedCollection === rule.index ? null : rule.index">
              {{ collection.rules.length }} 条规则<ChevronDown :size="13" aria-hidden="true" />
            </button>
            <button v-if="group" type="button" class="rule-policy rule-group-policy"
              :aria-expanded="expanded === rule.index" :aria-controls="'rule-group-' + rule.index"
              :title="'展开 ' + group.name" @click="toggleGroup(rule.index)">
              <GitBranch :size="13" aria-hidden="true" />
              <span>{{ group.name }}</span>
              <ChevronDown :size="13" aria-hidden="true" />
            </button>
            <span v-else class="rule-policy">{{ rule.type === 'SubRules' ? '保留组内策略' : rule.proxy }}</span>
            <template v-if="group?.now">
              <ChevronRight :size="13" class="rule-policy-arrow" aria-hidden="true" />
              <span class="rule-current-node">{{ group.now }}</span>
            </template>
            <span v-if="pendingRule === rule.index" class="runtime-rule-state"><LoaderCircle :size="13" class="spinner" aria-hidden="true" />更新中</span><span v-else-if="rule.extra?.disabled" class="runtime-rule-state">已停用</span>
            <button v-if="rule.extra" type="button" class="runtime-rule-switch" role="switch" :aria-checked="!rule.extra.disabled"
              :aria-label="'启用规则 ' + (rule.index + 1)" :title="rule.extra.disabled ? '重新启用' : '临时停用，重启后恢复'"
              :disabled="busy || saving || !!loadError" @click="toggleRule(rule)"><span></span></button>
            <button v-if="canEdit?.(rule)" type="button" class="icon-button" :disabled="busy" :aria-label="'编辑规则 ' + (rule.index + 1)" title="编辑规则" @click="emit('edit', rule)"><Pencil :size="15" /></button>
            <button v-if="canEdit?.(rule)" type="button" class="icon-button danger" :disabled="busy" :aria-label="'删除规则 ' + (rule.index + 1)" title="删除规则" @click="emit('remove', rule)"><Trash2 :size="15" /></button>
          </div>
        </div>
        <section v-if="collection?.rules?.length && expandedCollection === rule.index" class="runtime-rule-expansion" :aria-label="collection.name + ' 的规则'">
          <ol class="collection-rules"><li v-for="(text, index) in collection.rules" :key="index">{{ text }}</li></ol>
        </section>
        <section v-if="group && expanded === rule.index" :id="'rule-group-' + rule.index"
          class="runtime-rule-expansion" :aria-label="group.name + ' 的节点'">
          <div class="runtime-rule-group-head">
            <span><strong>{{ group.name }}</strong><small>{{ group.type }} · {{ group.all.length }} 个成员</small></span>
            <span v-if="group.now" class="runtime-rule-outbound"><CircleDot :size="14" aria-hidden="true" />{{ group.now }}</span>
          </div>
          <GroupNodes :key="group.name" :group="group" :disabled="busy || saving || !!groupError" @select="select(group, $event)" />
        </section>
      </li>
    </ol>
  </section>
</template>


<style scoped>
.runtime-rules { display: flex; flex-direction: column; gap: 12px; min-width: 0; min-height: 0; height: 100%; overflow: hidden; }
.runtime-rules-toolbar { position: static; flex: none; background: transparent; padding: 0; border: 0; gap: 8px; }
.runtime-rule-actions { display: flex; flex: none; gap: 8px; margin-left: auto; }
.runtime-rules-toolbar .search-box { flex: 1 1 240px; max-width: 420px; }
.runtime-rules > .inline-message { flex: none; max-height: 100px; overflow: auto; margin: 0; }
.runtime-rules-empty { flex: 1; min-height: 0; overflow: auto; background: var(--paper); border: 1px solid var(--line); border-radius: 0; }
.runtime-rule-list { flex: 1; min-height: 0; margin: 0; list-style: none; padding: 0; border: 1px solid var(--line); border-radius: 0; overflow: auto; overscroll-behavior: contain; scrollbar-gutter: stable; background: var(--paper); }
.runtime-rule-card { content-visibility: auto; contain-intrinsic-size: auto 64px; }
.runtime-rule-card.rule-dragged { opacity: .45; }
.runtime-rule-card.rule-drop-target { outline: 2px solid var(--accent); outline-offset: -2px; background: var(--selection); }
.rule-drag-handle { display: inline-grid; place-items: center; flex: none; align-self: stretch; width: 28px; min-height: 36px; padding: 0; border: 0; border-radius: 4px; color: var(--muted); background: transparent; touch-action: none; user-select: none; cursor: grab; }
.rule-drag-handle:hover:not(:disabled) { color: var(--accent); background: var(--selection); }
.rule-drag-handle:active:not(:disabled) { cursor: grabbing; }
.runtime-rule-card + .runtime-rule-card { border-top: 1px solid var(--line); }
.runtime-rule-summary { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 12px; min-height: 52px; }
.runtime-rule-card:hover .runtime-rule-summary { background: var(--surface); }
.runtime-rule-heading { display: flex; flex: 1; flex-wrap: wrap; align-items: center; gap: 6px 10px; min-width: 0; }
.runtime-rule-index { min-width: 24px; color: var(--muted); font: 12px/1.5 var(--mono); font-variant-numeric: tabular-nums; }
.runtime-rule-type { padding: 3px 7px; border-radius: 4px; background: var(--surface); color: var(--muted); font: 11px/1.5 var(--mono); }
.runtime-rule-heading strong { min-width: 0; color: var(--heading); font: 500 13px/1.6 var(--mono); overflow-wrap: anywhere; }
.runtime-rule-heading .rule-fallback { font-family: inherit; font-size: 14px; }
.runtime-rule-hits { color: var(--muted); font-size: 12px; white-space: nowrap; }
.runtime-rule-controls { display: flex; justify-content: flex-end; flex-wrap: wrap; align-items: center; gap: 6px; min-width: 0; max-width: 48%; }
.rule-policy { display: inline-flex; align-items: center; gap: 5px; min-width: 0; min-height: 28px; padding: 3px 7px; border: 1px solid var(--line); border-radius: 5px; color: var(--ink); font-size: 12px; background: var(--paper); }
.rule-group-policy { color: var(--accent); border-color: transparent; background: var(--selection); }
.rule-group-policy:hover { border-color: var(--accent); }
.rule-group-policy[aria-expanded='true'] > svg:last-child { transform: rotate(180deg); }
.rule-policy span { max-width: 190px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-policy > svg { flex: none; }
.rule-policy-arrow { flex: none; color: var(--muted); }
.rule-current-node { color: var(--muted); font-size: 12px; max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.runtime-rule-state { display: inline-flex; align-items: center; gap: 4px; color: var(--muted); font-size: 12px; }
.rule-disabled .runtime-rule-heading strong { color: var(--muted); }
.runtime-rule-switch { display: inline-flex; align-items: center; flex: none; width: 32px; height: 18px; padding: 2px; border: 0; border-radius: 12px; background: var(--control); margin-left: 6px; }
.runtime-rule-switch > span { width: 14px; height: 14px; border-radius: 50%; background: var(--paper); transition: transform 140ms ease; }
.runtime-rule-switch[aria-checked='true'] { background: var(--accent); }
.runtime-rule-switch[aria-checked='true'] > span { background: var(--on-accent); transform: translateX(14px); }
.runtime-rule-expansion { padding: 16px 20px 20px; border-top: 1px solid var(--line); background: var(--surface); }
.runtime-rule-group-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.runtime-rule-group-head > span:first-child { display: grid; gap: 3px; }
.runtime-rule-group-head strong { color: var(--heading); font-size: 14px; }
.runtime-rule-group-head small { color: var(--muted); font-size: 12px; }
.runtime-rule-outbound { display: inline-flex; align-items: center; gap: 5px; min-width: 0; color: var(--accent); font-size: 12px; overflow-wrap: anywhere; }

.collection-rules { margin: 0; padding-left: 32px; font: 12px/1.9 var(--mono); overflow-wrap: anywhere; }

@media (max-width: 1000px) {
  .runtime-rule-summary { display: grid; grid-template-columns: 28px minmax(0, 1fr); align-items: start; gap: 8px; }
  .runtime-rule-summary > .rule-drag-handle { grid-row: 1 / 3; }
  .runtime-rule-summary.without-handle { grid-template-columns: minmax(0, 1fr); }
  .without-handle .runtime-rule-heading, .without-handle .runtime-rule-controls { grid-column: 1; }
  .runtime-rule-heading { grid-column: 2; }
  .runtime-rule-controls { grid-column: 2; max-width: 100%; padding-left: 0; justify-content: flex-start; }
}
@media (max-width: 600px) {
  .runtime-rules { gap: 12px; }
  .runtime-rules-toolbar .search-box { max-width: none; flex-basis: 100%; }
  .runtime-rule-summary:not(.without-handle) { grid-template-columns: 44px minmax(0, 1fr); padding: 12px 8px; }
  .rule-drag-handle { width: 44px; min-height: 44px; }
  .runtime-rule-controls { padding-left: 0; gap: 8px; width: 100%; }
  .runtime-rule-heading { column-gap: 8px; }
  .runtime-rule-heading strong { flex: 1; padding-left: 0; }
  .runtime-rule-hits { padding-left: 0; }
  .rule-policy { min-height: 36px; }
  .rule-current-node { max-width: 130px; }
  .runtime-rule-switch { position: relative; margin-left: auto; }
  .runtime-rule-switch::after { position: absolute; content: ''; inset: -13px -6px; }
  .runtime-rule-expansion { padding: 12px 8px; }
  .runtime-rule-group-head { align-items: flex-start; }
}
@media (prefers-reduced-motion: reduce) { .runtime-rule-switch > span { transition: none; } }
</style>
