package server

import (
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/fileutil"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/safeoutput"

	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (p *panelServer) updateCore(w http.ResponseWriter, r *http.Request) {
	if !readJSON(w, r, &struct{}{}, 1024) {
		return
	}
	if p.core == nil {
		writeError(w, http.StatusServiceUnavailable, "内核管理尚未就绪。", nil)
		return
	}
	if !p.save.TryLock() {
		writeError(w, http.StatusConflict, "配置操作正在进行，请稍后重试。", nil)
		return
	}
	defer p.save.Unlock()
	if reason := p.coreUpdateDisabled(); reason != "" {
		writeError(w, http.StatusConflict, reason, nil)
		return
	}
	m := p.core
	m.mu.Lock()
	if m.busy || m.ctx.Err() != nil {
		m.mu.Unlock()
		writeError(w, http.StatusConflict, "内核任务正在进行，请稍后重试。", nil)
		return
	}
	m.busy = true
	m.phase = "checking"
	m.detail = "准备更新面板内核…"
	m.downloaded = 0
	m.total = 0
	m.speed = 0
	m.jobDone = make(chan struct{})
	m.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, 15*time.Minute)
		defer cancel()
		err := p.performCoreUpdate(ctx)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.busy = false
		if err != nil {
			m.phase = "error"
			m.detail = err.Error()
		} else {
			m.phase = "ready"
			m.detail = "Mihomo 内核更新完成。"
		}
		close(m.jobDone)
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}
func (p *panelServer) coreUpdateDisabled() string {
	if reason := p.coreDisabledReason(); reason != "" {
		return reason
	}
	owned := filepath.Join(p.coreDirectory(), "mihomo")
	if filepath.Clean(p.coreBinary()) != filepath.Clean(owned) {
		return "只允许更新面板安装的内核；已有外部二进制请通过原安装方式更新。"
	}
	info, err := os.Lstat(owned)
	if err != nil || !info.Mode().IsRegular() {
		return "请先安装面板内核。"
	}
	return ""
}
func (p *panelServer) performCoreUpdate(ctx context.Context) error {
	// Setup uses only home and the existing manager for download/progress. Staging
	// under the same filesystem allows an atomic replacement after validation.
	root, err := os.MkdirTemp(p.coreDirectory(), ".update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	staged := &panelServer{home: root, core: p.core}
	if err = os.MkdirAll(staged.coreDirectory(), 0700); err != nil {
		return err
	}
	if err = staged.installCore(ctx); err != nil {
		return err
	}
	candidate := filepath.Join(staged.coreDirectory(), "mihomo")
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	output, err := exec.CommandContext(check, candidate, "-v").CombinedOutput()
	cancel()
	if err != nil || !strings.Contains(strings.ToLower(string(output)), "mihomo") {
		return errors.New("新内核无法运行，已保留旧内核")
	}
	p.save.Lock()
	snapshot, err := p.readConfig()
	if err == nil && snapshot.Exists {
		check, cancel = context.WithTimeout(ctx, 35*time.Second)
		log := &limitedOutput{}
		cmd := exec.CommandContext(check, candidate, "-t", "-d", p.home, "-f", p.path)
		cmd.Stdout = log
		cmd.Stderr = log
		err = cmd.Run()
		cancel()
		if err != nil {
			err = fmt.Errorf("新内核配置校验失败，已保留旧内核：%s", safeoutput.Text(log.buffer.String()))
		}
	}
	p.save.Unlock()
	if err != nil {
		return err
	}
	m := p.core
	m.mu.Lock()
	wasRunning := m.command != nil
	m.mu.Unlock()
	// Never terminate a controller process that was started by another service.
	if !wasRunning {
		check, cancel = context.WithTimeout(ctx, 3*time.Second)
		externalRunning := p.controllerVersion(check) != ""
		cancel()
		if externalRunning {
			return errors.New("当前控制接口由其他进程提供，请通过原服务更新；面板未改动旧内核")
		}
	}
	target := filepath.Join(p.coreDirectory(), "mihomo")
	old, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	m.update("starting", "下载校验完成，正在替换内核…")
	if wasRunning {
		p.stopManagedCore()
	}
	if err = ctx.Err(); err == nil {
		err = os.Rename(candidate, target)
	}
	if err == nil && wasRunning {
		err = p.setupCore(ctx)
	}
	if err == nil {
		return nil
	}
	restoreErr := fileutil.AtomicWrite(target, old, 0700)
	if restoreErr == nil && wasRunning && m.ctx.Err() == nil {
		recovery, cancel := context.WithTimeout(m.ctx, 60*time.Second)
		defer cancel()
		p.stopManagedCore()
		restoreErr = p.setupCore(recovery)
	}
	if restoreErr != nil {
		return fmt.Errorf("更新失败：%v；恢复旧内核失败：%w", err, restoreErr)
	}
	return fmt.Errorf("更新失败，已恢复旧内核：%w", err)
}
