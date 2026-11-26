<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { X } from '@lucide/vue'
import { ElOption, ElSelect } from 'element-plus'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/option/style/css'
import { errorText, request } from '../../shared/api'
import type { RuntimeGroup } from '../../shared/api'
import type { RuleProviderEntry } from '../configuration/yaml'
import { createRuleSubmission } from './ruleForm'
import type { RuleSubmission } from './ruleForm'

const props = defineProps<{
  busy: boolean
  externalConfig: boolean
  targets: string[]
  providers: RuleProviderEntry[]
  save: (submission: RuleSubmission) => Promise<void>
}>()
const ruleDialog = ref<HTMLDialogElement>()
const saving = ref(false)
const disabled = computed(() => props.busy || saving.value)
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
const ruleTargets = computed(() => [...new Set([...props.targets, ...liveRuleTargets.value])])
const ruleTargetsLoading = ref(false)
const ruleError = ref('')

async function open(index?: number, rule = '', provider = '') {
  if (index === undefined && props.busy) return
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
  if (index === undefined && props.externalConfig && !ruleTargetsLoading.value) {
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
  if (disabled.value) return
  ruleError.value = ''
  try {
    const submission = createRuleSubmission({
      index: editingRule.value, raw: newRule.value, type: ruleType.value,
      value: ruleValue.value, target: ruleTarget.value || customRuleTarget.value,
      providerSource: ruleProviderSource.value,
      provider: { name: ruleValue.value.trim(), url: ruleProviderURL.value.trim(),
        format: ruleProviderFormat.value, behavior: ruleProviderBehavior.value, interval: Number(ruleProviderInterval.value) },
    }, props.providers)
    saving.value = true
    await props.save(submission)
    ruleDialog.value?.close()
  } catch (error) {
    ruleError.value = errorText(error)
  } finally {
    saving.value = false
  }
}

defineExpose({ open })
</script>

<template>
  <dialog ref="ruleDialog" class="confirmation import-dialog rule-drawer" aria-labelledby="rule-dialog-title" @cancel="disabled && $event.preventDefault()">
      <form @submit.prevent="commitRule">
        <div class="dialog-heading">
          <h2 id="rule-dialog-title">{{ editingRule === null ? '添加规则' : '编辑规则' }}</h2>
          <button type="button" class="icon-button" aria-label="关闭规则编辑" :disabled="disabled" @click="ruleDialog?.close()"><X :size="18" aria-hidden="true" /></button>
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
              <option v-for="provider in providers" :key="provider.name" :value="provider.name" />
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
          <button class="button secondary" type="button" :disabled="disabled" @click="ruleDialog?.close()">取消</button>
          <button class="button primary" :disabled="disabled">{{ disabled ? '正在保存…' : externalConfig ? '更新配置' : '保存并生效' }}</button>
        </div>
      </form>
  </dialog>
</template>
