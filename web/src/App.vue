<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import DraftInput from './DraftInput.vue'
import { isMap, isSeq, parseDocument } from 'yaml'
import type { Document } from 'yaml'
import {
  ArrowDownToLine, ArrowUpFromLine, Braces, Check, ChevronDown, Layers,
  CircleAlert, CloudDownload, FileCode2, GitBranch, LayoutDashboard, ListOrdered, LoaderCircle, Moon, Network, Plus,
  Redo2, RefreshCw, Save, Search, Settings2, Sun, Terminal, Trash2, Undo2, X,
} from '@lucide/vue'
import { ElOption, ElSelect } from 'element-plus'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/option/style/css'
import type Editor from './YamlEditor.vue'
import { useAppearance } from './theme'
import { applyOverride, editableMap, loadSubscription, readRuleProviders, readSubRuleGroups, subscriptionRuleCondition, scalar, setStrings, strings, writeRuleProvider } from './yaml'
import { errorText, request } from './api'
import type { CoreStatus, RuntimeGroup, RuntimeRule, Snapshot } from './api'
import Subscriptions from './Subscriptions.vue'
import RuntimeGroups from './RuntimeGroups.vue'
import RuntimeRules from './RuntimeRules.vue'
import RuntimeLogs from './RuntimeLogs.vue'
import RuntimeOverview from './RuntimeOverview.vue'
import Override from './Override.vue'

const YamlEditor = defineAsyncComponent(() => import('./YamlEditor.vue'))

const runtimeSections = [
  { key: 'overview', label: '概览', title: '运行概览', icon: LayoutDashboard },
  { key: 'groups', label: '代理组', title: '代理组', icon: GitBranch },
  { key: 'rules', label: '规则', title: '分流规则', icon: ListOrdered },
  { key: 'logs', label: '日志', title: '实时日志', icon: Terminal },
] as const
const configSections = [
  { key: 'subscriptions', label: '订阅', title: '订阅管理', icon: CloudDownload },
  { key: 'override', label: '覆写', title: '配置覆写', icon: Layers },
  { key: 'yaml', label: 'YAML 配置', title: 'YAML 配置', icon: Braces },
] as const
const sections = [...runtimeSections, ...configSections]
const mobileSections = runtimeSections.filter(item => item.key !== 'logs')
const moreSections = [runtimeSections[3], ...configSections]
const navigationGroups = [
  { label: '运行', items: runtimeSections },
  { label: '配置', items: configSections },
] as const

type Section = typeof sections[number]['key']
type Status = 'idle' | 'pending' | 'pass' | 'fail'
type Report = {
  validation: Status; saved: Status; applied: Status; message: string; detail?: string
  tone?: 'success' | 'error'; conflict?: boolean; viewDraft?: boolean
}
type ApplyResult = { saved: boolean; applied: boolean; backup: boolean; revision: string; detail?: string }

const dark = useAppearance()
function readSection(): Section {
  const key = window.location.hash.slice(1)
  if (key === 'rule-providers' || key === 'rule-draft') return 'rules'
  if (key === 'dns') return 'yaml'
  return sections.find(item => item.key === key)?.key ?? 'overview'
}

const section = ref<Section>(readSection())
const showsRuntimeRules = computed(() => section.value === 'rules')
const runtimeRulesView = ref<InstanceType<typeof RuntimeRules>>()
const activeSection = computed(() => sections.find(item => item.key === section.value) ?? runtimeSections[0])
const configActive = computed(() => moreSections.some(item => item.key === section.value))
const configMenu = ref<HTMLDialogElement>()
const workspace = ref<HTMLElement>()
const source = ref('')
const original = ref('')
const revision = ref<string | null>(null)
const exists = ref(false)
const externalConfig = ref(false)
const loading = ref(false)
const configReadFailed = ref(false)
const action = ref<'validate' | 'apply' | ''>('')
const pendingReload = ref(false)
const pendingGroupEdit = ref(false)
const overrideDirty = ref(false)
const core = ref<CoreStatus | null>(null)
const overviewView = ref<InstanceType<typeof RuntimeOverview>>()
const coreLabel = computed(() => core.value === null ? '正在检查 Mihomo' : core.value.connected ? 'Mihomo 已连接' : 'Mihomo 未连接')
const report = ref<Report>(idle())
const fileInput = ref<HTMLInputElement>()
const root = ref<HTMLElement>()
const workbenchBody = ref<HTMLElement>()
const yamlEditor = ref<InstanceType<typeof Editor>>()
const editorKey = ref(0)
const editorPosition = ref({ line: 1, column: 1, canUndo: false, canRedo: false })
const openGroup = ref<number | null>(0)
const undoRemoval = ref<string | null>(null)
const dialog = ref<HTMLDialogElement>()
const feedbackDialog = ref<HTMLDialogElement>()
const operationDialog = ref<HTMLDialogElement>()
const confirmation = ref({ title: '', message: '', action: '' })
const stagedId = ref('')
const staging = ref(false)
const groupTab = ref<'runtime' | 'configuration'>('runtime')
const activeView = computed(() => {
  if (section.value === 'groups') return `groups/${groupTab.value}`
  return section.value
})
watch(activeView, () => workspace.value?.scrollTo(0, 0), { flush: 'post' })
const runtimeRevision = ref(0)
const ruleDialog = ref<HTMLDialogElement>()
const newRule = ref('')
const editingRule = ref<number | null>(null)
const ruleType = ref('DOMAIN-SUFFIX')
const ruleValue = ref('')
const ruleTarget = ref('DIRECT')
const customRuleTarget = ref('')
const ruleProviderSource = ref('url')
const ruleProviderURL = ref('')
const ruleProviderFormat = ref('yaml')
const ruleProviderBehavior = ref('classical')
const ruleProviderInterval = ref(86400)
const ruleTypes = [
  { value: 'DOMAIN-SUFFIX', label: '域名及其子域名', placeholder: '例如 example.com' },
  { value: 'DOMAIN', label: '完整域名', placeholder: '例如 www.example.com' },
  { value: 'DOMAIN-KEYWORD', label: '域名包含关键词', placeholder: '例如 example' },
  { value: 'IP-CIDR', label: 'IPv4 地址或网段', placeholder: '例如 192.0.2.1 或 192.0.2.0/24' },
  { value: 'IP-CIDR6', label: 'IPv6 地址或网段', placeholder: '例如 2001:db8::1 或 2001:db8::/32' },
  { value: 'RULE-SET', label: '规则集', placeholder: '输入规则集名称' },
  { value: 'MATCH', label: '其余所有流量', placeholder: '' },
]
const rulePlaceholder = computed(() => ruleTypes.find(type => type.value === ruleType.value)?.placeholder)
const liveRuleTargets = ref<string[]>([])
const ruleTargetsLoading = ref(false)
const ruleError = ref('')
const useSavedOverride = ref(true)
let resolveConfirmation: ((accepted: boolean) => void) | undefined
const keyboard = /mac|iphone|ipad/i.test(navigator.platform) ? '⌘' : 'Ctrl+'

const busy = computed(() => loading.value || staging.value || action.value !== '')
const operationBusy = computed(() => staging.value || action.value !== '')
const operationTitle = computed(() => staging.value ? '正在载入订阅' : action.value === 'validate' ? '正在校验配置' : '正在应用配置')
const operationDetail = computed(() => staging.value ? '正在读取订阅并合并覆写…' : action.value === 'validate'
  ? '正在准备所需规则数据并校验配置，首次下载可能需要一些时间。' : '正在准备所需规则数据、校验配置并重载 Mihomo，首次下载可能需要一些时间。')
watch(operationBusy, active => {
  const modal = operationDialog.value
  if (active && modal && !modal.open) modal.showModal()
  else if (!active) modal?.close()
}, { flush: 'post' })
const dirty = computed(() => source.value !== original.value)
const hasConfigChanges = computed(() => dirty.value || pendingGroupEdit.value)
const editsConfiguration = computed(() => section.value === 'yaml' ||
  section.value === 'rules' || section.value === 'groups' && groupTab.value === 'configuration')
const showActionbar = computed(() => section.value === 'yaml' || section.value === 'groups' && groupTab.value === 'configuration' ||
  hasConfigChanges.value || pendingReload.value || configReadFailed.value || report.value.conflict ||
  section.value !== 'rules' && (!!report.value.message || !!action.value))
const canValidate = computed(() => !busy.value && revision.value !== null && Boolean(source.value.trim()))
const canApply = computed(() => !externalConfig.value && canValidate.value)
const lineCount = computed(() => source.value.split('\n').length)
const doc = computed(() => parseDocument(source.value, { uniqueKeys: true }))
const parsed = computed(() => source.value.trim() !== '' && doc.value.errors.length === 0 && isMap(doc.value.contents))
const rootEditable = computed(() => parsed.value && editableMap(doc.value, []))
const rules = computed(() => rootEditable.value ? strings(doc.value, ['rules']) : null)
const canAddRule = computed(() => !busy.value && revision.value !== null && (!source.value.trim() || rules.value !== null))
const groups = computed(() => {
  if (!rootEditable.value) return null
  const node = doc.value.getIn(['proxy-groups'], true)
  if (node === undefined) return []
  if (!isSeq(node)) return null
  return node.items.map((item, index) => ({
    index,
    label: scalar(doc.value, ['proxy-groups', index, 'name'], ''),
    type: scalar(doc.value, ['proxy-groups', index, 'type'], ''),
    members: strings(doc.value, ['proxy-groups', index, 'proxies']),
    editable: isMap(item) && !item.has('<<'),
  }))
})
const ruleProviders = computed(() => !source.value.trim() ? [] : rootEditable.value ? readRuleProviders(doc.value) : null)
const ruleCollections = computed(() => ruleProviders.value === null ? null : [...readSubRuleGroups(doc.value), ...ruleProviders.value])
const ruleTargets = computed(() => {
  const proxies = rootEditable.value ? doc.value.getIn(['proxies'], true) : undefined
  const proxyNames = isSeq(proxies) ? proxies.items.flatMap(node => {
    const name = isMap(node) ? node.get('name') : undefined
    return typeof name === 'string' ? [name] : []
  }) : []
  return [...new Set(['DIRECT', 'REJECT', ...(groups.value?.map(item => item.label).filter(Boolean) ?? []), ...proxyNames, ...liveRuleTargets.value])]
})
const groupTypes = ['select', 'url-test', 'fallback', 'load-balance', 'relay']
function idle(message = ''): Report {
  return { validation: 'idle', saved: 'idle', applied: 'idle', message }
}

function change(next: string) {
  if (next === source.value) return
  source.value = next
  undoRemoval.value = null
  report.value = idle()
}

function edit(update: (draft: Document) => void) {
  if (!rootEditable.value) return
  const draft = doc.value.clone()
  update(draft)
  change(draft.toString({ lineWidth: 0 }))
}

async function showRuleEditor(index?: number, rule = '', provider = '') {
  if (index === undefined && !canAddRule.value) return
  editingRule.value = index ?? null
  newRule.value = rule
  ruleType.value = provider ? 'RULE-SET' : 'DOMAIN-SUFFIX'
  ruleValue.value = provider
  ruleTarget.value = 'DIRECT'
  customRuleTarget.value = ''
  ruleError.value = ''
  ruleProviderSource.value = provider ? 'existing' : 'url'
  ruleProviderURL.value = ''
  ruleProviderFormat.value = 'yaml'
  ruleProviderBehavior.value = 'classical'
  ruleProviderInterval.value = 86400
  await nextTick()
  ruleDialog.value?.showModal()
  if (index === undefined && externalConfig.value && !ruleTargetsLoading.value) {
    ruleTargetsLoading.value = true
    try {
      const runtime = await request<RuntimeGroup[]>('/api/groups')
      liveRuleTargets.value = runtime.map(group => group.name)
    } catch {
      // The built-in targets and the draft's own targets remain available offline.
    } finally {
      ruleTargetsLoading.value = false
    }
  }
}

async function commitRule() {
  const existing = editingRule.value
  if (busy.value || (existing === null && !canAddRule.value)) return
  const type = ruleType.value
  const value = ruleValue.value.trim()
  const strategy = (ruleTarget.value || customRuleTarget.value).trim()
  const prefix = type === 'IP-CIDR' ? '/32' : type === 'IP-CIDR6' ? '/128' : ''
  const match = value && !value.includes('/') ? value + prefix : value
  const parts = type === 'MATCH' ? [type, strategy] : [type, match, strategy]
  if (existing === null && (parts.some(part => !part) || parts.some(part => /[,\r\n]/.test(part)))) {
    ruleError.value = '请填写匹配条件和策略，每次添加一条规则。'
    return
  }
  if (existing === null && type.startsWith('DOMAIN') && /[\s/:?#*]/.test(value)) {
    ruleError.value = '请输入域名或关键词。域名示例为 example.com。'
    return
  }
  const rule = existing === null ? parts.join(',') : newRule.value.trim()
  if (!rule.includes(',') || /[\r\n]/.test(rule) || !rule.split(',').at(-1)?.trim()) {
    ruleError.value = '请输入一条含策略的 Mihomo 规则。'
    return
  }
  const before = source.value
  if (existing !== null) {
    edit(draft => draft.setIn(['rules', existing], rule))
  } else {
    const fallback = rules.value?.findIndex(value => /^MATCH\s*,/i.test(value) ||
      value.startsWith(`SUB-RULE,${subscriptionRuleCondition},`)) ?? -1
    const index = type === 'RULE-SET' ? 0 : fallback < 0 ? (rules.value?.length ?? 0) : fallback
    const draft = doc.value.clone()
    if (type === 'RULE-SET') {
      try {
        if (ruleProviderSource.value === 'url') {
          writeRuleProvider(draft, { name: value, url: ruleProviderURL.value.trim(), format: ruleProviderFormat.value,
            behavior: ruleProviderBehavior.value, interval: Number(ruleProviderInterval.value) }, false)
          // The shared writer adds a DIRECT reference; replace it with the selected policy.
          const node = draft.getIn(['rules'], true)
          if (isSeq(node)) node.delete(0)
        } else if (!ruleProviders.value?.some(entry => entry.name === value)) {
          throw new Error('请选择已配置的规则集，或通过订阅地址添加。')
        }
      } catch (error) {
        ruleError.value = errorText(error)
        return
      }
    }
    const node = draft.getIn(['rules'], true)
    if (isSeq(node)) {
      const reference = type === 'RULE-SET' ? strings(draft, ['rules'])?.indexOf(rule) ?? -1 : -1
      const item = reference < 0 ? draft.createNode(rule) : node.items.splice(reference, 1)[0]
      node.items.splice(index, 0, item)
    } else draft.set('rules', draft.createNode([rule]))
    change(draft.toString({ lineWidth: 0 }))
  }
  if (!await saveRuleChanges(before)) {
    ruleError.value = report.value.message
    return
  }
  ruleDialog.value?.close()
}

async function removeRule(index: number) {
  if (busy.value) return
  const before = source.value
  edit(draft => { draft.deleteIn(['rules', index]) })
  if (await saveRuleChanges(before)) undoRemoval.value = before
}

async function restoreRule() {
  if (busy.value || undoRemoval.value === null) return
  const before = source.value
  change(undoRemoval.value)
  await saveRuleChanges(before)
}

async function moveRule(index: number, to: number) {
  if (busy.value || externalConfig.value || !rules.value || index === to ||
      index < 0 || index >= rules.value.length || to < 0 || to >= rules.value.length) return false
  const before = source.value
  edit(draft => {
    const node = draft.getIn(['rules'], true)
    if (!isSeq(node)) return
    const [item] = node.items.splice(index, 1)
    node.items.splice(to, 0, item)
  })
  return saveRuleChanges(before)
}

function matchesConfiguredRule(rule: RuntimeRule) {
  const configured = rules.value?.[rule.index]
  if (!configured) return false
  const comma = configured.indexOf(',')
  const configuredType = configured.slice(0, comma).replace(/[^a-z0-9]/gi, '').toLowerCase()
  const type = configuredType === 'ipcidr6' ? 'ipcidr' : configuredType
  const normalizedRuntimeType = rule.type.replace(/[^a-z0-9]/gi, '').toLowerCase()
  const runtimeType = normalizedRuntimeType === 'ipcidr6' ? 'ipcidr' : normalizedRuntimeType
  if (type !== runtimeType && !(type === 'subrule' && runtimeType === 'subrules')) return false
  const expected = type === 'match' ? rule.proxy : `${rule.payload},${rule.proxy}`
  const value = configured.slice(comma + 1).trim().toLowerCase()
  return value === expected.toLowerCase() || value === `${expected},no-resolve`.toLowerCase()
}

function canEditListedRule(rule: RuntimeRule) {
  return (!externalConfig.value || dirty.value) && matchesConfiguredRule(rule)
}

function editListedRule(rule: RuntimeRule) {
  if (!canEditListedRule(rule)) return
  void showRuleEditor(rule.index, rules.value?.[rule.index])
}

async function removeListedRule(rule: RuntimeRule) {
  if (!canEditListedRule(rule)) return
  await removeRule(rule.index)
}

async function reorderRuntimeRules(from: RuntimeRule, to: RuntimeRule) {
  if (busy.value || externalConfig.value) return false
  if (!matchesConfiguredRule(from) || !matchesConfiguredRule(to)) {
    report.value = { ...idle('运行规则与配置不一致，请重新读取配置后排序。'), tone: 'error' }
    return false
  }
  const saved = await moveRule(from.index, to.index)
  if (saved && !pendingReload.value) {
    await nextTick()
    runtimeRulesView.value?.focusRule(to.index)
  }
  return saved && !pendingReload.value
}

async function saveRuleChanges(before: string) {
  if (externalConfig.value) return true
  const saved = await run('apply')
  if (!saved) source.value = before
  return saved
}

async function addGroup() {
  const newIndex = groups.value?.length ?? 0
  const names = new Set(groups.value?.map(group => group.label))
  let index = 1
  while (names.has(`策略组 ${index}`)) index++
  const group = { name: `策略组 ${index}`, type: 'select', proxies: ['DIRECT'] }
  edit(draft => {
    const node = draft.getIn(['proxy-groups'], true)
    if (isSeq(node)) node.add(draft.createNode(group))
    else draft.set('proxy-groups', [group])
  })
  openGroup.value = newIndex
  await nextTick()
  const input = root.value?.querySelector<HTMLInputElement>(`input[data-group-index="${newIndex}"]`)
  input?.focus()
  input?.select()
}

async function removeGroup(index: number, name: string) {
  if (!await confirmReplacement('删除代理组？', '将从草稿移除 ' + name + '。保存前请检查规则和其他代理组中的引用。', '删除')) return
  edit(draft => { draft.deleteIn(['proxy-groups', index]) })
  openGroup.value = null
}

function lines(value: string) {
  return value.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
}

function updateField(path: (string | number)[], value: string) {
  pendingGroupEdit.value = false
  edit(draft => draft.setIn(path, value))
}

function updateLines(path: (string | number)[], value: string) {
  pendingGroupEdit.value = false
  edit(draft => setStrings(draft, path, lines(value)))
}

async function stageSubscription(id: string) {
  if (busy.value) return
  if (dirty.value && !await confirmReplacement('应用订阅配置？', '订阅中的节点、代理组和规则会覆盖当前未保存的修改。', '应用订阅')) return
  const before = source.value
  staging.value = true
  report.value = idle('正在载入订阅…')
  try {
    const [subscription, overlay] = await Promise.all([
      request<{ content: string }>(`/api/subscriptions/${id}`),
      useSavedOverride.value ? request<Snapshot>('/api/override').then(result => result.content) : Promise.resolve(''),
    ])
    change(loadSubscription(source.value, subscription.content, overlay))
    staging.value = false
    if (await saveRuleChanges(before)) {
      stagedId.value = externalConfig.value || !pendingReload.value ? id : ''
      if (!pendingReload.value) report.value = { ...idle(externalConfig.value ? '订阅配置已载入，规则已整理为一组。' : '订阅配置已应用，规则已整理为一组。'), tone: 'success' }
    }
  } catch (error) {
    report.value = { ...idle(errorText(error)), tone: 'error' }
  } finally {
    staging.value = false
  }
}

function renderOverride(content: string) {
  change(applyOverride(source.value, content))
  report.value = { ...idle('覆写已写入配置草稿。'), tone: 'success', viewDraft: true }
}

function ping() {
  void overviewView.value?.sample()
}

async function refreshAfterCoreSetup() {
  runtimeRevision.value++
  try {
    const status = await request<{ external?: boolean }>('/api/core')
    externalConfig.value = status.external === true
  } catch {
    // The normal refresh below reports a failed request without discarding a draft.
  }
  // Switching controllers changes whether the source is owned by another app.
  // Let refresh retain its normal unsaved-draft confirmation before replacing it.
  if (!loading.value) void refresh()
}

function confirmReplacement(title: string, message: string, action: string) {
  const modal = dialog.value
  if (!modal || modal.open) return Promise.resolve(false)
  confirmation.value = { title, message, action }
  modal.returnValue = ''
  return new Promise<boolean>(resolve => {
    resolveConfirmation = resolve
    modal.showModal()
  })
}

function closeConfirmation() {
  resolveConfirmation?.(dialog.value?.returnValue === 'confirm')
  resolveConfirmation = undefined
}

function switchSection(next: Section) {
  commitFocusedGroupField()
  configMenu.value?.close()
  if (section.value === next) return
  section.value = next
  window.location.hash = next
}

function switchGroupTab(next: typeof groupTab.value) {
  commitFocusedGroupField()
  groupTab.value = next
}

function syncSection() {
  const next = readSection()
  if (section.value === next) return
  commitFocusedGroupField()
  section.value = next
}

function commitFocusedGroupField() {
  const active = document.activeElement
  if (active instanceof HTMLElement && active.closest('[data-commit-on-blur]')) active.blur()
}

function protectDraft(event: BeforeUnloadEvent) {
  if (!hasConfigChanges.value && !overrideDirty.value && action.value !== 'apply') return
  event.preventDefault()
  event.returnValue = true
}

function saveShortcut(event: KeyboardEvent) {
  if ((!event.metaKey && !event.ctrlKey) || event.altKey || event.shiftKey ||
      event.isComposing || event.key.toLowerCase() !== 's' || document.querySelector('dialog[open]')) return
  if (!editsConfiguration.value || externalConfig.value) return
  event.preventDefault()
  if (hasConfigChanges.value || pendingReload.value) void run('apply')
}

async function refresh() {
  commitFocusedGroupField()
  if (busy.value) return
  if (dirty.value && !await confirmReplacement('重新读取配置？', '当前草稿有未保存的改动。重新读取会用磁盘配置替换草稿。', '重新读取')) return
  loading.value = true
  try {
    const snapshot = await request<Snapshot>('/api/config')
    source.value = original.value = snapshot.content
    stagedId.value = ''
    editorKey.value++
    undoRemoval.value = null
    revision.value = snapshot.revision
    exists.value = snapshot.exists
    externalConfig.value = snapshot.externalConfig === true
    if (snapshot.externalConfig) pendingReload.value = false
    configReadFailed.value = false
    report.value = idle()
  } catch (error) {
    configReadFailed.value = true
    describeError(error, false)
  } finally {
    loading.value = false
    void ping()
  }
}

function describeError(error: unknown, operation: boolean) {
  const failure = error as { message?: string; detail?: string; status?: number }
  report.value = {
    validation: failure.status === 422 ? 'fail' : 'idle',
    saved: operation && action.value === 'apply' && failure.status !== 422 && failure.status !== 409 ? 'fail' : 'idle',
    applied: 'idle',
    message: failure.message ?? '操作失败。',
    tone: 'error',
    detail: failure.detail,
    conflict: operation && failure.status === 409,
  }
}

async function run(operation: 'validate' | 'apply') {
  commitFocusedGroupField()
  if (operation === 'apply' ? !canApply.value : !canValidate.value) return false
  action.value = operation
  report.value = { ...idle('正在校验配置…'), validation: 'pending' }
  try {
    const payload = { content: source.value, revision: revision.value }
    if (operation === 'validate') {
      await request('/api/validate', payload)
      report.value = { ...idle('Mihomo 校验通过。'), validation: 'pass', tone: 'success' }
      return true
    } else {
      const result = await request<ApplyResult>('/api/apply', payload)
      if (result.saved) pendingReload.value = !result.applied
      if (result.applied) runtimeRevision.value++
      if (result.saved) {
        original.value = payload.content
        revision.value = result.revision
        exists.value = true
        undoRemoval.value = null
      }
      report.value = {
        validation: 'pass',
        saved: result.saved ? 'pass' : 'fail',
        applied: result.applied ? 'pass' : 'fail',
        message: result.applied ? (result.backup ? '已更新配置、保留备份并重载。' : 'Mihomo 已重载配置。') :
          result.saved ? '配置已保存，Mihomo 重载失败。' : '配置保存失败。',
        detail: result.detail,
        tone: result.applied ? 'success' : 'error',
      }
      void ping()
      return result.saved
    }
  } catch (error) {
    describeError(error, true)
    return false
  } finally {
    action.value = ''
  }
}

async function importFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (file.size > 2 * 1024 * 1024) {
    report.value = { ...idle('要导入的文件超过 2 MiB。'), validation: 'fail', tone: 'error' }
    return
  }
  if (dirty.value && !await confirmReplacement('用导入文件替换草稿？', '当前草稿有未保存的改动。你可以取消并先导出草稿。', '导入并替换')) return
  try {
    change(await file.text())
    stagedId.value = ''
    editorKey.value++
    switchSection('yaml')
  } catch (error) {
    describeError(error, false)
  }
}

function example() {
  change('mode: rule\nlog-level: info\n\ndns:\n  enable: true\n  enhanced-mode: fake-ip\n  nameserver:\n    - https://1.1.1.1/dns-query\n\nproxy-groups:\n  - name: 默认策略\n    type: select\n    proxies:\n      - DIRECT\n\nrules:\n  - DOMAIN-SUFFIX,example.com,默认策略\n  - MATCH,DIRECT\n')
}

function download() {
  commitFocusedGroupField()
  const url = URL.createObjectURL(new Blob([source.value], { type: 'application/yaml' }))
  const link = document.createElement('a')
  link.href = url
  link.download = 'miproxy-config.yaml'
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

const steps = computed(() => [
  { key: 'validation', label: '校验', state: report.value.validation },
  { key: 'saved', label: '写入文件', state: report.value.saved },
  { key: 'applied', label: '重载', state: report.value.applied },
].filter(step => step.state !== 'idle'))
const statusLabels: Record<Status, string> = { idle: '等待', pending: '进行中', pass: '完成', fail: '失败' }

onMounted(() => {
  window.addEventListener('keydown', saveShortcut)
  window.addEventListener('hashchange', syncSection)
  void refresh()
})
onUnmounted(() => {
  window.removeEventListener('keydown', saveShortcut)
  window.removeEventListener('hashchange', syncSection)
})

watch(() => hasConfigChanges.value || overrideDirty.value || action.value === 'apply', protect => {
  if (protect) window.addEventListener('beforeunload', protectDraft)
  else window.removeEventListener('beforeunload', protectDraft)
}, { flush: 'sync' })
onUnmounted(() => window.removeEventListener('beforeunload', protectDraft))
</script>

<template>
  <div ref="root" class="workspace-shell">
    <a class="skip-link" href="#main-content" @click.prevent="workspace?.focus()">跳转到主要内容</a>
    <aside class="sidebar" aria-label="MiProxy Panel">
      <a class="brand" href="#overview" aria-label="MiProxy Panel 首页">
        <span class="brand-mark"><Network :size="18" :stroke-width="2" aria-hidden="true" /></span>
        <strong>MiProxy <span>Panel</span></strong>
      </a>
      <nav class="navigation" aria-label="主导航">
        <template v-for="group in navigationGroups" :key="group.label">
          <span class="nav-group-heading">{{ group.label }}</span>
          <button v-for="item in group.items" :key="item.key" type="button"
            :aria-current="section === item.key ? 'page' : undefined" @click="switchSection(item.key)">
            <component :is="item.icon" :size="17" aria-hidden="true" />{{ item.label }}
          </button>
        </template>
      </nav>
    </aside>

    <div class="workspace-column">
    <main id="main-content" ref="workspace" class="workspace" :class="{ 'workspace--rules': section === 'rules', 'workspace--overview': section === 'overview' }" :aria-busy="operationBusy" tabindex="-1">
        <header class="workspace-heading">
          <h1 id="section-title">{{ activeSection.title }}</h1>
          <div class="workspace-tools">
            <button class="connection" type="button" :aria-label="coreLabel + '，点击重新检查'" :title="coreLabel" @click="ping">
              <span class="connection-dot" :class="{ online: core?.connected, offline: core && !core.connected }" aria-hidden="true"></span>
              <strong>Mihomo</strong><span>{{ core === null ? '检查中' : core.connected ? '已连接' : '未连接' }}</span>
            </button>
            <button class="icon-button theme-button" type="button" :aria-label="dark ? '切换到浅色主题' : '切换到暗色主题'"
              :title="dark ? '切换到浅色主题' : '切换到暗色主题'" @click="dark = !dark">
              <Sun v-if="dark" :size="17" aria-hidden="true" />
              <Moon v-else :size="17" aria-hidden="true" />
            </button>
          </div>
        </header>
        <div v-if="section === 'groups' || section === 'yaml'" class="page-controls">
          <div v-if="section === 'groups'" class="view-tabs" role="group" aria-label="代理组视图">
            <button class="button quiet" :aria-pressed="groupTab === 'runtime'" @click="switchGroupTab('runtime')">运行状态</button>
            <button class="button quiet" :aria-pressed="groupTab === 'configuration'" @click="switchGroupTab('configuration')">配置编辑</button>
          </div>
          <div v-if="section !== 'groups'" class="heading-actions">
            <div v-if="section === 'yaml'" class="file-tools" aria-label="文件操作">
              <input ref="fileInput" type="file" accept=".yaml,.yml,text/yaml,application/yaml" hidden @change="importFile">
              <button class="button quiet" type="button" :disabled="busy" title="重新读取" aria-label="重新读取" @click="refresh">
                <RefreshCw :size="16" aria-hidden="true" /><span>重新读取</span>
              </button>
              <button class="button quiet" type="button" :disabled="busy" title="导入 YAML" aria-label="导入 YAML" @click="fileInput?.click()">
                <ArrowUpFromLine :size="16" aria-hidden="true" /><span>导入</span>
              </button>
              <button class="button quiet" type="button" :disabled="!source || busy" title="导出 YAML" aria-label="导出 YAML" @click="download">
                <ArrowDownToLine :size="16" aria-hidden="true" /><span>导出</span>
              </button>
            </div>
          </div>
        </div>

        <div class="workbench" :class="{ 'workbench--flat': section === 'overview' || section === 'override' || section === 'subscriptions' || section === 'logs' || showsRuntimeRules || section === 'groups' && groupTab === 'runtime' }">
          <section ref="workbenchBody" class="workbench-body" aria-labelledby="section-title">
            <div v-if="section === 'rules' && report.message && !showActionbar" class="inline-message rule-feedback" :class="report.tone" :role="report.tone === 'error' ? 'alert' : 'status'">
              <span>{{ report.message }}</span>
              <button v-if="report.detail" type="button" class="button quiet compact" @click="feedbackDialog?.showModal()">查看详情</button>
              <button v-if="!busy" type="button" class="icon-button" aria-label="关闭提示" @click="report = idle()"><X :size="16" /></button>
            </div>
            <RuntimeOverview v-show="section === 'overview'" ref="overviewView" :active="section === 'overview'" :dark="dark" @status="core = $event" @configured="refreshAfterCoreSetup" />
            <div v-show="section === 'subscriptions'" class="subscriptions-pane">
              <Subscriptions v-model:use-saved-override="useSavedOverride" :staged-id="stagedId" :external-config="externalConfig" :disabled="busy || revision === null"
                :confirm-delete="name => confirmReplacement('移除订阅？', '将从订阅列表移除 ' + name + '。', '移除')"
                @stage="stageSubscription" />
            </div>
            <Override v-show="section === 'override'" :disabled="busy || !source.trim()" :render="renderOverride"
              :confirm-reload="() => confirmReplacement('重新读取覆写？', '当前覆写有未保存改动。', '重新读取')"
              @dirty="overrideDirty = $event" />
            <RuntimeGroups v-if="section === 'groups' && groupTab === 'runtime'" :key="runtimeRevision" />
            <RuntimeLogs :key="runtimeRevision" :active="section === 'logs'" :connected="core?.connected === true" />
            <RuntimeRules v-if="showsRuntimeRules" ref="runtimeRulesView" :revision="runtimeRevision" :applying="busy"
              :configured="rules" :collections="ruleCollections ?? []" :draft="dirty || pendingReload" :external-config="externalConfig"
              :can-add="canAddRule" :can-undo="undoRemoval !== null" :can-edit="canEditListedRule" :refresh-config="refresh"
              :reorder="externalConfig || rules === null ? undefined : reorderRuntimeRules"
              @add="showRuleEditor()" @edit="editListedRule" @remove="removeListedRule" @undo="restoreRule" />

            <div v-show="section === 'yaml'" class="editor-frame">
              <header class="editor-bar">
                <div class="filename">
                  <FileCode2 :size="17" aria-hidden="true" />
                  <span>config.yaml</span>
                  <i v-if="dirty" aria-label="有未保存改动" title="有未保存改动"></i>
                </div>
                <div class="editor-tools" aria-label="编辑操作">
                  <button v-if="!source && !busy" class="button quiet compact" type="button" @click="example">载入示例</button>
                  <button class="icon-button" type="button" title="撤销" aria-label="撤销" :disabled="busy || !editorPosition.canUndo" @click="yamlEditor?.undo()"><Undo2 :size="17" aria-hidden="true" /></button>
                  <button class="icon-button" type="button" title="重做" aria-label="重做" :disabled="busy || !editorPosition.canRedo" @click="yamlEditor?.redo()"><Redo2 :size="17" aria-hidden="true" /></button>
                  <button class="icon-button search-toggle" type="button" title="搜索 YAML" aria-label="搜索 YAML" :disabled="busy" @click="yamlEditor?.search()"><Search :size="17" aria-hidden="true" /></button>
                </div>
              </header>
              <YamlEditor :key="editorKey" ref="yamlEditor" :model-value="source" :disabled="busy" @update:model-value="change"
                @position="editorPosition = $event" />
            </div>

            <template v-if="section === 'groups' && groupTab === 'configuration'">
              <div v-if="loading" class="empty-state" role="status">正在读取配置…</div>
              <div v-else-if="!source.trim() || !rootEditable" class="empty-state">
                <Braces :size="24" aria-hidden="true" />
                <p>{{ source.trim() ? '这份配置需要在 YAML 源码中编辑。' : '导入已有配置或打开 YAML 编辑器开始编辑。' }}</p>
                <button class="button secondary" @click="switchSection('yaml')">打开 YAML 源码</button>
              </div>

              <template v-else-if="section === 'groups'">
                <div v-if="groups === null" class="empty-state"><p>该策略组结构需要在 YAML 源码中编辑。</p><button class="button secondary" @click="switchSection('yaml')">打开 YAML 源码</button></div>
                <div v-else class="workbench-content">
                  <div class="section-tools">
                    <span class="entry-count">{{ groups.length }} 个代理组</span>
                    <button class="button secondary" :disabled="busy" @click="addGroup"><Plus :size="16" aria-hidden="true" />添加代理组</button>
                  </div>
                  <div v-if="!groups.length" class="empty-state">还没有代理组。</div>
                  <article v-for="group in groups" :key="group.index" class="group">
                    <button class="group-summary" type="button" :aria-expanded="openGroup === group.index"
                      :aria-controls="'group-' + group.index" @click="openGroup = openGroup === group.index ? null : group.index">
                      <span class="group-icon"><GitBranch :size="16" aria-hidden="true" /></span>
                      <strong>{{ group.label || '策略组 ' + (group.index + 1) }}</strong>
                      <span v-if="group.type" class="group-type">{{ group.type }}</span>
                      <span v-if="group.members !== null" class="member-count">{{ group.members.length }} 个成员</span>
                      <ChevronDown :size="16" aria-hidden="true" />
                    </button>
                    <div v-show="openGroup === group.index" :id="'group-' + group.index" class="group-edit">
                      <template v-if="group.editable">
                        <div class="field-grid">
                          <label class="field">名称<DraftInput :model-value="group.label" :data-group-index="group.index" data-commit-on-blur
                            :disabled="busy" @pending="pendingGroupEdit = $event" @change="updateField(['proxy-groups', group.index, 'name'], $event)" /></label>
                          <div class="field"><span>类型</span><ElSelect :model-value="group.type" :disabled="busy" :aria-label="group.label + ' 的类型'" @change="updateField(['proxy-groups', group.index, 'type'], $event)">
                            <ElOption v-if="!groupTypes.includes(group.type)" :value="group.type" :label="group.type || '选择类型'" />
                            <ElOption v-for="type in groupTypes" :key="type" :value="type" :label="type" />
                          </ElSelect></div>
                        </div>
                        <label v-if="group.members !== null" class="field">
                          <span>节点或策略组 <small>每行一项</small></span>
                          <DraftInput type="textarea" :model-value="group.members.join('\n')" :rows="4" :disabled="busy" data-commit-on-blur
                            @pending="pendingGroupEdit = $event" @change="updateLines(['proxy-groups', group.index, 'proxies'], $event)" />
                        </label>
                        <p v-else class="hint">节点列表可在 YAML 源码中编辑。</p>
                        <div class="group-edit-actions">
                          <button class="button quiet compact" :disabled="busy" @click="removeGroup(group.index, group.label || '此代理组')">
                            <Trash2 :size="15" aria-hidden="true" />删除代理组
                          </button>
                        </div>
                      </template>
                      <p v-else class="hint">这组配置可在 YAML 源码中编辑。</p>
                    </div>
                  </article>
                </div>
              </template>

            </template>
          </section>
        </div>
    </main>

      <footer v-if="showActionbar" class="actionbar" aria-label="配置操作">
        <div class="file-state">
          <div class="draft-state">
            <span v-if="externalConfig" class="editor-status">由外部应用管理</span>
            <span v-else-if="section === 'yaml' && !loading && !report.message" class="editor-status">
              {{ lineCount }} 行<span aria-hidden="true">·</span>行 {{ editorPosition.line }}，列 {{ editorPosition.column }}
            </span>
          </div>
          <div v-if="report.message" class="draft-report">
            <span class="draft-label" :class="report.tone" role="status">
              <Check v-if="report.tone === 'success'" :size="15" aria-hidden="true" />
              <CircleAlert v-else-if="report.tone === 'error'" :size="15" aria-hidden="true" />
              <LoaderCircle v-else-if="busy" class="spinner" :size="15" aria-hidden="true" />
              <span>{{ report.message }}</span>
            </span>
            <button v-if="report.detail || steps.length > 1 && report.tone === 'error'" type="button" class="button quiet compact"
              aria-haspopup="dialog" @click="feedbackDialog?.showModal()">查看详情</button>
            <button v-if="report.viewDraft && section !== 'yaml'" type="button" class="button quiet compact"
              @click="switchSection('yaml')">查看 YAML 草稿</button>
            <button v-if="!busy && report.tone" type="button" class="icon-button dismiss-report" aria-label="关闭提示" @click="report = idle()">
              <X :size="14" aria-hidden="true" />
            </button>
          </div>
        </div>
        <div class="apply-actions">
          <button v-if="configReadFailed || report.conflict" type="button" class="button secondary" :disabled="busy" @click="refresh">重新读取配置</button>
          <button v-if="report.conflict || externalConfig && section !== 'yaml' && !!source" type="button" class="button secondary" :disabled="busy" @click="download">
            <ArrowDownToLine :size="15" aria-hidden="true" />导出草稿
          </button>
          <button v-if="revision !== null" type="button" class="button secondary" :disabled="!canValidate" @click="run('validate')">
            {{ action === 'validate' ? '校验中…' : '校验配置' }}
          </button>
          <button v-if="!externalConfig && revision !== null" type="button" class="button"
            :class="hasConfigChanges || pendingReload ? 'primary' : 'secondary'" :disabled="!canApply"
            :title="editsConfiguration && (hasConfigChanges || pendingReload) ? keyboard + 'S' : undefined" @click="run('apply')">
            <LoaderCircle v-if="action === 'apply'" class="spinner" :size="16" aria-hidden="true" />
            <Save v-else :size="16" aria-hidden="true" />
            {{ action === 'apply' ? '正在应用…' : hasConfigChanges ? '保存并应用' : pendingReload ? '重试重载' : '重载配置' }}
            <kbd v-if="!action && editsConfiguration && (hasConfigChanges || pendingReload)" aria-hidden="true">{{ keyboard }}S</kbd>
          </button>
        </div>
      </footer>
    </div>

    <nav class="mobile-navigation" aria-label="主导航">
      <button v-for="item in mobileSections" :key="item.key" type="button"
        :aria-current="section === item.key ? 'page' : undefined" @click="switchSection(item.key)">
        <component :is="item.icon" :size="19" aria-hidden="true" /><span>{{ item.label }}</span>
      </button>
      <button type="button" aria-haspopup="dialog" aria-controls="config-menu" :class="{ 'config-active': configActive }"
        @click="configMenu?.showModal()"><Settings2 :size="19" aria-hidden="true" /><span>更多</span></button>
    </nav>

    <dialog id="config-menu" ref="configMenu" class="confirmation mobile-settings" aria-labelledby="config-menu-title">
      <header>
        <h2 id="config-menu-title">更多功能</h2>
        <form method="dialog"><button type="submit" class="icon-button" aria-label="关闭更多功能"><X :size="18" aria-hidden="true" /></button></form>
      </header>
      <nav class="navigation" aria-label="更多功能导航">
        <button v-for="item in moreSections" :key="item.key" type="button"
          :aria-current="section === item.key ? 'page' : undefined" @click="switchSection(item.key)">
          <component :is="item.icon" :size="18" aria-hidden="true" />{{ item.title }}
        </button>
      </nav>
    </dialog>

    <dialog ref="ruleDialog" class="confirmation import-dialog rule-drawer" aria-labelledby="rule-dialog-title" @cancel="busy && $event.preventDefault()">
      <form @submit.prevent="commitRule">
        <div class="dialog-heading">
          <h2 id="rule-dialog-title">{{ editingRule === null ? '添加规则' : '编辑规则' }}</h2>
          <button type="button" class="icon-button" aria-label="关闭规则编辑" :disabled="busy" @click="ruleDialog?.close()"><X :size="18" aria-hidden="true" /></button>
        </div>
        <label v-if="editingRule !== null" class="field">Mihomo 规则
          <input v-model="newRule" required autofocus spellcheck="false" placeholder="DOMAIN-SUFFIX,example.com,DIRECT">
        </label>
        <template v-else>
          <div class="field"><span>匹配方式</span>
            <ElSelect v-model="ruleType" :teleported="false" aria-label="匹配方式" @change="ruleValue = ''; ruleError = ''">
              <ElOption v-for="type in ruleTypes" :key="type.value" :value="type.value" :label="type.label" />
            </ElSelect>
          </div>
          <div v-if="ruleType === 'RULE-SET'" class="field"><span>规则来源</span>
            <ElSelect v-model="ruleProviderSource" :teleported="false" aria-label="规则来源">
              <ElOption value="url" label="订阅地址" />
              <ElOption value="existing" label="已有规则集" />
            </ElSelect>
          </div>
          <label v-if="ruleType !== 'MATCH'" class="field">
            {{ ruleType === 'RULE-SET' ? '规则集名称' : '匹配条件' }}
            <input v-model="ruleValue" required autofocus spellcheck="false" :list="ruleType === 'RULE-SET' ? 'rule-provider-names' : undefined"
              :placeholder="rulePlaceholder" @input="ruleError = ''">
            <datalist v-if="ruleType === 'RULE-SET'" id="rule-provider-names">
              <option v-for="provider in ruleProviders ?? []" :key="provider.name" :value="provider.name" />
            </datalist>
          </label>
          <template v-if="ruleType === 'RULE-SET' && ruleProviderSource === 'url'">
            <label class="field">订阅地址
              <input v-model="ruleProviderURL" required type="url" spellcheck="false" placeholder="https://example.com/rules.yaml">
            </label>
            <div class="field-grid">
              <div class="field"><span>规则类型</span>
                <ElSelect v-model="ruleProviderBehavior" :teleported="false" aria-label="规则类型">
                  <ElOption value="classical" label="经典规则（classical）" />
                  <ElOption value="domain" label="域名（domain）" />
                  <ElOption value="ipcidr" label="IP 段（ipcidr）" />
                </ElSelect>
              </div>
              <div class="field"><span>文件格式</span>
                <ElSelect v-model="ruleProviderFormat" :teleported="false" aria-label="文件格式">
                  <ElOption v-for="format in ['yaml', 'text', 'mrs']" :key="format" :value="format" :label="format.toUpperCase()" />
                </ElSelect>
              </div>
            </div>
            <label class="field">更新间隔（秒）
              <input v-model.number="ruleProviderInterval" aria-label="更新间隔（秒）" required type="number" min="0" max="9223372036" step="1">
              <small>默认每天更新，填 0 仅手动更新。</small>
            </label>
          </template>
          <div class="field"><span>匹配后走</span>
            <ElSelect v-model="ruleTarget" :teleported="false" aria-label="匹配后走" filterable>
              <ElOption v-for="target in ruleTargets" :key="target" :value="target" :label="target === 'DIRECT' ? '直连（DIRECT）' : target === 'REJECT' ? '拦截（REJECT）' : target" />
              <ElOption value="" label="手动填写策略名称" />
            </ElSelect>
            <small v-if="ruleTargetsLoading" role="status">正在读取运行中的代理组…</small>
          </div>
          <label v-if="!ruleTarget" class="field">策略名称
            <input v-model="customRuleTarget" required spellcheck="false" placeholder="输入代理组或节点名称">
          </label>
        </template>
        <p v-if="editingRule === null && externalConfig" class="hint">生效配置由外部应用管理，修改后请导出配置。</p>
        <p v-else-if="editingRule === null" class="hint">保存后立即生效，自定义规则优先于订阅规则组匹配。</p>
        <p v-if="ruleError" class="inline-message error" role="alert">{{ ruleError }}</p>
        <div class="dialog-actions">
          <button class="button secondary" type="button" :disabled="busy" @click="ruleDialog?.close()">取消</button>
          <button class="button primary" :disabled="busy">{{ busy ? '正在保存…' : externalConfig ? '更新配置' : '保存并生效' }}</button>
        </div>
      </form>
    </dialog>

    <dialog ref="dialog" class="confirmation" aria-labelledby="confirmation-title" aria-describedby="confirmation-text" @close="closeConfirmation">
      <form method="dialog">
        <CircleAlert :size="23" aria-hidden="true" />
        <h2 id="confirmation-title">{{ confirmation.title }}</h2>
        <p id="confirmation-text">{{ confirmation.message }}</p>
        <div class="dialog-actions">
          <button class="button secondary" value="cancel" autofocus>取消</button>
          <button class="button primary" value="confirm">{{ confirmation.action }}</button>
        </div>
      </form>
    </dialog>

    <dialog ref="feedbackDialog" class="confirmation feedback" aria-labelledby="feedback-title">
      <form method="dialog">
        <h2 id="feedback-title">配置操作详情</h2>
        <p>{{ report.message }}</p>
        <ul v-if="steps.length" class="feedback-steps">
          <li v-for="step in steps" :key="step.key" :class="step.state">{{ step.label }} · {{ statusLabels[step.state] }}</li>
        </ul>
        <pre v-if="report.detail" class="feedback-detail">{{ report.detail }}</pre>
        <div class="dialog-actions"><button type="submit" class="button secondary" autofocus>关闭</button></div>
      </form>
    </dialog>

    <dialog ref="operationDialog" class="confirmation operation-loading" aria-labelledby="operation-title" aria-describedby="operation-detail" @cancel.prevent>
      <div role="status" aria-live="polite">
        <LoaderCircle class="spinner" :size="28" aria-hidden="true" />
        <h2 id="operation-title">{{ operationTitle }}</h2>
        <p id="operation-detail">{{ operationDetail }}</p>
      </div>
    </dialog>
  </div>
</template>
