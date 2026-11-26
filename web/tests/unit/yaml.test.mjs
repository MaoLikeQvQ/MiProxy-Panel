import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parse, parseDocument } from 'yaml'
import { applyOverride, loadSubscription, readRuleProviders, writeRuleProvider } from '../../src/features/configuration/yaml.ts'

test('subscription routes and saved overrides compose into one draft', () => {
  const local = `external-controller: 127.0.0.1:9090
secret: local
dns:
  enable: false
  nameserver: [1.1.1.1]
rule-providers:
  old: {type: file, path: old.yaml}
rules: ["MATCH,REJECT"]`
  const subscription = `node: &node {name: Example, type: direct}
proxies: [*node]
proxy-groups:
  - {name: Default, type: select, proxies: [Example]}
rules: ["DOMAIN-SUFFIX,example.com,Default", "MATCH,DIRECT"]
secret: remote`
  const overlay = `dns: {enable: true}
prepend-rules: ["DOMAIN,local.test,DIRECT"]`

  const draft = parse(loadSubscription(local, subscription, overlay))
  assert.equal(draft['external-controller'], '127.0.0.1:9090')
  assert.equal(draft.secret, 'local')
  assert.equal(draft['rule-providers'], undefined)
  assert.equal(draft.node, undefined)
  assert.equal(draft['prepend-rules'], undefined)
  assert.deepEqual(draft.dns, { enable: true, nameserver: ['1.1.1.1'] })
  assert.deepEqual(draft.proxies, [{ name: 'Example', type: 'direct' }])
  assert.deepEqual(draft.rules, ['DOMAIN,local.test,DIRECT', 'SUB-RULE,(OR,((NETWORK,TCP),(NETWORK,UDP))),订阅规则'])
  assert.deepEqual(draft['sub-rules']['订阅规则'], ['DOMAIN-SUFFIX,example.com,Default', 'MATCH,DIRECT'])
})

test('node-only subscriptions retain routing and prepended rules do not multiply', () => {
  const local = 'rules: ["MATCH,DIRECT"]\nproxy-groups: [{name: Local, type: select, proxies: [DIRECT]}]\n'
  const subscription = 'proxies: [{name: Example, type: direct}]\n'
  const overlay = 'prepend-rules: ["DOMAIN,local.test,DIRECT", "DOMAIN,local.test,DIRECT"]\n'

  const draft = loadSubscription(local, subscription, overlay)
  const repeated = parse(applyOverride(draft, overlay))
  assert.deepEqual(repeated['proxy-groups'], [{ name: 'Local', type: 'select', proxies: ['DIRECT'] }])
  assert.deepEqual(repeated.rules, ['DOMAIN,local.test,DIRECT', 'MATCH,DIRECT'])
})

test('adding and editing a remote rule provider preserves DNS and custom provider fields', () => {
  const doc = parseDocument('dns: {enable: true}\nrules: ["MATCH,DIRECT"]\n')
  const settings = {
    name: 'remote/example',
    url: 'https://example.test/rules.yaml',
    behavior: 'classical',
    format: 'yaml',
    interval: 3600,
  }
  writeRuleProvider(doc, settings, false)
  const path = ['rule-providers', settings.name]
  doc.setIn([...path, 'path'], './custom/rules.yaml')
  doc.setIn([...path, 'proxy'], 'DIRECT')
  assert.throws(() => writeRuleProvider(doc, settings, false), /同名/)

  const updated = { ...settings, url: 'https://example.test/rules.txt', format: 'text', interval: 90 }
  writeRuleProvider(doc, updated, true)
  assert.deepEqual(readRuleProviders(doc), [{ ...updated, kind: 'http', editable: true }])
  assert.deepEqual(doc.toJS()['rule-providers'][settings.name], {
    type: 'http', url: updated.url, behavior: 'classical', format: 'text', interval: 90,
    path: './custom/rules.yaml', proxy: 'DIRECT',
  })
  assert.equal(doc.getIn(['dns', 'enable']), true)
  assert.deepEqual(doc.toJS().rules, ['RULE-SET,remote/example,DIRECT', 'MATCH,DIRECT'])
})

test('rule provider form rejects non-HTTP sources and incompatible MRS input', () => {
  const doc = parseDocument('rules: ["MATCH,DIRECT"]')
  const settings = { name: 'remote', url: 'file:///data/rules.yaml', behavior: 'classical', format: 'yaml', interval: 0 }
  assert.throws(() => writeRuleProvider(doc, settings, false), /HTTP/)
  assert.throws(() => writeRuleProvider(doc, { ...settings, url: 'https://example.test/rules.mrs', format: 'mrs' }, false), /MRS/)
  assert.equal(doc.get('rule-providers'), undefined)
})

test('subscribing an ad rule provider prepends its reject rule without replacing existing rules', () => {
  const settings = { name: 'Ads', url: 'https://example.test/ads.mrs', behavior: 'domain', format: 'mrs', interval: 86400 }
  for (const source of ['', 'rules: ["DOMAIN,example.test,DIRECT", "MATCH,DIRECT"]']) {
    const doc = parseDocument(source)
    const previous = doc.toJS()?.rules ?? []
    writeRuleProvider(doc, settings, false, true)
    assert.deepEqual(doc.toJS().rules, ['RULE-SET,Ads,REJECT', ...previous])
    assert.equal(doc.getIn(['rule-providers', 'Ads', 'interval']), 86400)
    assert.throws(() => writeRuleProvider(doc, settings, false, true), /同名/)
    assert.deepEqual(doc.toJS().rules, ['RULE-SET,Ads,REJECT', ...previous])
  }
})

test('subscriptions keep their Geo data semantics while overlays remain authoritative', () => {
  const local = 'geodata-mode: false\ngeo-update-interval: 48\nrules: ["MATCH,DIRECT"]\n'
  const subscription = `proxies: [{name: Example, type: direct}]
geodata-mode: true
geodata-loader: standard
geosite-matcher: succinct
geox-url: {geoip: 'https://example.test/custom-geoip.dat'}
geo-auto-update: true
rules: ['GEOIP,CN,DIRECT', 'MATCH,DIRECT']`
  const draft = parse(loadSubscription(local, subscription, 'geo-auto-update: false'))
  assert.equal(draft['geodata-mode'], true)
  assert.equal(draft['geodata-loader'], 'standard')
  assert.equal(draft['geosite-matcher'], 'succinct')
  assert.deepEqual(draft['geox-url'], {geoip: 'https://example.test/custom-geoip.dat'})
  assert.equal(draft['geo-auto-update'], false)
  assert.equal(draft['geo-update-interval'], 48)
  const nodeOnly = parse(loadSubscription(local, 'proxies: [{name: Example, type: direct}]', ''))
  assert.equal(nodeOnly['geodata-mode'], false)
})
