package von

// PollShortcut installs the browser shortcut on new/navigated tabs and consumes
// at most one request. Only the attached browser is inspected; research is isolated.
func (c *Client) PollShortcut() string {
	targets, err := c.ListTargets()
	if err != nil {
		return ""
	}
	for _, target := range targets {
		if target.Type != "page" {
			continue
		}
		raw, err := c.Evaluate(target.WebSocketURL, `(() => {
    function install(win) {
      try {
        if (!win.__mimirShortcutInstalled) {
          win.__mimirShortcutInstalled = true;
          win.addEventListener('keydown', event => {
            if (event.ctrlKey && !event.altKey && !event.metaKey && !event.shiftKey && event.key.toLowerCase() === 'p') {
              event.preventDefault(); event.stopImmediatePropagation();
              if (!event.repeat) window.__mimirStartRequested = true;
            }
          }, true);
        }
        for (let i=0; i<win.frames.length; i++) install(win.frames[i]);
      } catch (_) {} // Cross-origin frames require their own CDP execution context.
    }
    install(window);
    const requested = window.__mimirStartRequested === true;
    window.__mimirStartRequested = false;
    return requested;
  })()`)
		if err == nil && raw == "true" {
			return target.ID
		}
	}
	return ""
}

func (c *Client) PinTarget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.targetID = id
}
