package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// The local socket uses filesystem permissions instead of Web login credentials.
// Only the service user (and root) can connect to this management interface.
func startLocalControl(handler http.Handler, path string) (func(), error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("PANEL_SOCKET 必须是绝对路径")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("创建本机控制目录：%w", err)
	}
	dirInfo, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("检查本机控制目录：%w", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0077 != 0 {
		return nil, errors.New("本机控制目录必须是权限为 0700 的普通目录")
	}
	if owner, ok := dirInfo.Sys().(*syscall.Stat_t); !ok || int(owner.Uid) != os.Geteuid() {
		return nil, errors.New("本机控制目录必须属于运行面板的用户")
	}
	if err := removeStaleLocalSocket(path); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("监听本机控制接口：%w", err)
	}
	// Cleanup must not remove a file or another listener that replaced our socket.
	listener.SetUnlinkOnClose(false)
	info, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("检查本机控制接口：%w", err)
	}
	cleanupSocket := func() {
		if current, err := os.Lstat(path); err == nil && os.SameFile(info, current) {
			_ = os.Remove(path)
		}
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		cleanupSocket()
		return nil, fmt.Errorf("设置本机控制接口权限：%w", err)
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 3 * time.Minute,
		IdleTimeout: 45 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Local management socket: %v", err)
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(ctx); err != nil {
				_ = server.Close()
			}
			cleanupSocket()
		})
	}, nil
}

func removeStaleLocalSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查已有本机控制接口：%w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("本机控制接口路径已存在，且不是 Unix Socket；不会覆盖此文件")
	}
	connection, dialErr := net.DialTimeout("unix", path, 300*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return errors.New("本机控制接口正在使用，请勿重复启动面板服务")
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) {
		return fmt.Errorf("无法确认已有本机控制接口是否已停止：%w", dialErr)
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return errors.New("本机控制接口路径发生变化，请重新启动")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("清理已停止的本机控制接口：%w", err)
	}
	return nil
}
