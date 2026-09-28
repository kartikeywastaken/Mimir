package von

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
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
	targetID string
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
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(c.HTTPBase + "/json")
	if err != nil {
		return nil, fmt.Errorf("browser not reachable at %s: %w", c.HTTPBase, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var t []Target
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return t, nil
}

// ActiveTarget returns first page target (the visible tab).
// Retries briefly because Brave/Chrome may register extension targets before the actual page.
func (c *Client) ActiveTarget() (*Target, error) {
	var lastSeen []Target
	for attempt := 0; attempt < 3; attempt++ {
		targets, err := c.ListTargets()
		if err != nil {
			return nil, err
		}
		lastSeen = targets
		c.mu.Lock()
		pinned := c.targetID
		c.mu.Unlock()
		if pinned != "" {
			for _, target := range targets {
				if target.ID == pinned {
					return &target, nil
				}
			}
			return nil, fmt.Errorf("the controlled tab was closed")
		}
		for _, target := range targets {
			if target.Type != "page" {
				continue
			}
			visible, err := c.Evaluate(target.WebSocketURL, `document.visibilityState === 'visible'`)
			if err == nil && visible == "true" {
				return &target, nil
			}
		}
		var fallback *Target
		for _, t := range targets {
			if t.Type == "page" {
				candidate := t
				if fallback == nil {
					fallback = &candidate
				}
				if t.URL != "" && t.URL != "about:blank" && !strings.HasPrefix(t.URL, "chrome://") {
					return &candidate, nil
				}
			}
		}
		if fallback != nil {
			return fallback, nil
		}
		if attempt < 2 {
			time.Sleep(1 * time.Second)
		}
	}

	// Create debug string
	debugInfo := ""
	for i, t := range lastSeen {
		debugInfo += fmt.Sprintf("[%d] Type: %s, URL: %s | ", i, t.Type, t.URL)
	}
	if debugInfo == "" {
		debugInfo = "No targets returned by browser."
	}

	return nil, fmt.Errorf("no page target found - is your browser running? Targets seen: %s", debugInfo)
}

// CreateBackgroundTab creates a new tab WITHOUT focusing it.
// Uses PUT /json/new?url - target is created in background, we don't call activate.
func (c *Client) CreateBackgroundTab(targetURL string) (*Target, error) {
	req, err := http.NewRequest("PUT", c.HTTPBase+"/json/new?"+url.QueryEscape(targetURL), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
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
			if cand.URL == targetURL {
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

type SearchEvidence struct {
	Title   string `json:"title,omitempty"`
	URL     string `json:"url,omitempty"`
	Passage string `json:"passage"`
}

// AdaptiveResearch searches in this client's browser process and, when snippets
// are available, reads up to four result pages. Callers must provide the
// dedicated search-browser client; the quiz client is never a fallback.
func (c *Client) AdaptiveResearch(query string, timeout time.Duration) ([]SearchEvidence, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	engines := []struct{ url, script string }{
		{"https://www.google.com/search?q=" + url.QueryEscape(query), `(() => {
   const results=[];
   document.querySelectorAll('div.MjjYud, div.g').forEach(el=>{
    const h=el.querySelector('h3'), a=h && h.closest('a'), p=el.querySelector('.VwiC3b, .IsZvec, [data-sncf]');
    if(a && p) results.push({title:h.innerText,url:a.href,passage:p.innerText});
   }); return JSON.stringify(results);
  })()`},
		{"https://www.bing.com/search?q=" + url.QueryEscape(query), `JSON.stringify(Array.from(document.querySelectorAll('#b_results .b_algo')).map(el=>({title:el.querySelector('h2')?.innerText || '',url:el.querySelector('h2 a')?.href || '',passage:el.querySelector('.b_caption p, p, .b_caption, .b_snippet, .b_lineclamp2, .b_lineclamp3')?.innerText || ''})))`},
		{"https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query), `JSON.stringify(Array.from(document.querySelectorAll('.result')).map(el=>({title:el.querySelector('.result__a')?.innerText || '',url:el.querySelector('.result__a')?.href || '',passage:el.querySelector('.result__snippet')?.innerText || ''})))`},
		{"https://search.yahoo.com/search?p=" + url.QueryEscape(query), `JSON.stringify(Array.from(document.querySelectorAll('#web .algo, .dd.algo')).map(el=>({title:el.querySelector('h3')?.innerText || '',url:el.querySelector('h3 a, a.ac-algo')?.href || '',passage:el.querySelector('.compText p, .algo-desc, .dd p')?.innerText || ''})))`},
	}
	var evidence []SearchEvidence
	seen := map[string]bool{}
	for _, engine := range engines {
		if !time.Now().Before(deadline) {
			break
		}
		target, err := c.CreateBackgroundTab(engine.url)
		if err != nil {
			continue
		}
		engineDeadline := time.Now().Add(8 * time.Second)
		initialCount := len(evidence)
		for time.Now().Before(engineDeadline) && time.Now().Before(deadline) {
			raw, err := c.Evaluate(target.WebSocketURL, engine.script)
			var results []SearchEvidence
			if err == nil && json.Unmarshal([]byte(raw), &results) == nil {
				for _, item := range results {
					item.URL = sourceURL(item.URL)
					if item.URL == "" || seen[item.URL] || strings.TrimSpace(item.Passage) == "" {
						continue
					}
					seen[item.URL] = true
					evidence = append(evidence, item)
				}
				if len(evidence)-initialCount >= 3 {
					break
				}
			}
			time.Sleep(300 * time.Millisecond)
		}
		_ = c.CloseTarget(target.ID)
		if len(evidence) >= 3 {
			break
		}
	}
	if len(evidence) == 0 {
		return nil, fmt.Errorf("no search evidence: providers blocked, empty, or timed out")
	}
	sort.SliceStable(evidence, func(a, b int) bool {
		return passageRelevance(query, evidence[a].Title+" "+evidence[a].Passage) > passageRelevance(query, evidence[b].Title+" "+evidence[b].Passage)
	})
	return c.enrichEvidence(query, evidence, deadline), nil
}

// Resolve public search redirects before attributing or opening source pages.
func sourceURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	if strings.HasSuffix(u.Hostname(), "google.com") && u.Path == "/url" {
		if q := u.Query().Get("q"); q != "" {
			return sourceURL(q)
		}
	}
	if strings.HasSuffix(u.Hostname(), "search.yahoo.com") {
		if start := strings.Index(u.EscapedPath(), "/RU="); start >= 0 {
			value := strings.Split(u.EscapedPath()[start+4:], "/RK=")[0]
			if decoded, err := url.PathUnescape(value); err == nil {
				return sourceURL(decoded)
			}
		}
	}
	if strings.HasSuffix(u.Hostname(), "bing.com") && u.Path == "/ck/a" {
		encoded := strings.TrimPrefix(u.Query().Get("u"), "a1")
		if decoded, err := base64.RawURLEncoding.DecodeString(encoded); err == nil {
			return sourceURL(string(decoded))
		}
	}
	if strings.HasSuffix(u.Hostname(), "duckduckgo.com") {
		if target := u.Query().Get("uddg"); target != "" {
			return sourceURL(target)
		}
	}
	u.Fragment = ""
	return u.String()
}

func (c *Client) enrichEvidence(query string, evidence []SearchEvidence, deadline time.Time) []SearchEvidence {
	pageJS := `(() => {
  if(document.readyState==='loading') return '[]';
  const parts=[];
  document.querySelectorAll('article p, main p, [role="main"] p, p').forEach(p=>{
   const t=(p.innerText||'').trim().replace(/\s+/g,' ');
   if(t.length>25 && parts.length<150)parts.push(t.slice(0,1500));
  });return JSON.stringify(parts);
 })()`
	limit := len(evidence)
	if limit > 4 {
		limit = 4
	}
	for i := 0; i < limit && time.Now().Before(deadline); i++ {
		target, err := c.CreateBackgroundTab(evidence[i].URL)
		if err != nil {
			continue
		}
		pageDeadline := time.Now().Add(6 * time.Second)
		var passages []string
		for time.Now().Before(pageDeadline) && time.Now().Before(deadline) {
			raw, err := c.Evaluate(target.WebSocketURL, pageJS)
			if err == nil && json.Unmarshal([]byte(raw), &passages) == nil && len(passages) > 0 {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		_ = c.CloseTarget(target.ID)
		if len(passages) == 0 {
			continue
		}
		sort.SliceStable(passages, func(a, b int) bool {
			return passageRelevance(query, passages[a]) > passageRelevance(query, passages[b])
		})
		if len(passages) > 4 {
			passages = passages[:4]
		}
		evidence[i].Passage = strings.Join(passages, "\n")
	}
	return evidence
}

func passageRelevance(query, passage string) int {
	passage = strings.ToLower(passage)
	seen := make(map[string]bool)
	score := 0
	for _, token := range strings.Fields(strings.ToLower(query)) {
		token = strings.Trim(token, ".,:;!?()[]{}\"'")
		if len(token) < 4 || seen[token] {
			continue
		}
		seen[token] = true
		score += strings.Count(passage, token)
	}
	return score
}

// BackgroundSearch is retained for compatibility with callers that only need
// passage text.
func (c *Client) BackgroundSearch(query string, timeout time.Duration) ([]string, error) {
	evidence, err := c.AdaptiveResearch(query, timeout)
	if err != nil {
		return nil, err
	}
	passages := make([]string, 0, len(evidence))
	for _, item := range evidence {
		passages = append(passages, item.Passage)
	}
	return passages, nil
}
