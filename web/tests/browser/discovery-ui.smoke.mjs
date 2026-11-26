// Start the Vite dev server before running. All API responses are mocked.
// PLAYWRIGHT_MODULE may point to an existing playwright/index.mjs installation.
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
import assert from 'node:assert/strict'
const browser = await chromium.launch({channel:process.env.PLAYWRIGHT_CHANNEL ?? 'chrome',headless:true})
const baseURL = process.env.MIPROXY_TEST_URL ?? 'http://127.0.0.1:18788/'
const available = [
  {id:'sparkle',source:'Sparkle',binary:'/Applications/Sparkle.app/Contents/MacOS/mihomo',config:'/private/sparkle/config.yaml',controller:'unix:/tmp/sparkle.sock',running:true,connected:true,version:'v-test-1'},
  {id:'other',source:'其他应用',running:true,connected:true,version:'v-test-2'},
  {id:'binary',source:'本机安装',binary:'/usr/local/bin/mihomo',running:false,connected:false},
  {id:'locked',source:'受保护内核',running:true,connected:false,reason:'控制密钥不可用，请在原应用启用控制接口。'},
]
async function fixture(options = {}) {
  const page = await browser.newPage()
  let scans = 0, states = 0, connects = 0, selected = 'panel', online = options.connected ?? false, external = false
  const selections = []
  let releaseDiscovery
  await page.route('**/api/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/core/discovery') {
      scans++
      if (options.holdDiscovery) await new Promise(resolve=>{ releaseDiscovery = resolve })
      if (options.scanError) return route.fulfill({status:500,contentType:'application/json',body:JSON.stringify({message:'自动检测失败'})})
      return route.fulfill({contentType:'application/json',body:JSON.stringify({candidates:options.candidates ?? available,warnings:options.warnings ?? [],selectedId:selected})})
    }
    if (path === '/api/core/select') {
      const {id} = route.request().postDataJSON()
      selections.push(id)
      if (options.selectError) return route.fulfill({status:409,contentType:'application/json',body:JSON.stringify({message:'该内核已退出，请重新检测。'})})
      selected = id
      external = available.some(item=>item.id===id && item.running)
      online = external
      return route.fulfill({contentType:'application/json',body:'{"selected":true}'})
    }
    if (path === '/api/core/connect') { connects++; online = true }
    if (path === '/api/core') states++
    const data = path === '/api/core' ? {connected:online,installed:selected==='binary',platform:'test',managed:online&&!external,autoStart:false,busy:options.busy ?? false,phase:options.busy?'checking':'idle',selectedId:selected,external,source:external?'Sparkle':undefined} : path === '/api/overview' ? {connected:online,controller:'http://127.0.0.1:19090',traffic:{samples:[]}} : path === '/api/config' ? {content:'rules: [MATCH,DIRECT]',revision:'r',exists:true,externalConfig:external} : path === '/api/override' ? {content:'',revision:'r',exists:false} : []
    return route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
  })
  await page.goto(baseURL)
  return {page, get scans(){return scans},get states(){return states},get connects(){return connects},selections, release:()=>releaseDiscovery?.()}
}
async function openManager(page) {
  const dialog = page.getByRole('dialog', {name:'内核管理',exact:true})
  if (!await dialog.isVisible()) await page.getByRole('button',{name:'管理内核',exact:true}).click()
  await dialog.waitFor()
}
try {
  const first = await fixture({holdDiscovery:true})
  await first.page.getByText('正在自动检测本机已有内核，请稍候…').waitFor()
  assert.equal(await first.page.getByRole('button',{name:'安装并连接',exact:true}).count(),0)
  first.release()
  await first.page.getByRole('list',{name:'检测到的已有内核'}).waitFor()
  assert.equal(first.scans,1)
  assert.deepEqual(first.selections,[],'Multiple detected instances require an explicit user selection')
  assert.equal(await first.page.getByRole('button',{name:'连接现有内核',exact:true}).count(),2)
  assert.equal(await first.page.getByRole('button',{name:'安装并连接',exact:true}).count(),0)
  const locked = first.page.locator('.core-candidate').filter({hasText:'受保护内核'})
  assert.equal(await locked.getByRole('button').count(),0,'Running but inaccessible core must not expose a start action')
  await locked.getByText('控制密钥不可用，请在原应用启用控制接口。').waitFor()
  assert.equal(await first.page.getByText('/private/sparkle/config.yaml',{exact:true}).isVisible(),false,'Paths stay in collapsed advanced details')
  await first.page.locator('.core-candidate').filter({hasText:'Sparkle'}).getByRole('button',{name:'连接现有内核',exact:true}).click()
  await first.page.getByRole('dialog',{name:'内核管理',exact:true}).waitFor({state:'hidden'})
  await openManager(first.page)
  await first.page.getByText('已接入现有内核，可查看状态、切换节点和查看日志。配置由原应用管理。').waitFor()
  assert.deepEqual(first.selections,['sparkle'])
  assert.equal(first.connects,0,'Selecting a running core must not launch a process')
  await first.page.getByRole('button',{name:'使用面板内核',exact:true}).click()
  await first.page.getByRole('button',{name:'安装并连接',exact:true}).waitFor()
  assert.deepEqual(first.selections,['sparkle','panel'])
  await first.page.locator('.core-candidate').filter({hasText:'本机安装'}).getByRole('button',{name:'使用并启动',exact:true}).click()
  await first.page.waitForFunction(()=>document.querySelector('.core-candidate.selected')?.textContent?.includes('本机安装'))
  assert.equal(first.connects,1,'An unused binary is selected before being started with panel configuration')
  assert.deepEqual(first.selections,['sparkle','panel','binary'])
  await first.page.close()
  console.log('PASS automatic scan, multi-instance explicit choice, safe actions, external connection, panel restore, binary reuse')

  const polling = await fixture({candidates:[],busy:true})
  await polling.page.waitForTimeout(1900)
  assert.ok(polling.states>=2,'Busy core state continues to poll')
  assert.equal(polling.scans,1,'Periodic state polling must not repeat expensive discovery')
  await polling.page.close()
  console.log('PASS discovery is separate from core status polling')

  const empty = await fixture({candidates:[]})
  await empty.page.getByRole('button',{name:'安装并连接',exact:true}).waitFor()
  await empty.page.getByRole('button',{name:'重新检测',exact:true}).click()
  await empty.page.waitForFunction(()=>!document.querySelector('.core-actions button.is-loading'))
  assert.equal(empty.scans,2,'Manual detection scans again')
  await empty.page.close()
  console.log('PASS clean machine offers installation; manual detection rescans')

  const failed = await fixture({selectError:true})
  await failed.page.getByRole('button',{name:'连接现有内核',exact:true}).first().click()
  await failed.page.getByRole('alert').filter({hasText:'该内核已退出，请重新检测。'}).waitFor()
  assert.equal(failed.connects,0)
  await failed.page.close()
  const unavailable = await fixture({scanError:true})
  await unavailable.page.getByRole('alert').filter({hasText:'自动检测失败'}).waitFor()
  assert.equal(await unavailable.page.getByRole('button',{name:'安装并连接',exact:true}).count(),0,'A failed scan must not be treated as no existing core')
  await unavailable.page.close()
  console.log('PASS selection/discovery errors remain visible without starting another core')

  const same = await fixture({candidates:[{...available[0],selected:true}],connected:true})
  await openManager(same.page)
  await same.page.getByRole('list',{name:'检测到的已有内核'}).waitFor()
  assert.equal(await same.page.getByRole('button',{name:'连接现有内核',exact:true}).count(),0,'Backend selected flag recognizes an already-used controller even with panel ID')
  await same.page.close()
  console.log('PASS backend selected marker avoids reconnecting the current controller')

  const renewed = await fixture({candidates:[{...available[0],selected:true}]})
  await renewed.page.getByRole('button',{name:'重新连接',exact:true}).click()
  await renewed.page.getByRole('dialog',{name:'内核管理',exact:true}).waitFor({state:'hidden'})
  await openManager(renewed.page)
  await renewed.page.getByText('已接入现有内核，可查看状态、切换节点和查看日志。配置由原应用管理。').waitFor()
  assert.deepEqual(renewed.selections,['sparkle'],'Rescanning can refresh an expired saved connection')
  await renewed.page.close()
  console.log('PASS rediscovery can refresh an expired saved connection')

  const orphan = await fixture({candidates:[{id:'old-config',source:'旧配置',config:'/private/old/config.yaml',running:false,connected:false}]})
  await orphan.page.getByRole('button',{name:'安装并连接',exact:true}).waitFor()
  assert.equal(await orphan.page.locator('.core-candidate').count(),0,'Config files alone are not installed cores')
  assert.equal(await orphan.page.getByText('/private/old/config.yaml',{exact:true}).count(),0)
  await orphan.page.close()
  console.log('PASS orphan configs are omitted and do not block a clean installation')
} finally { await browser.close() }
