// Run against the Vite dev server. API responses are mocked; no real subscription is accessed.
// MIPROXY_TEST_URL overrides the default dev URL. PLAYWRIGHT_MODULE may point to an installed
// playwright/index.mjs when Playwright is provided by an external development runtime.
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
import assert from 'node:assert/strict'
const browser = await chromium.launch({channel:process.env.PLAYWRIGHT_CHANNEL ?? 'chrome', headless:true})
const page = await browser.newPage()
let logs = 0
let connected = false
let downloading = false
let applyResult = {saved:true,applied:false,backup:true,revision:'r2',detail:'Reload unavailable'}
let pendingApply
let applyCalls = 0
await page.route('**/api/**', async route => {
  const path = new URL(route.request().url()).pathname
  if (path === '/api/logs') { logs++; return route.fulfill({status:200, contentType:'application/x-ndjson',body:'{"type":"info","payload":"smoke log"}\n'}) }
  if (path === '/api/apply') { applyCalls++; await new Promise(resolve => { pendingApply = resolve }); return route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(applyResult)}) }
  const data = path === '/api/core/discovery' ? {candidates:[],warnings:[],selectedId:'panel'} : path === '/api/overview' ? {connected, controller:'http://127.0.0.1:19090', traffic:{samples:[]}} : path === '/api/core' ? {connected,installed:false,platform:'test',managed:false,autoStart:false,busy:downloading,phase:downloading ? 'downloading' : 'idle',downloaded:5242880,total:10485760,speed:1048576,detail:downloading ? '正在下载内核' : ''} : path === '/api/config' ? {content:'rules: [MATCH,DIRECT]',revision:'r',exists:true} : path === '/api/override' ? {content:'',revision:'r',exists:false} : path === '/api/subscriptions' ? [{id:'test',name:'测试订阅',kind:'file',updated:new Date().toISOString()}] : path === '/api/subscriptions/test' ? {content:'proxies: [{name: Example, type: direct}]\nproxy-groups: [{name: Test, type: select, proxies: [Example]}]\nrules: [MATCH,Test]'} : []
  return route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(data)})
})
const nav = name => page.locator('.sidebar').getByRole('button',{name,exact:true}).click()
try {
  await page.goto(process.env.MIPROXY_TEST_URL ?? 'http://127.0.0.1:18788/')
  await page.getByRole('dialog',{name:'内核管理',exact:true}).waitFor()
  await page.getByRole('button',{name:'关闭内核管理',exact:true}).click()
  await page.waitForTimeout(500)
  assert.equal(logs,0,'No log request without connected core on overview')
  await nav('日志')
  await page.getByText('Mihomo 尚未连接，连接内核后将开始接收日志。').waitFor()
  await page.waitForTimeout(2700)
  assert.equal(logs,0,'No log retries without connected core on logs page')
  connected = true
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  await page.getByText('smoke log').first().waitFor()
  assert.ok(logs>0)
  connected = false
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  await page.getByText('Mihomo 尚未连接，连接内核后将开始接收日志。').waitFor()
  const disconnectedLogs = logs
  await page.waitForTimeout(700)
  assert.equal(logs,disconnectedLogs,'Disconnecting the core cancels log reconnection')
  const manager = page.getByRole('dialog',{name:'内核管理',exact:true})
  if (await manager.isVisible()) await page.getByRole('button',{name:'关闭内核管理',exact:true}).click()
  await nav('概览')
  const stoppedLogs = logs
  await page.waitForTimeout(700)
  assert.equal(logs,stoppedLogs,'Leaving logs cancels reconnection')
  console.log('PASS logs wait for connected core and active page; stop on navigation')

  downloading = true
  await page.getByRole('button',{name:'管理内核',exact:true}).click()
  await page.getByRole('button',{name:'重新检测',exact:true}).click()
  const progress = page.getByRole('progressbar',{name:'内核下载进度'})
  await progress.waitFor()
  assert.equal(await progress.getAttribute('value'),'50')
  assert.ok((await page.locator('.core-download').innerText()).includes('5 MiB / 10 MiB'))
  assert.ok((await page.locator('.core-download').innerText()).includes('1 MiB/s'))
  console.log('PASS real download bytes, percentage, speed, and stage render')
  await page.getByRole('button',{name:'关闭内核管理',exact:true}).click()

  await nav('订阅')
  await page.getByRole('button',{name:'应用订阅',exact:true}).click()
  await page.getByRole('dialog',{name:'正在应用配置'}).waitFor()
  assert.equal(await page.locator('.operation-loading').evaluate(el=>el.matches(':modal')),true)
  await page.keyboard.press('Escape')
  assert.equal(await page.locator('.operation-loading').evaluate(el=>el.open),true,'Loading cannot be dismissed')
  assert.equal(applyCalls,1)
  pendingApply()
  await page.getByRole('dialog',{name:'正在应用配置'}).waitFor({state:'hidden'})
  assert.equal(await page.locator('.subscription-staged').count(),0,'Saved but failed reload must not show applied badge')
  await page.locator('.actionbar').getByText('配置已保存，Mihomo 重载失败。').waitFor()
  console.log('PASS global modal loading blocks interaction; failed reload is not marked applied')

  applyResult = {...applyResult,applied:true,revision:'r3'}
  await page.getByRole('button',{name:'应用订阅',exact:true}).click()
  await page.getByRole('dialog',{name:'正在应用配置'}).waitFor()
  pendingApply()
  await page.locator('.subscription-staged').waitFor()
  assert.ok((await page.locator('.subscription-staged').innerText()).includes('已应用'))
  console.log('PASS successfully reloaded subscription is marked applied')

  await nav('规则')
  await page.getByRole('button',{name:'添加规则',exact:true}).click()
  await page.getByLabel('匹配条件',{exact:true}).fill('example.com')
  await page.getByRole('button',{name:'保存并生效',exact:true}).click()
  await page.getByRole('dialog',{name:'正在应用配置'}).waitFor()
  assert.equal(await page.locator('.rule-drawer').evaluate(el=>el.matches(':modal')),true)
  assert.equal(await page.locator('.operation-loading').evaluate(el=>el.matches(':modal')),true)
  assert.equal(await page.evaluate(() => document.elementFromPoint(innerWidth/2,innerHeight/2)?.closest('dialog')?.classList.contains('operation-loading')),true,'Loading must cover already-open native rule modal')
  pendingApply()
  await page.locator('.rule-drawer').waitFor({state:'hidden'})
  console.log('PASS global loading covers native rule editor; closes after completion')
} finally { await browser.close() }
