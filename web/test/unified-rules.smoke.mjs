// Mocked API regression for the unified rule list; never writes to a real core.
import assert from 'node:assert/strict'
import { parse, stringify } from '../node_modules/yaml/dist/index.js'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const browser = await chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL ?? 'chrome', headless: true })
const url = process.env.MIPROXY_TEST_URL ?? 'http://127.0.0.1:18788/'
const initial = {
  rules: ['DOMAIN-SUFFIX,example.com,DIRECT', 'SUB-RULE,(OR,((NETWORK,TCP),(NETWORK,UDP))),订阅规则'],
  'sub-rules': { '订阅规则': ['DOMAIN-SUFFIX,subscription.example,REJECT', 'MATCH,DIRECT'] },
}
async function scenario({ external = false, offline = false } = {}) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  let config = stringify(initial), runtime = parse(config), applied = true, applyCalls = 0, configReads = 0
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.route('**/api/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/rules' && offline) return route.fulfill({ status: 503, json: { message: '内核未连接' } })
    if (path === '/api/apply') {
      applyCalls++
      config = route.request().postDataJSON().content
      if (applied) runtime = parse(config)
      return route.fulfill({ json: { saved: true, applied, revision: `r${applyCalls}`, detail: applied ? '' : '重载失败' } })
    }
    let data = []
    if (path === '/api/config') { configReads++; data = { content: config, revision: 'r', exists: true, externalConfig: external } }
    if (path === '/api/rules') data = runtime.rules.map((text, index) => {
      const parts = text.split(',')
      const type = parts.shift(), proxy = parts.pop()
      return { index, type: type === 'SUB-RULE' ? 'SubRules' : type, payload: parts.join(','), proxy }
    })
    if (path === '/api/overview') data = { connected: true, traffic: { samples: [] } }
    if (path === '/api/core') data = { connected: true, installed: true, platform: 'test', managed: !external, phase: 'idle' }
    if (path === '/api/core/discovery') data = { candidates: [], warnings: [] }
    if (path === '/api/override') data = { content: '', revision: 'r', exists: false }
    return route.fulfill({ json: data })
  })
  await page.goto(url)
  await page.locator('.sidebar').getByRole('button', { name: '规则', exact: true }).click()
  const list = page.getByRole('region', { name: '规则列表', exact: true })
  await list.locator('.runtime-rule-card').first().waitFor()
  for (const name of ['运行规则', '配置规则', '规则集', '订阅规则集', '整理为规则组'])
    assert.equal(await page.getByRole('button', { name, exact: true }).count(), 0, `${name} is removed`)
  const add = list.getByRole('button', { name: '添加规则', exact: true })
  assert.equal(await add.count(), 1)
  assert.equal(await add.evaluate(el => el.previousElementSibling?.getAttribute('aria-label')), '刷新规则')
  await list.getByRole('button', { name: '2 条规则', exact: true }).click()
  await list.getByRole('region', { name: '订阅规则 的规则', exact: true }).getByText('DOMAIN-SUFFIX,subscription.example,REJECT', { exact: true }).waitFor()
  await add.click()
  const dialog = page.getByRole('dialog', { name: '添加规则', exact: true })
  await dialog.waitFor()
  await dialog.getByLabel('匹配条件', { exact: true }).fill('new.example')
  const reads = configReads
  await dialog.getByRole('button', { name: external ? '更新配置' : '保存并生效', exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
  await list.getByText('new.example', { exact: true }).waitFor()
  assert.equal(configReads, reads, 'Automatic runtime refresh must preserve config and undo state')
  if (external) {
    await list.getByText('当前修改仅保存在草稿中，请导出后在原应用应用。', { exact: true }).waitFor()
    assert.equal(applyCalls, 0, 'External core is never written by rule form')
  } else {
    await list.getByRole('button', { name: '编辑规则 1', exact: true }).click()
    const edit = page.getByRole('dialog', { name: '编辑规则', exact: true })
    await edit.getByLabel('Mihomo 规则', { exact: true }).fill('DOMAIN-SUFFIX,edited.example,DIRECT')
    await edit.getByRole('button', { name: '保存并生效', exact: true }).click()
    await edit.waitFor({ state: 'hidden' })
    await list.getByText('edited.example', { exact: true }).waitFor()
  }
  await list.getByRole('button', { name: '删除规则 1', exact: true }).click()
  await list.getByRole('button', { name: '撤销删除', exact: true }).click()
  await list.getByText(external ? 'example.com' : 'edited.example', { exact: true }).waitFor()
  if (!external) {
    applied = false
    await add.click()
    await dialog.getByLabel('匹配条件', { exact: true }).fill('pending.example')
    await dialog.getByRole('button', { name: '保存并生效', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
    await list.getByText('以下为配置内容，尚未成功重载到内核。', { exact: true }).waitFor()
    await list.getByText('pending.example', { exact: true }).waitFor()
  }
  applied = true
  await add.click()
  await dialog.getByRole('combobox', { name: '匹配方式', exact: true }).locator('xpath=ancestor::div[contains(@class, "el-select__wrapper")]').click()
  await dialog.getByRole('option', { name: '规则集', exact: true }).click()
  await dialog.getByLabel('规则集名称', { exact: true }).fill('remote-test')
  await dialog.getByLabel('订阅地址', { exact: true }).fill('https://example.com/rules.yaml')
  await dialog.getByLabel('更新间隔（秒）', { exact: true }).fill('3600')
  await dialog.getByRole('combobox', { name: '匹配后走', exact: true }).locator('xpath=ancestor::div[contains(@class, "el-select__wrapper")]').click()
  await dialog.getByRole('option', { name: '拦截（REJECT）', exact: true }).click()
  await dialog.getByRole('button', { name: external ? '更新配置' : '保存并生效', exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
  await list.getByText('remote-test', { exact: true }).waitFor()
  let saved = parse(config)
  if (external) {
    const downloadEvent = page.waitForEvent('download')
    await page.getByRole('button', { name: '导出草稿', exact: true }).click()
    const download = await downloadEvent
    const { readFile } = await import('node:fs/promises')
    saved = parse(await readFile(await download.path(), 'utf8'))
    assert.equal(applyCalls, 0)
  }
  assert.deepEqual(saved['rule-providers']['remote-test'], { type: 'http', url: 'https://example.com/rules.yaml', behavior: 'classical', format: 'yaml', interval: 3600 })
  assert.equal(saved.rules[0], 'RULE-SET,remote-test,REJECT')
  assert.ok(!saved.rules.includes('RULE-SET,remote-test,DIRECT'), 'Only the chosen policy is referenced')
  await add.click()
  await dialog.getByRole('combobox', { name: '匹配方式', exact: true }).locator('xpath=ancestor::div[contains(@class, "el-select__wrapper")]').click()
  await dialog.getByRole('option', { name: '规则集', exact: true }).click()
  await dialog.getByRole('combobox', { name: '规则来源', exact: true }).locator('xpath=ancestor::div[contains(@class, "el-select__wrapper")]').click()
  await dialog.getByRole('option', { name: '已有规则集', exact: true }).click()
  assert.equal(await dialog.getByLabel('订阅地址', { exact: true }).count(), 0)
  await page.getByRole('button', { name: '关闭规则编辑', exact: true }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.evaluate(() => document.documentElement.dataset.theme = 'dark')
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Mobile has no page overflow')
  const boxes = await Promise.all([list.getByRole('button', { name: '刷新规则', exact: true }).boundingBox(), add.boundingBox()])
  assert.ok(boxes[0] && boxes[1] && Math.abs(boxes[0].y - boxes[1].y) < 2, 'Add stays beside refresh on mobile')
  assert.deepEqual(errors, [])
  await page.screenshot({ path: `/tmp/miproxy-unified-rules-${external ? 'external' : offline ? 'offline' : 'managed'}.png` })
  await page.close()
}
try {
  await scenario()
  console.log('PASS unified list, inline collection, direct add, edit/delete/undo, reload failure, mobile dark layout')
  await scenario({ external: true })
  console.log('PASS external draft remains editable and never applies to external core')
  await scenario({ offline: true })
  console.log('PASS offline configuration list and rule editing')
} finally { await browser.close() }
