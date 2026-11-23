package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mohamed-foly/orchdock/internal/server"
)

func init() {
	register("notify", "<item> [message]", "mark an item as needing you (for scripts: e.g. at the end of a long job)", cmdNotify)
}

func cmdNotify(e *Env, args []string) error {
	fs := newFlags("notify", e.Err)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, -1, "notify <item> [message]"); err != nil {
		return err
	}
	return callService(e, http.MethodPost, "/api/attention/"+url.PathEscape(pos[0]), map[string]string{"reason": strings.Join(pos[1:], " ")})
}

// callService sends one request to the running service of this home.
func callService(e *Env, method, path string, body any) error {
	info, alive := server.ReadInfo(e.Home)
	if !alive {
		return fmt.Errorf("%w: the OrchDock service is not running (start it with 'orchdock ui' or 'orchdock serve')", ErrUnavailable)
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", info.Port, path), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set(server.TokenHeader, info.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&msg)
		return fmt.Errorf("the service refused: %s %s", resp.Status, msg.Error)
	}
	return nil
}
