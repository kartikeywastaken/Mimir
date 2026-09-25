#!/usr/bin/env bash
set -e
# Mimir setup — gum + bubbletea + Laya (no OpenAI key)

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RAMDISK="/tmp/ramdisk"
RAMDISK_DMG="/tmp/ramdisk.dmg"

# 1) Ensure 3GB HFS+ RAM disk is mounted for APFS EILSEQ workaround
if ! mount | grep -qE "on (/private)?${RAMDISK} "; then
  echo "-> creating 3GB HFS+ RAM disk for builds & caches..."
  mkdir -p "${RAMDISK}"
  if [ ! -f "${RAMDISK_DMG}" ]; then
    hdiutil create -size 3g -fs "HFS+" -volname RAMDISK "${RAMDISK_DMG}" >/dev/null 2>&1 || true
  fi
  hdiutil attach "${RAMDISK_DMG}" -mountpoint "${RAMDISK}" >/dev/null 2>&1 || true
fi
mkdir -p "${RAMDISK}/uv-cache" "${RAMDISK}/uv-tools" "${RAMDISK}/hf-home"
export TMPDIR="${RAMDISK}"
export UV_CACHE_DIR="${RAMDISK}/uv-cache"
export UV_TOOL_DIR="${RAMDISK}/uv-tools"
export HF_HOME="${RAMDISK}/hf-home"

if ! gum --version >/dev/null 2>&1; then
  echo "-> gum not working or not installed..."
  if command -v brew >/dev/null 2>&1; then
    TMPDIR="${RAMDISK}" brew reinstall gum 2>/dev/null || echo "brew reinstall gum failed (can continue without gum)"
  else
    echo "brew not found — install gum manually: https://github.com/charmbracelet/gum"
  fi
else
  echo "v gum already installed and working: $(which gum)"
fi

echo "-> checking Python + virtualenv"
if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 not found — install Python 3.11+"
  exit 1
fi

# Create project virtual environment to isolate from system Python and avoid PEP 668 errors
VENV_DIR="${ROOT_DIR}/.venv"
if [ ! -d "${VENV_DIR}" ]; then
  echo "-> creating virtual environment at ${VENV_DIR}..."
  if command -v uv >/dev/null 2>&1; then
    uv venv "${VENV_DIR}"
  else
    python3 -m venv "${VENV_DIR}"
  fi
fi
VENV_PY="${VENV_DIR}/bin/python"

# Repair system pip if corrupted, as a safeguard
if ! python3 -m pip --version >/dev/null 2>&1; then
  echo "-> repairing system pip via ensurepip..."
  python3 -m ensurepip --upgrade >/dev/null 2>&1 || true
fi

# laya-browser (Benny93) — agent-browser + laya-mlx
if ! command -v laya-browser >/dev/null 2>&1; then
  echo "-> installing laya-browser (https://github.com/Benny93/laya-browser)..."
  if command -v uv >/dev/null 2>&1; then
    uv tool install git+https://github.com/Benny93/laya-browser || echo "uv tool install failed, falling back to local venv"
  fi
else
  echo "v laya-browser already installed: $(which laya-browser)"
fi

if ! command -v agent-browser >/dev/null 2>&1; then
  echo "-> installing agent-browser (required by laya-browser)..."
  npm install -g agent-browser 2>&1 | tail -5 || echo "install via: npm i -g agent-browser"
else
  echo "v agent-browser: $(which agent-browser)"
fi

# Laya local model — installed into .venv via uv (fast & clean)
echo "-> checking Laya model in virtualenv (local, no API key)"
if ! "${VENV_PY}" -c "import importlib.util, sys; found=any(importlib.util.find_spec(p) for p in ['laya_coreml','laya_mlx']); sys.exit(0 if found else 1)" 2>/dev/null; then
  echo "   installing Laya backend (laya-mlx / laya-coreml) into .venv..."
  if command -v uv >/dev/null 2>&1; then
    uv pip install --python "${VENV_PY}" laya-mlx || uv pip install --python "${VENV_PY}" 'laya-coreml[demo]' || echo "Laya install failed"
  else
    "${VENV_DIR}/bin/pip" install laya-mlx || "${VENV_DIR}/bin/pip" install 'laya-coreml[demo]' || echo "pip install failed"
  fi
else
  echo "v Laya already installed in .venv"
fi

echo "-> go mod tidy (via RAM disk workaround for Go APFS EILSEQ)"
cd "${ROOT_DIR}"
TMPDIR="${RAMDISK}" go mod tidy

echo ""
if gum --version >/dev/null 2>&1; then
  gum style --border rounded --padding "1 2" --border-foreground 212 "o MIMIR ready (Laya)" "1) laya-browser open https://your-quiz.com" "2) make run  (no OPENAI_API_KEY needed)" "3) first solve downloads ~800MB model"
else
  echo "o MIMIR ready (Laya) — no OpenAI key needed"
  echo "1) laya-browser open https://your-quiz.com"
  echo "2) make run"
fi
echo ""
echo "Note: layaForWeb (Vishal) != laya-coreml (mizorewww) — same base Laya model, different runtimes (ONNX WASM vs Core ML). laya-browser uses laya-mlx."

