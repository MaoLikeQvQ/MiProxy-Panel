package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/fileutil"
)

// Updating is restricted to an installation recorded by our own installer.
// A candidate is staged and validated before the running service is touched.
func (s terminalService) updateBinary(ctx context.Context, binaryPath, expectedVersion string) (string, error) {
	if s.manager == "" {
		return "", errors.New(s.detail)
	}
	if s.euid() != 0 {
		return "", errors.New("更新需要 root 权限，请以 root 身份运行 miproxy")
	}
	marker, err := s.readFile(terminalInstallMarker)
	expected := "miproxy-panel-install-v1\nmanager=" + s.manager + "\nbinary=" + s.binary + "\n"
	if err != nil || string(marker) != expected {
		return "", errors.New("未找到匹配的安装记录，请使用原部署方式更新")
	}
	if !filepath.IsAbs(binaryPath) {
		return "", errors.New("请提供新版本二进制的绝对路径")
	}
	info, err := os.Lstat(binaryPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 256<<20 {
		return "", errors.New("新版本必须是 256 MiB 以内的普通二进制文件")
	}
	current, err := os.Lstat(s.binary)
	if err != nil || !current.Mode().IsRegular() {
		return "", errors.New("已安装二进制路径无效，已取消更新")
	}
	candidate, err := os.CreateTemp(filepath.Dir(s.binary), ".miproxy-update-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(candidate.Name())
	input, err := os.Open(binaryPath)
	if err != nil {
		candidate.Close()
		return "", err
	}
	n, copyErr := io.Copy(candidate, io.LimitReader(input, (256<<20)+1))
	err = errors.Join(copyErr, input.Close(), candidate.Chmod(0755), candidate.Sync(), candidate.Close())
	if err != nil {
		return "", err
	}
	if n == 0 || n > 256<<20 {
		return "", errors.New("新版本大小无效")
	}
	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
	output, err := s.run(checkCtx, candidate.Name(), "--version")
	checkCancel()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(output), "MiProxy Panel ") || (expectedVersion != "" && strings.TrimSpace(output) != "MiProxy Panel "+expectedVersion) {
		return "", errors.New("新版本无法在当前设备运行，或不是 MiProxy Panel；服务保持原状态")
	}
	old, err := os.ReadFile(s.binary)
	if err != nil {
		return "", err
	}
	status := s.Status(ctx)
	if status.Running {
		if _, err = s.Action(ctx, "stop"); err != nil {
			return "", err
		}
	}
	if err = ctx.Err(); err == nil {
		err = os.Rename(candidate.Name(), s.binary)
	}
	if err == nil && status.Running {
		_, err = s.Action(ctx, "start")
	}
	if err != nil {
		recovery, finish := context.WithTimeout(context.Background(), 20*time.Second)
		defer finish()
		restoreErr := fileutil.AtomicWrite(s.binary, old, current.Mode().Perm())
		if restoreErr == nil && status.Running {
			_, restoreErr = s.Action(recovery, "start")
		}
		if restoreErr != nil {
			return "", fmt.Errorf("更新失败：%v；恢复旧版本失败：%w", err, restoreErr)
		}
		return "", fmt.Errorf("更新失败，已恢复旧版本：%w", err)
	}
	return "面板二进制已更新。" + terminalYes(status.Running, "服务已重新启动。", "服务保持停止。"), nil
}
