package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web/dist
var webAssets embed.FS

var version = "dev"

func main() {
	if err := runCommand(os.Args[0], os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "MiProxy:", err)
		os.Exit(1)
	}
}

func runCommand(name string, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "--version", "version":
			fmt.Println("MiProxy Panel", version)
			return nil
		case "--help", "-h", "help":
			fmt.Println("MiProxy Panel\n\n  miproxy                     打开终端管理面板\n  miproxy-panel tui            打开终端管理面板\n  miproxy-panel [serve]        启动后台 Web 服务\n  miproxy --version           查看版本\n\n终端无需登录；读取 /etc/miproxy-panel.env，或 MIPROXY_ENV_FILE 指定的文件。")
			return nil
		case "tui":
			if len(args) != 1 {
				return errors.New("tui 不接受额外参数")
			}
			return runTerminal()
		case "serve":
			if len(args) != 1 {
				return errors.New("serve 不接受额外参数")
			}
			return runPanel()
		default:
			return fmt.Errorf("未知命令 %q，运行 --help 查看用法", args[0])
		}
	}
	if filepath.Base(name) == "miproxy" {
		return runTerminal()
	}
	return runPanel()
}

func runPanel() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	address := env("PANEL_LISTEN", "127.0.0.1:8787")
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		log.Fatal("PANEL_LISTEN must be a host:port address")
	}
	password := os.Getenv("PANEL_PASSWORD")
	if password == "" && !isLoopback(host) {
		log.Fatal("PANEL_PASSWORD is required for a non-loopback listener")
	}

	configPath, err := filepath.Abs(env("PANEL_CONFIG", "./config/config.yaml"))
	if err != nil {
		log.Fatal(err)
	}
	home, err := filepath.Abs(env("MIHOMO_HOME", filepath.Dir(configPath)))
	if err != nil {
		log.Fatal(err)
	}
	runtimePath := env("MIHOMO_CONFIG_PATH", configPath)
	if !filepath.IsAbs(runtimePath) {
		log.Fatal("MIHOMO_CONFIG_PATH must be an absolute path")
	}

	controllerAddress := env("MIHOMO_CONTROLLER", "http://127.0.0.1:9090")
	controller, client, err := controllerClient(controllerAddress)
	if err != nil {
		log.Fatal(err)
	}
	externalConfig, err := strconv.ParseBool(env("PANEL_EXTERNAL_CONFIG", "false"))
	if err != nil {
		log.Fatal("PANEL_EXTERNAL_CONFIG must be true or false")
	}
	reloadPayload, err := strconv.ParseBool(env("MIHOMO_RELOAD_PAYLOAD", "false"))
	if err != nil {
		log.Fatal("MIHOMO_RELOAD_PAYLOAD must be true or false")
	}

	assets, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		log.Fatal(err)
	}
	panel := &panelServer{
		path:           configPath,
		runtimePath:    runtimePath,
		mihomo:         env("MIHOMO_BINARY", "mihomo"),
		home:           home,
		control:        controller,
		controlAddr:    controllerAddress,
		externalConfig: externalConfig,
		reloadPayload:  reloadPayload,
		secret:         os.Getenv("MIHOMO_SECRET"),
		client:         client,
		subscriptions:  subscriptionClient(),
	}
	panel.connectionHistory = openConnectionHistory(configPath, controllerAddress)
	panel.core = newCoreManager(ctx)
	defer panel.closeCore()
	if err := panel.restoreCoreConnection(); err != nil {
		panel.discoveryWarning = "已保存的内核连接无法恢复，请重新检测。"
		log.Printf("Restore core connection: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", panel.getConfig)
	mux.HandleFunc("POST /api/validate", panel.validateConfig)
	mux.HandleFunc("POST /api/apply", panel.applyConfig)
	mux.HandleFunc("GET /api/status", panel.status)
	mux.HandleFunc("GET /api/core", panel.detectCore)
	mux.HandleFunc("POST /api/core/connect", panel.connectCore)
	mux.HandleFunc("POST /api/core/update", panel.updateCore)
	mux.HandleFunc("GET /api/core/discovery", panel.listCoreDiscovery)
	mux.HandleFunc("POST /api/core/select", panel.selectCoreConnection)
	mux.HandleFunc("GET /api/overview", panel.runtimeOverview)
	mux.HandleFunc("GET /api/subscriptions", panel.listSubscriptions)
	mux.HandleFunc("POST /api/subscriptions", panel.addSubscription)
	mux.HandleFunc("GET /api/subscriptions/{id}", panel.getSubscription)
	mux.HandleFunc("POST /api/subscriptions/{id}", panel.updateSubscription)
	mux.HandleFunc("POST /api/subscriptions/{id}/apply", panel.applySubscription)
	mux.HandleFunc("DELETE /api/subscriptions/{id}", panel.deleteSubscription)
	mux.HandleFunc("GET /api/override", panel.getOverride)
	mux.HandleFunc("POST /api/override", panel.saveOverride)
	mux.HandleFunc("GET /api/groups", panel.runtimeGroups)
	mux.HandleFunc("POST /api/groups", panel.selectProxy)
	mux.HandleFunc("POST /api/nodes/delay", panel.measureNodeDelay)
	mux.HandleFunc("GET /api/rules", panel.runtimeRules)
	mux.HandleFunc("POST /api/rules/state", panel.setRuntimeRuleState)
	mux.HandleFunc("GET /api/rule-providers", panel.runtimeRuleProviders)
	mux.HandleFunc("POST /api/rule-providers/detect", panel.detectRuleProvider)
	mux.HandleFunc("POST /api/rule-providers/update", panel.updateRuleProvider)
	mux.HandleFunc("GET /api/logs", panel.streamLogs)
	mux.Handle("/", http.FileServerFS(assets))
	closeLocalControl, err := startLocalControl(mux, localControlPath(configPath))
	if err != nil {
		return err
	}
	defer closeLocalControl()

	server := &http.Server{
		Addr:              address,
		Handler:           protect(password, mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       45 * time.Second,
	}
	go panel.monitorConnection(ctx)
	if _, err := os.Stat(filepath.Join(panel.coreDirectory(), "enabled")); err == nil && panel.coreDisabledReason() == "" {
		panel.beginCoreSetup()
	}
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 8*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("MiProxy Panel listening on %s", address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func isLoopback(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

func controllerClient(address string) (string, *http.Client, error) {
	client := &http.Client{CheckRedirect: rejectRedirect}
	if socket, ok := strings.CutPrefix(address, "unix:"); ok {
		if !filepath.IsAbs(socket) {
			return "", nil, errors.New("MIHOMO_CONTROLLER requires an absolute Unix socket path")
		}
		dialer := &net.Dialer{}
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socket)
			},
		}
		return "http://localhost", client, nil
	}
	controller, err := url.Parse(address)
	if err != nil || controller.Hostname() == "" || controller.User != nil ||
		controller.RawQuery != "" || controller.Fragment != "" ||
		(controller.Scheme != "http" && controller.Scheme != "https") {
		return "", nil, errors.New("MIHOMO_CONTROLLER must be an HTTP(S) URL or unix:/absolute/path.sock")
	}
	return strings.TrimRight(controller.String(), "/"), client, nil
}

func rejectRedirect(*http.Request, []*http.Request) error {
	return errors.New("Mihomo controller redirected the request")
}

func protect(password string, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if password == "" {
			host, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				host = strings.Trim(r.Host, "[]")
			}
			if !isLoopback(host) {
				http.Error(w, "Invalid local host", http.StatusForbidden)
				return
			}
		} else {
			user, provided, ok := r.BasicAuth()
			digest := sha256.Sum256([]byte(provided))
			if !ok || user != "admin" || subtle.ConstantTimeCompare(digest[:], expected[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="MiProxy Panel", charset="UTF-8"`)
				http.Error(w, "Authentication required", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
