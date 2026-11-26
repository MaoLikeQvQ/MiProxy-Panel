<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref, watch } from 'vue'
import { Check, FileCode2, ListPlus, RefreshCw, Save, Search } from '@lucide/vue'
import { errorText, request } from '../../shared/api'
import type { Snapshot } from '../../shared/api'
import { checkOverride } from './yaml'
import type Editor from './YamlEditor.vue'
const YamlEditor = defineAsyncComponent(() => import('./YamlEditor.vue'))

const props = defineProps<{ disabled: boolean; render: (content: string) => void; confirmReload: () => Promise<boolean> }>()
const emit = defineEmits<{ dirty: [changed: boolean] }>()
const source = ref('')
const saved = ref('')
const revision = ref<string>()
const busy = ref(true)
const failure = ref('')
const notice = ref('')
const editor = ref<InstanceType<typeof Editor>>()
const dirty = computed(() => source.value !== saved.value)
watch(dirty, changed => emit('dirty', changed))

async function load() {
  if (dirty.value && !await props.confirmReload()) return
  busy.value = true
  failure.value = notice.value = ''
  try {
    const result = await request<Snapshot>('/api/override')
    source.value = saved.value = result.content
    revision.value = result.revision
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    busy.value = false
  }
}

async function save() {
  busy.value = true
  failure.value = notice.value = ''
  try {
    checkOverride(source.value)
    const result = await request<Snapshot>('/api/override', { content: source.value, revision: revision.value })
    saved.value = result.content
    revision.value = result.revision
    notice.value = '已保存覆写。下次载入订阅时可以叠加这份覆写。'
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    busy.value = false
  }
}

function render() {
  failure.value = ''
  try {
    props.render(source.value)
  } catch (error) {
    failure.value = errorText(error)
  }
}

onMounted(load)
</script>

<template>
  <section class="override-view" aria-label="YAML 覆写">
    <div class="override-panel">
      <div class="editor-bar">
        <span class="filename"><FileCode2 :size="17" aria-hidden="true" />override.yaml<i v-if="dirty" title="覆写有未保存改动"></i></span>
        <div class="editor-tools">
          <button type="button" class="icon-button" title="搜索覆写内容" aria-label="搜索覆写内容" :disabled="busy || !revision" @click="editor?.search()"><Search :size="16" aria-hidden="true" /></button>
          <button type="button" class="button secondary" :disabled="busy" @click="load"><RefreshCw :size="16" :class="{ spinner: busy }" aria-hidden="true" />重新读取</button>
        </div>
      </div>
      <YamlEditor ref="editor" v-model="source" aria-label="YAML 覆写内容" :disabled="busy || !revision" />
      <div class="override-status" role="status">
        <span class="override-draft-state" :class="{ dirty }"><i v-if="dirty" aria-hidden="true"></i><Check v-else-if="revision && !busy" :size="14" aria-hidden="true" />{{ busy ? '正在处理覆写…' : dirty ? '覆写有未保存改动' : revision ? '覆写已保存' : '覆写尚未读取' }}</span>
        <span class="hint">映射逐层合并，列表整体替换。使用 prepend-rules 在最前面添加规则。</span>
      </div>
    </div>
    <div class="override-actions">
      <p class="hint">保存覆写后，载入订阅时可选择叠加。写入草稿后，在配置操作栏保存并应用。</p>
      <div class="override-action-buttons">
        <button type="button" class="button secondary" :disabled="busy || !revision || !dirty" @click="save"><Save :size="16" aria-hidden="true" />保存覆写</button>
        <button type="button" class="button primary" :disabled="disabled || busy || !source.trim()" @click="render"><ListPlus :size="16" aria-hidden="true" />写入草稿</button>
      </div>
    </div>
    <p v-if="failure" class="inline-message error" role="alert">{{ failure }}</p>
    <p v-else-if="notice" class="inline-message success" role="status">{{ notice }}</p>
  </section>
</template>

<style scoped>
.override-view { display: grid; gap: 16px; min-width: 0; }
.override-panel { overflow: hidden; border: 1px solid var(--line); border-radius: 10px; background: var(--paper); }
.override-panel .editor-bar { min-height: 58px; padding: 10px 16px; background: var(--paper); }
.override-panel .editor-tools { gap: 8px; }
.override-panel :deep(.cm-editor) { height: max(300px, calc(100dvh - 365px)); min-height: 300px; }
.override-status { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px 24px; padding: 10px 16px; border-top: 1px solid var(--line); background: var(--surface); }
.override-draft-state { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); font-size: 12px; }
.override-draft-state.dirty { color: var(--draft); }
.override-draft-state i { width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
.override-actions { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 0; border: 0; background: transparent; }
.override-actions > .hint { max-width: 55ch; line-height: 1.7; }
.override-action-buttons { display: flex; flex: none; gap: 8px; }
.override-view > .inline-message { margin: 0; }
@media (max-width: 768px) {
  .override-panel .editor-bar { padding: 8px 12px; }
  .override-panel :deep(.cm-editor) { height: max(280px, calc(100dvh - 440px)); min-height: 280px; }
  .override-status { padding: 10px 12px; }
  .override-actions { align-items: stretch; flex-direction: column; gap: 12px; }
  .override-action-buttons > .button { flex: 1; }
}
</style>
