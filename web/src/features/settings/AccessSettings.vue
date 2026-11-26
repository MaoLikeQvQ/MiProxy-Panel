<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RefreshCw, Save, ShieldCheck } from '@lucide/vue'
import { errorText, request } from '../../shared/api'

type AccessState = { passwordSet: boolean; username: string; localOnly: boolean }
const settings = ref<AccessState>()
const password = ref('')
const confirmation = ref('')
const loading = ref(false)
const saving = ref(false)
const failure = ref('')
const notice = ref('')
const confirmClear = ref(false)

function describe(error: unknown) {
  const status = error && typeof error === 'object' && 'status' in error ? error.status : undefined
  return status === 401 ? '访问凭据已变更，请刷新页面，使用用户名 admin 和当前密码重新登录。' : errorText(error)
}

async function load() {
  if (loading.value || saving.value) return
  loading.value = true
  failure.value = notice.value = ''
  try {
    settings.value = await request<AccessState>('/api/settings/access')
  } catch (error) {
    failure.value = describe(error)
  } finally {
    loading.value = false
  }
}

async function save(clear = false) {
  if (loading.value || saving.value || !settings.value) return
  failure.value = notice.value = ''
  if (!clear && ([...password.value].length < 8 || new TextEncoder().encode(password.value).length > 1024)) {
    failure.value = '访问密码至少 8 个字符，且不超过 1024 字节。'
    return
  }
  if (!clear && password.value !== confirmation.value) {
    failure.value = '两次输入的密码不一致。'
    return
  }
  saving.value = true
  try {
    settings.value = await request<AccessState>('/api/settings/access', { password: clear ? '' : password.value })
    password.value = confirmation.value = ''
    confirmClear.value = false
    notice.value = clear ? '已清除访问密码，Web 面板仅允许本机或 SSH 转发访问。' : '访问密码已保存。浏览器再次要求认证时，请使用用户名 admin 和新密码登录。'
  } catch (error) {
    failure.value = describe(error)
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="access-settings" aria-label="访问设置">
    <div class="access-settings-head">
      <div class="access-settings-state">
        <ShieldCheck :size="18" aria-hidden="true" />
        <span>{{ loading ? '正在读取访问设置…' : settings ? settings.passwordSet ? '已设置访问密码' : '仅允许本机访问' : '暂时无法读取访问设置' }}</span>
      </div>
      <button type="button" class="button secondary" :disabled="loading || saving" @click="load"><RefreshCw :size="15" :class="{ spinner: loading }" aria-hidden="true" />刷新状态</button>
    </div>
    <p class="hint">通过本机浏览器或 SSH 转发进入此页设置密码。设置后，Web 登录用户名为 admin；终端仍通过本机控制接口访问。</p>
    <form class="access-settings-form" @submit.prevent="save()">
      <label class="field">{{ settings?.passwordSet ? '新访问密码' : '访问密码' }}
        <input v-model="password" type="password" autocomplete="new-password" minlength="8" required :disabled="!settings || loading || saving" placeholder="至少 8 个字符">
      </label>
      <label class="field">确认密码
        <input v-model="confirmation" type="password" autocomplete="new-password" minlength="8" required :disabled="!settings || loading || saving" placeholder="再次输入访问密码">
      </label>
      <div class="access-settings-actions">
        <button type="submit" class="button primary" :disabled="!settings || loading || saving"><Save :size="15" aria-hidden="true" />{{ saving ? '正在保存…' : '保存访问密码' }}</button>
        <button v-if="settings?.passwordSet && !confirmClear" type="button" class="button quiet danger" :disabled="loading || saving" @click="confirmClear = true">清除访问密码</button>
      </div>
    </form>
    <div v-if="confirmClear" class="access-settings-clear" role="group" aria-label="确认清除访问密码">
      <p>清除后仅允许本机或 SSH 转发访问，当前远程连接将无法继续使用。</p>
      <div class="access-settings-actions">
        <button type="button" class="button secondary" :disabled="saving" @click="confirmClear = false">取消</button>
        <button type="button" class="button danger" :disabled="saving" @click="save(true)">确认清除</button>
      </div>
    </div>
    <p v-if="failure" class="inline-message error" role="alert">{{ failure }}</p>
    <p v-else-if="notice" class="inline-message success" role="status">{{ notice }}</p>
  </section>
</template>

<style scoped>
.access-settings { display: grid; gap: 18px; max-width: 760px; min-width: 0; }
.access-settings-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.access-settings-state { display: flex; align-items: center; gap: 8px; color: var(--heading); font-weight: 600; }
.access-settings > .hint { margin: 0; line-height: 1.8; }
.access-settings-form { display: grid; gap: 18px; max-width: 480px; }
.access-settings-form .field { margin: 0; }
.access-settings-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.access-settings-clear { display: grid; gap: 12px; padding: 16px; border: 1px solid var(--line); background: var(--surface); }
.access-settings-clear p { margin: 0; line-height: 1.7; }
.access-settings > .inline-message { margin: 0; }
@media (max-width: 600px) {
  .access-settings-head { align-items: flex-start; }
  .access-settings-head > .button { flex: none; }
  .access-settings-actions > .button { min-height: 40px; }
}
</style>
