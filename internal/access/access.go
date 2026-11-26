// Package access owns persisted Web access credentials and local bootstrap access.
// It is independent of the panel handlers and does not read authentication env vars.
package access

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/fileutil"
)

const iterations = 120000
const maxSettingsFile = 4096

type credentials struct {
	Version    int    `json:"version"`
	Salt       string `json:"salt,omitempty"`
	Hash       string `json:"hash,omitempty"`
	Iterations int    `json:"iterations,omitempty"`
}

// Settings is the public state; salts and password hashes never leave the server.
type Settings struct {
	PasswordSet bool   `json:"passwordSet"`
	Username    string `json:"username"`
	LocalOnly   bool   `json:"localOnly"`
}

// Manager protects Web requests and serializes credential persistence.
type Manager struct {
	mu   sync.RWMutex
	path string
	data credentials
}

type localRequestKey struct{}

// Open restores the access file, treating absence as local-only access. Invalid
// files fail startup rather than silently removing authentication.
func Open(path string) (*Manager, error) {
	manager := &Manager{path: path, data: credentials{Version: 1}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return manager, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取访问设置：%w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxSettingsFile || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("访问设置必须为 4 KiB 以内、权限为 0600 的普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取访问设置：%w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxSettingsFile+1))
	decoder.DisallowUnknownFields()
	var stored credentials
	if err := decoder.Decode(&stored); err != nil || !valid(stored) {
		return nil, errors.New("访问设置文件无效，请检查本机访问设置文件")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("访问设置文件包含多余内容")
	}
	manager.data = stored
	return manager, nil
}

func valid(data credentials) bool {
	if data.Version != 1 {
		return false
	}
	if data.Hash == "" {
		return data.Salt == "" && data.Iterations == 0
	}
	salt, saltErr := hex.DecodeString(data.Salt)
	hash, hashErr := hex.DecodeString(data.Hash)
	return saltErr == nil && hashErr == nil && len(salt) == 16 && len(hash) == sha256.Size && data.Iterations == iterations
}

// Handler applies security headers and the latest persisted access policy.
func (m *Manager) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w, r)
		if !m.authorize(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LocalHandler marks requests received through the permission-protected Unix
// socket. Only the server's local transport must use this wrapper.
func (m *Manager) LocalHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w, r)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localRequestKey{}, true)))
	})
}

func securityHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
	}
}

func isLoopback(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

func localWebRequest(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(peer).IsLoopback() {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = strings.Trim(r.Host, "[]")
	}
	return isLoopback(host)
}

func permitted(r *http.Request, data credentials) bool {
	if local, _ := r.Context().Value(localRequestKey{}).(bool); local {
		return true
	}
	if data.Hash == "" {
		return localWebRequest(r)
	}
	username, password, ok := r.BasicAuth()
	if !ok || username != "admin" || len(password) > 1024 {
		return false
	}
	salt, _ := hex.DecodeString(data.Salt)
	expected, _ := hex.DecodeString(data.Hash)
	provided, err := pbkdf2.Key(sha256.New, password, salt, data.Iterations, sha256.Size)
	return err == nil && subtle.ConstantTimeCompare(provided, expected) == 1
}

func deny(w http.ResponseWriter, passwordSet bool) {
	if passwordSet {
		w.Header().Set("WWW-Authenticate", `Basic realm="MiProxy Panel", charset="UTF-8"`)
		writeError(w, http.StatusUnauthorized, "访问密码已设置或已更改，请使用用户名 admin 和当前密码重新登录。")
		return
	}
	writeError(w, http.StatusForbidden, "尚未设置访问密码，仅允许本机访问；请通过本机或 SSH 转发进入访问设置。")
}

func (m *Manager) authorize(w http.ResponseWriter, r *http.Request) bool {
	m.mu.RLock()
	data := m.data
	m.mu.RUnlock()
	if permitted(r, data) {
		return true
	}
	deny(w, data.Hash != "")
	return false
}

func public(data credentials) Settings {
	return Settings{PasswordSet: data.Hash != "", Username: "admin", LocalOnly: data.Hash == ""}
}

// GetSettings reports access state without exposing credential material.
func (m *Manager) GetSettings(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w, r)
	if !m.authorize(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "请使用 GET 读取访问设置。")
		return
	}
	m.mu.RLock()
	settings := public(m.data)
	m.mu.RUnlock()
	writeJSON(w, http.StatusOK, settings)
}

// UpdateSettings atomically stores a new salted password hash. An empty password
// restores local-only access; persistence failure leaves the active policy intact.
func (m *Manager) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w, r)
	if !m.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "请使用 POST 更新访问设置。")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "访问设置须以 JSON 提交。")
		return
	}
	var input struct {
		Password *string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Password == nil {
		writeError(w, http.StatusBadRequest, "请输入有效的访问密码设置。")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, http.StatusBadRequest, "访问设置包含多余或过大的内容。")
		return
	}
	password := *input.Password
	if !utf8.ValidString(password) || len(password) > 1024 || (password != "" && len([]rune(password)) < 8) {
		writeError(w, http.StatusBadRequest, "访问密码至少 8 个字符，且不超过 1024 字节。")
		return
	}
	updated := credentials{Version: 1}
	if password != "" {
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			writeError(w, http.StatusInternalServerError, "生成访问设置失败。")
			return
		}
		hash, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "生成访问设置失败。")
			return
		}
		updated.Salt, updated.Hash, updated.Iterations = hex.EncodeToString(salt), hex.EncodeToString(hash), iterations
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Recheck after derivation in case another request changed the password.
	if !permitted(r, m.data) {
		deny(w, m.data.Hash != "")
		return
	}
	content, err := json.Marshal(updated)
	if err != nil || fileutil.AtomicWrite(m.path, append(content, '\n'), 0600) != nil {
		writeError(w, http.StatusInternalServerError, "保存访问设置失败，原访问方式保持不变。")
		return
	}
	m.data = updated
	writeJSON(w, http.StatusOK, public(updated))
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Message string `json:"message"`
	}{Message: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
