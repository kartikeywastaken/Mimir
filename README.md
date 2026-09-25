# Mimir — Laya Browser Auto-Answer Finder

> **Mimir** (Norse wisdom) — sees the question, thinks on-device with Laya, whispers the answer. Never switches your tab. No OpenAI key.

```
Question visible in browser
        ↓
Agent extracts question + choices  (via laya-browser snapshot or Von CDP, DOM scan)
        ↓
Laya typed-decision model runs locally (~5ms ANE / ~15ms MLX, no API key)
        ↓
Overlay shows:  Suggested answer: B  |  Reason: Laya laya-coreml (92%)  |  Confidence: 92%
        ↓
YOU choose — or auto-mark if confident
```

Recursive background search: Mimir spawns a **hidden tab** to google/research in parallel without ever stealing focus (`Target.createTarget` without `activate`), feeds snippets into Laya's `state` (up to 96/512/1024 tokens depending on model).

Built in **Go** + **Bubble Tea** TUI + **Gum**.

---

## Are `layaForWeb`, `laya-coreml`, and `laya-browser` the same?

**No — same base model lineage, different runtimes/ports.** All are ports of base **Laya** by ConvAI Innovations (`NandhaKishorM/laya`, ModernBERT-large + typed heads, Apache-2.0).

| Repo | Author | Runtime | Model download | Tokens | Speed | Download |
|------|--------|---------|----------------|--------|-------|----------|
| **layaForWeb**<br>`https://vishalmysore.github.io/layaForWeb/` | Vishal Mysore | ONNX Runtime Web (WASM/WebGPU) in browser | few hundred MB from this site, once per visit | state 512, options 192 each, ~20 options | in-browser, CPU/WASM | This gets downloaded on first Load |
| **laya-coreml**<br>`https://github.com/mizorewww/laya-coreml` | mizorewww | Core ML + Apple Neural Engine | `aac6fef/laya-multilingual-coreml-ane` (~800MB) from Hugging Face | ANE: **96** total, general: 1024 | **4.98ms P50** on M3 Max (ANE FP16) | `pip install laya-coreml` |
| **laya-mlx**<br>`https://github.com/mizorewww/laya-mlx` | mizorewww | MLX (Apple Silicon GPU) | `aac6fef/laya-mlx` (~800MB) | 1024 | ~15ms | `pip install laya-mlx` |
| **laya-browser**<br>`https://github.com/Benny93/laya-browser` | Benny93 | **Wrapper** around `agent-browser` (Playwright) + `laya-mlx` | `aac6fef/laya-mlx` (~800MB) on first English selector | inherits laya-mlx 1024 | ~15ms for ≤32 elements | `uv tool install git+https://github.com/Benny93/laya-browser` |

**So:**
- `layaForWeb` **≠** `laya-coreml` — different ports (ONNX vs CoreML). Vishal's site runs the English model in the browser; mizorewww's runs on ANE.
- `laya-browser` **uses** `laya-mlx` (mizorewww's MLX port) under the hood to resolve English selectors like `click "log in"` locally, with no LLM token cost. It's an `agent-browser` drop-in.

**Mimir uses `laya-browser`'s idea but for quiz solving:** instead of `choice` over snapshot elements, we do `choice` over answer options. Solver at `internal/laya/client.go:1` tries `laya-coreml` first (fastest on Apple Silicon), falls back to `laya-mlx` (same as laya-browser), falls back to mock. **No OpenAI key needed** — see `scripts/laya_solve.py:1`.

---

## Quick Start (Laya, no API key)

```bash
cd ~/Desktop/Mimir
make setup      # installs gum, laya-coreml/mlx, downloads model ~800MB on first solve
make run        # launch TUI
# or
./bin/mimir
```

**Prereqs:**
- Go 1.22+, Apple Silicon, macOS 14+, Python 3.11+
- **laya-browser** (recommended) *or* Von:
  ```bash
  # Option A: laya-browser (local Laya, no token cost) — as you asked
  uv tool install git+https://github.com/Benny93/laya-browser
  # also need agent-browser on PATH
  npm install -g agent-browser  # or: npx agent-browser

  # Option B: Von (Chromium CDP) still supported
  # /Applications/Von.app/Contents/MacOS/Von --remote-debugging-port=9222 --user-data-dir=/tmp/von-mimir

  # Laya model (pick one, first run downloads)
  pip install 'laya-coreml[demo]'   # best: ~5ms ANE
  # or
  pip install laya-mlx              # same as laya-browser
  ```

**Browser Setup — laya-browser (your request):**
```bash
# This is the browser you asked to use: https://github.com/Benny93/laya-browser
# It's agent-browser but English selectors resolve locally via Laya in ms.

laya-browser open https://example.com/your-quiz
# Mimir will connect via CDP or via `laya-browser snapshot -i --json`
# The same "hidden tab" trick works: laya-browser open --new-tab https://google.com/search?q=...
# but Mimir's von/client.go does it via CDP without focus switch.

# Check model cached
hf download aac6fef/laya-mlx  # or aac6fef/laya-multilingual-coreml-ane
```

**Von Setup (alternative):**
```bash
/Applications/Von.app/Contents/MacOS/Von --remote-debugging-port=9222 --user-data-dir=/tmp/von-mimir
```

---

## Config

```bash
cp config.yaml.example config.yaml
# edit browser.kind (laya-browser vs von), laya.model
```

- `laya.model`: `aac6fef/laya-multilingual-coreml-ane` (96 tokens, fastest) or `aac6fef/laya-mlx` (1024)
- `layaForWeb` limits (512 state / 192 per option) are handled by truncation in `scripts/laya_solve.py:45`

## TUI Keybindings

| Key | Action |
|-----|--------|
| `e` | Extract question + choices from active tab (CDP or snapshot) |
| `s` | Solve via **Laya local** (+ background search) |
| `o` | Toggle overlay on page (no tab switch) |
| `a` | Auto mode: extract → search → solve → overlay (loop) |
| `g` | Toggle background Google research (hidden tab) |
| `q` / `ctrl+c` | Quit |

## Architecture

```
cmd/mimir/main.go:1            → entry, bubbletea, laya.New() (no OpenAI)
internal/laya/client.go:1      → Go → Python bridge to laya-coreml/mlx (same model as laya-browser)
scripts/laya_solve.py:1        → Python: agent.predict(state, {"q": {"type":"choice", "criteria":[...]}})
internal/solver/solver.go:1    → Builds Laya state (question + hidden-tab snippets) → calls laya
internal/von/client.go:1       → CDP: ListTargets, CreateBackgroundTab (hidden), Evaluate, InjectOverlay
internal/extractor/extractor.go:1 → DOM heuristics (radio, data-testid, etc.)
internal/overlay/overlay.go:1  → JS floating card
internal/tui/app.go:1          → Bubble Tea model (idle → extracting → Laya solving → done)
```

**How Laya solving works (vs OpenAI before):**
Before: `question + choices + search snippets → OpenAI gpt-4o-mini → JSON`. Now: `state=question + snippets, criteria=choices → Laya choice → label + confidence`. No network, no key. Tournament for >32 options (like laya-browser's chunking at `laya_browser.py:18`).

**How Recursive Background Search works (kept):**
1. `von.CreateBackgroundTab("https://google.com/search?q=...")` — not activated
2. Poll `Runtime.evaluate` until results
3. Close tab, feed snippets into Laya `state`
4. User never sees tab switch

This mirrors `layaForWeb`'s 100% local inference — nothing you type leaves the machine — but now for quiz answers.

## Customizing

- Add site selectors in `internal/extractor/extractor.go:10`
- Change Laya instructions in `config.yaml` or `internal/solver/solver.go:24`
- For `layaForWeb` in-browser mode, load `https://vishalmysore.github.io/layaForWeb/` in a hidden laya-browser tab and post via `Runtime.evaluate` — Mimir's Go solver already truncates to its 512/192 limits.

## macOS Build Note

Go 1.27 on APFS `F_PREALLOCATE` fails with `EILSEQ` for binaries >=5M (`outbuf_darwin.go:17`). `Makefile:14` works around by building on FAT32 RAM disk (`/tmp/ramdisk`, `TMPDIR=/tmp/ramdisk`). The built `bin/mimir` is 10M and runs fine.

## Roadmap

- [ ] Direct `laya-browser snapshot` extractor (instead of CDP) for pure laya-browser flow
- [ ] Confidence threshold auto-click via `laya-browser click "B"`
- [ ] layaForWeb WASM hidden-tab alternative for non-Apple Silicon

---
Built for research — you choose the final answer, Laya just whispers locally.
# Mimir
