package terminal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/buildinfo"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/download"
)

const panelRepository = "MaoLikeQvQ/MiProxy-Panel"
const panelLatestReleaseURL = "https://api.github.com/repos/" + panelRepository + "/releases/latest"
const maxPanelBinary = 256 << 20

var panelVersionPattern = regexp.MustCompile("^v?[0-9]+\\.[0-9]+\\.[0-9]+$")

type panelReleaseAsset struct {
	Name   string
	URL    string
	Digest string
	Size   int64
}
type panelRelease struct {
	Tag      string
	Asset    panelReleaseAsset
	Checksum panelReleaseAsset
}
type panelDownloadProgress struct{ Downloaded, Total int64 }

func newPanelReleaseClient() *http.Client {
	client := download.NewClient(10 * time.Minute)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 8 {
			return errors.New("GitHub 下载重定向无效")
		}
		host := req.URL.Hostname()
		if host != "github.com" && host != "api.github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com" {
			return errors.New("GitHub 下载跳转到了非发布资源地址")
		}
		return nil
	}
	return client
}
func panelAssetName(system, arch, arm string) (string, error) {
	switch system {
	case "linux", "darwin":
	default:
		return "", errors.New("当前系统不支持自动更新面板")
	}
	switch arch {
	case "amd64", "arm64":
	case "386", "riscv64", "mips64", "mips64le":
		if system != "linux" {
			return "", errors.New("不支持当前系统与架构组合")
		}
	case "arm":
		if system != "linux" || (arm != "5" && arm != "6" && arm != "7") {
			return "", errors.New("无法识别 ARM 架构")
		}
		arch = "armv" + arm
	case "mips", "mipsle":
		if system != "linux" {
			return "", errors.New("不支持当前架构")
		}
		arch += "_softfloat"
	default:
		return "", errors.New("当前架构未提供自动更新产物")
	}
	return "miproxy-panel_" + system + "_" + arch, nil
}
func currentPanelAsset() (string, error) {
	arm := "7"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "GOARM" {
				arm = strings.Split(setting.Value, ",")[0]
			}
		}
	}
	return panelAssetName(runtime.GOOS, runtime.GOARCH, arm)
}
func panelVersionParts(tag string) ([3]uint64, error) {
	var parts [3]uint64
	if !panelVersionPattern.MatchString(tag) {
		return parts, errors.New("发布版本必须为 v主版本.次版本.修订版本")
	}
	for i, part := range strings.Split(strings.TrimPrefix(tag, "v"), ".") {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return parts, errors.New("发布版本号无效")
		}
		parts[i] = n
	}
	return parts, nil
}
func panelReleaseNewer(latest, current string) (bool, error) {
	next, err := panelVersionParts(latest)
	if err != nil {
		return false, err
	}
	if current == "dev" {
		return true, nil
	}
	before, err := panelVersionParts(current)
	if err != nil {
		return false, errors.New("当前版本号无法比较，请使用带正式版本号的发布文件")
	}
	for i := range next {
		if next[i] != before[i] {
			return next[i] > before[i], nil
		}
	}
	return false, nil
}
func validPanelAssetURL(raw, tag, name string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "/"+panelRepository+"/releases/download/"+tag+"/"+name
}
func panelReleaseRequest(ctx context.Context, client *http.Client, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "MiProxy-Panel/"+buildinfo.Version)
	if strings.HasPrefix(address, "https://api.github.com/") {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 GitHub 失败：%w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		switch response.StatusCode {
		case 404:
			return nil, errors.New("GitHub 仓库尚未提供可用的正式 Release 或对应资源")
		case 403, 429:
			return nil, errors.New("GitHub 请求被限制，请稍后重试或检查下载网络")
		}
		return nil, fmt.Errorf("GitHub 返回 HTTP %d", response.StatusCode)
	}
	return response, nil
}
func checkPanelRelease(ctx context.Context, client *http.Client, assetName string) (panelRelease, error) {
	var result panelRelease
	response, err := panelReleaseRequest(ctx, client, panelLatestReleaseURL)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	var data struct {
		Tag               string `json:"tag_name"`
		Draft, Prerelease bool
		Assets            []struct {
			Name, Digest string
			URL          string `json:"browser_download_url"`
			Size         int64
		}
	}
	if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&data) != nil {
		return result, errors.New("GitHub 版本信息无效")
	}
	if _, err = panelVersionParts(data.Tag); err != nil || data.Draft || data.Prerelease {
		return result, errors.New("GitHub 返回的版本不是有效的正式发布")
	}
	result.Tag = data.Tag
	for _, a := range data.Assets {
		if a.Name != assetName && a.Name != "checksums.txt" {
			continue
		}
		if !validPanelAssetURL(a.URL, data.Tag, a.Name) {
			return panelRelease{}, errors.New("发布资源不属于本项目 GitHub Release")
		}
		asset := panelReleaseAsset{Name: a.Name, URL: a.URL, Digest: a.Digest, Size: a.Size}
		if a.Name == assetName {
			if result.Asset.Name != "" {
				return panelRelease{}, errors.New("发布资源名称重复")
			}
			result.Asset = asset
		} else {
			result.Checksum = asset
		}
	}
	if result.Asset.Name == "" {
		return result, fmt.Errorf("最新版本 %s 未提供当前设备的 %s", data.Tag, assetName)
	}
	if result.Asset.Size <= 0 || result.Asset.Size > maxPanelBinary {
		return result, errors.New("发布文件大小无效")
	}
	if result.Checksum.Name == "" {
		return result, errors.New("Release 缺少 checksums.txt，已停止更新")
	}
	return result, nil
}
func panelChecksum(content []byte, name string) ([]byte, error) {
	var digest []byte
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		if digest != nil {
			return nil, errors.New("校验清单包含重复文件")
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("SHA-256 校验值无效")
		}
		digest = decoded
	}
	if digest == nil {
		return nil, errors.New("校验清单缺少当前设备文件")
	}
	return digest, nil
}
func downloadPanelRelease(ctx context.Context, client *http.Client, release panelRelease, directory string, progress func(panelDownloadProgress)) (string, error) {
	if !validPanelAssetURL(release.Asset.URL, release.Tag, release.Asset.Name) || !validPanelAssetURL(release.Checksum.URL, release.Tag, "checksums.txt") {
		return "", errors.New("发布资源地址无效")
	}
	response, err := panelReleaseRequest(ctx, client, release.Checksum.URL)
	if err != nil {
		return "", err
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	response.Body.Close()
	if err != nil {
		return "", err
	}
	if len(content) > 1<<20 {
		return "", errors.New("校验清单过大")
	}
	digest, err := panelChecksum(content, release.Asset.Name)
	if err != nil {
		return "", err
	}
	if release.Asset.Digest != "" && release.Asset.Digest != "sha256:"+hex.EncodeToString(digest) {
		return "", errors.New("GitHub 资源摘要与校验清单不一致")
	}
	response, err = panelReleaseRequest(ctx, client, release.Asset.URL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	file, err := os.CreateTemp(directory, "miproxy-panel-download-*")
	if err != nil {
		return "", err
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(file.Name())
		}
	}()
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	buffer := make([]byte, 64<<10)
	var received int64
	last := time.Now()
	for {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			received += int64(n)
			if received > maxPanelBinary || received > release.Asset.Size {
				return "", errors.New("下载大小超过发布信息，已停止更新")
			}
			if _, err = writer.Write(buffer[:n]); err != nil {
				return "", err
			}
			if progress != nil && (time.Since(last) > 150*time.Millisecond || received == release.Asset.Size) {
				progress(panelDownloadProgress{received, release.Asset.Size})
				last = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("下载中断：%w", readErr)
		}
	}
	if received != release.Asset.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(digest)) {
		return "", errors.New("面板 SHA-256 或文件大小校验失败，已保留当前版本")
	}
	if err = errors.Join(file.Chmod(0700), file.Sync(), file.Close()); err != nil {
		return "", err
	}
	complete = true
	return filepath.Clean(file.Name()), nil
}
