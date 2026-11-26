package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxConfig = 2 << 20

type panelServer struct {
	path, runtimePath, mihomo, home, control, secret string
	controlAddr                                      string
	externalConfig                                   bool
	reloadPayload                                    bool
	client                                           *http.Client
	subscriptions                                    *http.Client
	connectionHistory                                *connectionHistory
	trafficHistory                                   trafficHistory
	core                                             *coreManager
	geoData                                          sync.Mutex
	selection                                        atomic.Pointer[coreConnection]
	discoveryMu                                      sync.Mutex
	discoveryCandidates                              map[string]coreCandidate
	discoveryWarning                                 string
	save                                             sync.Mutex
	library                                          sync.Mutex
}

type configSnapshot struct {
	Content        string `json:"content"`
	Revision       string `json:"revision"`
	Exists         bool   `json:"exists"`
	ExternalConfig bool   `json:"externalConfig,omitempty"`
	mode           os.FileMode
}

type configInput struct {
	Content  string `json:"content"`
	Revision string `json:"revision"`
}

func (p *panelServer) readConfig() (configSnapshot, error) {
	if connection := p.connection(); connection.External && connection.Config != "" {
		if snapshot, err := readConfigFile(connection.Config); err == nil && snapshot.Exists {
			return snapshot, nil
		}
	}
	return readConfigFile(p.path)
}

func readConfigFile(path string) (configSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return configSnapshot{Revision: "missing", mode: 0600}, nil
	}
	if err != nil {
		return configSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return configSnapshot{}, errors.New("配置路径必须指向独立的普通文件")
	}
	if info.Size() > maxConfig {
		return configSnapshot{}, errors.New("配置超过 2 MiB")
	}

	file, err := os.Open(path)
	if err != nil {
		return configSnapshot{}, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxConfig+1))
	if err != nil {
		return configSnapshot{}, err
	}
	if len(content) > maxConfig {
		return configSnapshot{}, errors.New("配置超过 2 MiB")
	}
	return configSnapshot{
		Content:  string(content),
		Revision: revision(content),
		Exists:   true,
		mode:     info.Mode().Perm(),
	}, nil
}

func revision(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func (p *panelServer) getConfig(w http.ResponseWriter, _ *http.Request) {
	snapshot, err := p.readConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取配置失败。", err)
		return
	}
	snapshot.ExternalConfig = p.connection().External
	writeJSON(w, http.StatusOK, snapshot)
}

func (p *panelServer) validateConfig(w http.ResponseWriter, r *http.Request) {
	input, ok := readInput(w, r)
	if !ok {
		return
	}
	if err := p.validate(r.Context(), input.Content); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Mihomo 校验失败。", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"valid": true})
}

type applyResult struct {
	Saved    bool   `json:"saved"`
	Applied  bool   `json:"applied"`
	Backup   bool   `json:"backup"`
	Revision string `json:"revision"`
	Detail   string `json:"detail,omitempty"`
}

func (p *panelServer) applyConfig(w http.ResponseWriter, r *http.Request) {
	if p.connection().External {
		writeError(w, http.StatusForbidden, "请在管理 Mihomo 的应用中保存并应用配置。", nil)
		return
	}
	input, ok := readInput(w, r)
	if !ok {
		return
	}

	if !p.save.TryLock() {
		writeError(w, http.StatusConflict, "已有一次配置应用正在进行。请稍后重试。", nil)
		return
	}
	defer p.save.Unlock()
	current, err := p.readConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取配置失败。", err)
		return
	}
	if current.Revision != input.Revision {
		writeError(w, http.StatusConflict, "磁盘上的配置已更新。请导出草稿或重新读取后再保存。", nil)
		return
	}
	if err = p.validate(r.Context(), input.Content); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Mihomo 校验失败。", err)
		return
	}
	latest, err := p.readConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存前读取配置失败。", err)
		return
	}
	if latest.Revision != current.Revision {
		writeError(w, http.StatusConflict, "校验期间磁盘上的配置发生变更。", nil)
		return
	}
	if r.Context().Err() != nil {
		return
	}

	result := applyResult{Saved: true, Revision: revision([]byte(input.Content))}
	if current.Revision != result.Revision {
		if current.Exists {
			if err = atomicWrite(p.path+".bak", []byte(current.Content), 0600); err != nil {
				writeError(w, http.StatusInternalServerError, "创建备份失败。", err)
				return
			}
			result.Backup = true
		}
		if err = atomicWrite(p.path, []byte(input.Content), latest.mode); err != nil {
			writeError(w, http.StatusInternalServerError, "保存配置失败。", err)
			return
		}
	}

	if err = p.reload(r.Context(), input.Content); err != nil {
		result.Detail = err.Error()
	} else {
		result.Applied = true
	}
	writeJSON(w, http.StatusOK, result)
}

func (p *panelServer) validate(parent context.Context, content string) error {
	preparation, finish := context.WithTimeout(parent, 2*time.Minute)
	defer finish()
	connection := p.connectionFor(parent)
	preparation = context.WithValue(preparation, coreConnectionContextKey{}, connection)
	if err := p.prepareGeoData(preparation, content); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(p.path), ".miproxy-check-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(preparation, 35*time.Second)
	defer cancel()
	binary := connection.Binary
	if binary == "" {
		binary = p.coreBinary()
	}
	command := exec.CommandContext(ctx, binary, "-t", "-d", p.connectionHome(preparation), "-f", file.Name())
	output := &limitedOutput{}
	command.Stdout = output
	command.Stderr = output
	if err = command.Run(); err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("校验未完成：%w", ctx.Err())
	}
	if message := strings.TrimSpace(output.buffer.String()); message != "" {
		return errors.New(message)
	}
	return err
}

type limitedOutput struct{ buffer bytes.Buffer }

func (w *limitedOutput) Write(data []byte) (int, error) {
	if remaining := 8192 - w.buffer.Len(); remaining > 0 {
		w.buffer.Write(data[:min(len(data), remaining)])
	}
	return len(data), nil
}

func (p *panelServer) reload(parent context.Context, content string) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	input := struct {
		Path    string `json:"path,omitempty"`
		Payload string `json:"payload,omitempty"`
	}{Path: p.runtimePath}
	if p.reloadPayload {
		input.Path, input.Payload = "", content
	}
	payload, _ := json.Marshal(input)
	response, err := p.request(ctx, http.MethodPut, "/configs?force=true", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Mihomo 返回 HTTP %d：%s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

func (p *panelServer) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	version := p.controllerVersion(ctx)
	if version == "" {
		writeJSON(w, http.StatusOK, map[string]bool{"connected": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "version": version})
}

func (p *panelServer) controllerVersion(ctx context.Context) string {
	response, err := p.request(ctx, http.MethodGet, "/version", nil)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var version struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&version); err != nil {
		return ""
	}
	return version.Version
}

func (p *panelServer) request(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	connection := p.connectionFor(ctx)
	request, err := http.NewRequestWithContext(ctx, method, connection.Control+endpoint, body)
	if err != nil {
		return nil, err
	}
	if connection.Secret != "" {
		request.Header.Set("Authorization", "Bearer "+connection.Secret)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return connection.Client.Do(request)
}

func atomicWrite(path string, content []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".miproxy-save-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(content); err != nil {
		return err
	}
	if err = file.Chmod(mode); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func readInput(w http.ResponseWriter, r *http.Request) (configInput, bool) {
	var input configInput
	if !readJSON(w, r, &input, maxConfig*6+1024) {
		return input, false
	}
	if strings.TrimSpace(input.Content) == "" || len(input.Content) > maxConfig {
		writeError(w, http.StatusBadRequest, "请输入 YAML 配置，大小须在 2 MiB 以内。", nil)
		return input, false
	}
	return input, true
}

func readJSON(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "application/json" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, http.StatusUnsupportedMediaType, "请求须为同源 JSON。", nil)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "请求内容无效。", nil)
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, message string, err error) {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	writeJSON(w, status, map[string]string{"message": message, "detail": detail})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
