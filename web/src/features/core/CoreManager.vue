<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Check, Cpu, Download, Play, RefreshCw, Settings2, X } from '@lucide/vue'
import { ElButton } from 'element-plus/es/components/button/index'
import 'element-plus/es/components/button/style/css'
import { errorText, request } from '../../shared/api'
import type { CoreCandidate, CoreDiscovery, CoreInstallation } from '../../shared/api'
import { formatBytes } from '../../shared/format'

const props = defineProps<{ connected: boolean; controller?: string }>()
const emit = defineEmits<{ connected: []; configured: [] }>()
const core = ref<CoreInstallation>()
const checking = ref(false)
const submitting = ref(false)
const failure = ref('')
const statusFailure = ref('')
const discoveryFailure = ref('')
const discovery = ref<CoreDiscovery>()
const scanning = ref(true)
const selecting = ref('')
const panelChosen = ref(false)
const managerDialog = ref<HTMLDialogElement>()
let timer: number | undefined
let pending: AbortController | undefined
let discoveryPending: AbortController | undefined
let disposed = false
let wasBusy = false
let detection: Promise<CoreInstallation | undefined> | undefined
let confirmedConnection: boolean | undefined
let disconnectNotified = false
let automaticOpenPending = false
let automaticOpenTimer: number | undefined
let awaitingConnection = false
let expectedSelection: string | undefined
let detectionSequence = 0
let awaitingAfter = 0

const busy = computed(() => submitting.value || core.value?.busy === true)
const selectedId = computed(() => core.value?.selectedId ?? discovery.value?.selectedId ?? 'panel')
const candidates = computed(() => discovery.value?.candidates.filter(candidate => candidate.id !== 'panel') ?? [])
const coreCandidates = computed(() => candidates.value
  .filter(candidate => candidate.running || candidate.connected || candidate.binary)
  .sort((first, second) => Number(isSelected(second)) - Number(isSelected(first))))
const panelSelected = computed(() => selectedId.value === 'panel')
const showPanelAction = computed(() => discovery.value && !scanning.value && panelSelected.value &&
  (!coreCandidates.value.length || panelChosen.value))
const showPanelChoice = computed(() => discovery.value && !scanning.value &&
  (!panelSelected.value || coreCandidates.value.length > 0 && !showPanelAction.value && !core.value?.connected && !coreCandidates.value.some(isSelected)))
const phaseLabels: Record<string, string> = {
  checking: '正在检测', fetching: '正在获取版本', release: '正在获取版本', downloading: '正在下载',
  extracting: '正在解压安装', installing: '正在安装', verifying: '正在校验',
  starting: '正在启动', connecting: '正在连接',
}
const phaseLabel = computed(() => phaseLabels[core.value?.phase ?? ''] ?? '正在准备')
const downloadPercent = computed(() => core.value?.total && core.value.total > 0
  ? Math.min(100, Math.floor((core.value.downloaded ?? 0) / core.value.total * 100)) : undefined)
const sourceLabel = computed(() => {
  if (!core.value) return '—'
  if (core.value.external) return core.value.source || '外部内核'
  if (core.value.managed) return '面板内核'
  if (core.value.connected) return coreCandidates.value.find(isSelected)?.source || '现有内核'
  return core.value.installed ? '本机内核' : '尚未选择'
})
const currentCandidate = computed(() => discovery.value?.candidates.find(isSelected))

function openManager() {
  automaticOpenPending = false
  window.clearTimeout(automaticOpenTimer)
  const modal = managerDialog.value
  if (modal && !modal.open && !document.querySelector('dialog:modal')) modal.showModal()
}

function closeManager() {
  managerDialog.value?.close()
}

function managerClosed() {
  automaticOpenPending = false
  window.clearTimeout(automaticOpenTimer)
  if (confirmedConnection !== true) disconnectNotified = true
}

function tryAutomaticOpen() {
  window.clearTimeout(automaticOpenTimer)
  if (!automaticOpenPending || disposed || document.hidden) return
  if (managerDialog.value?.open) {
    automaticOpenPending = false
    return
  }
  // Let an in-progress configuration dialog finish before asking for a core.
  if (document.querySelector('dialog:modal')) {
    automaticOpenTimer = window.setTimeout(tryAutomaticOpen, 250)
    return
  }
  openManager()
}

function observeConnection(result: CoreInstallation, sequence: number) {
  if (result.connected) {
    confirmedConnection = true
    disconnectNotified = false
    automaticOpenPending = false
    window.clearTimeout(automaticOpenTimer)
    if (awaitingConnection && sequence > awaitingAfter && (!expectedSelection || !result.selectedId || result.selectedId === expectedSelection)) {
      awaitingConnection = false
      expectedSelection = undefined
      closeManager()
      emit('connected')
    }
    return
  }
  if (confirmedConnection === true) disconnectNotified = false
  confirmedConnection = false
  if (!result.busy && !disconnectNotified) {
    disconnectNotified = true
    automaticOpenPending = true
    tryAutomaticOpen()
  }
}

async function detect(force = false): Promise<CoreInstallation | undefined> {
  if (disposed || !force && document.hidden) return
  if (detection) {
    const result = await detection
    if (!force || disposed) return result
  }
  const attempt = readCore()
  detection = attempt
  try {
    return await attempt
  } finally {
    if (detection === attempt) detection = undefined
  }
}

async function readCore(): Promise<CoreInstallation | undefined> {
  window.clearTimeout(timer)
  const sequence = ++detectionSequence
  const attempt = new AbortController()
  pending = attempt
  checking.value = true
  const deadline = window.setTimeout(() => attempt.abort(), 5000)
  try {
    const result = await request<CoreInstallation>('/api/core', undefined, 'GET', attempt.signal)
    if (disposed) return
    core.value = result
    statusFailure.value = ''
    if (wasBusy && !result.busy) {
      emit('configured')
      if (result.connected) emit('connected')
    }
    wasBusy = result.busy
    observeConnection(result, sequence)
    return result
  } catch (error) {
    if (!disposed) statusFailure.value = errorText(error)
  } finally {
    window.clearTimeout(deadline)
    pending = undefined
    checking.value = false
    if (!disposed && !document.hidden) timer = window.setTimeout(() => void detect(), core.value?.busy ? 1500 : 10000)
  }
}

async function connect() {
  if (busy.value || scanning.value || !discovery.value) return
  submitting.value = true
  failure.value = ''
  try {
    await request('/api/core/connect', {})
    wasBusy = true
    awaitingConnection = true
    awaitingAfter = detectionSequence
    expectedSelection = selectedId.value
    await detect(true)
  } catch (error) {
    awaitingConnection = false
    expectedSelection = undefined
    failure.value = errorText(error)
  } finally {
    submitting.value = false
  }
}

async function discover() {
  if (discoveryPending || disposed) return
  scanning.value = true
  discoveryFailure.value = ''
  const attempt = new AbortController()
  discoveryPending = attempt
  const deadline = window.setTimeout(() => attempt.abort(), 12000)
  try {
    const result = await request<CoreDiscovery>('/api/core/discovery', undefined, 'GET', attempt.signal)
    if (disposed || attempt.signal.aborted) return
    discovery.value = result
  } catch (error) {
    if (!disposed) discoveryFailure.value = attempt.signal.aborted ? '自动检测超时，请重新检测。' : errorText(error)
  } finally {
    window.clearTimeout(deadline)
    discoveryPending = undefined
    if (!disposed) scanning.value = false
  }
}

function recheck() {
  failure.value = ''
  void detect()
  void discover()
}

function isSelected(candidate: CoreCandidate) {
  return candidate.selected === true || candidate.id === selectedId.value
}

function candidateStatus(candidate: CoreCandidate) {
  if (isSelected(candidate) && core.value?.connected) return '当前使用'
  if (isSelected(candidate) && !candidate.connected) return '已选择，未连接'
  if (candidate.connected) return '可连接'
  return candidate.running ? '运行中，暂不可连接' : candidate.binary ? '已安装，未运行' : '发现配置，尚未接入'
}

function candidateName(candidate: CoreCandidate) {
  if (isSelected(candidate) && core.value?.managed) return '面板内核'
  return candidate.source
}

async function selectCandidate(candidate: CoreCandidate) {
  if (busy.value || scanning.value || candidate.running && !candidate.connected) return
  submitting.value = true
  selecting.value = candidate.id
  failure.value = ''
  try {
    const result = await request<{ selectedId?: string }>('/api/core/select', { id: candidate.id })
    if (disposed) return
    const chosenId = result.selectedId ?? candidate.id
    if (core.value) core.value.selectedId = chosenId
    if (discovery.value) {
      discovery.value.selectedId = chosenId
      discovery.value.candidates = discovery.value.candidates.map(item => ({ ...item, selected: item.id === chosenId }))
    }
    if (candidate.id === 'panel') panelChosen.value = true
    emit('configured')
    if (candidate.id !== 'panel' && !candidate.running && candidate.binary) {
      await request('/api/core/connect', {})
      wasBusy = true
    }
    awaitingConnection = true
    awaitingAfter = detectionSequence
    expectedSelection = chosenId
    await detect(true)
    await discover()
  } catch (error) {
    awaitingConnection = false
    expectedSelection = undefined
    if (!disposed) failure.value = errorText(error)
  } finally {
    submitting.value = false
    selecting.value = ''
  }
}

function selectPanel() {
  void selectCandidate({ id: 'panel', source: '面板内核', running: false, connected: false })
}

function visibility() {
  window.clearTimeout(timer)
  if (!document.hidden) {
    void detect()
    tryAutomaticOpen()
  }
}
onMounted(() => {
  void detect()
  void discover()
  document.addEventListener('visibilitychange', visibility)
})
onUnmounted(() => {
  disposed = true
  window.clearTimeout(timer)
  window.clearTimeout(automaticOpenTimer)
  pending?.abort()
  discoveryPending?.abort()
  document.removeEventListener('visibilitychange', visibility)
})
</script>

<template>
  <div class="core-manager">
    <dl class="core-summary-details">
      <div><dt>当前版本</dt><dd>{{ core?.version || '—' }}</dd></div>
      <div><dt>内核来源</dt><dd>{{ sourceLabel }}</dd></div>
    </dl>
    <p v-if="busy" class="core-summary-progress" role="status">{{ phaseLabel }}<template v-if="core?.phase === 'downloading' && downloadPercent !== undefined"> · {{ downloadPercent }}%</template></p>
    <el-button class="manage-core-button" @click="openManager"><Settings2 :size="15" aria-hidden="true" />管理内核</el-button>
  </div>
  <Teleport to="body">
    <dialog ref="managerDialog" class="confirmation core-management-dialog" aria-labelledby="core-management-title" aria-describedby="core-management-description" @close="managerClosed">
      <header class="core-management-heading">
        <div><h2 id="core-management-title">内核管理</h2><p id="core-management-description">连接已有内核，或由面板安装并运行。</p></div>
        <button type="button" class="icon-button" aria-label="关闭内核管理" @click="closeManager"><X :size="19" aria-hidden="true" /></button>
      </header>
      <div class="core-management-body">
        <div class="core-environment">
          <span class="environment-device"><Cpu :size="14" aria-hidden="true" />{{ core?.platform || '正在检测设备' }}</span>
          <span v-if="core?.autoStart && core.managed">随面板启动</span>
          <span v-else-if="core?.external">配置由原应用管理</span>
          <span v-else-if="!core?.connected">尚未选择内核</span>
        </div>
        <details v-if="!currentCandidate && (core?.binary || core?.connected && controller)" class="core-advanced standalone-details">
          <summary>内核详细信息</summary>
          <dl class="installation-details">
            <div v-if="core?.binary"><dt>内核路径</dt><dd>{{ core.binary }}</dd></div>
            <div v-if="controller"><dt>控制地址</dt><dd>{{ controller }}</dd></div>
          </dl>
        </details>
        <p v-if="scanning" class="core-message" role="status">正在自动检测本机已有内核，请稍候…</p>
        <p v-if="failure || discoveryFailure || statusFailure" class="core-message error" role="alert">{{ failure || discoveryFailure || statusFailure }}</p>
        <p v-else-if="core?.busy || core?.phase === 'error'" class="core-message" :class="{ error: core.phase === 'error' }" :role="core.phase === 'error' ? 'alert' : 'status'">{{ core.detail }}</p>
        <p v-else-if="core?.disabledReason && !core.connected" class="core-message">{{ core.disabledReason }}</p>
        <div v-if="core?.busy && core.phase === 'downloading'" class="core-download" role="group" aria-label="内核下载进度">
          <div class="core-download-summary">
            <span>{{ formatBytes(core.downloaded ?? 0) }}<template v-if="core.total"> / {{ formatBytes(core.total) }}</template></span>
            <span>{{ core.speed ? `${formatBytes(core.speed)}/s` : '等待下载数据…' }}<template v-if="downloadPercent !== undefined"> · {{ downloadPercent }}%</template></span>
          </div>
          <progress :value="downloadPercent" max="100" aria-label="内核下载进度" :aria-valuetext="downloadPercent === undefined ? '已下载 ' + formatBytes(core.downloaded ?? 0) : downloadPercent + '%'" />
        </div>
        <p v-if="core?.external" class="external-core-note">已接入现有内核，可查看状态、切换节点和查看日志。配置由原应用管理。</p>
        <ul v-if="coreCandidates.length" class="core-candidates" aria-label="检测到的已有内核">
          <li v-for="candidate in coreCandidates" :key="candidate.id" class="core-candidate" :class="{ selected: isSelected(candidate) }">
            <div class="candidate-row">
              <span class="candidate-icon" aria-hidden="true"><Cpu :size="20" :stroke-width="1.6" /></span>
              <div class="candidate-info">
                <div class="candidate-heading"><strong>{{ candidateName(candidate) }}</strong><span v-if="candidate.version" class="candidate-version">{{ candidate.version }}</span></div>
                <span class="candidate-status" :class="{ current: isSelected(candidate) && core?.connected, available: candidate.connected }">
                  <Check v-if="isSelected(candidate) && core?.connected" :size="13" aria-hidden="true" /><i v-else aria-hidden="true"></i>{{ candidateStatus(candidate) }}
                </span>
              </div>
              <el-button v-if="candidate.connected && (!isSelected(candidate) || core && !core.connected) || !candidate.running && candidate.binary && !(isSelected(candidate) && core?.connected)" type="primary"
                :loading="selecting === candidate.id" :disabled="busy || scanning" @click="selectCandidate(candidate)">
                {{ candidate.connected ? isSelected(candidate) ? '重新连接' : '连接现有内核' : isSelected(candidate) ? '启动此内核' : '使用并启动' }}
              </el-button>
            </div>
            <p v-if="candidate.reason" class="candidate-reason">{{ candidate.reason }}</p>
            <p v-if="!candidate.running && candidate.binary" class="candidate-reason">使用面板配置和代理端口启动。</p>
            <details v-if="candidate.binary || candidate.config || candidate.controller" class="core-advanced">
              <summary>详细信息</summary>
              <dl class="installation-details">
                <div v-if="candidate.binary"><dt>内核路径</dt><dd>{{ candidate.binary }}</dd></div>
                <div v-if="candidate.config"><dt>配置路径</dt><dd>{{ candidate.config }}</dd></div>
                <div v-if="candidate.controller"><dt>控制地址</dt><dd>{{ candidate.controller }}</dd></div>
              </dl>
            </details>
          </li>
        </ul>
        <div v-else-if="!scanning && discovery && !discoveryFailure && !core?.busy" class="core-empty" role="status">
          <Cpu :size="24" :stroke-width="1.5" aria-hidden="true" />
          <strong>{{ core?.connected ? '当前内核已连接' : '未发现可用内核' }}</strong>
          <p>{{ core?.connected ? '可以重新检测，查看可用的内核信息。' : '可以安装官方 Mihomo，或启动已有应用后重新检测。' }}</p>
        </div>
        <p v-for="warning in discovery?.warnings ?? []" :key="warning" class="core-message" role="status">{{ warning }}</p>
        <div v-if="showPanelChoice || core && !connected && !core.connected && showPanelAction" class="panel-core-option">
          <div><strong>{{ core?.installed && panelSelected ? '面板内核' : '安装独立内核' }}</strong><p>{{ core?.installed && panelSelected ? '使用现有安装，由面板启动和管理。' : '下载官方 Mihomo，使用面板配置运行。' }}</p><p v-if="busy" class="installation-note">关闭弹窗后，任务仍会继续。</p></div>
          <el-button v-if="core && !connected && !core.connected && !core.disabledReason && showPanelAction" type="primary" :loading="busy" :disabled="checking && !busy" @click="connect">
            <component :is="core.installed ? Play : Download" v-if="!busy" :size="16" aria-hidden="true" />
            {{ busy ? phaseLabel : core.installed ? '启动并连接' : '安装并连接' }}
          </el-button>
          <el-button v-if="showPanelChoice" :loading="selecting === 'panel'" :disabled="busy || scanning" @click="selectPanel">使用面板内核</el-button>
        </div>
      </div>
      <footer class="core-actions">
        <el-button :loading="checking || scanning" :disabled="busy || scanning" @click="recheck">
          <RefreshCw v-if="!checking && !scanning" :size="15" aria-hidden="true" />重新检测
        </el-button>
        <el-button class="core-done" @click="closeManager">完成</el-button>
      </footer>
    </dialog>
  </Teleport>
</template>

<style scoped>
.core-manager { display: grid; gap: 12px; }
.core-summary-details { display: grid; gap: 10px; margin: 0; }
.core-summary-details > div { display: flex; justify-content: space-between; align-items: baseline; gap: 16px; }
.core-summary-details dt { color: var(--muted); font-size: 12px; }
.core-summary-details dd { margin: 0; color: var(--ink); font-size: 13px; overflow-wrap: anywhere; text-align: right; }
.core-summary-progress { color: var(--accent-ink); font-size: 12px; }
.manage-core-button { justify-self: start; margin: 0; height: 34px; }
.manage-core-button :deep(.el-button > span) { gap: 7px; }
.core-management-dialog { width: min(600px, calc(100% - 32px)); max-height: min(760px, calc(100dvh - 48px)); padding: 0; overflow: hidden; border: 1px solid var(--line); border-radius: 14px; }
.core-management-dialog[open] { display: flex; flex-direction: column; }
.core-management-heading { display: flex; flex: none; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 24px 24px 20px; }
.core-management-heading h2 { margin: 0 0 6px; font-size: 19px; letter-spacing: -.025em; }
.core-management-heading p { margin: 0; color: var(--muted); font-size: 13px; }
.core-management-heading .icon-button { flex: none; margin: -3px -5px 0 0; }
.core-management-body { display: grid; gap: 16px; min-height: 0; overflow: auto; padding: 0 24px 24px; overscroll-behavior: contain; }
.core-management-body .core-message { margin: 0; }
.core-candidates { display: grid; gap: 10px; margin: 0; padding: 0; list-style: none; }
.core-candidate { min-width: 0; padding: 16px; border: 1px solid var(--line); border-radius: 8px; }
.core-candidate.selected { border-color: var(--live-line); background: var(--live-bg); }
.candidate-heading { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 9px; }
.candidate-heading strong { color: var(--heading); font-size: 14px; font-weight: 600; overflow-wrap: anywhere; }
.candidate-version, .candidate-reason { color: var(--muted); overflow-wrap: anywhere; }
.candidate-reason { font-size: 12px; line-height: 1.6; }
.core-advanced { min-width: 0; margin: 10px 0 0 52px; font-size: 12px; color: var(--muted); }
.core-advanced > summary { width: fit-content; cursor: pointer; line-height: 24px; }
.core-advanced[open] > summary { margin-bottom: 8px; }
.installation-details { display: grid; gap: 10px; margin: 0; padding-top: 10px; border-top: 1px solid var(--line); }
.installation-details > div { display: grid; grid-template-columns: 80px minmax(0, 1fr); gap: 16px; align-items: baseline; }
.installation-details dt { white-space: nowrap; color: var(--muted); font-size: 12px; }
.installation-details dd { margin: 0; color: var(--ink); font: 11px/1.7 var(--mono); overflow-wrap: anywhere; }
.core-message { padding: 12px; border-radius: 6px; background: var(--surface); color: var(--muted); font-size: 13px; line-height: 1.7; overflow-wrap: anywhere; white-space: pre-wrap; }
.core-message.error { color: var(--error); background: var(--error-bg); }
.core-download { display: grid; gap: 8px; }
.core-download-summary { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 12px; color: var(--muted); font-size: 12px; font-variant-numeric: tabular-nums; }
.core-download progress { display: block; width: 100%; height: 6px; border: 0; border-radius: 3px; overflow: hidden; accent-color: var(--accent); }
.core-download progress::-webkit-progress-bar { background: var(--line); border-radius: 3px; }
.core-download progress::-webkit-progress-value { background: var(--accent); border-radius: 3px; }
.core-actions { display: flex; flex: none; align-items: center; justify-content: space-between; gap: 12px; padding: 16px 24px; border-top: 1px solid var(--line); background: var(--surface); }
.core-actions .el-button { height: 36px; margin: 0; padding-inline: 12px; font-size: 14px; font-weight: 500; }
.core-actions :deep(.el-button > span) { display: inline-flex; align-items: center; gap: 8px; }
.core-environment { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 16px; padding-bottom: 16px; border-bottom: 1px solid var(--line); color: var(--muted); font-size: 12px; }
.environment-device { display: inline-flex; align-items: center; gap: 7px; font-family: var(--mono); }
.candidate-row { display: flex; align-items: center; gap: 12px; }
.candidate-icon { display: grid; place-items: center; flex: none; width: 40px; height: 40px; border: 1px solid var(--line); border-radius: 10px; color: var(--muted); background: var(--paper); }
.selected .candidate-icon { color: var(--accent-ink); border-color: var(--live-line); }
.candidate-info { display: grid; flex: 1; gap: 5px; min-width: 0; }
.candidate-status { display: inline-flex; align-items: center; gap: 5px; color: var(--muted); font-size: 12px; }
.candidate-status i { width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
.candidate-status.available { color: var(--success); }
.candidate-status.current { color: var(--accent-ink); }
.candidate-version { font: 11px/1.5 var(--mono); }
.candidate-reason { margin: 10px 0 0 52px; }
.standalone-details { margin: 0; }
.panel-core-option { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding-top: 16px; border-top: 1px solid var(--line); }
.panel-core-option strong { font-size: 13px; font-weight: 500; color: var(--ink); }
.panel-core-option p { margin: 5px 0 0; font-size: 12px; color: var(--muted); line-height: 1.6; }
.external-core-note { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.6; }
.core-empty { display: grid; justify-items: center; gap: 8px; padding: 16px 12px; text-align: center; color: var(--muted); }
.core-empty strong { margin-top: 4px; color: var(--heading); font-size: 14px; font-weight: 500; }
.core-empty p { margin: 0; font-size: 12px; line-height: 1.6; }
.panel-core-option .installation-note { color: var(--accent-ink); }
.candidate-row .el-button, .panel-core-option .el-button { flex: none; height: 34px; margin: 0; padding-inline: 12px; font-size: 12px; font-weight: 500; }
.panel-core-option :deep(.el-button > span) { display: inline-flex; align-items: center; gap: 7px; }
.core-actions .core-done { min-width: 72px; }
@media (max-width: 600px) {
  .core-management-dialog { width: calc(100% - 24px); max-height: calc(100dvh - 32px - env(safe-area-inset-top) - env(safe-area-inset-bottom)); }
  .core-management-heading { padding: 20px 16px 16px; }
  .core-management-body { padding: 0 16px 20px; }
  .core-management-heading h2 { font-size: 18px; }
  .core-management-heading .icon-button { min-width: 44px; min-height: 44px; margin-top: -8px; }
  .core-candidate { padding: 12px; }
  .candidate-row { flex-wrap: wrap; gap: 10px; }
  .candidate-icon { width: 36px; height: 36px; border-radius: 8px; }
  .candidate-info { flex-basis: calc(100% - 46px); }
  .candidate-row .el-button { margin-left: 46px; min-height: 44px; }
  .core-advanced, .candidate-reason { margin-left: 46px; }
  .standalone-details { margin-left: 0; }
  .installation-details > div { grid-template-columns: minmax(0, 1fr); gap: 3px; }
  .panel-core-option { align-items: flex-start; flex-direction: column; gap: 12px; }
  .core-actions { padding: 12px 16px calc(12px + env(safe-area-inset-bottom)); }
  .core-actions .el-button, .panel-core-option .el-button, .manage-core-button { min-height: 44px; }
}
</style>
