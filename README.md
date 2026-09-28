# Mimir — Laya Browser Auto-Answer Finder

> **Mimir** (Norse wisdom) — extracts form questions, solves them with local Laya, an isolated research browser, or both, and fills verified answers. It never submits the form.

```
Question visible in browser
        ↓
Mimir extracts questions and stable input targets via CDP
        ↓
Local, Web, or Hybrid pipeline obtains a real answer
        ↓
Mimir fills the matching field and verifies its DOM value
        ↓
Mimir advances through safe Next controls and stops before Submit
```

Web research runs in a separate headless Chromium process on port `9223`. Search tabs and source pages never open in the quiz browser on port `9222`.

### Solving modes

- **Local:** Laya handles selectable answers. Short-answer and paragraph fields use the isolated browser because Laya's typed-decision model does not generate free text. Local failures fall back to Web.
- **Web:** Browser evidence answers every supported field; selectable questions fall back to Laya when research cannot resolve them.
- **Hybrid:** The browser gathers evidence and Laya selects or validates selectable answers. A valid result from either component can be used if the other fails.

Mimir supports radio buttons, multi-select checkboxes, dropdowns, short answers, and paragraphs. Before solving, it classifies fields as objective questions, respondent-owned fields, or non-questions. Personal/profile fields and ambiguous prompts are left untouched. Answers below the 70% safety threshold, missing evidence, and failed DOM writes are also left unchanged. Mock answers are never written.

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

**Mimir uses `laya-browser`'s idea but for form solving:** instead of `choice` over snapshot elements, it performs typed decisions over answer options. The solver tries `laya-coreml` first and then `laya-mlx`. If neither real model is available, it returns an error and uses the configured Web fallback; it never fabricates a mock choice. **No OpenAI key is needed.**

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
| `ctrl+p` | Start solving after the form is open and respondent details are filled |
| `m` | Choose solving mode |
| `b` | Choose browser |
| `/` | Open the command composer |
| `q` / `ctrl+c` | Quit |

Mimir uses the terminal's configured typeface and works best with a modern
monospace font that includes Unicode runes. Set `MIMIR_ASCII=1` to use the
plain `Mimir` wordmark when the rune glyph is unavailable. The fullscreen TUI
runs in the terminal's alternate screen and restores normal terminal behavior
when it exits. The selected mode is persisted in the OS user-config directory.
Mimir does not begin extracting or solving when the browser opens; it waits for
`ctrl+p`, then spaces question attempts by two seconds.

The centered UI includes a compact decision trace showing classification,
pipeline/backend, evidence sources, confidence, fallbacks, verified fills, and
fields deliberately left for the respondent. It reports operational reasoning
without exposing or inventing hidden model chain-of-thought.

## Architecture

```
cmd/mimir/main.go:1            → entry, bubbletea, laya.New() (no OpenAI)
internal/laya/client.go:1      → Go → Python bridge to laya-coreml/mlx (same model as laya-browser)
scripts/laya_solve.py:1        → Python: agent.predict(state, {"q": {"type":"choice", "criteria":[...]}})
internal/solver/solver.go:1    → Local/Web/Hybrid decisions and structured answers
internal/von/client.go:1       → isolated adaptive web research via CDP
internal/extractor/extractor.go:1 → DOM extraction and stable target stamping
internal/extractor/fill.go:1   → type-aware filling, readback verification, safe Next
internal/tui/app.go:1          → fullscreen workflow, fallbacks, progress, summaries
```

**How Laya solving works:** `state=question + optional evidence, criteria=choices → Laya choice → label + confidence`. Checkbox options are evaluated independently so multiple values can be selected. Laya inference is local and needs no API key.

**How adaptive research works:**
1. Start or reuse a dedicated headless browser on port `9223`.
2. Read search-result evidence, falling back between supported search engines.
3. For weak, ambiguous, or long-form evidence, inspect up to three public result pages.
4. Close research tabs and return structured title, URL, and passage evidence.

Web and Hybrid modes—and Local mode for free-text fields or fallback—send the question text to public search services. The quiz browser is never used for this traffic.

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
Mimir fills answers for review; the user always controls final submission.
