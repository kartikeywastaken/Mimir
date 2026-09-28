#!/usr/bin/env python3
"""
Mimir Laya solver — bridges Go to local Laya model.

Uses laya-coreml (Core ML ANE, ~5ms) if available, else laya-mlx (MLX, ~15ms) which
is the same backend that Benny93/laya-browser uses (aac6fef/laya-mlx).

Install one of:
  pip install 'laya-coreml[demo]'   # Apple Silicon, macOS 15+, best for Mimir
  pip install laya-mlx              # fallback, also pulled by `uv tool install laya-browser`

First run downloads the model (~800 MB) from Hugging Face. No API key needed.

Protocol: reads JSON from stdin, writes JSON to stdout.
Input:  {"state": "...", "question": "...", "choices": [{"label":"A","text":"..."}], "instructions": "..."}
Output: {"choice": "B) ...", "answer": "B", "confidence": 0.92, "probabilities": {...}}

This matches layaForWeb's shape but runs locally — nothing leaves the machine.
"""
import json
import os
import sys

# Ensure Hugging Face model cache uses RAM disk to avoid APFS EILSEQ 92 bug
if "HF_HOME" not in os.environ and os.path.isdir("/tmp/ramdisk"):
    os.environ["HF_HOME"] = "/tmp/ramdisk/hf-home"

# Token limits
LIMITS = {
    "laya-coreml-ane": 96,      # Multilingual ANE B1/L96
    "laya-coreml": 1024,        # general
    "laya-mlx": 1024,
    "layaForWeb": 512,
}

def truncate(text, max_tokens=400):
    # very rough: 1 token ~= 4 chars
    max_chars = max_tokens * 4
    if len(text) > max_chars:
        return text[:max_chars-3] + "..."
    return text

def load_agent():
    model = os.environ.get("LAYA_MODEL", "aac6fef/laya-multilingual-coreml-ane")
    # Try laya-coreml first (Core ML ANE) — ~5ms on M3 Max
    try:
        import laya_coreml as laya  # type: ignore
        try:
            agent = laya.load(model)
            print(f"laya: using laya-coreml {model}", file=sys.stderr)
            return agent, "laya-coreml", 96 if "ane" in model else 1024
        except Exception as e:
            print(f"laya-coreml load failed ({e}), trying laya-mlx...", file=sys.stderr)
    except ImportError:
        pass

    # Fallback: laya-mlx — same as laya-browser (aac6fef/laya-mlx)
    try:
        import laya_mlx as laya  # type: ignore
        mlx_model = os.environ.get("LAYA_MODEL", "")
        if not mlx_model or "coreml" in mlx_model:
            mlx_model = "aac6fef/laya-mlx"
        agent = None
        try:
            agent = laya.load(mlx_model, batch_size=32)
        except Exception as e:
            print(f"laya-mlx load {mlx_model} failed ({e}), trying aac6fef/laya-mlx...", file=sys.stderr)
            try:
                agent = laya.load("aac6fef/laya-mlx", batch_size=32)
                mlx_model = "aac6fef/laya-mlx"
            except Exception as e2:
                print(f"laya-mlx fallback load failed ({e2})", file=sys.stderr)
        if agent is not None:
            print(f"laya: using laya-mlx {mlx_model}", file=sys.stderr)
            return agent, "laya-mlx", 1024
        print("laya-mlx model unavailable", file=sys.stderr)
        return None, "none", 0
    except ImportError as e:
        print(f"neither laya-coreml nor laya-mlx installed: {e}", file=sys.stderr)
        return None, "none", 0

_agent_cache = None
_agent_type = None
_agent_limit = 0

def get_agent():
    global _agent_cache, _agent_type, _agent_limit
    if _agent_cache is None:
        _agent_cache, _agent_type, _agent_limit = load_agent()
    return _agent_cache, _agent_type, _agent_limit

def solve_one(data):
    state = data.get("state") or data.get("question") or ""
    question = data.get("question") or ""
    # Checkbox calls have identical Y/N choices: their individual option must
    # appear in state, otherwise every option receives the same prediction.
    if question and question not in state:
        state = question + "\n\n" + state
    choices = data.get("choices") or []
    instructions = data.get("instructions") or "Which answer correctly answers the question?"

    # Build criteria as "A) text" — Laya's tournament works best with labelled options
    criteria = []
    label_map = {}
    for c in choices:
        label = c.get("label", "")
        text = c.get("text", "")
        crit = f"{label}) {text}" if label else text
        criteria.append(crit)
        label_map[crit] = label
        label_map[crit + " "] = label  # laya-mlx pads duplicate with space

    if not criteria:
        return {"error": "no choices"}

    agent, kind, limit = get_agent()
    if agent is None:
        return {"error": "no Laya model is installed or loadable"}

    # Truncate to fit token limits
    # layaForWeb: 192 tokens per option, 512 state
    # laya-coreml ANE: 96 total, general: 1024
    if kind == "laya-coreml" and limit == 96:
        # very strict — truncate aggressively
        state = truncate(state, 80)
        criteria = [truncate(c, 40) for c in criteria]
    else:
        state = truncate(state, 400)
        criteria = [truncate(c, 150) for c in criteria]

    # Laya needs at least 2 options per choice; duplicate if single
    if len(criteria) == 1:
        criteria = criteria + [criteria[0] + " "]

    q = {"q": {"type": "choice", "instructions": instructions, "criteria": criteria}}

    try:
        result = agent.predict(state, q)
        ans = result["answers"]["q"]
        choice_text = ans["choice"]  # e.g. "B) Paris"
        confidence = float(ans.get("confidence", 0.5))
        # Map back to label
        # choice_text may be exactly one of criteria, possibly with trailing space
        label = label_map.get(choice_text.strip(), None)
        if label is None:
            # try prefix match "B) ..."
            for crit, lbl in label_map.items():
                if choice_text.strip().startswith(lbl + ")"):
                    label = lbl
                    break
        if not label:
            return {"error": f"Laya returned an unmapped choice: {choice_text!r}"}
        # Clamp confidence 0.5-5.0 temperature already handled by laya-coreml shim
        return {
            "choice": choice_text,
            "answer": label,
            "confidence": confidence,
            "probabilities": ans.get("probabilities", {}),
            "backend": kind,
            "model": os.environ.get("LAYA_MODEL", ""),
        }
    except Exception as e:
        import traceback
        traceback.print_exc(file=sys.stderr)
        return {"error": str(e)}

def main():
    try:
        raw = sys.stdin.read()
        if not raw.strip():
            json.dump({"error": "empty input"}, sys.stdout)
            return
        data = json.loads(raw)
        out = solve_one(data)
        json.dump(out, sys.stdout)
    except Exception as e:
        json.dump({"error": f"solver error: {e}"}, sys.stdout)

if __name__ == "__main__":
    main()
