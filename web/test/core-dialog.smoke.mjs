// Run against a local panel or Vite server. All API responses are mocked.
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
import assert from 'node:assert/strict'

const browser = await chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL ?? 'chrome', headless: true })
const baseURL = process.env.MIPROXY_TEST_URL ?? 'http://127.0.0.1:18788/'
const candidates = [{ id: 'sparkle', source: 'Sparkle', version: 'v-test', running: true, connected: true }]

async function fixture(options = {}) {
  const page = await browser.newPage({ viewport: options.mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 } })
  if (options.dark) await page.addInitScript(() => localStorage.setItem('miproxy-panel-appearance', 'dark'))
  let connected = options.connected ?? false
  let busy = options.busy ?? false
  let selectedId = 'panel'
  let states = 0
  await page.route('**/api/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/core') {
      states++
      if (options.deferStatus && states === 1) await page.waitForTimeout(600)
      if (options.statusError) return route.fulfill({ status: 503, contentType: 'application/json', body: '{"message":"状态读取失败"}' })
    }
    if (path === '/api/core/select') {
      selectedId = route.request().postDataJSON().id
      connected = true
    }
    const data = path === '/api/core' ? {
      connected, busy, installed: false, platform: 'darwin/arm64', managed: false, autoStart: false,
      selectedId, source: selectedId === 'sparkle' ? 'Sparkle' : '面板配置', external: selectedId === 'sparkle',
      version: connected ? 'v-test' : undefined, phase: busy ? 'downloading' : 'idle',
      downloaded: 5242880, total: 10485760, speed: 1048576, detail: busy ? '正在下载内核' : '',
    } : path === '/api/core/discovery' ? { candidates, warnings: [], selectedId }
      : path === '/api/overview' ? { connected, version: connected ? 'v-test' : '', controller: 'http://127.0.0.1:9090', traffic: { samples: [] } }
      : path === '/api/config' ? { content: 'rules: [MATCH,DIRECT]', exists: true, revision: 'r', externalConfig: selectedId === 'sparkle' }
      : path === '/api/override' ? { content: '', exists: false, revision: 'r' }
      : path === '/api/core/select' ? { selected: true } : []
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
  })
  await page.goto(baseURL + (options.logs ? '#logs' : ''))
  const dialog = page.getByRole('dialog', { name: '内核管理', exact: true })
  async function poll() {
    const before = states
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    for (let attempt = 0; states <= before && attempt < 30; attempt++) await page.waitForTimeout(50)
    assert.ok(states > before, 'The test must trigger another core state request')
    await page.waitForTimeout(100)
  }
  return { page, dialog, poll, online: value => { connected = value }, busy: value => { busy = value } }
}

try {
  const first = await fixture()
  await first.dialog.waitFor({ timeout: 2500 })
  assert.equal(await first.dialog.evaluate(element => element.matches(':modal')), true)
  await first.dialog.getByText('Sparkle', { exact: true }).waitFor()
  await first.dialog.getByRole('button', { name: '关闭内核管理', exact: true }).click()
  await first.dialog.waitFor({ state: 'hidden' })
  await first.poll()
  await first.poll()
  assert.equal(await first.dialog.isVisible(), false, 'Polling must respect dismissal')
  await first.page.getByRole('button', { name: '管理内核', exact: true }).click()
  await first.dialog.waitFor()
  await first.page.keyboard.press('Escape')
  await first.dialog.waitFor({ state: 'hidden' })
  await first.page.getByRole('button', { name: '管理内核', exact: true }).click()
  await first.dialog.getByRole('button', { name: '连接现有内核', exact: true }).click()
  await first.dialog.waitFor({ state: 'hidden' })
  await first.page.close()
  console.log('PASS disconnected auto prompt, dismissal, manual reopen, Escape and close after connection')

  const online = await fixture({ connected: true })
  await online.page.waitForTimeout(400)
  assert.equal(await online.dialog.isVisible(), false, 'Connected startup must not interrupt the dashboard')
  await online.page.getByRole('button', { name: '管理内核', exact: true }).click()
  await online.poll()
  assert.equal(await online.dialog.isVisible(), true, 'A manual management dialog stays open during connected polling')
  online.online(false)
  await online.poll()
  await online.page.keyboard.press('Escape')
  await online.page.waitForTimeout(400)
  assert.equal(await online.dialog.isVisible(), false, 'A disconnection while managing must not queue a second prompt after dismissal')
  online.online(true)
  await online.poll()
  online.online(false)
  await online.poll()
  await online.dialog.waitFor()
  await online.page.keyboard.press('Escape')
  await online.poll()
  assert.equal(await online.dialog.isVisible(), false)
  online.online(true)
  await online.poll()
  online.online(false)
  await online.poll()
  await online.dialog.waitFor()
  await online.page.close()
  console.log('PASS connected startup stays quiet, manual management remains open, disconnect prompts once per episode')

  const unavailable = await fixture({ statusError: true })
  await unavailable.page.waitForTimeout(400)
  assert.equal(await unavailable.dialog.isVisible(), false, 'A panel request error is not a confirmed core disconnection')
  await unavailable.page.close()

  const background = await fixture({ busy: true })
  await background.page.getByRole('button', { name: '管理内核', exact: true }).click()
  await background.dialog.getByRole('progressbar', { name: '内核下载进度' }).waitFor()
  await background.page.keyboard.press('Escape')
  await background.dialog.waitFor({ state: 'hidden' })
  await background.page.getByRole('button', { name: '管理内核', exact: true }).click()
  assert.equal(await background.dialog.getByRole('progressbar', { name: '内核下载进度' }).getAttribute('value'), '50')
  await background.page.close()
  console.log('PASS status failure does not prompt and background progress remains accessible')

  const blocked = await fixture({ deferStatus: true })
  await blocked.page.evaluate(() => {
    const blocker = document.createElement('dialog')
    blocker.id = 'existing-operation'
    document.body.append(blocker)
    blocker.showModal()
  })
  await blocked.page.waitForTimeout(800)
  assert.equal(await blocked.dialog.isVisible(), false, 'An existing modal retains priority')
  await blocked.page.evaluate(() => document.querySelector('#existing-operation').close())
  await blocked.dialog.waitFor()
  await blocked.page.close()
  console.log('PASS automatic prompt waits for an existing operation dialog')

  const mobile = await fixture({ mobile: true, dark: true, logs: true })
  await mobile.dialog.waitFor()
  const box = await mobile.dialog.boundingBox()
  assert.ok(box.x >= 0 && box.y >= 0 && box.x + box.width <= 390 && box.y + box.height <= 844, 'Dialog stays inside the mobile viewport')
  assert.equal(await mobile.page.locator('html').getAttribute('data-theme'), 'dark')
  await mobile.dialog.getByRole('button', { name: '关闭内核管理', exact: true }).click()
  await mobile.page.close()
  console.log('PASS mobile dark dialog fits the viewport and is visible outside the overview route')
} finally { await browser.close() }
