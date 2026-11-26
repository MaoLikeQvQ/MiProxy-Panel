<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { ArrowRight, CloudDownload, FileUp, Check, Link2, LoaderCircle, MoreHorizontal, Plus, RefreshCw, Search, Trash2 } from '@lucide/vue'
import { ElDropdown, ElDropdownItem, ElDropdownMenu } from 'element-plus'
import 'element-plus/es/components/dropdown/style/css'
import { errorText, request } from '../../shared/api'

type Usage = { upload: number; download: number; total: number; expire: number }
type Subscription = { id: string; name: string; kind: 'url' | 'file'; updated: string; usage?: Usage }

const props = defineProps<{ disabled: boolean; stagedId: string; externalConfig?: boolean; confirmDelete: (name: string) => Promise<boolean> }>()
const emit = defineEmits<{ stage: [id: string] }>()
const useSavedOverride = defineModel<boolean>('useSavedOverride', { required: true })
const entries = ref<Subscription[]>([])
const loading = ref(true)
const importing = ref(false)
const importError = ref('')
const listError = ref('')
const notice = ref('')
const rowBusy = ref('')
const rowOperation = ref<'update' | 'delete'>()
const modal = ref<HTMLDialogElement>()
const name = ref('')
const url = ref('')
const file = ref<File>()
const method = ref<'url' | 'file'>('url')
const fileInput = ref<HTMLInputElement>()
const query = ref('')
const hasEntries = computed(() => entries.value.length > 0)
const shown = computed(() => entries.value.filter(entry => entry.name.toLowerCase().includes(query.value.trim().toLowerCase())))
const knownSize = 2 * 1024 * 1024
const date = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short', hour12: false })

function formatDate(value: string | number) {
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? '—' : date.format(parsed)
}

function size(value: number) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  const rank = value > 0 ? Math.min(units.length - 1, Math.floor(Math.log(value) / Math.log(1024))) : 0
  return `${(value / 1024 ** rank).toFixed(rank ? 1 : 0)} ${units[rank]}`
}

async function openImport() {
  method.value = 'url'
  file.value = undefined
  if (fileInput.value) fileInput.value.value = ''
  name.value = url.value = importError.value = ''
  await nextTick()
  modal.value?.showModal()
}

function switchImportMethod(next: 'url' | 'file') {
  if (method.value === next) return
  method.value = next
  file.value = undefined
  importError.value = ''
}

async function load() {
  loading.value = true
  listError.value = ''
  try {
    entries.value = await request<Subscription[]>('/api/subscriptions')
  } catch (error) {
    listError.value = errorText(error)
  } finally {
    loading.value = false
  }
}

function chooseFile(event: Event) {
  file.value = (event.target as HTMLInputElement).files?.[0]
  if (file.value && !name.value.trim()) name.value = file.value.name
}

async function add() {
  if (method.value === 'file' && (!file.value || file.value.size > knownSize)) {
    importError.value = '请选择 2 MiB 以内的 YAML 文件。'
    return
  }
  importing.value = true
  importError.value = ''
  try {
    const payload = method.value === 'url' ? { url: url.value } : { content: await file.value!.text() }
    const entry = await request<Subscription>('/api/subscriptions', { name: name.value, ...payload })
    if (!entries.value.some(item => item.id === entry.id)) entries.value.push(entry)
    notice.value = `已导入 ${entry.name}。`
    modal.value?.close()
    url.value = ''
  } catch (error) {
    importError.value = errorText(error)
  } finally {
    importing.value = false
  }
}

async function act(entry: Subscription, operation: 'update' | 'delete') {
  if (rowBusy.value) return
  rowBusy.value = entry.id
  rowOperation.value = operation
  listError.value = notice.value = ''
  try {
    if (operation === 'delete' && !await props.confirmDelete(entry.name)) return
    const path = `/api/subscriptions/${entry.id}`
    if (operation === 'update') {
      const updated = await request<Subscription>(path, {})
      entries.value = entries.value.map(item => item.id === updated.id ? updated : item)
      notice.value = `已更新 ${entry.name}，可重新应用订阅。`
    } else {
      await request(path, undefined, 'DELETE')
      entries.value = entries.value.filter(item => item.id !== entry.id)
      notice.value = `已移除 ${entry.name}。`
    }
  } catch (error) {
    listError.value = errorText(error)
  } finally {
    rowBusy.value = ''
    rowOperation.value = undefined
  }
}

onMounted(load)
</script>

<template>
  <section class="subscription-view" aria-label="订阅管理">
    <div class="view-toolbar subscription-toolbar">
      <label class="search-box"><Search :size="15" /><input v-model="query" type="search" placeholder="搜索订阅" aria-label="搜索订阅"></label>
      <span class="entry-count">{{ query.trim() ? `${shown.length} / ${entries.length}` : entries.length }} 个订阅</span>
      <label class="subscription-option"><input v-model="useSavedOverride" type="checkbox" :disabled="disabled">载入时叠加覆写</label>
      <button class="button secondary icon-control" title="刷新订阅列表" aria-label="刷新订阅列表" :disabled="loading || !!rowBusy" @click="load"><RefreshCw :size="16" :class="{ spinner: loading }" aria-hidden="true" /></button>
      <button type="button" class="button primary subscription-import" @click="openImport">
        <Plus :size="16" aria-hidden="true" />导入订阅
      </button>
    </div>

    <p v-if="listError" class="inline-message error" role="alert">{{ listError }}</p>
    <p v-else-if="notice" class="inline-message success" role="status">{{ notice }}</p>
    <div v-if="loading && !hasEntries" class="empty-state subscription-empty" role="status">正在读取订阅…</div>
    <div v-else-if="!hasEntries && !listError" class="empty-state subscription-empty">
      <span class="empty-icon"><CloudDownload :size="24" :stroke-width="1.6" /></span>
      <h2>还没有订阅</h2>
      <p>可导入 Clash / Mihomo 链接或本地 YAML 文件。</p>
    </div>
    <div v-else-if="hasEntries" class="table-frame">
      <table class="subscription-table">
        <thead><tr><th scope="col">订阅</th><th scope="col">流量</th><th scope="col">更新时间</th><th scope="col">操作</th></tr></thead>
        <tbody>
          <tr v-for="entry in shown" :key="entry.id" :class="{ staged: stagedId === entry.id }" :aria-busy="rowBusy === entry.id">
            <td class="subscription-name-cell">
              <span class="subscription-icon"><Link2 v-if="entry.kind === 'url'" :size="17" /><FileUp v-else :size="17" /></span>
              <div class="subscription-name"><strong>{{ entry.name }}</strong><span>{{ entry.kind === 'url' ? '远程订阅' : '本地 YAML' }}<span v-if="stagedId === entry.id" class="subscription-staged"><Check :size="12" aria-hidden="true" />{{ externalConfig ? '已载入配置' : '已应用' }}</span></span></div>
            </td>
            <td class="subscription-quota-cell">
              <div class="quota">
                <template v-if="entry.usage && entry.usage.total > 0">
                  <strong>{{ size(entry.usage.upload + entry.usage.download) }} / {{ size(entry.usage.total) }}</strong>
                  <progress :aria-label="entry.name + ' 已用流量'" :value="Math.min(1, (entry.usage.upload + entry.usage.download) / entry.usage.total)" max="1"></progress>
                </template>
                <span v-else class="hint">无流量数据</span>
                <span v-if="entry.usage?.expire" class="hint">到期 {{ formatDate(entry.usage.expire * 1000) }}</span>
              </div>
            </td>
            <td class="subscription-time-cell"><time :datetime="entry.updated">{{ formatDate(entry.updated) }}</time></td>
            <td class="subscription-actions-cell">
              <div class="subscription-actions">
                <button class="button secondary compact" :disabled="disabled || !!rowBusy || loading" @click="emit('stage', entry.id)">
                  {{ externalConfig ? '载入配置' : stagedId === entry.id ? '重新应用' : '应用订阅' }}<ArrowRight :size="14" />
                </button>
                <button v-if="entry.kind === 'url'" class="icon-button" :disabled="!!rowBusy || loading" :aria-label="'更新 ' + entry.name" title="更新订阅" @click="act(entry, 'update')">
                  <RefreshCw :size="15" :class="{ spinner: rowBusy === entry.id && rowOperation === 'update' }" />
                </button>
                <ElDropdown trigger="click" :disabled="!!rowBusy || loading" @command="act(entry, 'delete')">
                  <button type="button" class="icon-button" :disabled="!!rowBusy || loading" :aria-label="entry.name + ' 的更多操作'" title="更多操作">
                    <LoaderCircle v-if="rowBusy === entry.id && rowOperation === 'delete'" :size="17" class="spinner" aria-hidden="true" /><MoreHorizontal v-else :size="17" aria-hidden="true" />
                  </button>
                  <template #dropdown>
                    <ElDropdownMenu>
                      <ElDropdownItem command="delete" class="subscription-delete"><Trash2 :size="15" aria-hidden="true" />移除订阅</ElDropdownItem>
                    </ElDropdownMenu>
                  </template>
                </ElDropdown>
              </div>
            </td>
          </tr>
          <tr v-if="!shown.length"><td colspan="4" class="table-empty">没有匹配的订阅。</td></tr>
        </tbody>
      </table>
    </div>

    <dialog ref="modal" class="confirmation import-dialog" aria-labelledby="import-title" @cancel="importing && $event.preventDefault()">
      <form @submit.prevent="add">
        <h2 id="import-title">导入订阅</h2>
        <div class="view-tabs import-method" role="group" aria-label="导入方式">
          <button class="button quiet" type="button" :aria-pressed="method === 'url'" :disabled="importing" @click="switchImportMethod('url')"><Link2 :size="14" />从链接</button>
          <button class="button quiet" type="button" :aria-pressed="method === 'file'" :disabled="importing" @click="switchImportMethod('file')"><FileUp :size="14" />从文件</button>
        </div>
        <label v-if="method === 'file'" class="field">
          YAML 文件<input ref="fileInput" type="file" accept=".yaml,.yml,text/yaml,application/yaml" required :disabled="importing" @change="chooseFile">
        </label>
        <label v-else class="field">
          订阅链接<input v-model="url" type="url" required maxlength="4096" :disabled="importing" autocomplete="off" spellcheck="false" placeholder="https://" autofocus>
        </label>
        <label class="field">
          订阅名称<input v-model="name" required maxlength="100" :disabled="importing" autocomplete="off" placeholder="例如：常用订阅">
        </label>
        <p v-if="importError" class="inline-message error" role="alert">{{ importError }}</p>
        <div class="dialog-actions">
          <button type="button" class="button secondary" :disabled="importing" @click="modal?.close()">取消</button>
          <button class="button primary" :disabled="importing"><LoaderCircle v-if="importing" :size="16" class="spinner" aria-hidden="true" />{{ importing ? '正在导入…' : '导入' }}</button>
        </div>
      </form>
    </dialog>
  </section>
</template>


<style scoped>
.subscription-view { display: grid; gap: 16px; min-width: 0; }
.subscription-view .subscription-toolbar { padding: 0; gap: 8px; background: transparent; border: 0; }
.subscription-toolbar .search-box { flex: 1 1 220px; max-width: 340px; }
.subscription-toolbar .entry-count { margin-right: auto; }
.subscription-toolbar .subscription-option { min-height: 36px; margin-right: 8px; }
.subscription-import { margin-left: 0; }
.subscription-view > .inline-message { margin: 0; }
.subscription-empty { min-height: 280px; background: var(--paper); border: 1px solid var(--line); border-radius: 10px; }
.subscription-empty .empty-icon { margin: 0; width: 48px; height: 48px; }
.subscription-empty h2 { color: var(--heading); font-size: 16px; }
.subscription-table { table-layout: fixed; }
.subscription-table th, .subscription-table td { padding: 12px 16px; }
.subscription-table th { padding-block: 11px; }
.subscription-table th:first-child { width: 33%; }
.subscription-table th:nth-child(2) { width: 26%; }
.subscription-table th:nth-child(3) { width: 20%; }
.subscription-table th:last-child { width: 21%; min-width: 168px; }
.subscription-name-cell { overflow-wrap: anywhere; }
.subscription-name > span { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; margin-top: 3px; }
.subscription-staged { display: inline-flex; align-items: center; gap: 3px; color: var(--accent); }
.subscription-table .subscription-time-cell time { font-size: 12px; font-variant-numeric: tabular-nums; }
.subscription-actions { gap: 4px; }
.subscription-actions .button { min-height: 32px; font-size: 13px; padding-inline: 10px; }
.subscription-actions .icon-button { width: 32px; height: 32px; }
.quota { max-width: 190px; gap: 6px; }
.quota strong { font-size: 12px; font-weight: 500; }
.quota progress { height: 5px; }
.subscription-delete { gap: 8px; color: var(--error); }
@media (max-width: 1050px) {
  .subscription-toolbar .subscription-option { order: 5; flex: 1 0 100%; }
  .subscription-table th, .subscription-table td { padding-inline: 12px; }
  .subscription-table th:first-child { width: 30%; }
  .subscription-table th:nth-child(2) { width: 24%; }
  .subscription-table th:last-child { width: 26%; }
}
@media (max-width: 600px) {
  .subscription-view { gap: 12px; }
  .subscription-toolbar .search-box { flex-basis: calc(100% - 44px); max-width: none; }
  .subscription-toolbar .entry-count { order: 2; }
  .subscription-toolbar > .icon-control { order: 1; }
  .subscription-toolbar .subscription-import { order: 3; }
  .subscription-toolbar .subscription-option { order: 4; font-size: 12px; }
  .subscription-table { table-layout: auto; }
  .subscription-table tr { padding: 16px; gap: 12px 8px; }
  .subscription-table td { padding: 0; }
  .subscription-name { max-width: calc(100% - 48px); }
  .subscription-table td:nth-child(2) { grid-column: 1 / -1; }
  .subscription-table .subscription-time-cell { grid-column: 1 / -1; text-align: left; }
  .quota { max-width: none; }
  .quota strong { white-space: nowrap; }
  .subscription-table .subscription-time-cell::before { content: '更新于 ' ; color: var(--muted); font-size: 12px; }
  .subscription-actions { padding-top: 4px; }
  .subscription-actions .button, .subscription-actions .icon-button { min-height: 44px; }
  .subscription-actions .icon-button { width: 44px; }
  .subscription-empty { min-height: 240px; }
}
</style>
