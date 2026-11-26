// Package server owns HTTP routing, Mihomo coordination and persisted application state.
package server

import (
	"context"
	"errors"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/access"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/runtimeenv"
	webui "github.com/MaoLikeQvQ/MiProxy-Panel/web"
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

func Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	address := runtimeenv.Value("PANEL_LISTEN", "127.0.0.1:8787")
	_, _, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("PANEL_LISTEN must be a host:port address")
	}

	configPath, err := filepath.Abs(runtimeenv.Value("PANEL_CONFIG", "./config/config.yaml"))
	if err != nil {
		return err
	}
	home, err := filepath.Abs(runtimeenv.Value("MIHOMO_HOME", filepath.Dir(configPath)))
	if err != nil {
		return err
	}
	runtimePath := runtimeenv.Value("MIHOMO_CONFIG_PATH", configPath)
	if !filepath.IsAbs(runtimePath) {
		return errors.New("MIHOMO_CONFIG_PATH must be an absolute path")
	}

	controllerAddress := runtimeenv.Value("MIHOMO_CONTROLLER", "http://127.0.0.1:9090")
	controller, client, err := controllerClient(controllerAddress)
	if err != nil {
		return err
	}
	externalConfig, err := strconv.ParseBool(runtimeenv.Value("PANEL_EXTERNAL_CONFIG", "false"))
	if err != nil {
		return errors.New("PANEL_EXTERNAL_CONFIG must be true or false")
	}
	reloadPayload, err := strconv.ParseBool(runtimeenv.Value("MIHOMO_RELOAD_PAYLOAD", "false"))
	if err != nil {
		return errors.New("MIHOMO_RELOAD_PAYLOAD must be true or false")
	}

	accessManager, err := access.Open(configPath + ".access.json")
	if err != nil {
		return err
	}
	assets, err := webui.Assets()
	if err != nil {
		return err
	}
	panel := &panelServer{
		path:           configPath,
		runtimePath:    runtimePath,
		mihomo:         runtimeenv.Value("MIHOMO_BINARY", "mihomo"),
		home:           home,
		control:        controller,
		controlAddr:    controllerAddress,
		externalConfig: externalConfig,
		reloadPayload:  reloadPayload,
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
	mux.HandleFunc("GET /api/settings/access", accessManager.GetSettings)
	mux.HandleFunc("POST /api/settings/access", accessManager.UpdateSettings)
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
	closeLocalControl, err := startLocalControl(accessManager.LocalHandler(mux), runtimeenv.SocketPath(configPath))
	if err != nil {
		return err
	}
	defer closeLocalControl()

	server := &http.Server{
		Addr:              address,
		Handler:           accessManager.Handler(mux),
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
