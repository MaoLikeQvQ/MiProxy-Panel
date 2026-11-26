package server

import (
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/download"

	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const geoReleaseBase = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/"

type geoDependency struct {
	name, source string
	aliases      []string
	custom       bool
}

// Logical rules can nest GEOIP/GEOSITE matchers inside AND, OR and NOT.
var geoRulePattern = regexp.MustCompile(`(?i)(?:^|[(,])\s*(GEOIP|SRC-GEOIP|GEOSITE|IP-ASN|SRC-IP-ASN)\s*,\s*([^,()]+)`)

func geoDependencies(content string) ([]geoDependency, error) {
	var config map[string]any
	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		return nil, err
	}
	needed := make(map[string]bool)
	var scan func(any)
	scan = func(value any) {
		switch value := value.(type) {
		case string:
			if strings.HasPrefix(strings.ToLower(value), "geosite:") {
				needed["geosite"] = true
			}
			for _, match := range geoRulePattern.FindAllStringSubmatch(value, -1) {
				switch strings.ToUpper(match[1]) {
				case "GEOIP", "SRC-GEOIP":
					if !strings.EqualFold(strings.TrimSpace(match[2]), "LAN") {
						needed["geoip"] = true
					}
				case "GEOSITE":
					needed["geosite"] = true
				case "IP-ASN", "SRC-IP-ASN":
					needed["asn"] = true
				}
			}
		case []any:
			for _, item := range value {
				scan(item)
			}
		case map[string]any:
			for key, item := range value {
				scan(key)
				scan(item)
			}
		}
	}
	scan(config["rules"])
	scan(config["sub-rules"])
	if dns, ok := config["dns"].(map[string]any); ok && dns["enable"] == true {
		scan(dns["nameserver-policy"])
		scan(dns["fake-ip-filter"])
		if fallback, ok := dns["fallback"].([]any); ok && len(fallback) > 0 {
			// Mihomo defaults fallback-filter.geoip to true when omitted.
			filter, _ := dns["fallback-filter"].(map[string]any)
			if enabled, set := filter["geoip"]; !set || enabled == true {
				needed["geoip"] = true
			}
			if sites, ok := filter["geosite"].([]any); ok && len(sites) > 0 {
				needed["geosite"] = true
			}
		}
	}
	urls, _ := config["geox-url"].(map[string]any)
	var dependencies []geoDependency
	add := func(kind, name, filename string, aliases ...string) {
		source := geoReleaseBase + filename
		custom := false
		if value, ok := urls[kind].(string); ok {
			source, custom = value, value != source
		}
		dependencies = append(dependencies, geoDependency{name: name, source: source, aliases: aliases, custom: custom})
	}
	if needed["geoip"] {
		if config["geodata-mode"] == true {
			add("geoip", "GeoIP.dat", "geoip.dat", "GeoIP.dat")
		} else {
			add("mmdb", "geoip.metadb", "geoip.metadb", "Country.mmdb", "geoip.db", "geoip.metadb")
		}
	}
	if needed["geosite"] {
		add("geosite", "GeoSite.dat", "geosite.dat", "GeoSite.dat")
	}
	if needed["asn"] {
		add("asn", "ASN.mmdb", "GeoLite2-ASN.mmdb", "ASN.mmdb")
	}
	return dependencies, nil
}

// Prepare missing data with the panel's network settings before invoking -t.
// The kernel's isolated validation process has not applied the user's DNS yet.
func (p *panelServer) prepareGeoData(ctx context.Context, content string) error {
	dependencies, err := geoDependencies(content)
	if err != nil {
		return err
	}
	if len(dependencies) == 0 {
		return nil
	}
	p.geoData.Lock()
	defer p.geoData.Unlock()
	for _, dependency := range dependencies {
		if err := ctx.Err(); err != nil {
			return err
		}
		present, err := existingGeoData(p.connectionHome(ctx), dependency.aliases)
		if err != nil {
			return err
		}
		if present {
			continue
		}
		if err := p.downloadGeoData(ctx, dependency); err != nil {
			// Subscription URLs can contain access tokens; do not include them in errors.
			reason := err.Error()
			var requestError *url.Error
			if errors.As(err, &requestError) {
				reason = "无法连接下载服务"
			}
			if errors.Is(err, context.DeadlineExceeded) {
				reason = "下载超时"
			}
			if errors.Is(err, context.Canceled) {
				reason = "操作已取消"
			}
			return fmt.Errorf("缺少 %s，下载地理规则数据失败（%s）。请检查系统代理或网络连接后重新应用；也可将该文件放入 Mihomo 工作目录。", dependency.name, reason)
		}
	}
	return nil
}

func existingGeoData(home string, aliases []string) (bool, error) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		for _, alias := range aliases {
			if !strings.EqualFold(entry.Name(), alias) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return false, err
			}
			if !info.Mode().IsRegular() {
				return false, fmt.Errorf("地理数据 %s 必须为普通文件", entry.Name())
			}
			if info.Size() > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

func (p *panelServer) downloadGeoData(parent context.Context, dependency geoDependency) error {
	if !validSubscriptionURL(dependency.source) {
		return errors.New("地理数据下载地址无效")
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	client := download.NewClient(90 * time.Second)
	if p.core != nil {
		client = p.core.download
	}
	if dependency.custom {
		// Custom configuration must retain the same public-address boundary as subscriptions.
		client = p.subscriptions
		if client == nil {
			client = subscriptionClient()
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, dependency.source, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "MiProxy-Panel")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/json") {
		return errors.New("下载返回了非地理数据文件")
	}
	file, err := os.CreateTemp(p.connectionHome(ctx), ".miproxy-geodata-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(response.Body, (64<<20)+1))
	if err != nil {
		return err
	}
	if size == 0 || size > 64<<20 {
		return errors.New("地理数据文件大小无效")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(p.connectionHome(ctx), dependency.name))
}
