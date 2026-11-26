// Settings form regression with synthetic data; real auth is tested in internal/access.
import assert from 'node:assert/strict'
const { chromium }=await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const browser=await chromium.launch({channel:process.env.PLAYWRIGHT_CHANNEL ?? 'chrome',headless:true})
try{
 const page=await browser.newPage()
 let passwordSet=false,updates=0,unauthorized=false
 await page.route('**/api/**',route=>{
  const path=new URL(route.request().url()).pathname
  if(path==='/api/settings/access'){
   if(unauthorized)return route.fulfill({status:401,json:{message:'expired'}})
   if(route.request().method()==='POST'){updates++;passwordSet=Boolean(route.request().postDataJSON().password)}
   return route.fulfill({json:{passwordSet,localOnly:!passwordSet,username:'admin'}})
  }
  const data=path==='/api/core'?{connected:true,phase:'ready'}:path==='/api/overview'?{connected:true,traffic:{samples:[]}}:path==='/api/config'?{content:'rules: ["MATCH,DIRECT"]',revision:'r',exists:true}:path==='/api/override'?{content:'',revision:'r',exists:false}:[]
  return route.fulfill({json:data})
 })
 await page.goto((process.env.MIPROXY_TEST_URL??'http://127.0.0.1:18788/')+'#settings')
 const settings=page.getByRole('region',{name:'访问设置',exact:true})
 await settings.getByText('仅允许本机访问',{exact:true}).waitFor()
 await settings.getByLabel('访问密码',{exact:true}).fill('example-password')
 await settings.getByLabel('确认密码',{exact:true}).fill('different-password')
 await settings.getByRole('button',{name:'保存访问密码',exact:true}).click()
 await settings.getByText('两次输入的密码不一致。',{exact:true}).waitFor()
 assert.equal(updates,0)
 await settings.getByLabel('确认密码',{exact:true}).fill('example-password')
 await settings.getByRole('button',{name:'保存访问密码',exact:true}).click()
 await settings.getByText('已设置访问密码',{exact:true}).waitFor()
 assert.equal(updates,1)
 await settings.getByRole('button',{name:'清除访问密码',exact:true}).click()
 assert.equal(updates,1,'Clearing requires confirmation')
 await settings.getByRole('button',{name:'确认清除',exact:true}).click()
 await settings.getByText('仅允许本机访问',{exact:true}).waitFor()
 assert.equal(updates,2)
 unauthorized=true
 await settings.getByRole('button',{name:'刷新状态',exact:true}).click()
 await settings.getByRole('alert').getByText('访问凭据已变更，请刷新页面，使用用户名 admin 和当前密码重新登录。',{exact:true}).waitFor()
 console.log('PASS access settings: confirmation, password validation, save, clear, expired-credential feedback')
}finally{await browser.close()}
