package main

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSystemDownloadProxySettings(t *testing.T) {
	proxies := parseSystemDownloadProxies(`<dictionary> {
  HTTPEnable : 1
  HTTPProxy : 127.0.0.1
  HTTPPort : 7890
  HTTPSEnable : 1
  HTTPSProxy : ::1
  HTTPSPort : 7891
}`)
	if proxies["http"].String() != "http://127.0.0.1:7890" || proxies["https"].String() != "http://[::1]:7891" {
		t.Fatalf("unexpected proxies: %v", proxies)
	}
	for _, output := range []string{
		"HTTPEnable : 0\nHTTPProxy : 127.0.0.1\nHTTPPort : 7890",
		"HTTPEnable : 1\nHTTPProxy : bad@host\nHTTPPort : 7890",
		"HTTPEnable : 1\nHTTPProxy : localhost\nHTTPPort : 65536",
	} {
		if len(parseSystemDownloadProxies(output)) != 0 {
			t.Fatalf("invalid or disabled proxy accepted: %s", output)
		}
	}
}

func TestDownloadClientHonorsExplicitProxyEnvironment(t *testing.T) {
	// ProxyFromEnvironment caches process environment on first use.
	if os.Getenv("MIPROXY_TEST_PROXY_CHILD") != "1" {
		t.Setenv("MIPROXY_TEST_PROXY_CHILD", "1")
		t.Setenv("HTTPS_PROXY", "http://127.0.0.1:12345")
		t.Setenv("https_proxy", "")
		t.Setenv("NO_PROXY", "")
		t.Setenv("no_proxy", "")
		output, err := exec.Command(os.Args[0], "-test.run=^TestDownloadClientHonorsExplicitProxyEnvironment$").CombinedOutput()
		if err != nil {
			t.Fatalf("proxy check failed: %s, %v", output, err)
		}
		return
	}
	client := newDownloadClient(time.Second)
	transport := client.Transport.(*http.Transport)
	proxy, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "github.com"}})
	if err != nil || proxy == nil || proxy.String() != "http://127.0.0.1:12345" {
		t.Fatalf("explicit proxy ignored: %v, %v", proxy, err)
	}
}
