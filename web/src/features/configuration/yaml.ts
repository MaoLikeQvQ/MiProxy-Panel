import { isMap, isScalar, isSeq, parseDocument } from 'yaml'
import type { Document } from 'yaml'

type Path = (string | number)[]

export function scalar<T>(doc: Document, path: Path, fallback: T): T {
  const node = doc.getIn(path, true)
  return isScalar(node) && typeof node.value === typeof fallback ? node.value as T : fallback
}

export function strings(doc: Document, path: Path): string[] | null {
  const node = doc.getIn(path, true)
  if (node === undefined) return []
  if (!isSeq(node) || !node.items.every(item => isScalar(item) && typeof item.value === 'string')) return null
  return node.items.map(item => (item as { value: string }).value)
}

export function editableMap(doc: Document, path: Path): boolean {
  const node = doc.getIn(path, true)
  return node === undefined || (isMap(node) && !node.has('<<'))
}

export function setStrings(doc: Document, path: Path, values: string[]): void {
  const node = doc.getIn(path, true)
  if (!isSeq(node)) {
    doc.setIn(path, values)
    return
  }
  values.forEach((value, index) => {
    if (node.get(index) !== value) node.set(index, value)
  })
  while (node.items.length > values.length) node.delete(node.items.length - 1)
}

export type RuleProviderSettings = {
  name: string
  url: string
  behavior: string
  format: string
  interval: number
}

export type RuleProviderEntry = RuleProviderSettings & { kind: string; editable: boolean; rules?: string[] }

export function ruleProviderHost(url: string): string {
  if (!/^https?:\/\//i.test(url)) return ''
  try {
    return new URL(url).host
  } catch {
    return ''
  }
}

export function readRuleProviders(doc: Document): RuleProviderEntry[] | null {
  const providers = doc.getIn(['rule-providers'], true)
  if (providers === undefined) return []
  if (!isMap(providers) || providers.has('<<')) return null
  const entries: RuleProviderEntry[] = []
  for (const { key, value } of providers.items) {
    if (!isScalar(key) || typeof key.value !== 'string') return null
    const name = key.value
    const path = ['rule-providers', name]
    const kind = scalar<string>(doc, [...path, 'type'], '')
    entries.push({
      name,
      kind,
      url: scalar(doc, [...path, 'url'], ''),
      behavior: scalar(doc, [...path, 'behavior'], ''),
      format: scalar(doc, [...path, 'format'], 'yaml'),
      interval: scalar(doc, [...path, 'interval'], 0),
      editable: kind === 'http' && isMap(value) && !value.has('<<'),
    })
  }
  return entries
}

export const subscriptionRuleCondition = '(OR,((NETWORK,TCP),(NETWORK,UDP)))'

export function readSubRuleGroups(doc: Document): RuleProviderEntry[] {
  const groups = doc.getIn(['sub-rules'], true)
  if (!isMap(groups)) return []
  return groups.items.flatMap(({ key }) => {
    if (!isScalar(key) || typeof key.value !== 'string') return []
    const name = key.value
    const rules = strings(doc, ['sub-rules', name])
    if (rules === null) return []
    return [{ name, kind: 'sub-rule', editable: false, url: '', behavior: '', format: 'yaml', interval: 0, rules }]
  })
}

export function groupSubscriptionRules(doc: Document) {
  const rules = strings(doc, ['rules'])
  if (rules === null) throw new Error('当前规则结构需要在 YAML 中编辑。')
  if (!rules.length) return
  if (!editableMap(doc, ['sub-rules'])) throw new Error('当前子规则结构需要在 YAML 中编辑。')
  const groups = doc.getIn(['sub-rules'], true)
  let name = '订阅规则'
  let suffix = 2
  while (isMap(groups) && groups.has(name)) name = `订阅规则 ${suffix++}`
  const originalRules = doc.getIn(['rules'], true)
  doc.setIn(['sub-rules', name], originalRules)
  doc.set('rules', doc.createNode([`SUB-RULE,${subscriptionRuleCondition},${name}`]))
}

/** Validate a remote rule provider without modifying the YAML document. */
export function validateRuleProviderSettings(settings: RuleProviderSettings, editing = false) {
  const { name, url, behavior, format, interval } = settings
  if ((!editing && name !== name.trim()) || !name.trim() || /[,\r\n\x00]/.test(name) || name === '.' || name === '..') {
    throw new Error('请输入规则集名称，名称不能包含逗号或换行。')
  }
  if (!ruleProviderHost(url)) {
    throw new Error('请输入完整的 HTTP 或 HTTPS 规则集地址。')
  }
  if (!Number.isSafeInteger(interval) || interval < 0 || interval > 9_223_372_036) {
    throw new Error('请输入有效的非负整数更新间隔。')
  }
  if (!['domain', 'ipcidr', 'classical'].includes(behavior) || !['yaml', 'text', 'mrs'].includes(format) ||
    (format === 'mrs' && behavior === 'classical')) {
    throw new Error('请选择与远程文件匹配的规则类型和格式。MRS 仅支持域名或 IP 段。')
  }
}

export function writeRuleProvider(doc: Document, settings: RuleProviderSettings, editing: boolean, reject = false) {
  validateRuleProviderSettings(settings, editing)
  const { name, url, behavior, format, interval } = settings
  const entries = readRuleProviders(doc)
  const existing = entries?.find(entry => entry.name === name)
  if (entries === null || (editing && !existing?.editable)) {
    throw new Error('请在 YAML 源码中编辑这个规则集。')
  }
  if (!editing && existing) throw new Error('已存在同名规则集。')
  const rules = !editing || reject ? strings(doc, ['rules']) : []
  if (rules === null) throw new Error('当前规则列表的结构不支持自动添加规则集引用。')

  const path = ['rule-providers', name]
  const fields = { url, behavior, format, interval }
  if (editing) {
    for (const [field, value] of Object.entries(fields)) doc.setIn([...path, field], value)
  } else {
    doc.setIn(path, doc.createNode({ type: 'http', ...fields }))
  }
  if (!editing || reject) {
    const rule = `RULE-SET,${name},${reject ? 'REJECT' : 'DIRECT'}`
    const node = doc.getIn(['rules'], true)
    if (isSeq(node)) {
      const index = rules.indexOf(rule)
      const existingRule = index < 0 ? doc.createNode(rule) : node.items.splice(index, 1)[0]
      node.items.unshift(existingRule)
    } else doc.set('rules', doc.createNode([rule]))
  }
}

const routeFields = ['proxies', 'proxy-providers', 'proxy-groups', 'rule-providers', 'rules', 'sub-rules']
const subscriptionGeoFields = ['geodata-mode', 'geodata-loader', 'geosite-matcher', 'geox-url', 'geo-auto-update', 'geo-update-interval']
const maxConfig = 2 * 1024 * 1024

export function parseMapping(content: string) {
  const draft = parseDocument(content.trim() ? content : '{}', { uniqueKeys: true, merge: true })
  if (draft.errors.length || !isMap(draft.contents)) throw new Error('请输入有效的单份 YAML 映射。')
  return draft
}

function resolvedMapping(content: string): Map<string, unknown> {
  const result = parseMapping(content).toJS({ mapAsMap: true, maxAliasCount: 100 }) as unknown
  if (!(result instanceof Map) || Array.from(result.keys()).some(key => typeof key !== 'string')) {
    throw new Error('YAML 顶层字段名称须为字符串。')
  }
  return result
}

function serialize(draft: Document) {
  const content = draft.toString({ lineWidth: 0 })
  if (new TextEncoder().encode(content).length > maxConfig) throw new Error('生成的配置超过 2 MiB。')
  return content
}

export function loadSubscription(base: string, content: string, overlay: string) {
  const draft = parseMapping(base)
  const incoming = resolvedMapping(content)
  const replacesRouting = routeFields.slice(2).some(key => incoming.has(key))
  for (const key of routeFields) {
    if (incoming.has(key)) draft.set(key, draft.createNode(incoming.get(key)))
    else if (replacesRouting) draft.delete(key)
  }
  for (const key of subscriptionGeoFields) {
    if (incoming.has(key)) draft.set(key, draft.createNode(incoming.get(key)))
  }
  if (incoming.has('rules')) groupSubscriptionRules(draft)
  return serialize(overlay.trim() ? mergeOverride(draft, overlay) : draft)
}

function mergeOverride(draft: Document, overlay: string) {
  const values = resolvedMapping(overlay)
  const prepend = getPrependedRules(values)
  values.delete('prepend-rules')
  function merge(path: Path, value: unknown) {
    const current = draft.getIn(path, true)
    if (value instanceof Map && isMap(current) && !current.has('<<')) {
      for (const [key, child] of value) merge([...path, key], child)
    } else {
      draft.setIn(path, value instanceof Map || Array.isArray(value) ? draft.createNode(value) : value)
    }
  }
  for (const [key, value] of values) merge([key], value)
  if (prepend.length) {
    const existing = strings(draft, ['rules'])
    if (existing === null) throw new Error('现有 rules 不是字符串列表，无法前置规则。')
    const added = Array.from(new Set(prepend))
    const front = new Set(added)
    setStrings(draft, ['rules'], [...added, ...existing.filter(rule => !front.has(rule))])
  }
  return draft
}

export function applyOverride(base: string, overlay: string) {
  if (!base.trim()) throw new Error('请先载入订阅或 YAML 配置。')
  return serialize(mergeOverride(parseMapping(base), overlay))
}

export function checkOverride(content: string) {
  getPrependedRules(resolvedMapping(content))
}

function getPrependedRules(content: Map<string, unknown>): string[] {
  if (!content.has('prepend-rules')) return []
  const rules = content.get('prepend-rules')
  if (!Array.isArray(rules) || rules.some(rule => typeof rule !== 'string' || !rule.includes(','))) {
    throw new Error('prepend-rules 须为 Mihomo 规则字符串列表。')
  }
  return rules
}
