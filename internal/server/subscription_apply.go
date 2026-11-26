package server

import (
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/fileutil"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/profile"

	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
)

// A changed configuration invalidates the current-subscription label. The record
// contains only the library ID and config revision, never subscription credentials.
type activeSubscription struct{ ID, Revision string }

func (p *panelServer) activeSubscriptionID() string {
	if p.connection().External {
		return ""
	}
	info, err := os.Lstat(p.path + ".active-subscription.json")
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return ""
	}
	f, err := os.Open(p.path + ".active-subscription.json")
	if err != nil {
		return ""
	}
	defer f.Close()
	var active activeSubscription
	if json.NewDecoder(io.LimitReader(f, 4096)).Decode(&active) != nil {
		return ""
	}
	current, err := p.readConfig()
	if err != nil || !current.Exists || current.Revision != active.Revision {
		return ""
	}
	return active.ID
}

func (p *panelServer) applySubscription(w http.ResponseWriter, r *http.Request) {
	if !readJSON(w, r, &struct{}{}, 1024) {
		return
	}
	if p.connection().External {
		writeError(w, http.StatusForbidden, "当前配置由外部应用管理，请在原应用中切换订阅。", nil)
		return
	}
	id := r.PathValue("id")
	p.library.Lock()
	entries, err := p.readSubscriptions()
	p.library.Unlock()
	if err != nil {
		writeError(w, 500, "读取订阅失败。", err)
		return
	}
	var entry *subscription
	for i := range entries {
		if entries[i].ID == id {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		writeError(w, 404, "订阅已不存在，请刷新列表。", nil)
		return
	}
	current, err := p.readConfig()
	if err != nil {
		writeError(w, 500, "读取当前配置失败。", err)
		return
	}
	override, err := readConfigFile(p.path + ".override.yaml")
	if err != nil {
		writeError(w, 500, "读取已保存的覆写失败。", err)
		return
	}
	content, err := profile.MergeSubscription(current.Content, entry.Content, override.Content)
	if err != nil {
		writeError(w, 422, "生成订阅配置失败。", err)
		return
	}
	// Reuse the normal validation, optimistic concurrency, save and reload path.
	payload, _ := json.Marshal(configInput{Content: content, Revision: current.Revision})
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/apply", bytes.NewReader(payload))
	if err != nil {
		writeError(w, 500, "构建配置请求失败。", err)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	p.applyConfig(&subscriptionApplyWriter{ResponseWriter: w, panel: p, id: id}, request)
}

// Observe the successful normal apply response before forwarding it. A failed
// reload never creates an active-subscription marker.
type subscriptionApplyWriter struct {
	http.ResponseWriter
	panel  *panelServer
	id     string
	status int
}

func (w *subscriptionApplyWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *subscriptionApplyWriter) Write(data []byte) (int, error) {
	size := len(data)
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status >= 200 && w.status < 300 {
		var result applyResult
		if json.Unmarshal(data, &result) == nil {
			if result.Applied {
				record, _ := json.Marshal(activeSubscription{ID: w.id, Revision: result.Revision})
				if err := fileutil.AtomicWrite(w.panel.path+".active-subscription.json", record, 0600); err != nil {
					result.Detail = "配置已应用，但记录当前订阅失败：" + err.Error()
					data, _ = json.Marshal(result)
				}
			} else if result.Saved {
				// Even an identical saved revision can fail to reload. Its previous
				// marker must not claim a successful switch after that failure.
				if err := os.Remove(w.panel.path + ".active-subscription.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
					result.Detail += "；清除当前订阅记录失败：" + err.Error()
					data, _ = json.Marshal(result)
				}
			}
		}
	}
	_, err := w.ResponseWriter.Write(data)
	return size, err
}
