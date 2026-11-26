<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { Files, LoaderCircle, Pencil, RefreshCw, Search, X } from '@lucide/vue'
import { errorText, request } from './api'
import { ruleProviderHost } from './yaml'
import type { RuleProviderEntry, RuleProviderSettings } from './yaml'

const props = defineProps<{
  entries: RuleProviderEntry[]
  visible: boolean
  disabled: boolean
  externalConfig: boolean
  canUpdate: boolean
  canReference: boolean
  runtimeRevision: number
  save: (settings: RuleProviderSettings, editing: boolean, reject: boolean) => Promise<void>
}>()
const emit = defineEmits<{ reference: [entry: RuleProviderEntry] }>()

const intervalUnits = [
  { seconds: 86400, label: '天' },
  { seconds: 3600, label: '小时' },
  { seconds: 60, label: '分钟' },
  { seconds: 1, label: '秒' },
]
const behaviors = [
  { value: 'classical', label: '经典规则（classical）' },
  { value: 'domain', label: '域名（domain）' },
  { value: 'ipcidr', label: 'IP 段（ipcidr）' },
]
const formats = ['yaml', 'text', 'mrs']
const date = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'short', hour12: false })

const query = ref('')
const shown = computed(() => {
  const text = query.value.trim().toLowerCase()
  return props.entries.filter(entry => [entry.name, ruleProviderHost(entry.url), entry.behavior, entry.format,
    entry.kind === 'sub-rule' ? '订阅规则组 保留各条策略' : '']
    .some(value => value?.toLowerCase().includes(text)))
})

const status = ref<Record<string, { updatedAt: string }>>({})
const checking = ref(false)
const updating = ref('')
const statusError = ref('')
const message = ref('')
const updateError = ref('')
const dialog = ref<HTMLDialogElement>()
const rulesDialog = ref<HTMLDialogElement>()
const viewedGroup = ref<RuleProviderEntry>()
const error = ref('')
const saving = ref(false)
const editing = ref(false)
const reject = ref(false)
const autoUpdate = ref(true)
const form = ref<RuleProviderSettings>({ name: '', url: '', format: 'yaml', behavior: 'classical', interval: 86400 })
const count = ref<number | ''>(1)
const unit = ref(86400)
type DetectionField = 'format' | 'behavior'
type Detection = { format: string; behavior?: string }
const detection = ref<{ state: 'pending' | 'success' | 'manual'; message: string }>()
const manualFields = new Set<DetectionField>()
let detectionTimer: number | undefined
let detectionRequest: AbortController | undefined

function cancelDetection() {
  window.clearTimeout(detectionTimer)
  detectionRequest?.abort()
  detectionRequest = undefined
  detection.value = undefined
}

async function detect(address: string, attempt: AbortController) {
  try {
    const result = await request<Detection>('/api/rule-providers/detect', { url: address }, 'POST', attempt.signal)
    if (detectionRequest !== attempt) return
    if (!manualFields.has('format')) form.value.format = result.format
    if (result.behavior && !manualFields.has('behavior')) form.value.behavior = result.behavior
    const format = result.format.toUpperCase()
    detection.value = result.behavior
      ? { state: 'success', message: `识别为 ${format} · ${result.behavior}。` }
      : { state: 'manual', message: `识别为 ${format}，请手动选择规则类型。` }
  } catch (failure) {
    if (detectionRequest !== attempt) return
    detection.value = { state: 'manual', message: `${errorText(failure)} 可以手动选择格式和类型。` }
  } finally {
    if (detectionRequest === attempt) detectionRequest = undefined
  }
}

watch(() => form.value.url.trim(), address => {
  manualFields.clear()
  cancelDetection()
  if (!dialog.value?.open || address.length > 4096 || !ruleProviderHost(address)) return
  const attempt = new AbortController()
  detectionRequest = attempt
  detection.value = { state: 'pending', message: '正在识别规则文件…' }
  detectionTimer = window.setTimeout(() => void detect(address, attempt), 650)
})
onUnmounted(cancelDetection)

function selectedUnit(seconds: number) {
  return intervalUnits.find(({ seconds: factor }) => seconds > 0 && seconds % factor === 0) ?? intervalUnits[3]
}

function intervalLabel(seconds: number) {
  if (seconds <= 0) return '仅手动更新'
  const factor = selectedUnit(seconds)
  return `每 ${seconds / factor.seconds} ${factor.label}`
}

function loaded(name: string) {
  return Object.hasOwn(status.value, name)
}

function lastUpdate(name: string) {
  if (!loaded(name)) return '—'
  const time = new Date(status.value[name].updatedAt)
  return time.getTime() > 0 ? date.format(time) : '—'
}

async function refreshStatus() {
  checking.value = true
  statusError.value = ''
  try {
    status.value = await request<Record<string, { updatedAt: string }>>('/api/rule-providers')
  } catch (error) {
    status.value = {}
    statusError.value = errorText(error)
  } finally {
    checking.value = false
  }
}

async function update(entry: RuleProviderEntry) {
  if (!props.canUpdate || updating.value || checking.value || !loaded(entry.name)) return
  updating.value = entry.name
  updateError.value = message.value = ''
  try {
    await request('/api/rule-providers/update', { name: entry.name })
    message.value = `已更新规则集 ${entry.name}。`
    await refreshStatus()
  } catch (error) {
    updateError.value = errorText(error)
  } finally {
    updating.value = ''
  }
}

async function open(entry?: RuleProviderEntry) {
  if (props.disabled) return
  cancelDetection()
  manualFields.clear()
  editing.value = entry !== undefined
  reject.value = false
  form.value = entry ? { ...entry, behavior: entry.behavior || 'classical' } :
    { name: '', url: '', format: 'yaml', behavior: 'classical', interval: 86400 }
  autoUpdate.value = form.value.interval > 0
  const interval = form.value.interval || 86400
  unit.value = selectedUnit(interval).seconds
  count.value = interval / unit.value
  error.value = ''
  await nextTick()
  dialog.value?.showModal()
}

async function viewRules(entry: RuleProviderEntry) {
  viewedGroup.value = entry
  await nextTick()
  rulesDialog.value?.showModal()
}

async function commit() {
  if (saving.value || props.disabled || detection.value?.state === 'pending') return
  error.value = ''
  saving.value = true
  try {
    if (autoUpdate.value && (count.value === '' || count.value <= 0)) throw new Error('请输入大于零的更新间隔。')
    const fields = form.value
    await props.save({
      ...fields,
      name: editing.value ? fields.name : fields.name.trim(),
      url: fields.url.trim(),
      interval: autoUpdate.value ? Number(count.value) * unit.value : 0,
    }, editing.value, !editing.value && reject.value)
    dialog.value?.close()
  } catch (errorValue) {
    error.value = errorText(errorValue)
  } finally {
    saving.value = false
  }
}

watch([() => props.visible, () => props.runtimeRevision], ([visible]) => {
  if (visible) void refreshStatus()
}, { immediate: true })
defineExpose({ open })
</script>

<template>
  <section v-show="visible" class="provider-view" aria-label="规则集列表">
    <div class="view-toolbar provider-toolbar">
      <label class="search-box"><Search :size="16" aria-hidden="true" /><input v-model="query" type="search" placeholder="搜索规则集、来源或类型" aria-label="搜索规则集"></label>
      <span class="entry-count">{{ query.trim() ? `${shown.length} / ${entries.length}` : entries.length }} 个规则集</span>
      <button class="button secondary compact" :disabled="checking || !!updating" @click="refreshStatus">
        <RefreshCw :size="16" :class="{ spinner: checking }" aria-hidden="true" />刷新状态
      </button>
    </div>
    <p v-if="statusError || updateError" class="inline-message error" role="alert">{{ updateError || statusError }}</p>
    <p v-if="message" class="inline-message success" role="status">{{ message }}</p>
    <div v-if="!entries.length" class="empty-state provider-empty">
      <Files :size="28" :stroke-width="1.6" aria-hidden="true" />
      <h2>还没有规则集</h2>
      <p>点击「订阅规则集」添加规则来源。</p>
    </div>
    <div v-else class="provider-table-frame"><table class="subscription-table provider-table">
      <thead><tr>
        <th scope="col">规则集</th><th scope="col">类型与格式</th><th scope="col">更新间隔</th>
        <th scope="col">内核最近更新</th><th scope="col">操作</th>
      </tr></thead>
      <tbody>
        <tr v-for="entry in shown" :key="entry.kind + '-' + entry.name" :aria-busy="entry.kind === 'http' && updating === entry.name">
          <td class="provider-name">
            <strong>{{ entry.name }}</strong>
            <small>{{ entry.kind === 'sub-rule' ? `订阅规则组 · ${entry.rules?.length ?? 0} 条规则` : ruleProviderHost(entry.url) || (entry.kind === 'file' ? '本地文件' : entry.kind === 'inline' ? '内联规则' : '在 YAML 中配置') }}</small>
          </td>
          <td><span v-if="entry.kind === 'sub-rule'" class="type-tag">保留各条策略</span><span v-else-if="entry.behavior" class="type-tag">{{ entry.behavior }} · {{ entry.format }}</span><span v-else>—</span></td>
          <td class="provider-interval">{{ entry.kind === 'http' ? intervalLabel(entry.interval) : '—' }}</td>
          <td class="provider-updated"><span class="provider-updated-label">最近更新 </span>{{ entry.kind === 'sub-rule' ? '—' : lastUpdate(entry.name) }}</td>
          <td class="subscription-actions-cell">
            <div class="subscription-actions">
              <button v-if="entry.kind === 'sub-rule'" type="button" class="button secondary compact" @click="viewRules(entry)">查看规则</button>
              <button class="button quiet compact" :disabled="disabled || !canReference || entry.name.includes(',')" @click="emit('reference', entry)">引用</button>
              <button v-if="entry.editable" class="icon-button" :disabled="disabled" title="编辑规则集" :aria-label="'编辑 ' + entry.name" @click="open(entry)">
                <Pencil :size="15" aria-hidden="true" />
              </button>
              <button v-if="entry.kind === 'http'" class="icon-button" :disabled="!canUpdate || checking || !!updating || !loaded(entry.name)"
                :title="canUpdate && loaded(entry.name) ? '立即更新' : '规则集生效后可更新'" :aria-label="'立即更新 ' + entry.name" @click="update(entry)">
                <RefreshCw :size="15" :class="{ spinner: updating === entry.name }" aria-hidden="true" />
              </button>
            </div>
          </td>
        </tr>
        <tr v-if="!shown.length"><td colspan="5" class="table-empty">没有匹配的规则集。</td></tr>
      </tbody>
    </table></div>
  </section>
  <dialog ref="rulesDialog" class="confirmation subscription-rules-dialog" aria-labelledby="subscription-rules-title" aria-describedby="subscription-rules-description">
    <header class="subscription-rules-heading">
      <div>
        <h2 id="subscription-rules-title">{{ viewedGroup?.name }}</h2>
        <p id="subscription-rules-description">{{ viewedGroup?.rules?.length ?? 0 }} 条规则，按订阅顺序匹配并保留各条策略。</p>
      </div>
      <button type="button" class="icon-button" autofocus aria-label="关闭规则列表" @click="rulesDialog?.close()"><X :size="18" aria-hidden="true" /></button>
    </header>
    <ol v-if="viewedGroup?.rules?.length" class="subscription-rules-list" tabindex="0" aria-label="订阅规则内容">
      <li v-for="(rule, index) in viewedGroup.rules" :key="index"><span class="subscription-rule-index" aria-hidden="true">{{ index + 1 }}</span><code>{{ rule }}</code></li>
    </ol>
    <p v-else class="empty-state">这个规则组暂无规则。</p>
  </dialog>
  <dialog ref="dialog" class="confirmation import-dialog provider-dialog" aria-labelledby="provider-dialog-title" @close="cancelDetection" @cancel="saving && $event.preventDefault()">
    <form @submit.prevent="commit" :aria-busy="saving">
      <fieldset class="provider-fields" :disabled="saving">
      <h2 id="provider-dialog-title">{{ editing ? '编辑规则集' : '订阅规则集' }}</h2>
      <label class="field">规则集名称<input v-model="form.name" :readonly="editing" :autofocus="!editing" required spellcheck="false"></label>
      <label class="field">规则集 URL<input v-model="form.url" :autofocus="editing" type="url" required spellcheck="false" autocomplete="off" placeholder="https://"
        :aria-describedby="detection ? 'provider-detection' : undefined"></label>
      <div v-if="detection" class="provider-detection" :class="detection.state">
        <LoaderCircle v-if="detection.state === 'pending'" :size="14" class="spinner" aria-hidden="true" />
        <span id="provider-detection" role="status">{{ detection.message }}</span>
        <button v-if="detection.state === 'pending'" type="button" class="button quiet compact" @click="cancelDetection">手动选择</button>
      </div>
      <div class="field-grid">
        <label class="field">规则类型<select v-model="form.behavior" @change="manualFields.add('behavior')">
          <option v-for="item in behaviors" :key="item.value" :value="item.value">{{ item.label }}</option>
        </select></label>
        <label class="field">文件格式<select v-model="form.format" @change="manualFields.add('format')">
          <option v-for="format in formats" :key="format" :value="format">{{ format.toUpperCase() }}</option>
        </select></label>
      </div>
      <label v-if="!editing" class="subscription-option provider-option">
        <input v-model="reject" type="checkbox">拦截匹配的流量
      </label>
      <label class="subscription-option provider-option"><input v-model="autoUpdate" type="checkbox">自动更新</label>
      <div v-if="autoUpdate" class="field">
        <label for="provider-interval">更新间隔</label>
        <div class="provider-period">
          <input id="provider-interval" v-model.number="count" type="number" min="1" step="1" required>
          <select v-model="unit" aria-label="间隔单位">
            <option v-for="item in intervalUnits" :key="item.seconds" :value="item.seconds">{{ item.label }}</option>
          </select>
        </div>
      </div>
      <p class="hint">{{ externalConfig ? '生效配置由外部应用管理。规则集生效后，由 Mihomo 按设置更新。' : '保存后立即生效，新规则集会优先匹配；默认直连，勾选拦截后拒绝匹配的流量。' }}</p>
      <p v-if="error" class="inline-message error" role="alert">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="button secondary" @click="dialog?.close()">取消</button>
        <button class="button primary" :disabled="disabled || detection?.state === 'pending'">{{ saving ? '正在保存…' : externalConfig ? '更新配置' : '保存并生效' }}</button>
      </div>
      </fieldset>
    </form>
  </dialog>
</template>

<style scoped>
.subscription-rules-dialog { width: min(calc(100% - 32px), 900px); max-height: min(85dvh, 800px); padding: 0; overflow: hidden; }
.subscription-rules-dialog[open] { display: flex; flex-direction: column; }
.subscription-rules-heading { display: flex; flex: none; justify-content: space-between; align-items: flex-start; gap: 16px; padding: 20px 24px; border-bottom: 1px solid var(--line); }
.subscription-rules-heading > div { min-width: 0; }
.subscription-rules-heading h2 { margin-bottom: 6px; overflow-wrap: anywhere; }
.subscription-rules-heading p { font-size: 13px; }
.subscription-rules-list { min-height: 0; margin: 0; padding: 8px 0; overflow: auto; overscroll-behavior: contain; list-style: none; }
.subscription-rules-list li { display: grid; grid-template-columns: 44px minmax(0, 1fr); gap: 12px; align-items: baseline; padding: 8px 24px; }
.subscription-rules-list li + li { border-top: 1px solid var(--line); }
.subscription-rule-index { color: var(--muted); text-align: right; font: 12px/1.6 var(--mono); font-variant-numeric: tabular-nums; }
.subscription-rules-list code { color: var(--ink); font: 12px/1.7 var(--mono); overflow-wrap: anywhere; white-space: pre-wrap; }
.provider-fields { border: 0; padding: 0; margin: 0; min-width: 0; }
.provider-view { display: grid; gap: 16px; min-width: 0; }
.provider-toolbar { gap: 8px; padding: 0; border: 0; background: transparent; }
.provider-toolbar .search-box { flex: 1 1 220px; max-width: 360px; }
.provider-view > .inline-message { margin: 0; }
.provider-empty { min-height: 260px; background: var(--paper); }
.provider-empty > svg { color: var(--accent); }
.provider-empty h2 { color: var(--heading); font-size: 16px; }
.provider-table-frame { overflow: hidden; border: 1px solid var(--line); border-radius: 10px; background: var(--paper); }
.provider-table th, .provider-table td { padding: 12px 16px; }
.provider-table th { padding-block: 11px; }
.provider-name { width: 30%; }
.provider-name strong { display: block; color: var(--heading); font-weight: 600; overflow-wrap: anywhere; }
.provider-name small { display: block; margin-top: 4px; color: var(--muted); font-size: 12px; overflow-wrap: anywhere; }
.provider-updated-label { display: none; }
.provider-interval, .provider-updated { color: var(--muted); font-size: 12px; font-variant-numeric: tabular-nums; }
.provider-option { margin-block: 12px; }
.provider-detection { display: flex; align-items: center; flex-wrap: wrap; gap: 6px 8px; margin-bottom: 16px; padding: 10px 12px; border-radius: 6px; background: var(--surface); color: var(--muted); font-size: 12px; }
.provider-detection.success { color: var(--success); background: var(--success-bg); }
.provider-detection .button { margin-left: auto; }
.provider-detection > svg { flex: none; }
.provider-period { display: grid; grid-template-columns: minmax(0, 1fr) 100px; gap: 8px; }
.provider-period input, .provider-period select { width: 100%; }
@media (max-width: 1050px) {
  .provider-table th, .provider-table td { padding-inline: 12px; }
}
@media (max-width: 600px) {
  .subscription-rules-heading { padding: 16px; }
  .subscription-rules-list li { grid-template-columns: 28px minmax(0, 1fr); gap: 8px; padding: 10px 12px; }
  .provider-view { gap: 12px; }
  .provider-toolbar .search-box { max-width: none; flex-basis: 100%; }
  .provider-table tr { gap: 8px; padding: 16px; }
  .provider-table td { padding: 0; }
  .provider-table .provider-name { width: auto; }
  .provider-table .provider-interval { text-align: right; }
  .provider-table .provider-updated { align-self: center; }
  .provider-updated-label { display: inline; }
  .provider-table .subscription-actions-cell { grid-column: 2; grid-row: 3; }
  .provider-table .subscription-actions { gap: 4px; }
  .provider-table .subscription-actions .button, .provider-table .subscription-actions .icon-button { min-height: 44px; }
  .provider-table .subscription-actions .icon-button { width: 44px; }
}
</style>
