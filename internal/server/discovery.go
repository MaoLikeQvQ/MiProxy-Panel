package server

import (
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const maxDiscoveredCores = 32

type coreCandidate struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	Binary     string `json:"binary,omitempty"`
	Config     string `json:"config,omitempty"`
	Home       string `json:"-"`
	Controller string `json:"controller,omitempty"`
	Secret     string `json:"-"`
	Running    bool   `json:"running"`
	Connected  bool   `json:"connected"`
	Version    string `json:"version,omitempty"`
	Reason     string `json:"reason,omitempty"`
	PID        int    `json:"-"`
	secrets    []string
}

type discoveredCoreProcess struct {
	PID         int
	Binary, Cwd string
	Args        []string
	// ps prints argv without preserving argument boundaries. Known flag values
	// must be reassembled when unquoted paths contain spaces.
	CollapsedArgs bool
	Reason        string
}

type discoveredCoreFlags struct {
	Home, Config, Controller, Socket, Secret string
	HasController, HasSocket, HasSecret      bool
}

func (p *panelServer) discoverCores(parent context.Context) ([]coreCandidate, []string) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	processes, warnings := scanLocalCoreProcesses(ctx)
	candidates := collectCoreCandidates(ctx, processes, p.localCoreInstallations())
	if ctx.Err() != nil {
		warnings = append(warnings, "扫描已达到时间限制，结果可能不完整。")
	}
	return candidates, warnings
}

func isCoreExecutable(path string) bool {
	name := strings.ToLower(filepath.Base(strings.TrimSuffix(path, " (deleted)")))
	return strings.Contains(name, "mihomo") || name == "clash" || name == "clash-meta" || name == "clash_meta" || name == "crashcore" || name == "clash.meta"
}

func scanLocalCoreProcesses(ctx context.Context) ([]discoveredCoreProcess, []string) {
	if runtime.GOOS == "linux" {
		return scanProcCores(ctx, "/proc")
	}
	if runtime.GOOS != "darwin" {
		return nil, []string{"当前系统仅检查常见安装位置。"}
	}
	output, err := discoveryCommand(ctx, "/bin/ps", "-axo", "pid=,comm=")
	if err != nil {
		return nil, []string{"无法读取本机进程列表，已继续检查常见安装位置。"}
	}
	var found []discoveredCoreProcess
	for _, line := range strings.Split(string(output), "\n") {
		if ctx.Err() != nil || len(found) >= maxDiscoveredCores {
			break
		}
		pid, binary, ok := parseCorePSLine(line)
		if !ok || !isCoreExecutable(binary) {
			continue
		}
		process := discoveredCoreProcess{PID: pid, Binary: binary, CollapsedArgs: true}
		args, err := discoveryCommand(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "command=")
		if err != nil {
			process.Reason = "无权限读取运行中内核的启动参数。"
		} else {
			raw := strings.TrimSpace(string(args))
			raw = strings.TrimSpace(strings.TrimPrefix(raw, binary))
			process.Args = splitCoreCommand(raw)
		}
		cwd, _ := discoveryCommand(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn")
		for _, field := range strings.Split(string(cwd), "\n") {
			if strings.HasPrefix(field, "n/") {
				process.Cwd = strings.TrimPrefix(field, "n")
				break
			}
		}
		found = append(found, process)
	}
	return found, nil
}

func scanProcCores(ctx context.Context, root string) ([]discoveredCoreProcess, []string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, []string{"无法读取本机进程列表，已继续检查常见安装位置。"}
	}
	var found []discoveredCoreProcess
	for index, entry := range entries {
		if index >= 8192 || ctx.Err() != nil || len(found) >= maxDiscoveredCores {
			break
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		comm, _ := readDiscoveryFile(filepath.Join(dir, "comm"), 1024)
		binary, _ := os.Readlink(filepath.Join(dir, "exe"))
		if !isCoreExecutable(binary) && !isCoreExecutable(strings.TrimSpace(string(comm))) {
			continue
		}
		process := discoveredCoreProcess{PID: pid, Binary: strings.TrimSuffix(binary, " (deleted)")}
		args, err := readDiscoveryFile(filepath.Join(dir, "cmdline"), 64<<10)
		if err != nil {
			process.Reason = "无权限读取运行中内核的启动参数。"
		} else {
			process.Args = strings.Split(strings.TrimRight(string(args), "\x00"), "\x00")
			if len(process.Args) > 0 {
				if process.Binary == "" {
					process.Binary = process.Args[0]
				}
				process.Args = process.Args[1:]
			}
		}
		process.Cwd, _ = os.Readlink(filepath.Join(dir, "cwd"))
		found = append(found, process)
	}
	return found, nil
}

func parseCorePSLine(line string) (int, string, bool) {
	line = strings.TrimSpace(line)
	separator := strings.IndexFunc(line, unicode.IsSpace)
	if separator < 0 {
		return 0, "", false
	}
	pid, err := strconv.Atoi(line[:separator])
	binary := strings.TrimSpace(line[separator:])
	return pid, binary, err == nil && pid > 0 && binary != ""
}

// This is tokenization only: no shell expansion, subprocess execution or env reads.
func splitCoreCommand(line string) []string {
	var args []string
	var current strings.Builder
	var quote rune
	escaped, started := false, false
	for _, character := range line {
		if escaped {
			current.WriteRune(character)
			escaped = false
			started = true
			continue
		}
		if character == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			} else {
				current.WriteRune(character)
			}
			started = true
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			started = true
			continue
		}
		if unicode.IsSpace(character) {
			if started {
				args = append(args, current.String())
				current.Reset()
				started = false
			}
			continue
		}
		current.WriteRune(character)
		started = true
	}
	if escaped {
		current.WriteRune('\\')
	}
	if started {
		args = append(args, current.String())
	}
	return args
}

func parseDiscoveredCoreFlags(args []string, collapsed bool) discoveredCoreFlags {
	var flags discoveredCoreFlags
	for index := 0; index < len(args); index++ {
		if !strings.HasPrefix(args[index], "-") {
			continue
		}
		name, value, equals := strings.Cut(strings.TrimLeft(args[index], "-"), "=")
		switch name {
		case "d", "dir", "home", "f", "config", "ext-ctl", "external-controller", "ext-ctl-unix", "external-controller-unix", "secret":
		default:
			continue
		}
		if !equals {
			if index+1 >= len(args) {
				continue
			}
			index++
			value = args[index]
		}
		if collapsed {
			for index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
				index++
				value += " " + args[index]
			}
		}
		switch name {
		case "d", "dir", "home":
			flags.Home = value
		case "f", "config":
			flags.Config = value
		case "ext-ctl", "external-controller":
			flags.Controller = value
			flags.HasController = true
		case "ext-ctl-unix", "external-controller-unix":
			flags.Socket = value
			flags.HasSocket = true
		case "secret":
			flags.Secret = value
			flags.HasSecret = true
		}
	}
	return flags
}

func discoveredAbsolute(path, cwd string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if cwd == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

func processCoreCandidates(process discoveredCoreProcess) []coreCandidate {
	flags := parseDiscoveredCoreFlags(process.Args, process.CollapsedArgs)
	candidate := coreCandidate{Source: "运行中的 Mihomo", Binary: process.Binary, Running: true, PID: process.PID, Reason: process.Reason}
	candidate.Home = discoveredAbsolute(flags.Home, process.Cwd)
	if flags.Config != "" {
		candidate.Config = discoveredAbsolute(flags.Config, process.Cwd)
	}
	if candidate.Home == "" && candidate.Config != "" {
		candidate.Home = filepath.Dir(candidate.Config)
	}
	if candidate.Home == "" {
		if home, err := os.UserHomeDir(); err == nil {
			candidate.Home = filepath.Join(home, ".config", "mihomo")
		}
	}
	if candidate.Config == "" && candidate.Home != "" {
		candidate.Config = filepath.Join(candidate.Home, "config.yaml")
	}
	candidate.Source = discoveredCoreSource(candidate.Binary, candidate.Config, candidate.Home)
	candidates := readCandidateControllers(candidate)
	if len(candidates) > 0 {
		candidate.Secret = candidates[0].Secret
		if candidate.Reason == "" {
			candidate.Reason = candidates[0].Reason
		}
	}
	if flags.HasController || flags.HasSocket {
		candidates = nil
		if flags.HasController && flags.Controller != "" {
			clone := candidate
			clone.Controller = flags.Controller
			candidates = append(candidates, clone)
		}
		if flags.HasSocket && flags.Socket != "" {
			clone := candidate
			clone.Controller = "unix:" + discoveredAbsolute(flags.Socket, process.Cwd)
			candidates = append(candidates, clone)
		}
	}
	if len(candidates) == 0 {
		candidates = []coreCandidate{candidate}
	}
	if flags.HasSecret {
		for index := range candidates {
			candidates[index].Secret = flags.Secret
		}
	}

	return candidates
}

// A process path identifies the managing application without exposing command
// arguments or using an unstable PID as the candidate's visible label.
func discoveredCoreSource(binary, config, home string) string {
	sources := []struct {
		name     string
		patterns []string
	}{
		{"Sparkle", []string{"/sparkle.app/", "/application support/sparkle/", "/.config/sparkle/", "com.github.sparkle", "io.github.sparkle"}},
		{"ShellCrash", []string{"/shellcrash/"}},
		{"OpenClash", []string{"/openclash/"}},
		{"Clash Verge", []string{"/clash verge.app/", "/clash-verge/", "/clash-verge-rev/", "clash-verge-rev.clash-verge-rev", "/verge-mihomo"}},
	}
	for _, path := range []string{binary, config, home} {
		normalized := strings.ToLower(filepath.ToSlash(path)) + "/"
		for _, source := range sources {
			for _, pattern := range source.patterns {
				if strings.Contains(normalized, pattern) {
					return source.name
				}
			}
		}
	}
	return "运行中的 Mihomo"
}

type discoveredControllerConfig struct {
	TCP    string `yaml:"external-controller"`
	Socket string `yaml:"external-controller-unix"`
	Secret string `yaml:"secret"`
}

func readCandidateControllers(candidate coreCandidate) []coreCandidate {
	if candidate.Config == "" {
		return []coreCandidate{candidate}
	}
	data, err := readDiscoveryFile(candidate.Config, maxConfig)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			candidate.Reason = "无权限读取内核配置和控制接口设置。"
		} else if candidate.Reason == "" {
			candidate.Reason = "未找到可读取的内核配置，无法从文件发现控制接口。"
		}
		return []coreCandidate{candidate}
	}
	var config discoveredControllerConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		candidate.Reason = "内核配置无法解析，无法发现控制接口。"
		return []coreCandidate{candidate}
	}
	if candidate.Secret == "" {
		candidate.Secret = config.Secret
	}
	if candidate.Controller != "" {
		return []coreCandidate{candidate}
	}
	var candidates []coreCandidate
	if config.Socket != "" {
		copy := candidate
		copy.Controller = "unix:" + discoveredAbsolute(config.Socket, candidate.Home)
		candidates = append(candidates, copy)
	}
	if config.TCP != "" {
		copy := candidate
		copy.Controller = config.TCP
		candidates = append(candidates, copy)
	}
	if len(candidates) == 0 {
		return []coreCandidate{candidate}
	}
	return candidates
}

func (p *panelServer) localCoreInstallations() []coreCandidate {
	var candidates []coreCandidate
	binary := p.mihomo
	if managed, err := exec.LookPath(filepath.Join(p.home, ".miproxy", "core", "mihomo")); err == nil {
		binary = managed
	}
	current := coreCandidate{Source: "当前面板", Binary: binary, Config: p.path, Home: p.home, Controller: p.controlAddr, Secret: p.secret}
	if binary, err := exec.LookPath(current.Binary); err == nil {
		current.Binary = binary
	} else {
		current.Binary = ""
	}
	candidates = append(candidates, current)
	home, _ := os.UserHomeDir()
	type location struct {
		source, home string
		binaries     []string
	}
	locations := []location{
		{"Mihomo", filepath.Join(home, ".config", "mihomo"), []string{"mihomo"}},
		{"Clash", filepath.Join(home, ".config", "clash"), []string{"clash-meta", "clash"}},
		{"Mihomo", "/etc/mihomo", []string{"/usr/bin/mihomo", "/usr/local/bin/mihomo", "/opt/homebrew/bin/mihomo"}},
		{"ShellCrash", "/etc/ShellCrash", []string{"/etc/ShellCrash/CrashCore", "/etc/ShellCrash/mihomo"}},
		{"ShellCrash", "/usr/share/ShellCrash", []string{"/usr/share/ShellCrash/CrashCore"}},
		{"OpenClash", "/etc/openclash", []string{"/etc/openclash/core/clash_meta", "/etc/openclash/core/clash"}},
		{"Sparkle", filepath.Join(home, "Library", "Application Support", "Sparkle"), []string{"/Applications/Sparkle.app/Contents/MacOS/mihomo", "/Applications/Sparkle.app/Contents/Resources/mihomo"}},
		{"Sparkle", filepath.Join(home, ".config", "sparkle"), nil},
	}
	if runtime.GOOS == "darwin" {
		support := filepath.Join(home, "Library", "Application Support")
		entries, _ := os.ReadDir(support)
		for _, entry := range entries {
			name := strings.ToLower(entry.Name())
			if entry.IsDir() && strings.Contains(name, "sparkle") && len(locations) < 20 {
				locations = append(locations, location{"Sparkle", filepath.Join(support, entry.Name()), nil})
			}
		}
	}
	for _, place := range locations {
		binary := ""
		for _, path := range place.binaries {
			if found, err := exec.LookPath(path); err == nil {
				binary = found
				break
			}
		}
		foundConfig := false
		for _, filename := range []string{"config.yaml", "config.yml", "clash.yaml", "mihomo.yaml", "runtime.yaml", "yamls/config.yaml", "configs/config.yaml"} {
			path := filepath.Join(place.home, filename)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				candidates = append(candidates, coreCandidate{Source: place.source, Binary: binary, Config: path, Home: place.home})
				foundConfig = true
			}
		}
		if !foundConfig && binary != "" {
			candidates = append(candidates, coreCandidate{Source: place.source, Binary: binary, Home: place.home})
		}
	}
	return candidates
}

func normalizeDiscoveredController(address string) (string, error) {
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, "unix:") {
		socket := strings.TrimPrefix(address, "unix:")
		if !filepath.IsAbs(socket) {
			return "", errors.New("控制 Socket 必须为本机绝对路径。")
		}
		return "unix:" + filepath.Clean(socket), nil
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	endpoint, err := url.Parse(address)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return "", errors.New("控制接口地址无效。")
	}
	host := endpoint.Hostname()
	switch strings.ToLower(host) {
	case "", "0.0.0.0", "localhost":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	if !isLoopback(host) {
		return "", errors.New("只支持发现运行面板本机的控制接口，未探测此地址。")
	}
	port := endpoint.Port()
	if port == "" {
		if endpoint.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", errors.New("控制接口端口无效。")
	}
	return endpoint.Scheme + "://" + net.JoinHostPort(host, port), nil
}

func collectCoreCandidates(ctx context.Context, processes []discoveredCoreProcess, installations []coreCandidate) []coreCandidate {
	candidates := make([]coreCandidate, 0, maxDiscoveredCores)
	byIdentity := make(map[string]int)
	add := func(candidate coreCandidate) {
		key := ""
		if candidate.Controller != "" {
			normalized, err := normalizeDiscoveredController(candidate.Controller)
			if err != nil {
				candidate.Controller = ""
				candidate.Reason = err.Error()
			} else {
				candidate.Controller = normalized
				key = "api:" + normalized
			}
		}
		if key == "" {
			key = "file:" + candidate.Binary + "\x00" + candidate.Config
		}
		if key == "file:\x00" {
			return
		}
		if index, exists := byIdentity[key]; exists {
			current := &candidates[index]
			secrets := append(current.secrets, candidate.Secret)
			if candidate.Running && !current.Running {
				*current = candidate
			}
			if current.Binary == "" {
				current.Binary = candidate.Binary
			}
			if current.Config == "" {
				current.Config = candidate.Config
				current.Home = candidate.Home
			}
			current.Running = current.Running || candidate.Running
			if current.PID == 0 {
				current.PID = candidate.PID
			}
			current.secrets = secrets
			return
		}
		if len(candidates) >= maxDiscoveredCores {
			return
		}
		candidate.secrets = []string{candidate.Secret}
		byIdentity[key] = len(candidates)
		candidates = append(candidates, candidate)
	}
	for _, process := range processes {
		discovered := processCoreCandidates(process)
		hasController := false
		for _, candidate := range discovered {
			if candidate.Controller != "" {
				hasController = true
			}
			add(candidate)
		}
		if !hasController && runtime.GOOS == "darwin" && process.PID > 0 && ctx.Err() == nil {
			for _, address := range discoverProcessEndpoints(ctx, process.PID) {
				candidate := discovered[0]
				candidate.Controller = address
				add(candidate)
			}
		}
	}
	for _, installation := range installations {
		for _, candidate := range readCandidateControllers(installation) {
			add(candidate)
		}
	}
	var workers sync.WaitGroup
	jobs := make(chan int)
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				probeDiscoveredCore(ctx, &candidates[index])
			}
		}()
	}
	for index := range candidates {
		if ctx.Err() != nil {
			break
		}
		select {
		case jobs <- index:
		case <-ctx.Done():
		}
	}
	close(jobs)
	workers.Wait()
	for index := range candidates {
		candidate := &candidates[index]
		if ctx.Err() != nil && !candidate.Connected && candidate.Reason == "" {
			candidate.Reason = "扫描时间不足，尚未验证此控制接口。"
		}
		digest := sha256.Sum256([]byte(candidate.Binary + "\x00" + candidate.Config + "\x00" + candidate.Controller))
		candidate.ID = hex.EncodeToString(digest[:12])
		candidate.secrets = nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Connected != candidates[j].Connected {
			return candidates[i].Connected
		}
		return candidates[i].Running && !candidates[j].Running
	})
	return candidates
}

func probeDiscoveredCore(parent context.Context, candidate *coreCandidate) {
	if candidate.Controller == "" {
		if candidate.Reason == "" {
			candidate.Reason = "未发现控制接口，请检查内核配置。"
		}
		return
	}
	endpoint, client, err := controllerClient(candidate.Controller)
	if err != nil {
		candidate.Reason = "控制接口地址无效。"
		return
	}
	if client.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client.Transport = transport
	}
	if transport, ok := client.Transport.(*http.Transport); ok {
		defer transport.CloseIdleConnections()
	}
	ctx, cancel := context.WithTimeout(parent, 1200*time.Millisecond)
	defer cancel()
	seen := make(map[string]bool)
	for _, secret := range candidate.secrets {
		if seen[secret] || len(seen) >= 3 {
			continue
		}
		seen[secret] = true
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/version", nil)
		if secret != "" {
			request.Header.Set("Authorization", "Bearer "+secret)
		}
		response, err := client.Do(request)
		if err != nil {
			candidate.Reason = "无法连接控制接口，内核可能未运行或当前用户没有访问权限。"
			return
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			response.Body.Close()
			candidate.Running = true
			candidate.Reason = "控制接口认证失败，请检查内核密钥。"
			continue
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			candidate.Reason = "该地址未提供可用的 Mihomo 控制接口。"
			return
		}
		var version struct {
			Version string `json:"version"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&version)
		response.Body.Close()
		if err != nil || strings.TrimSpace(version.Version) == "" {
			candidate.Reason = "控制接口返回的数据不是有效的 Mihomo 版本信息。"
			return
		}
		candidate.Connected, candidate.Running, candidate.Version, candidate.Secret, candidate.Reason = true, true, version.Version, secret, ""
		return
	}
}

func readDiscoveryFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("发现文件不是普通文件或超过读取限制")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, errors.New("发现文件超过读取限制")
	}
	return data, err
}

func discoveryCommand(parent context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 700*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	output := &discoveryOutput{}
	command.Stdout = output
	if err := command.Run(); err != nil {
		return nil, err
	}
	return output.data, nil
}

type discoveryOutput struct{ data []byte }

func (output *discoveryOutput) Write(data []byte) (int, error) {
	if len(output.data)+len(data) > 2<<20 {
		return 0, fmt.Errorf("进程列表超过扫描限制")
	}
	output.data = append(output.data, data...)
	return len(data), nil
}

func discoverProcessEndpoints(ctx context.Context, pid int) []string {
	tcp, _ := discoveryCommand(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-Fn")
	sockets, _ := discoveryCommand(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-U", "-Fn")
	return parseDiscoveredEndpoints(string(tcp), string(sockets))
}

func parseDiscoveredEndpoints(tcp, sockets string) []string {
	var addresses []string
	seen := make(map[string]bool)
	add := func(raw string) {
		if len(addresses) >= 8 {
			return
		}
		address, err := normalizeDiscoveredController(raw)
		if err == nil && !seen[address] {
			seen[address] = true
			addresses = append(addresses, address)
		}
	}
	for _, line := range strings.Split(sockets, "\n") {
		if strings.HasPrefix(line, "n/") {
			path := strings.TrimPrefix(line, "n")
			path, _, _ = strings.Cut(path, " type=")
			path = strings.TrimSuffix(path, " (LISTEN)")
			if !strings.Contains(path, "->") {
				add("unix:" + path)
			}
		}
	}
	for _, line := range strings.Split(tcp, "\n") {
		if !strings.HasPrefix(line, "n") {
			continue
		}
		address := strings.TrimSuffix(strings.TrimPrefix(line, "n"), " (LISTEN)")
		if strings.HasPrefix(address, "*:") {
			address = "127.0.0.1:" + strings.TrimPrefix(address, "*:")
		}
		add(address)
	}
	return addresses
}
