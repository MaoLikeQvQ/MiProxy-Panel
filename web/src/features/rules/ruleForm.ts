import { validateRuleProviderSettings } from '../configuration/yaml'
import type { RuleProviderEntry, RuleProviderSettings } from '../configuration/yaml'

/** Validated form data; the application owns YAML persistence and core reload. */
export type RuleSubmission = {
  index: number | null
  type: string
  rule: string
  provider?: RuleProviderSettings
}

type RuleForm = {
  index: number | null
  raw: string
  type: string
  value: string
  target: string
  providerSource: string
  provider: RuleProviderSettings
}

/** Validate the rule form before passing one complete change to the application. */
export function createRuleSubmission(form: RuleForm, providers: RuleProviderEntry[]): RuleSubmission {
  const { index, type } = form
  const value = form.value.trim()
  const target = form.target.trim()
  const prefix = type === 'IP-CIDR' ? '/32' : type === 'IP-CIDR6' ? '/128' : ''
  const match = value && !value.includes('/') ? value + prefix : value
  const parts = type === 'MATCH' ? [type, target] : [type, match, target]
  if (index === null && (parts.some(part => !part) || parts.some(part => /[,\r\n]/.test(part)))) {
    throw new Error('请填写匹配条件和策略，每次添加一条规则。')
  }
  if (index === null && type.startsWith('DOMAIN') && /[\s/:?#*]/.test(value)) {
    throw new Error('请输入域名或关键词。域名示例为 example.com。')
  }
  const rule = index === null ? parts.join(',') : form.raw.trim()
  if (!rule.includes(',') || /[\r\n]/.test(rule) || !rule.split(',').at(-1)?.trim()) {
    throw new Error('请输入一条含策略的 Mihomo 规则。')
  }
  if (index !== null || type !== 'RULE-SET') return { index, type, rule }
  if (form.providerSource !== 'url') {
    if (!providers.some(provider => provider.name === value)) {
      throw new Error('请选择已配置的规则集，或通过订阅地址添加。')
    }
    return { index, type, rule }
  }
  const provider = form.provider
  validateRuleProviderSettings(provider)
  if (providers.some(entry => entry.name === provider.name)) throw new Error('已存在同名规则集。')
  return { index, type, rule, provider }
}
