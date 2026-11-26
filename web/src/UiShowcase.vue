<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { Check, CircleAlert, LoaderCircle, Plus, Search, X } from '@lucide/vue'
import { ElInput, ElOption, ElSelect } from 'element-plus'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/option/style/css'

const query = ref('')
const name = ref('')
const level = ref('all')
const tab = ref('runtime')
const dialog = ref<HTMLDialogElement>()
const pending = ref(false)
const notice = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
const colors = [
  { label: '页面', variable: '--canvas' },
  { label: '内容', variable: '--paper' },
  { label: '强调', variable: '--accent' },
  { label: '成功', variable: '--success' },
  { label: '警告', variable: '--draft' },
  { label: '错误', variable: '--error' },
]
function simulate() {
  pending.value = true
  notice.value = ''
  timer = setTimeout(() => {
    pending.value = false
    notice.value = '示例操作已完成'
  }, 1200)
}
onUnmounted(() => clearTimeout(timer))
</script>

<template>
  <div class="showcase">
    <p class="showcase-description">面板共用的组件与交互状态。切换右上角主题，可核对浅色与暗色效果。</p>
    <section class="sample-panel" aria-labelledby="sample-controls">
      <header><h2 id="sample-controls">输入与筛选</h2><span>36px 控件 · 6px 圆角 · 14px 文字</span></header>
      <div class="sample-body">
        <div class="sample-toolbar">
          <ElSelect v-model="level" aria-label="示例日志级别" class="sample-select">
            <ElOption value="all" label="所有级别" />
            <ElOption value="info" label="信息 · info" />
            <ElOption value="warning" label="警告 · warning" />
            <ElOption value="error" label="错误 · error" />
          </ElSelect>
          <label class="search-box"><Search :size="16" aria-hidden="true" /><input v-model="query" type="search" aria-label="示例搜索" placeholder="搜索日志内容"><button v-if="query" type="button" class="search-clear" aria-label="清除示例搜索" @click="query = ''"><X :size="14" /></button></label>
          <button type="button" class="button secondary" @click="query = ''; level = 'all'">重置筛选</button>
        </div>
        <div class="field-grid">
          <label class="field">订阅名称<ElInput v-model="name" aria-label="示例订阅名称" placeholder="输入订阅名称" clearable /></label>
          <label class="field">不可编辑<ElInput model-value="当前操作期间不可编辑" aria-label="示例禁用输入" disabled /></label>
        </div>
      </div>
    </section>
    <div class="sample-columns">
      <section class="sample-panel" aria-labelledby="sample-actions">
        <header><h2 id="sample-actions">操作与反馈</h2></header>
        <div class="sample-body">
          <div class="sample-row"><button type="button" class="button primary" :disabled="pending" @click="simulate"><LoaderCircle v-if="pending" :size="16" class="spinner" /><Plus v-else :size="16" />{{ pending ? '正在处理' : '示例操作' }}</button><button type="button" class="button secondary" @click="dialog?.showModal()">打开弹窗</button><button type="button" class="button quiet" @click="notice = '已触发文字操作'">文字操作</button></div>
          <div class="sample-row"><button type="button" class="button primary" disabled>主要操作</button><button type="button" class="button secondary" disabled>次要操作</button><button type="button" class="button danger" @click="notice = '已触发危险操作示例'">危险操作</button></div>
          <p class="sample-help">使用 Tab 查看键盘焦点，点击示例操作查看加载状态。</p>
          <p v-if="notice" class="inline-message success" role="status"><Check :size="16" aria-hidden="true" />{{ notice }}</p>
        </div>
      </section>
      <section class="sample-panel" aria-labelledby="sample-states">
        <header><h2 id="sample-states">标签与状态</h2></header>
        <div class="sample-body">
          <div class="sample-row"><span class="type-tag">Selector</span><span class="sample-status success"><i></i>已连接</span><span class="sample-status warning"><i></i>待应用</span><span class="sample-status error"><i></i>连接失败</span></div>
          <p class="inline-message success"><Check :size="16" aria-hidden="true" />配置已保存并应用</p>
          <p class="inline-message error"><CircleAlert :size="16" aria-hidden="true" />示例错误，请检查配置后重试</p>
        </div>
      </section>
    </div>
    <section class="sample-panel" aria-labelledby="sample-layout">
      <header><h2 id="sample-layout">视图切换与空状态</h2></header>
      <div class="sample-body">
        <div class="view-tabs sample-tabs" role="group" aria-label="示例视图"><button type="button" class="button quiet" :aria-pressed="tab === 'runtime'" @click="tab = 'runtime'">运行状态</button><button type="button" class="button quiet" :aria-pressed="tab === 'draft'" @click="tab = 'draft'">配置草稿</button></div>
        <div class="sample-empty"><Search :size="24" :stroke-width="1.5" aria-hidden="true" /><strong>{{ tab === 'runtime' ? '没有匹配的日志' : '还没有配置草稿' }}</strong><p>{{ tab === 'runtime' ? '调整级别或搜索关键词后重试。' : '导入已有配置后开始编辑。' }}</p></div>
      </div>
    </section>
    <section class="sample-panel" aria-labelledby="sample-colors"><header><h2 id="sample-colors">主题颜色</h2></header><div class="color-samples"><div v-for="color in colors" :key="color.variable"><i :style="{ background: `var(${color.variable})` }"></i><span>{{ color.label }}</span><code>{{ color.variable }}</code></div></div></section>
    <dialog ref="dialog" class="confirmation" aria-labelledby="sample-dialog-title"><form method="dialog"><div class="dialog-heading"><h2 id="sample-dialog-title">弹窗示例</h2><button type="submit" class="icon-button" aria-label="关闭弹窗示例"><X :size="18" /></button></div><p>弹窗会约束键盘焦点。按 Esc 或关闭按钮返回，焦点会回到打开弹窗的位置。</p><div class="dialog-actions"><button type="submit" class="button secondary" autofocus>关闭</button></div></form></dialog>
  </div>
</template>

<style scoped>
.showcase { display: grid; gap: 20px; }
.showcase-description { color: var(--muted); line-height: 1.7; }
.sample-panel { min-width: 0; border: 1px solid var(--line); border-radius: var(--panel-radius); background: var(--paper); }
.sample-panel > header { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; padding: 16px 20px; border-bottom: 1px solid var(--line); }
.sample-panel h2 { font-size: 16px; font-weight: 600; }
.sample-panel header > span { color: var(--muted); font-size: 12px; }
.sample-body { display: grid; gap: 20px; padding: 20px; }
.sample-toolbar, .sample-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.sample-select { width: 144px; }
.sample-toolbar .search-box { flex: 1; width: auto; }
.sample-columns { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; }
.sample-help { color: var(--muted); font-size: 12px; }
.sample-body .inline-message { display: flex; align-items: center; gap: 8px; margin: 0; }
.sample-status { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; }
.sample-status i { width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
.success { color: var(--success); }
.warning { color: var(--draft); }
.error { color: var(--error); }
.sample-tabs { justify-self: start; }
.sample-empty { display: grid; justify-items: center; gap: 8px; padding: 24px; color: var(--muted); text-align: center; }
.sample-empty strong { font-size: 14px; font-weight: 500; color: var(--ink); }
.sample-empty p { font-size: 12px; }
.color-samples { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 16px; padding: 20px; }
.color-samples > div { display: grid; gap: 8px; }
.color-samples i { height: 48px; border: 1px solid var(--line); border-radius: 6px; }
.color-samples code { font: 11px var(--mono); color: var(--muted); }
.search-clear { display: grid; place-items: center; width: 26px; height: 26px; padding: 0; border: 0; border-radius: 4px; background: transparent; color: var(--muted); }
.search-clear:hover { background: var(--hover); }
@media (max-width: 1000px) { .sample-columns { grid-template-columns: minmax(0, 1fr); } }
@media (max-width: 600px) {
  .sample-toolbar .search-box { flex-basis: calc(100% - 152px); }
  .sample-panel > header, .sample-body { padding: 16px; }
  .color-samples { grid-template-columns: repeat(3, minmax(0, 1fr)); }
}
</style>
