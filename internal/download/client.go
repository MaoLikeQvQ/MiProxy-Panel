package download

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Go honors proxy environment variables, but does not read macOS network settings.
// Use the user's existing system HTTP proxy for downloads when no environment
// override is present. This leaves controller and subscription clients unchanged.
func NewClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 20 * time.Second
	if runtime.GOOS == "darwin" && !hasProxyEnvironment() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		output, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
		cancel()
		if err == nil {
			proxies := parseSystemDownloadProxies(string(output))
			transport.Proxy = func(request *http.Request) (*url.URL, error) {
				host := request.URL.Hostname()
				if strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback() {
					return nil, nil
				}
				return proxies[request.URL.Scheme], nil
			}
		}
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func hasProxyEnvironment() bool {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

func parseSystemDownloadProxies(output string) map[string]*url.URL {
	settings := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), " : ")
		if found {
			settings[key] = strings.TrimSpace(value)
		}
	}
	proxies := make(map[string]*url.URL)
	for _, protocol := range []string{"HTTP", "HTTPS"} {
		if settings[protocol+"Enable"] != "1" {
			continue
		}
		host := settings[protocol+"Proxy"]
		port, err := strconv.Atoi(settings[protocol+"Port"])
		if err != nil || port < 1 || port > 65535 || host == "" || strings.ContainsAny(host, "/@\r\n\t ") {
			continue
		}
		proxies[strings.ToLower(protocol)] = &url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port))}
	}
	return proxies
}
