<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { ArrowLeft, Check, ChevronRight, GitBranch, RefreshCw, Search, X } from '@lucide/vue'
import { errorText, request } from './api'
import type { RuntimeGroup } from './api'
import GroupNodes from './GroupNodes.vue'

const groups = ref<RuntimeGroup[]>([])
const selected = ref('')
const groupFilter = ref('')
const loading = ref(true)
const switching = ref(false)
const switchingName = ref('')
const loadError = ref('')
const switchError = ref('')
const failedName = ref('')
const announcement = ref('')
const mobileDetail = ref(false)
const groupList = ref<HTMLUListElement>()
const backButton = ref<HTMLButtonElement>()
let listScroll = 0
let pageScroll = 0

const visibleGroups = computed(() => {
  const query = groupFilter.value.trim().toLowerCase()
  return groups.value.filter(group => group.name.toLowerCase().includes(query))
})
const group = computed(() => groups.value.find(item => item.name === selected.value) ?? groups.value[0])
const typeNames: Record<string, string> = {
  Selector: '手动选择', URLTest: '自动测速', Fallback: '故障转移', LoadBalance: '负载均衡',
}

async function chooseGroup(name: string) {
  listScroll = groupList.value?.scrollTop ?? 0
  pageScroll = groupList.value?.closest('main')?.scrollTop ?? 0
  selected.value = name
  switchError.value = failedName.value = announcement.value = ''
  mobileDetail.value = true
  await nextTick()
  if (window.matchMedia('(max-width: 768px)').matches) backButton.value?.focus()
}

async function showGroups() {
  mobileDetail.value = false
  await nextTick()
  if (groupList.value) {
    groupList.value.scrollTop = listScroll
    groupList.value.querySelector<HTMLButtonElement>('[aria-pressed="true"]')?.focus({ preventScroll: true })
    const workspace = groupList.value.closest('main')
    if (workspace) workspace.scrollTop = pageScroll
  }
}

async function refresh() {
  loading.value = true
  loadError.value = ''
  try {
    const result = await request<RuntimeGroup[]>('/api/groups')
    const order = result.find(group => group.name === 'GLOBAL')?.all ?? []
    const positions = new Map(order.map((name, index) => [name, index]))
    groups.value = result.sort((a, b) =>
      (positions.get(a.name) ?? Infinity) - (positions.get(b.name) ?? Infinity) || a.name.localeCompare(b.name, 'zh-CN'))
    if (!groups.value.some(item => item.name === selected.value)) {
      selected.value = groups.value[0]?.name ?? ''
      mobileDetail.value = false
    }
  } catch (error) {
    loadError.value = errorText(error)
  } finally {
    loading.value = false
  }
}

async function select(current: RuntimeGroup, name: string) {
  if (current.type !== 'Selector' || current.now === name || switching.value) return
  switching.value = true
  switchingName.value = name
  switchError.value = failedName.value = announcement.value = ''
  try {
    await request('/api/groups', { group: current.name, name })
    current.now = name
    announcement.value = `已切换到 ${name}`
  } catch (error) {
    failedName.value = name
    switchError.value = errorText(error)
  } finally {
    switching.value = false
    switchingName.value = ''
  }
}

onMounted(refresh)
</script>

<template>
  <section class="runtime-groups" aria-label="运行中的代理组">
    <div v-if="loadError" class="inline-message error" role="alert">
      {{ loadError }}
      <button type="button" class="button quiet compact" :disabled="loading" @click="refresh">重试</button>
    </div>
    <div class="group-workspace" :class="{ 'has-mobile-detail': mobileDetail && group }">
      <section class="group-picker" aria-label="选择代理组">
        <div class="group-picker-heading">
          <h2>代理组 <span>{{ groups.length }}</span></h2>
          <button type="button" class="icon-button" title="刷新代理组" aria-label="刷新代理组" :disabled="loading || switching" @click="refresh">
            <RefreshCw :size="16" :class="{ spinner: loading }" aria-hidden="true" />
          </button>
        </div>
        <label class="search-box group-search">
          <Search :size="16" aria-hidden="true" />
          <input v-model="groupFilter" type="search" placeholder="搜索代理组" aria-label="搜索代理组">
          <button v-if="groupFilter" type="button" class="search-clear" aria-label="清除代理组搜索" @click="groupFilter = ''"><X :size="14" /></button>
        </label>
        <div v-if="loading && !groups.length" class="empty-state" role="status">正在读取代理组…</div>
        <div v-else-if="!visibleGroups.length" class="empty-state">{{ groupFilter ? '没有匹配的代理组' : '暂无代理组' }}</div>
        <ul v-else ref="groupList" class="group-picker-list">
          <li v-for="item in visibleGroups" :key="item.name">
            <button type="button" :aria-pressed="group?.name === item.name" :disabled="switching" @click="chooseGroup(item.name)">
              <GitBranch :size="17" class="group-picker-icon" aria-hidden="true" />
              <span class="group-picker-label">
                <strong :title="item.name">{{ item.name }}</strong>
                <small :title="item.now || item.type">{{ item.now || typeNames[item.type] || item.type }}</small>
              </span>
              <ChevronRight :size="15" aria-hidden="true" />
            </button>
          </li>
        </ul>
      </section>

      <section v-if="group" class="group-detail" aria-label="代理组详情">
        <button ref="backButton" type="button" class="button quiet mobile-group-back" @click="showGroups">
          <ArrowLeft :size="16" aria-hidden="true" />代理组
        </button>
        <div class="group-detail-head">
          <div class="group-detail-title">
            <h2>{{ group.name }}</h2>
            <span class="type-tag">{{ typeNames[group.type] || group.type }}</span>
          </div>
          <span class="group-node-count">{{ group.all.length }} 个节点</span>
        </div>
        <div class="active-node">
          <Check :size="16" aria-hidden="true" />
          <span>当前使用</span>
          <strong>{{ group.now || '自动选择' }}</strong>
        </div>
        <p v-if="announcement" class="group-notice" role="status"><Check :size="15" aria-hidden="true" />{{ announcement }}</p>
        <p v-if="group.type !== 'Selector'" class="group-auto-note">该组由 Mihomo 自动选择节点。</p>
        <GroupNodes :group="group" :disabled="switching || loading" :switching-name="switchingName"
          :failed-name="failedName" :selection-error="switchError" measure @select="select(group, $event)" />
      </section>
      <div v-else-if="!loading" class="group-detail empty-state">暂无可用代理组，载入配置后刷新。</div>
    </div>
  </section>
</template>

<style scoped>
.runtime-groups { min-width: 0; }
.runtime-groups > .inline-message { margin: 0 0 16px; }
.group-workspace { display: grid; grid-template-columns: 210px minmax(0, 1fr); align-items: stretch; gap: 24px; }
.group-picker, .group-detail { min-width: 0; background: var(--paper); }
.group-picker { padding: 0 16px 0 0; border-right: 1px solid var(--line); }
.group-picker-heading { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 8px; }
.group-picker-heading h2 { display: flex; align-items: center; gap: 8px; color: var(--heading); font-size: 14px; font-weight: 650; }
.group-picker-heading h2 span, .group-node-count { color: var(--muted); font-size: 12px; font-weight: 400; font-variant-numeric: tabular-nums; }
.group-search { width: 100%; max-width: none; }
.search-clear { display: inline-grid; place-items: center; flex: none; width: 24px; height: 24px; padding: 0; border: 0; border-radius: 4px; background: transparent; color: var(--muted); }
.search-clear:hover { background: var(--hover); color: var(--heading); }
.group-picker-list { display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px; max-height: calc(100dvh - 280px); overflow-y: auto; margin: 8px -4px 0; padding: 4px; list-style: none; }
.group-picker-list button { display: grid; grid-template-columns: 17px minmax(0, 1fr) 15px; align-items: center; gap: 8px; width: 100%; min-height: 52px; padding: 6px 8px; border: 1px solid transparent; border-radius: 6px; background: transparent; color: var(--muted); text-align: left; }
.group-picker-list button:hover:not(:disabled) { background: var(--hover); }
.group-picker-list button[aria-pressed='true'] { background: var(--selection); color: var(--accent); box-shadow: none; border-color: transparent; }
.group-picker-icon { display: block; width: 17px; height: 17px; border-radius: 0; background: transparent; color: currentColor; }
.group-picker-label { display: grid; min-width: 0; gap: 1px; }
.group-picker-label strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; font-weight: 600; color: var(--heading); }
.group-picker-label small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--muted); font-size: 12px; }
.group-picker-list button[aria-pressed='true'] strong { color: var(--accent); }
.group-detail { padding: 6px 0 0; }
.group-detail-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 0; border: 0; }
.group-detail-title { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; min-width: 0; }
.group-detail-title h2 { font-size: 16px; font-weight: 650; color: var(--heading); overflow-wrap: anywhere; }
.group-detail-title .type-tag { text-transform: none; }
.group-node-count { white-space: nowrap; }
.active-node { display: flex; align-items: baseline; gap: 6px; margin-top: 8px; color: var(--muted); font-size: 12px; }
.active-node > svg { flex: none; align-self: center; color: var(--accent); }
.active-node > span { flex: none; }
.active-node strong { min-width: 0; color: var(--heading); font-size: 12px; font-weight: 500; overflow-wrap: anywhere; }
.group-notice { display: flex; gap: 6px; align-items: center; color: var(--success); margin-top: 12px; font-size: 12px; overflow-wrap: anywhere; }
.group-notice svg { flex: none; }
.group-auto-note { margin-top: 12px; color: var(--muted); font-size: 12px; }
.mobile-group-back { display: none; }
@media (min-width: 769px) and (max-width: 1100px) {
  .group-workspace { grid-template-columns: 210px minmax(0, 1fr); gap: 16px; }
}
@media (max-width: 768px) {
  .group-workspace { grid-template-columns: minmax(0, 1fr); gap: 0; }
  .group-picker { padding: 0; border-right: 0; }
  .group-picker-list { max-height: none; }
  .group-picker-list button { min-height: 60px; }
  .group-picker-list button > svg { display: block; }
  .group-detail { display: none; padding: 0; }
  .has-mobile-detail .group-picker { display: none; }
  .has-mobile-detail .group-detail { display: block; }
  .mobile-group-back { display: inline-flex; min-height: 44px; padding: 0 4px; margin: -8px 0 8px; }
  .active-node { flex-wrap: wrap; row-gap: 4px; }
}
</style>
