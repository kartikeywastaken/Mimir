package extractor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Question represents a single question pulled from the visible tab
type Question struct {
	Index      int      `json:"index"`
	Text       string   `json:"text"`
	Choices    []Choice `json:"choices"`
	RawHTML    string   `json:"raw_html,omitempty"`
	SolvedAns  string   `json:"solved_ans,omitempty"`
	Confidence int      `json:"confidence,omitempty"`
	Marked     bool     `json:"marked,omitempty"`
}

type Choice struct {
	Label string `json:"label"` // A, B, C, D
	Text  string `json:"text"`
}

type BatchResult struct {
	Questions []Question `json:"questions"`
	IsLogin   bool       `json:"is_login"`
}

// ExtractAllJS detects all questions on the page, groups choices, and stamps
// data-mimir attributes for zero-popup, high-precision DOM marking.
const ExtractAllJS = `
(() => {
  function visible(el) {
    if (!el) return false;
    const s = window.getComputedStyle(el);
    return s.display !== 'none' && s.visibility !== 'hidden' && s.opacity !== '0' && el.offsetParent !== null;
  }
  function text(el) {
    return (el.innerText || el.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 1200);
  }

  // Detect login/auth screen
  const hasPassword = document.querySelector('input[type="password"]') !== null;
  const loginForm = document.querySelector('form[action*="login"], form[id*="login"], form[class*="login"], [data-testid*="login"]');
  const isLoginPage = hasPassword || (loginForm !== null && document.querySelectorAll('input[type="radio"], input[type="checkbox"]').length === 0);

  // Group radio inputs by name
  const radioGroups = {};
  const radios = Array.from(document.querySelectorAll('input[type="radio"], input[type="checkbox"]')).filter(visible);
  radios.forEach(r => {
    const name = r.name || r.id || 'default_group';
    if (!radioGroups[name]) radioGroups[name] = [];
    radioGroups[name].push(r);
  });

  const questions = [];
  const groupNames = Object.keys(radioGroups);

  if (groupNames.length > 0) {
    // Process each group as a question
    groupNames.forEach((name, qIdx) => {
      const inputs = radioGroups[name];
      // Find question prompt by searching upwards and before the first input
      let promptText = '';
      let cur = inputs[0];
      let card = cur.closest('fieldset, .question, .quiz-card, .form-group, [class*="question"], [class*="item"], div');
      
      // Try to find a heading or prompt inside the container
      if (card) {
        const headings = card.querySelectorAll('h1, h2, h3, h4, [role="heading"], legend, .prompt, [class*="stem"], p');
        for (const h of headings) {
          if (visible(h)) {
            const t = text(h);
            if (t.length > 15 && h.querySelectorAll('input').length === 0) {
              promptText = t;
              break;
            }
          }
        }
      }

      if (!promptText) {
        // Walk back through siblings of the input or its parent
        let walker = inputs[0].parentElement;
        while (walker && walker !== document.body && !promptText) {
          let prev = walker.previousElementSibling;
          while (prev) {
            if (visible(prev)) {
              const t = text(prev);
              if (t.length > 15 && prev.querySelectorAll('input').length === 0) {
                promptText = t;
                break;
              }
            }
            prev = prev.previousElementSibling;
          }
          walker = walker.parentElement;
        }
      }

      if (!promptText) {
        promptText = 'Question ' + (qIdx + 1);
      }

      // Collect choices from this group
      const choices = [];
      inputs.forEach((inp, cIdx) => {
        let choiceText = '';
        // 1. label containing the input or referenced by for attribute
        let lbl = inp.closest('label');
        if (!lbl && inp.id) {
          lbl = document.querySelector('label[for="' + inp.id + '"]');
        }
        if (lbl) {
          choiceText = text(lbl);
        } else if (inp.parentElement) {
          choiceText = text(inp.parentElement);
        }
        if (!choiceText) {
          choiceText = inp.value || ('Option ' + (cIdx + 1));
        }

        // Clean label prefix (e.g. "A) ", "B. ")
        choiceText = choiceText.replace(/^[A-Z][\).:-]\s*/, '').trim();
        const label = String.fromCharCode(65 + cIdx);

        // Stamp element for deterministic robust marking
        inp.setAttribute('data-mimir-q', String(qIdx));
        inp.setAttribute('data-mimir-opt', label);
        if (lbl) {
          lbl.setAttribute('data-mimir-q', String(qIdx));
          lbl.setAttribute('data-mimir-opt', label);
        }

        choices.push({ label, text: choiceText });
      });

      if (choices.length > 0) {
        questions.push({
          index: qIdx,
          text: promptText,
          choices: choices
        });
      }
    });
  }

  // Fallback if no standard radio groups: check if there's a single question card
  if (questions.length === 0 && !isLoginPage) {
    const singleQSelectors = ['[data-testid*="question"]', '[data-qa*="question"]', '.question', '#question', 'h1', 'h2', 'h3'];
    let qEl = null;
    for (const sel of singleQSelectors) {
      const els = Array.from(document.querySelectorAll(sel)).filter(visible);
      for (const e of els) {
        const t = text(e);
        if (t.length > 20 && (t.includes('?') || t.split(' ').length > 5)) {
          if (e.querySelectorAll('input').length === 0) {
            qEl = e; break;
          }
        }
      }
      if (qEl) break;
    }

    if (qEl) {
      const qText = text(qEl);
      const choiceEls = Array.from(document.querySelectorAll('label, [role="radio"], .option, .choice, li')).filter(visible);
      const validChoices = [];
      const seen = new Set();
      choiceEls.forEach((el, idx) => {
        let t = text(el).replace(/^[A-D][\).:-]\s*/, '').trim();
        if (t.length > 1 && t.length < 400 && !seen.has(t)) {
          seen.add(t);
          const label = String.fromCharCode(65 + validChoices.length);
          el.setAttribute('data-mimir-q', '0');
          el.setAttribute('data-mimir-opt', label);
          validChoices.push({ label, text: t });
        }
      });
      if (validChoices.length > 0) {
        questions.push({
          index: 0,
          text: qText,
          choices: validChoices
        });
      }
    }
  }

  return JSON.stringify({
    questions: questions,
    is_login: isLoginPage && questions.length === 0
  });
})()
`

// Legacy single-question script kept for backward compatibility
const ExtractJS = ExtractAllJS

// MarkAnswerJS generates JavaScript that executes Combined Robust Dispatch:
// Native .click() + explicit .checked = true + bubbling change & input events + mouse events
func MarkAnswerJS(qIndex int, label string) string {
	return fmt.Sprintf(`
(() => {
  const selector = '[data-mimir-q="%d"][data-mimir-opt="%s"]';
  const target = document.querySelector(selector);
  if (!target) return false;

  const input = target.tagName === 'INPUT' ? target : (target.querySelector('input') || target);

  try {
    input.scrollIntoView({ block: 'nearest', behavior: 'instant' });
    input.focus();
    input.click();

    if (input.type === 'radio' || input.type === 'checkbox') {
      input.checked = true;
    }

    // Bubbling events for React / Vue / Angular / Canvas LMS
    input.dispatchEvent(new Event('input', { bubbles: true, cancelable: true }));
    input.dispatchEvent(new Event('change', { bubbles: true, cancelable: true }));

    // Pointer and mouse events
    input.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    input.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }));
    return true;
  } catch (err) {
    return false;
  }
})()
`, qIndex, label)
}

// ParseBatchResult parses the JSON string returned by ExtractAllJS
func ParseBatchResult(jsonStr string) (*BatchResult, error) {
	var inner string
	if err := json.Unmarshal([]byte(jsonStr), &inner); err == nil {
		jsonStr = inner
	}

	var batch BatchResult
	if err := json.Unmarshal([]byte(jsonStr), &batch); err != nil {
		return nil, fmt.Errorf("parse batch extractor result: %w | raw: %s", err, jsonStr)
	}

	for i := range batch.Questions {
		batch.Questions[i].Index = i
		for j := range batch.Questions[i].Choices {
			if batch.Questions[i].Choices[j].Label == "" {
				batch.Questions[i].Choices[j].Label = string(rune('A' + j))
			}
			batch.Questions[i].Choices[j].Label = strings.ToUpper(strings.TrimSpace(batch.Questions[i].Choices[j].Label))
		}
	}
	return &batch, nil
}

// ParseResult maintains compatibility with single-question callers
func ParseResult(jsonStr string) (*Question, error) {
	batch, err := ParseBatchResult(jsonStr)
	if err == nil && len(batch.Questions) > 0 {
		return &batch.Questions[0], nil
	}

	var inner string
	if err := json.Unmarshal([]byte(jsonStr), &inner); err == nil {
		jsonStr = inner
	}
	var q Question
	if err := json.Unmarshal([]byte(jsonStr), &q); err != nil {
		return nil, fmt.Errorf("parse extractor result: %w | raw: %s", err, jsonStr)
	}
	for i := range q.Choices {
		if q.Choices[i].Label == "" {
			q.Choices[i].Label = string(rune('A' + i))
		}
		q.Choices[i].Label = strings.ToUpper(strings.TrimSpace(q.Choices[i].Label))
	}
	return &q, nil
}
