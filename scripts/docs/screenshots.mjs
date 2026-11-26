// Synthetic Web data and a real PTY terminal capture; no private core or subscription is accessed.
import { readFile, mkdir, readdir } from 'node:fs/promises'
import path from 'node:path'
import { execFileSync } from 'node:child_process'
import { tmpdir } from 'node:os'
const modulePath=process.env.PLAYWRIGHT_MODULE ?? 'playwright'
const { chromium }=await import(modulePath)
const output=path.resolve('docs/screenshots')
await mkdir(output,{recursive:true})
const browser=await chromium.launch({channel:process.env.PLAYWRIGHT_CHANNEL ?? 'chrome',headless:true})
try{
 const page=await browser.newPage({viewport:{width:1280,height:820},deviceScaleFactor:1})
 const now=Date.now()
 const samples=Array.from({length:40},(_,i)=>({time:now-(39-i)*3000,download:(1.1+Math.sin(i/3)*.6)*1048576,upload:(.14+Math.cos(i/4)*.06)*1048576,startsSegment:i===0}))
 const yaml='proxy-groups: [{name: 默认策略, type: select, proxies: [DIRECT, REJECT]}]\nrules: ["DOMAIN-SUFFIX,example.com,默认策略", "MATCH,DIRECT"]\n'
 await page.route('**/api/**',route=>{
  const endpoint=new URL(route.request().url()).pathname
  const data=endpoint==='/api/core'?{connected:true,version:'v1.19.34',installed:true,platform:'linux/arm64',managed:true,source:'面板内核',phase:'ready'}
   :endpoint==='/api/overview'?{connected:true,version:'v1.19.34',controller:'http://127.0.0.1:9090',counters:{active:18,downloaded:3.4*1073741824,uploaded:280*1048576,sampledAt:now},memory:32*1048576,traffic:{samples,rate:samples.at(-1)},history:{revision:'demo',events:[{id:2,time:now-180000,connected:true},{id:1,time:now-240000,connected:false}]}}
   :endpoint==='/api/config'?{content:yaml,revision:'demo',exists:true}
   :endpoint==='/api/override'?{content:'',revision:'demo',exists:false}
   :endpoint==='/api/core/discovery'?{candidates:[],warnings:[]}
   :endpoint==='/api/groups'?[{name:'默认策略',type:'Selector',now:'DIRECT',all:['DIRECT','REJECT']}]
   :endpoint==='/api/rules'?[{index:0,type:'DomainSuffix',payload:'example.com',proxy:'默认策略'},{index:1,type:'Match',payload:'',proxy:'DIRECT'}]:[]
  return route.fulfill({json:data})
 })
 await page.goto(process.env.MIPROXY_TEST_URL??'http://127.0.0.1:18788/')
 await page.waitForTimeout(700)
 await page.screenshot({path:path.join(output,'web-overview.png')})
 await page.locator('.sidebar').getByRole('button',{name:'规则',exact:true}).click()
 await page.getByRole('button',{name:'添加规则',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'添加规则',exact:true})
 await dialog.getByRole('combobox',{name:'匹配方式',exact:true}).locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")]').click()
 await dialog.getByRole('option',{name:'规则集',exact:true}).click()
 await dialog.getByLabel('规则集名称',{exact:true}).fill('广告拦截')
 await dialog.getByLabel('订阅地址',{exact:true}).fill('https://example.com/rules/ads.yaml')
 await page.screenshot({path:path.join(output,'web-rules.png')})
 const capture=path.join(tmpdir(),'miproxy-doc-terminal.ansi')
 execFileSync('python3',['scripts/docs/capture_terminal.py','--binary',process.env.MIPROXY_SCREENSHOT_BINARY??'artifacts/release/v0.1.1/miproxy-panel_darwin_arm64','--output',capture])
 const ansi=await readFile(capture,'utf8')
 const vite=path.join(path.dirname(modulePath),'../playwright-core/lib/vite/traceViewer')
 const xterm=(await readdir(path.join(vite,'assets'))).find(name=>name.startsWith('xtermModule-'))
 const css=(await readdir(vite)).find(name=>name.startsWith('xtermModule.')&&name.endsWith('.css'))
 await page.unrouteAll()
 await page.route('http://terminal.local/**',async route=>{
  const file=new URL(route.request().url()).pathname
  if(file==='/')return route.fulfill({contentType:'text/html',body:'<html><head><link rel="stylesheet" href="/'+css+'"></head><body style="margin:0;background:#172234;padding:22px"><div id="terminal"></div></body></html>'})
  if(file.endsWith('.js')){
   let js=await readFile(path.join(vite,file),'utf8')
   if(file.includes('xtermModule-'))js=js.replace(/^import[^;]+;/,'var e=(factory)=>{let value;return ()=>{if(!value){let module={exports:{}};factory(module.exports,module);value=module.exports}return value}};')
   return route.fulfill({contentType:'text/javascript',body:js})
  }
  return route.fulfill({contentType:'text/css',body:await readFile(path.join(vite,file),'utf8')})
 })
 await page.goto('http://terminal.local/')
 await page.evaluate(async({xterm,ansi})=>{
  const {default:module}=await import('/assets/'+xterm)
  const terminal=new module.Terminal({cols:112,rows:30,fontSize:16,fontFamily:'Menlo, monospace',theme:{background:'#172234',foreground:'#e7edf5'}})
  terminal.open(document.getElementById('terminal'))
  await new Promise(resolve=>terminal.write(ansi,resolve));terminal.blur()
 },{xterm,ansi})
 await page.waitForTimeout(300)
 await page.locator('body').screenshot({path:path.join(output,'terminal.png')})
 console.log('Captured Web overview, rule editor, and real terminal demo.')
}finally{await browser.close()}
