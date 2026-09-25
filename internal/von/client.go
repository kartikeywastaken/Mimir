package von

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Client talks to Von over Chrome DevTools Protocol (CDP).
// Von is Chromium-based, so cdp at http://127.0.0.1:9222 works.
// We never activate background tabs - that's the "no tab switch" trick.

type Client struct {
	HTTPBase string // e.g. http://127.0.0.1:9222
	mu       sync.Mutex
	nextID   int
}

func New(httpBase string) *Client {
	if httpBase == "" {
		httpBase = "http://127.0.0.1:9222"
	}
	return &Client{HTTPBase: httpBase}
}

type Target struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	Type         string `json:"type"`
	WebSocketURL string `json:"webSocketDebuggerUrl"`
}

// ListTargets GET /json
func (c *Client) ListTargets() ([]Target, error) {
	resp, err := http.Get(c.HTTPBase + "/json")
	if err != nil {
		return nil, fmt.Errorf("von not reachable at %s: %w", c.HTTPBase, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var t []Target
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return t, nil
}

// ActiveTarget returns first page target (the visible tab)
func (c *Client) ActiveTarget() (*Target, error) {
	targets, err := c.ListTargets()
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		if t.Type == "page" {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("no page target found - is Von running?")
}

// CreateBackgroundTab creates a new tab WITHOUT focusing it.
// Uses PUT /json/new?url - target is created in background, we don't call activate.
func (c *Client) CreateBackgroundTab(url string) (*Target, error) {
	resp, err := http.Get(c.HTTPBase + "/json/new?" + "url=" + url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var t Target
	if err := json.Unmarshal(b, &t); err != nil {
		// some Von builds return empty on create, so list again
		targets, _ := c.ListTargets()
		for _, cand := range targets {
			if cand.URL == url {
				return &cand, nil
			}
		}
		return nil, err
	}
	return &t, nil
}

func (c *Client) CloseTarget(id string) error {
	resp, err := http.Get(c.HTTPBase + "/json/close/" + id)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// Evaluate runs JS in the target via CDP Runtime.evaluate
func (c *Client) Evaluate(wsURL, expression string) (string, error) {
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	msg := map[string]any{
		"id":     id,
		"method": "Runtime.evaluate",
		"params": map[string]any{
			"expression":    expression,
			"returnByValue": true,
			"awaitPromise":  true,
		},
	}
	if err := conn.WriteJSON(msg); err != nil {
		return "", err
	}
	deadline := time.Now().Add(10 * time.Second)
	_ = conn.SetReadDeadline(deadline)

	for {
		var resp map[string]any
		if err := conn.ReadJSON(&resp); err != nil {
			return "", err
		}
		rawID, ok := resp["id"]
		if !ok {
			// Asynchronous event notification, skip
			continue
		}
		var matches bool
		switch v := rawID.(type) {
		case float64:
			matches = int(v) == id
		case int:
			matches = v == id
		}
		if !matches {
			continue
		}

		if errMap, ok := resp["error"].(map[string]any); ok {
			errMsg := "cdp error"
			if m, ok := errMap["message"].(string); ok {
				errMsg = m
			}
			return "", fmt.Errorf("cdp evaluate: %s", errMsg)
		}

		if res, ok := resp["result"].(map[string]any); ok {
			if ex, ok := res["exceptionDetails"].(map[string]any); ok {
				return "", fmt.Errorf("js exception: %v", ex["text"])
			}
			if r, ok := res["result"].(map[string]any); ok {
				if v, ok := r["value"]; ok {
					switch x := v.(type) {
					case string:
						return x, nil
					default:
						b, _ := json.Marshal(x)
						return string(b), nil
					}
				}
				b, _ := json.Marshal(r)
				return string(b), nil
			}
		}
		b, _ := json.Marshal(resp)
		return string(b), nil
	}
}

// InjectOverlay injects a floating div into the page. No navigation.
func (c *Client) InjectOverlay(wsURL, html string) error {
	esc, _ := json.Marshal(html) // js string escape
	js := fmt.Sprintf(`(() => {
		let el = document.getElementById('__mimir_overlay');
		if(!el){ el = document.createElement('div'); el.id='__mimir_overlay'; document.body.appendChild(el); }
		el.innerHTML = %s;
		el.style.cssText = 'position:fixed;top:16px;right:16px;z-index:2147483647;background:#111;color:#fff;padding:16px 20px;border-radius:12px;box-shadow:0 8px 32px rgba(0,0,0,0.5);font-family:ui-sans-serif,system-ui;max-width:380px;border:1px solid #333;';
		return 'injected';
	})()`, string(esc))
	_, err := c.Evaluate(wsURL, js)
	return err
}

func (c *Client) RemoveOverlay(wsURL string) error {
	_, err := c.Evaluate(wsURL, `(() => { const el=document.getElementById('__mimir_overlay'); if(el) el.remove(); return 'removed'})()`)
	return err
}

// BackgroundSearch opens a hidden google tab, scrapes snippets, closes it.
// This is the "recursive" trick - user never sees tab switch.
func (c *Client) BackgroundSearch(query string, timeout time.Duration) ([]string, error) {
	if timeout == 0 {
		timeout = 8 * time.Second
	}
	q := fmt.Sprintf("https://www.google.com/search?q=%s", url.QueryEscape(query))
	t, err := c.CreateBackgroundTab(q)
	if err != nil {
		return nil, err
	}
	defer c.CloseTarget(t.ID)

	// wait for page load
	time.Sleep(2 * time.Second)

	// poll for results
	js := `(function(){
		const els = Array.from(document.querySelectorAll('div[data-sncf], div.g, div.MjjYud, a h3'));
		// fallback: just grab text snippets
		let out = [];
		document.querySelectorAll('div.g, div.MjjYud').forEach(d=>{
			let h = d.querySelector('h3');
			let s = d.innerText || d.textContent;
			if(h && s) out.push(h.innerText + ' - ' + s.slice(0,300));
			if(out.length>=5) return;
		});
		if(out.length===0) out.push(document.body.innerText.slice(0,1200));
		return JSON.stringify(out.slice(0,5));
	})()`
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		res, err := c.Evaluate(t.WebSocketURL, js)
		if err == nil && len(res) > 10 && res != `""` {
			var arr []string
			// res is JSON stringified array inside JSON string - double parse
			var inner string
			if json.Unmarshal([]byte(res), &inner) == nil {
				json.Unmarshal([]byte(inner), &arr)
			} else {
				json.Unmarshal([]byte(res), &arr)
			}
			if len(arr) > 0 && arr[0] != "" {
				return arr, nil
			}
		}
		time.Sleep(800 * time.Millisecond)
	}
	return nil, fmt.Errorf("background search timeout")
}
