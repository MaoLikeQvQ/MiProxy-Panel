package terminal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/runtimeenv"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/safeoutput"
)

// The terminal uses a private local socket, so it never asks for Web credentials.
type terminalClient struct {
	http *http.Client
}

func newTerminalClient(socket string) *terminalClient {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	return &terminalClient{http: &http.Client{
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		}},
		Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("本机控制接口不能重定向") },
	}}
}

func (c *terminalClient) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("无法连接本机面板服务，请在“服务管理”中启动，或检查 PANEL_CONFIG / PANEL_SOCKET 与服务是否一致")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct{ Message, Detail string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&failure)
		if failure.Message == "" {
			return fmt.Errorf("面板请求失败（HTTP %d）", resp.StatusCode)
		}
		return errors.New(safeoutput.Text(strings.TrimSpace(failure.Message + " " + failure.Detail)))
	}
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
		return err
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(output); err != nil {
		return errors.New("面板返回了无效响应")
	}
	return nil
}

func (c *terminalClient) logs(ctx context.Context, deliver func(string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/logs", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("无法连接内核日志")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("内核日志暂不可用，请检查内核连接")
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 256<<10)
	for scanner.Scan() {
		var entry struct{ Type, Payload string }
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			deliver(safeoutput.Text(strings.ToUpper(entry.Type) + "  " + entry.Payload))
		}
	}
	return scanner.Err()
}

// Read deployment values without sourcing a shell or printing credentials.
// Explicit process environment always wins over the deployment file.
func loadTerminalEnvironment(path string, required bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取部署环境文件：%w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return errors.New("部署环境文件必须是 64 KiB 以内的普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取部署环境文件：%w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		key, value, ok := strings.Cut(text, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "PANEL_PASSWORD" || key == "MIHOMO_SECRET" || !(strings.HasPrefix(key, "PANEL_") || strings.HasPrefix(key, "MIHOMO_")) {
			continue
		}
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				return fmt.Errorf("部署环境文件第 %d 行引号不完整", line)
			}
			value = value[1 : len(value)-1]
		} else if strings.HasPrefix(value, "\"") {
			value, err = strconv.Unquote(value)
			if err != nil {
				return fmt.Errorf("部署环境文件第 %d 行引号无效", line)
			}
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("部署环境文件第 %d 行变量无效", line)
		}
	}
	return scanner.Err()
}

func terminalConfigPath() (string, error) {
	return filepath.Abs(runtimeenv.Value("PANEL_CONFIG", "./config/config.yaml"))
}

func (c *terminalClient) switchSubscription(ctx context.Context, id string) (applyResult, error) {
	var result applyResult
	err := c.request(ctx, http.MethodPost, "/api/subscriptions/"+url.PathEscape(id)+"/apply", struct{}{}, &result)
	if err == nil && !result.Applied {
		err = errors.New("配置已保存，但内核重载失败：" + safeoutput.Text(result.Detail) + "；修复内核连接后请重新应用")
	}
	return result, err
}
