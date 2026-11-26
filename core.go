package main

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const coreReleaseURL = "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest"

// The manager only owns processes it starts. Existing controllers remain external.
type coreManager struct {
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	download   *http.Client
	phase      string
	detail     string
	downloaded int64
	total      int64
	speed      float64
	busy       bool
	command    *exec.Cmd
	done       chan struct{}
	jobDone    chan struct{}
}

type coreSnapshot struct {
	Connected      bool    `json:"connected"`
	Version        string  `json:"version,omitempty"`
	Installed      bool    `json:"installed"`
	Binary         string  `json:"binary,omitempty"`
	Platform       string  `json:"platform"`
	Managed        bool    `json:"managed"`
	AutoStart      bool    `json:"autoStart"`
	Busy           bool    `json:"busy"`
	Phase          string  `json:"phase"`
	Detail         string  `json:"detail,omitempty"`
	DisabledReason string  `json:"disabledReason,omitempty"`
	Downloaded     int64   `json:"downloaded"`
	Total          int64   `json:"total"`
	Speed          float64 `json:"speed"`
	SelectedID     string  `json:"selectedId"`
	Source         string  `json:"source"`
	External       bool    `json:"external"`
}

func newCoreManager(parent context.Context) *coreManager {
	ctx, cancel := context.WithCancel(parent)
	download := newDownloadClient(10 * time.Minute)
	download.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 8 {
			return errors.New("内核下载重定向无效")
		}
		return nil
	}
	return &coreManager{
		ctx: ctx, cancel: cancel, phase: "idle",
		download: download,
	}
}

func (p *panelServer) coreDirectory() string {
	return filepath.Join(p.home, ".miproxy", "core")
}

func (p *panelServer) coreBinary() string {
	if selected := p.selection.Load(); selected != nil && selected.Binary != "" {
		return selected.Binary
	}
	if path, err := exec.LookPath(filepath.Join(p.coreDirectory(), "mihomo")); err == nil {
		return path
	}
	return p.mihomo
}

func (p *panelServer) coreDisabledReason() string {
	connection := p.connection()
	if connection.External {
		return "内核由外部应用管理，请在该应用中启动并检查控制接口。"
	}
	address, err := url.Parse(connection.Address)
	if err != nil || address.Scheme != "http" || !isLoopback(address.Hostname()) || address.Port() == "" ||
		address.Path != "" && address.Path != "/" {
		return "自动安装和启动需要将 MIHOMO_CONTROLLER 设置为本机 HTTP 控制地址，例如 http://127.0.0.1:9090。"
	}
	if filepath.Clean(p.runtimePath) != filepath.Clean(p.path) {
		return "当前配置使用独立的内核挂载路径，请在运行 Mihomo 的设备或容器中管理内核。"
	}
	if _, err := coreAssetPlatform(); err != nil {
		return err.Error()
	}
	return ""
}

func (p *panelServer) coreState(ctx context.Context) coreSnapshot {
	connection := p.connection()
	ctx = context.WithValue(ctx, coreConnectionContextKey{}, connection)
	snapshot := coreSnapshot{Platform: runtime.GOOS + "/" + runtime.GOARCH, Phase: "idle", DisabledReason: p.coreDisabledReason()}
	snapshot.SelectedID, snapshot.Source, snapshot.External = connection.ID, connection.Source, connection.External
	snapshot.Binary, _ = exec.LookPath(p.coreBinary())
	snapshot.Installed = snapshot.Binary != ""
	_, err := os.Stat(filepath.Join(p.coreDirectory(), "enabled"))
	snapshot.AutoStart = err == nil
	if connection.External {
		snapshot.AutoStart = false
	}
	if p.core != nil {
		p.core.mu.Lock()
		snapshot.Busy, snapshot.Phase, snapshot.Detail = p.core.busy, p.core.phase, p.core.detail
		snapshot.Managed = p.core.command != nil && !connection.External
		snapshot.Downloaded, snapshot.Total, snapshot.Speed = p.core.downloaded, p.core.total, p.core.speed
		p.core.mu.Unlock()
	}
	snapshot.Version = p.controllerVersion(ctx)
	snapshot.Connected = snapshot.Version != ""
	if connection.External {
		snapshot.Busy, snapshot.Phase, snapshot.Detail = false, "idle", ""
		if snapshot.Connected {
			snapshot.Phase, snapshot.Detail = "ready", "已连接现有内核，配置由原应用管理。"
		}
	}
	return snapshot
}

func (p *panelServer) detectCore(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, p.coreState(ctx))
}

func (p *panelServer) connectCore(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !readJSON(w, r, &input, 1024) {
		return
	}
	if p.core == nil {
		writeError(w, http.StatusServiceUnavailable, "内核管理尚未就绪。", nil)
		return
	}
	if reason := p.coreDisabledReason(); reason != "" {
		writeError(w, http.StatusConflict, reason, nil)
		return
	}
	if !p.beginCoreSetup() {
		writeError(w, http.StatusConflict, "内核安装或连接正在进行，请稍候。", nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (p *panelServer) beginCoreSetup() bool {
	m := p.core
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy || m.ctx.Err() != nil {
		return false
	}
	m.busy, m.phase, m.detail = true, "checking", "正在检测内核和控制接口…"
	m.downloaded, m.total, m.speed = 0, 0, 0
	m.jobDone = make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, 15*time.Minute)
		defer cancel()
		err := p.setupCore(ctx)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.busy = false
		if err != nil {
			m.phase, m.detail = "error", err.Error()
		} else {
			m.phase, m.detail = "ready", "Mihomo 已连接。"
		}
		close(m.jobDone)
	}()
	return true
}

func (m *coreManager) update(phase, detail string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.phase, m.detail = phase, detail
}

func (p *panelServer) setupCore(ctx context.Context) error {
	if reason := p.coreDisabledReason(); reason != "" {
		return errors.New(reason)
	}
	connection := p.connection()
	ctx = context.WithValue(ctx, coreConnectionContextKey{}, connection)
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	version := p.controllerVersion(probe)
	cancel()
	if version != "" {
		return nil
	}
	m := p.core
	m.mu.Lock()
	running := m.command != nil
	m.mu.Unlock()
	if running {
		return errors.New("面板启动的内核仍在运行，但控制接口不可达。请检查 MIHOMO_SECRET 和配置，勿重复启动。")
	}
	// A failed API request can mean bad authentication, not a missing process.
	address, _ := url.Parse(connection.Address)
	listener, err := net.Listen("tcp", address.Host)
	if err != nil {
		return errors.New("控制端口已被占用或无法监听。请检查现有内核、MIHOMO_CONTROLLER 和 MIHOMO_SECRET。")
	}
	_ = listener.Close()
	if err = os.MkdirAll(p.coreDirectory(), 0700); err != nil {
		return fmt.Errorf("无法创建内核目录：%w", err)
	}
	if _, err = exec.LookPath(p.coreBinary()); err != nil {
		if selected := p.selection.Load(); selected != nil && selected.Binary != "" {
			return errors.New("已选择的内核文件不可执行或已不存在，请重新检测后选择。")
		}
		if err = p.installCore(ctx); err != nil {
			return err
		}
	}
	m.update("verifying", "正在检查内核和启动配置…")
	versionCtx, versionCancel := context.WithTimeout(ctx, 5*time.Second)
	output, err := exec.CommandContext(versionCtx, p.coreBinary(), "-v").CombinedOutput()
	versionCancel()
	if err != nil || !strings.Contains(strings.ToLower(string(output)), "mihomo") {
		return errors.New("内核无法执行或不是 Mihomo，请检查二进制架构和 MIHOMO_BINARY。")
	}
	// Serialize initial configuration creation with normal saves.
	p.save.Lock()
	snapshot, err := p.readConfig()
	if err == nil && !snapshot.Exists {
		content, marshalErr := yaml.Marshal(map[string]any{
			"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "info",
			"external-controller": address.Host, "secret": connection.Secret, "rules": []string{"MATCH,DIRECT"},
		})
		err = marshalErr
		if err == nil {
			// Another service can create this file independently of the panel's lock.
			var file *os.File
			file, err = os.OpenFile(p.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if errors.Is(err, os.ErrExist) {
				snapshot, err = p.readConfig()
			} else if err == nil {
				_, writeErr := file.Write(content)
				err = errors.Join(writeErr, file.Sync(), file.Close())
				snapshot.Content = string(content)
			}
		}
	}
	if err == nil && connection.Secret == "" {
		var config struct {
			Secret string `yaml:"secret"`
		}
		if yaml.Unmarshal([]byte(snapshot.Content), &config) == nil && config.Secret != "" {
			err = errors.New("配置设置了控制密钥，请先将 MIHOMO_SECRET 配置为同一密钥再启动。")
		}
	}
	if err == nil {
		err = p.validate(ctx, snapshot.Content)
	}
	p.save.Unlock()
	if err != nil {
		return fmt.Errorf("启动配置检查失败：%w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	m.update("starting", "正在启动 Mihomo…")
	command := exec.CommandContext(m.ctx, p.coreBinary(), "-d", p.home, "-f", p.path, "-ext-ctl", address.Host)
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	// Environment avoids placing the API secret on the command line.
	command.Env = append(os.Environ(), "CLASH_OVERRIDE_SECRET="+connection.Secret)
	logs := &coreOutput{}
	command.Stdout, command.Stderr = logs, logs
	m.mu.Lock()
	err = command.Start()
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("启动内核失败：%w", err)
	}
	done := make(chan struct{})
	m.command, m.done = command, done
	m.mu.Unlock()
	go func() {
		err := command.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		m.command = nil
		if !m.busy && m.ctx.Err() == nil {
			m.phase, m.detail = "error", "面板启动的内核已退出，请重新检测并启动。"
			if err != nil {
				m.detail += " " + err.Error()
			}
		}
		close(done)
	}()
	m.update("connecting", "内核已启动，正在连接控制接口…")
	ready, readyCancel := context.WithTimeout(ctx, 45*time.Second)
	defer readyCancel()
	for {
		probe, cancel := context.WithTimeout(ready, time.Second)
		version := p.controllerVersion(probe)
		cancel()
		if version != "" {
			if err = atomicWrite(filepath.Join(p.coreDirectory(), "enabled"), []byte("enabled\n"), 0600); err != nil {
				p.stopManagedCore()
				return fmt.Errorf("无法保存内核自动启动设置：%w", err)
			}
			return nil
		}
		select {
		case <-done:
			return fmt.Errorf("内核启动后退出：%s", logs.message(connection.Secret))
		case <-ready.Done():
			p.stopManagedCore()
			return fmt.Errorf("内核控制接口连接超时，请检查配置和密钥。%s", logs.message(connection.Secret))
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (p *panelServer) stopManagedCore() {
	m := p.core
	m.mu.Lock()
	command, done := m.command, m.done
	if command != nil {
		_ = command.Process.Signal(syscall.SIGTERM)
	}
	m.mu.Unlock()
	if command != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}
}

func (p *panelServer) closeCore() {
	m := p.core
	m.cancel()
	m.mu.Lock()
	jobDone := m.jobDone
	m.mu.Unlock()
	if jobDone != nil {
		<-jobDone
	}
	p.stopManagedCore()
}

// Keep bounded startup diagnostics; the live log page takes over once connected.
type coreOutput struct {
	mu   sync.Mutex
	data []byte
}

func (o *coreOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data = append(o.data, data...)
	if len(o.data) > 4096 {
		o.data = o.data[len(o.data)-4096:]
	}
	return len(data), nil
}

func (o *coreOutput) message(secret string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	message := strings.TrimSpace(string(o.data))
	if secret != "" {
		message = strings.ReplaceAll(message, secret, "[密钥]")
	}
	return message
}

func coreAssetPlatform() (string, error) {
	arm := "7"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "GOARM" {
				arm = strings.Split(setting.Value, ",")[0]
			}
		}
	}
	return assetPlatform(runtime.GOOS, runtime.GOARCH, arm)
}

func assetPlatform(system, arch, arm string) (string, error) {
	if system == "darwin" && (arch == "amd64" || arch == "arm64") {
		if arch == "amd64" {
			arch = "amd64-v1"
		}
		return system + "-" + arch, nil
	}
	if system == "linux" {
		switch arch {
		case "amd64":
			arch = "amd64-v1"
		case "arm":
			if arm != "5" && arm != "6" && arm != "7" {
				return "", errors.New("无法识别 ARM 指令集，请手动配置 MIHOMO_BINARY。")
			}
			arch = "armv" + arm
		case "mips", "mipsle":
			arch += "-softfloat"
		case "arm64", "386", "mips64", "mips64le", "riscv64":
		default:
			return "", errors.New("当前架构暂不支持自动下载，请手动配置 MIHOMO_BINARY。")
		}
		return system + "-" + arch, nil
	}
	return "", errors.New("当前系统暂不支持自动下载，请手动配置 MIHOMO_BINARY。")
}

type coreRelease struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

func (p *panelServer) installCore(ctx context.Context) error {
	m := p.core
	m.update("fetching", "正在获取 Mihomo 官方稳定版…")
	response, err := m.downloadCoreFile(ctx, coreReleaseURL)
	if err != nil {
		return fmt.Errorf("获取官方版本失败：%w", err)
	}
	var release coreRelease
	err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&release)
	response.Body.Close()
	if err != nil {
		return errors.New("官方版本信息无效，请稍后重试。")
	}
	platform, err := coreAssetPlatform()
	if err != nil {
		return err
	}
	name := "mihomo-" + platform + "-" + release.Tag + ".gz"
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		digest, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
		if err != nil || !strings.HasPrefix(asset.Digest, "sha256:") || len(digest) != sha256.Size {
			return errors.New("官方版本缺少 SHA-256 校验值，已停止安装。")
		}
		address, err := url.Parse(asset.URL)
		if err != nil || address.Scheme != "https" || address.Host != "github.com" || !strings.HasPrefix(address.Path, "/MetaCubeX/mihomo/releases/download/") {
			return errors.New("官方版本的下载地址无效。")
		}
		m.update("downloading", "正在下载 Mihomo "+release.Tag+"（"+platform+"）…")
		response, err := m.downloadCoreFile(ctx, asset.URL)
		if err != nil {
			return fmt.Errorf("下载内核失败：%w", err)
		}
		defer response.Body.Close()
		m.mu.Lock()
		m.downloaded, m.total, m.speed = 0, max(response.ContentLength, 0), 0
		m.mu.Unlock()
		return p.unpackCore(ctx, &coreDownloadReader{
			source: response.Body, total: response.ContentLength, manager: m,
			label: "正在下载 Mihomo " + release.Tag,
		}, digest)
	}
	return fmt.Errorf("官方版本未提供 %s 的内核，请手动配置 MIHOMO_BINARY。", platform)
}

type coreDownloadReader struct {
	source          io.Reader
	manager         *coreManager
	label           string
	total, received int64
	updated         time.Time
	started         time.Time
}

func (r *coreDownloadReader) Read(data []byte) (int, error) {
	if r.started.IsZero() {
		r.started = time.Now()
	}
	size, err := r.source.Read(data)
	r.received += int64(size)
	if err != nil || time.Since(r.updated) >= 250*time.Millisecond {
		r.updated = time.Now()
		progress := fmt.Sprintf("%.1f MiB", float64(r.received)/(1<<20))
		if r.total > 0 {
			progress += fmt.Sprintf(" / %.1f MiB", float64(r.total)/(1<<20))
		}
		r.manager.mu.Lock()
		r.manager.phase, r.manager.detail = "downloading", r.label+" · "+progress
		r.manager.downloaded, r.manager.total = r.received, max(r.total, 0)
		r.manager.speed = float64(r.received) / max(time.Since(r.started).Seconds(), 0.001)
		r.manager.mu.Unlock()
	}
	return size, err
}

func (m *coreManager) downloadCoreFile(ctx context.Context, address string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "MiProxy-Panel")
	response, err := m.download.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("下载服务返回 HTTP %d，请检查网络或稍后重试", response.StatusCode)
	}
	return response, nil
}

func (p *panelServer) unpackCore(ctx context.Context, source io.Reader, digest []byte) error {
	// Hash the compressed release asset, including its gzip trailer, before executing it.
	archive, err := os.CreateTemp(p.coreDirectory(), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(source, (64<<20)+1))
	if err != nil {
		return fmt.Errorf("下载内核中断：%w", err)
	}
	if size > 64<<20 {
		return errors.New("内核压缩包超过 64 MiB，已停止安装。")
	}
	p.core.update("verifying", "正在校验 SHA-256 并解压内核…")
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(digest)) {
		return errors.New("内核 SHA-256 校验失败，已停止安装，请重新下载。")
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("内核压缩包无效：%w", err)
	}
	defer reader.Close()
	binary, err := os.CreateTemp(p.coreDirectory(), ".install-*")
	if err != nil {
		return err
	}
	defer os.Remove(binary.Name())
	defer binary.Close()
	size, err = io.Copy(binary, io.LimitReader(reader, (192<<20)+1))
	if err != nil {
		return fmt.Errorf("解压内核失败：%w", err)
	}
	if size == 0 || size > 192<<20 {
		return errors.New("解压后的内核大小无效。")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = binary.Chmod(0700); err != nil {
		return err
	}
	if err = binary.Sync(); err != nil {
		return err
	}
	if err = binary.Close(); err != nil {
		return err
	}
	if err = os.Rename(binary.Name(), filepath.Join(p.coreDirectory(), "mihomo")); err != nil {
		return err
	}
	return nil
}
