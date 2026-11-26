package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

var terminalRouteFields = []string{"proxies", "proxy-providers", "proxy-groups", "rule-providers", "rules", "sub-rules"}
var terminalGeoFields = []string{"geodata-mode", "geodata-loader", "geosite-matcher", "geox-url", "geo-auto-update", "geo-update-interval"}

// Resolve aliases before moving fields between documents. Otherwise replacing an
// anchored routing field can leave a local setting pointing to a missing anchor.
func mergeTerminalSubscription(base, incoming, overlay string) (string, error) {
	draft, err := terminalYAMLMapping(base)
	if err != nil {
		return "", fmt.Errorf("当前配置无效：%w", err)
	}
	subscription, err := terminalYAMLMapping(incoming)
	if err != nil {
		return "", fmt.Errorf("订阅配置无效：%w", err)
	}
	complete := false
	for _, key := range terminalRouteFields[2:] {
		complete = complete || terminalMapValue(subscription, key) != nil
	}
	for _, key := range terminalRouteFields {
		if value := terminalMapValue(subscription, key); value != nil {
			terminalSetMap(draft, key, value)
		} else if complete {
			terminalDeleteMap(draft, key)
		}
	}
	for _, key := range terminalGeoFields {
		if value := terminalMapValue(subscription, key); value != nil {
			terminalSetMap(draft, key, value)
		}
	}
	if terminalMapValue(subscription, "rules") != nil {
		if err := terminalGroupRules(draft); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(overlay) != "" {
		override, err := terminalYAMLMapping(overlay)
		if err != nil {
			return "", fmt.Errorf("覆写配置无效：%w", err)
		}
		prepend := terminalMapValue(override, "prepend-rules")
		var front []string
		if prepend != nil {
			front, err = terminalRuleStrings(prepend)
			if err != nil {
				return "", errors.New("prepend-rules 须为 Mihomo 规则字符串列表")
			}
			for _, rule := range front {
				if !strings.Contains(rule, ",") {
					return "", errors.New("prepend-rules 须为 Mihomo 规则字符串列表")
				}
			}
			terminalDeleteMap(override, "prepend-rules")
		}
		terminalMergeMapping(draft, override)
		if len(front) > 0 {
			existing, err := terminalRuleStrings(terminalMapValue(draft, "rules"))
			if err != nil {
				return "", errors.New("现有 rules 不是字符串列表，无法前置规则")
			}
			seen := make(map[string]bool)
			rules := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, rule := range front {
				if !seen[rule] {
					rules.Content = append(rules.Content, terminalYAMLString(rule))
					seen[rule] = true
				}
			}
			for _, rule := range existing {
				if !seen[rule] {
					rules.Content = append(rules.Content, terminalYAMLString(rule))
				}
			}
			terminalSetMap(draft, "rules", rules)
		}
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(draft); err != nil {
		return "", fmt.Errorf("生成配置失败：%w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	if output.Len() > maxConfig {
		return "", errors.New("生成的配置超过 2 MiB")
	}
	return output.String(), nil
}

func terminalYAMLMapping(content string) (*yaml.Node, error) {
	if len(content) > maxConfig {
		return nil, errors.New("YAML 配置超过 2 MiB")
	}
	if strings.TrimSpace(content) == "" {
		content = "{}"
	}
	decoder := yaml.NewDecoder(strings.NewReader(content))
	var document, extra yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("请输入有效的单份 YAML 映射")
	}
	if decoder.Decode(&extra) != io.EOF || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("请输入有效的单份 YAML 映射")
	}
	budget := 100000
	return terminalResolveYAML(document.Content[0], make(map[*yaml.Node]bool), &budget, 0)
}

func terminalResolveYAML(node *yaml.Node, stack map[*yaml.Node]bool, budget *int, depth int) (*yaml.Node, error) {
	if node == nil || depth > 100 || *budget <= 0 {
		return nil, errors.New("YAML 结构或锚点展开过于复杂")
	}
	*budget--
	if stack[node] {
		return nil, errors.New("YAML 包含循环锚点，无法安全合并")
	}
	stack[node] = true
	defer delete(stack, node)
	if node.Kind == yaml.AliasNode {
		return terminalResolveYAML(node.Alias, stack, budget, depth+1)
	}
	copy := *node
	copy.Anchor, copy.Alias, copy.Content = "", nil, nil
	switch node.Kind {
	case yaml.MappingNode:
		if len(node.Content)%2 != 0 {
			return nil, errors.New("YAML 映射结构无效")
		}
		seen := make(map[string]bool)
		var inherited []*yaml.Node
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || (key.Tag != "!!str" && key.Tag != "!!merge") {
				return nil, errors.New("YAML 映射字段名称须为字符串")
			}
			if seen[key.Value] {
				return nil, fmt.Errorf("YAML 包含重复字段 %q", key.Value)
			}
			seen[key.Value] = true
			value, err := terminalResolveYAML(node.Content[i+1], stack, budget, depth+1)
			if err != nil {
				return nil, err
			}
			if key.Tag == "!!merge" {
				if value.Kind == yaml.MappingNode {
					inherited = append(inherited, value)
				} else if value.Kind == yaml.SequenceNode {
					for _, item := range value.Content {
						if item.Kind != yaml.MappingNode {
							return nil, errors.New("YAML 合并锚点须指向映射")
						}
						inherited = append(inherited, item)
					}
				} else {
					return nil, errors.New("YAML 合并锚点须指向映射")
				}
				continue
			}
			keyCopy := *key
			keyCopy.Anchor = ""
			copy.Content = append(copy.Content, &keyCopy, value)
		}
		// Explicit fields override every merge source. Within a merge sequence,
		// earlier mappings override later mappings, as required by YAML merge.
		for _, source := range inherited {
			for i := 0; i < len(source.Content); i += 2 {
				if terminalMapValue(&copy, source.Content[i].Value) == nil {
					copy.Content = append(copy.Content, source.Content[i], source.Content[i+1])
				}
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			value, err := terminalResolveYAML(child, stack, budget, depth+1)
			if err != nil {
				return nil, err
			}
			copy.Content = append(copy.Content, value)
		}
	case yaml.ScalarNode:
	default:
		return nil, errors.New("YAML 包含无法合并的节点")
	}
	return &copy, nil
}

func terminalMapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func terminalSetMap(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, terminalYAMLString(key), value)
}

func terminalDeleteMap(mapping *yaml.Node, key string) {
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func terminalYAMLString(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func terminalRuleStrings(node *yaml.Node) ([]string, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, errors.New("规则须为字符串列表")
	}
	var values []string
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, errors.New("规则须为字符串列表")
		}
		values = append(values, item.Value)
	}
	return values, nil
}

func terminalGroupRules(draft *yaml.Node) error {
	rules := terminalMapValue(draft, "rules")
	values, err := terminalRuleStrings(rules)
	if err != nil {
		return errors.New("订阅 rules 须为字符串列表")
	}
	if len(values) == 0 {
		return nil
	}
	groups := terminalMapValue(draft, "sub-rules")
	if groups == nil {
		groups = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		terminalSetMap(draft, "sub-rules", groups)
	}
	if groups.Kind != yaml.MappingNode {
		return errors.New("订阅 sub-rules 须为映射")
	}
	name := "订阅规则"
	for suffix := 2; terminalMapValue(groups, name) != nil; suffix++ {
		name = fmt.Sprintf("订阅规则 %d", suffix)
	}
	terminalSetMap(groups, name, rules)
	terminalSetMap(draft, "rules", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{
		terminalYAMLString("SUB-RULE,(OR,((NETWORK,TCP),(NETWORK,UDP)))," + name),
	}})
	return nil
}

func terminalMergeMapping(current, override *yaml.Node) {
	for i := 0; i < len(override.Content); i += 2 {
		key, value := override.Content[i].Value, override.Content[i+1]
		existing := terminalMapValue(current, key)
		if value.Kind == yaml.MappingNode && existing != nil && existing.Kind == yaml.MappingNode && terminalMapValue(existing, "<<") == nil {
			terminalMergeMapping(existing, value)
		} else {
			terminalSetMap(current, key, value)
		}
	}
}
