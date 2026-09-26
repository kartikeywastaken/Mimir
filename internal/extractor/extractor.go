package extractor

import (
	"encoding/json"
	"fmt"
	"strings"
)

type QuestionType string

const (
	TypeChoice   QuestionType = "choice"
	TypeText     QuestionType = "text"
	TypeDropdown QuestionType = "dropdown"
)

// Question represents a single question pulled from the visible tab
type Question struct {
	Index      int          `json:"index"`
	Type       QuestionType `json:"type,omitempty"` // "choice", "text", "dropdown"
	Text       string       `json:"text"`
	Choices    []Choice     `json:"choices,omitempty"`
	RawHTML    string       `json:"raw_html,omitempty"`
	SolvedAns  string       `json:"solved_ans,omitempty"`
	Confidence int          `json:"confidence,omitempty"`
	Marked     bool         `json:"marked,omitempty"`
}

type Choice struct {
	Label string `json:"label"` // A, B, C, D
	Text  string `json:"text"`
}

type BatchResult struct {
	Questions       []Question `json:"questions"`
	IsLogin         bool       `json:"is_login"`
	EmbeddedQuizURL string     `json:"embedded_quiz_url,omitempty"`
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

  // 0. Detect embedded quiz iframes (e.g. Google Forms embedded in blog/LMS)
  let embeddedQuizURL = '';
  const iframes = Array.from(document.querySelectorAll('iframe'));
  for (const f of iframes) {
    const src = f.src || f.getAttribute('data-src') || '';
    if (src.includes('docs.google.com/forms') || 
        src.includes('forms.gle') || 
        src.includes('forms.office.com') || 
        src.includes('typeform.com') || 
        src.includes('quizlet.com') ||
        (src.includes('quiz') && !src.includes('ad')) ||
        (src.includes('exam') && !src.includes('ad'))) {
      embeddedQuizURL = src;
      break;
    }
  }

  // Filter out blog comments, footers, cookie banners, and non-quiz inputs
  function isQuizInput(el) {
    if (!visible(el)) return false;
    if (el.closest('#commentform, .comment-form, #respond, #comments, .comments-area, footer, [id*="cookie"], [class*="cookie"]')) {
      return false;
    }
    const name = (el.name || el.id || el.getAttribute('aria-label') || '').toLowerCase();
    if (name.includes('comment') || name.includes('subscribe') || name.includes('cookie') || name.includes('consent') || name.includes('author') || name.includes('email') || name.includes('website')) {
      return false;
    }
    return true;
  }

  // Detect login/auth screen
  const hasPassword = document.querySelector('input[type="password"]') !== null;
  const loginForm = document.querySelector('form[action*="login"], form[id*="login"], form[class*="login"], [data-testid*="login"]');
  const isLoginPage = hasPassword || (loginForm !== null && document.querySelectorAll('input[type="radio"], input[type="checkbox"]').length === 0);

  const questions = [];

  // Strategy 1: Find structured question cards (Google Forms .Qr7Oae, Canvas, Blackboard, standard quiz cards)
  const cardSelectors = '[role="listitem"], .Qr7Oae, .freebirdFormviewerComponentsQuestionBaseRoot, fieldset, .question, .quiz-card, .form-group';
  const allFound = Array.from(document.querySelectorAll(cardSelectors)).filter(visible).filter(c => !c.closest('#commentform, .comment-form, #respond, #comments, footer'));
  // Filter to top-level cards only: drop cards that are contained inside another card
  const rawCards = allFound.filter(c => !allFound.some(other => other !== c && other.contains(c)));

  if (rawCards.length > 0) {
    rawCards.forEach((card) => {
      // Find prompt text
      let promptText = '';
      const headings = card.querySelectorAll('[role="heading"], .M7eMe, legend, h1, h2, h3, h4, .prompt, [class*="stem"], [class*="title"]');
      for (const h of headings) {
        if (visible(h)) {
          const t = text(h);
          if (t.length > 4 && h.querySelectorAll('input, [role="radio"]').length === 0) {
            promptText = t;
            break;
          }
        }
      }
      if (!promptText) {
        promptText = text(card).split('\n')[0] || ('Question ' + (questions.length + 1));
      }
      // Clean trailing point counts like "2 points" or "1 point"
      promptText = promptText.replace(/\s*\d+\s*points?\s*$/i, '').trim();

      const qIdx = questions.length;

      // 1. Multiple Choice (radios / checkboxes)
      const choiceInputs = Array.from(card.querySelectorAll('input[type="radio"], input[type="checkbox"], [role="radio"], [role="checkbox"]')).filter(isQuizInput);
      if (choiceInputs.length >= 2) {
        const choices = [];
        choiceInputs.forEach((inp, cIdx) => {
          let choiceText = '';
          let lbl = inp.closest('label') || (inp.id ? document.querySelector('label[for="' + inp.id + '"]') : null);
          if (!lbl && inp.getAttribute('aria-labelledby')) {
            lbl = document.getElementById(inp.getAttribute('aria-labelledby'));
          }
          if (lbl) choiceText = text(lbl);
          else if (inp.parentElement) choiceText = text(inp.parentElement);
          if (!choiceText) choiceText = inp.value || inp.getAttribute('aria-label') || ('Option ' + (cIdx + 1));
          choiceText = choiceText.replace(/^[A-Z][\).:-]\s*/, '').trim();
          const label = String.fromCharCode(65 + cIdx);

          inp.setAttribute('data-mimir-q', String(qIdx));
          inp.setAttribute('data-mimir-opt', label);
          if (lbl) {
            lbl.setAttribute('data-mimir-q', String(qIdx));
            lbl.setAttribute('data-mimir-opt', label);
          }
          choices.push({ label, text: choiceText });
        });

        questions.push({
          index: qIdx,
          type: "choice",
          text: promptText,
          choices: choices
        });
        return;
      }

      // 2. Dropdown Question (Google Forms listbox or HTML select)
      const listbox = card.querySelector('[role="listbox"], select');
      if (listbox && visible(listbox)) {
        listbox.setAttribute('data-mimir-q', String(qIdx));
        const choices = [];
        if (listbox.tagName === 'SELECT') {
          Array.from(listbox.options).forEach((opt) => {
            const optVal = (opt.text || opt.value || '').trim();
            if (optVal.length > 0 && !optVal.toLowerCase().includes('choose')) {
              choices.push({ label: String.fromCharCode(65 + choices.length), text: optVal });
            }
          });
        } else {
          // Google Forms [role="listbox"]
          const opts = Array.from(card.querySelectorAll('[role="option"]')).filter(o => {
            const t = (o.getAttribute('data-value') || o.innerText || '').trim();
            return t.length > 0 && !t.toLowerCase().includes('choose');
          });
          opts.forEach((o) => {
            const optText = (o.getAttribute('data-value') || o.innerText || '').trim();
            const label = String.fromCharCode(65 + choices.length);
            o.setAttribute('data-mimir-q', String(qIdx));
            o.setAttribute('data-mimir-opt', label);
            choices.push({ label, text: optText });
          });
        }

        questions.push({
          index: qIdx,
          type: "dropdown",
          text: promptText,
          choices: choices
        });
        return;
      }

      // 3. Short answer or paragraph text input
      const textInput = card.querySelector('input[type="text"], input:not([type]), textarea, [role="textbox"]');
      if (textInput && visible(textInput) && isQuizInput(textInput)) {
        textInput.setAttribute('data-mimir-q', String(qIdx));
        questions.push({
          index: qIdx,
          type: "text",
          text: promptText,
          choices: []
        });
        return;
      }
    });
  }

  // Strategy 2: Fallback if no cards found (flat HTML pages with radio groups)
  if (questions.length === 0) {
    const radioGroups = {};
    const allRadios = Array.from(document.querySelectorAll('input[type="radio"], input[type="checkbox"], [role="radio"], [role="checkbox"]')).filter(isQuizInput);
    allRadios.forEach(r => {
      let groupKey = r.name || r.id || 'default_group';
      if (!radioGroups[groupKey]) radioGroups[groupKey] = [];
      radioGroups[groupKey].push(r);
    });

    Object.keys(radioGroups).forEach(name => {
      const inputs = radioGroups[name];
      if (inputs.length < 2) return;
      let promptText = 'Question ' + (questions.length + 1);
      let cur = inputs[0];
      let walker = cur.parentElement;
      while (walker && walker !== document.body) {
        let prev = walker.previousElementSibling;
        while (prev) {
          if (visible(prev)) {
            const t = text(prev);
            if (t.length > 5 && prev.querySelectorAll('input').length === 0) {
              promptText = t;
              break;
            }
          }
          prev = prev.previousElementSibling;
        }
        if (promptText !== 'Question ' + (questions.length + 1)) break;
        walker = walker.parentElement;
      }

      const qIdx = questions.length;
      const choices = [];
      inputs.forEach((inp, cIdx) => {
        let lbl = inp.closest('label') || (inp.id ? document.querySelector('label[for="' + inp.id + '"]') : null);
        let choiceText = lbl ? text(lbl) : text(inp.parentElement);
        if (!choiceText) choiceText = inp.value || ('Option ' + (cIdx + 1));
        const label = String.fromCharCode(65 + cIdx);
        inp.setAttribute('data-mimir-q', String(qIdx));
        inp.setAttribute('data-mimir-opt', label);
        choices.push({ label, text: choiceText });
      });

      questions.push({
        index: qIdx,
        type: "choice",
        text: promptText,
        choices: choices
      });
    });
  }

  return JSON.stringify({
    questions: questions,
    is_login: isLoginPage && questions.length === 0,
    embedded_quiz_url: embeddedQuizURL
  });
})()
`

// Legacy single-question script kept for backward compatibility
const ExtractJS = ExtractAllJS

// MarkAnswerJS generates JavaScript that fills or clicks the given answer:
// Supports Multiple Choice (click + dispatch), Dropdown (UI label + aria-selected + hidden entry), and Text Inputs (execCommand + value + hidden entry)
func MarkAnswerJS(qIndex int, answer string) string {
	b, _ := json.Marshal(answer)
	return fmt.Sprintf(`
(() => {
  const ans = %s;

  // Helper to sync Google Form hidden entry input (e.g. entry.347167026)
  function syncGoogleFormEntry(container, val) {
    if (!container) return;
    const card = container.closest('.Qr7Oae, [role="listitem"]') || container;
    const cardHTML = card.outerHTML || '';
    const m = cardHTML.match(/entry\.(\d+)/) || cardHTML.match(/\[(\d{8,12})/);
    if (m) {
      const hidden = document.querySelector('input[name="entry.' + (m[1] || m[2]) + '"]');
      if (hidden) {
        hidden.value = val;
        hidden.dispatchEvent(new Event('input', { bubbles: true }));
        hidden.dispatchEvent(new Event('change', { bubbles: true }));
      }
    }
  }

  // 1. Try Multiple Choice match by label: [data-mimir-q="Q"][data-mimir-opt="B"]
  let choiceTarget = document.querySelector('[data-mimir-q="%d"][data-mimir-opt="' + ans + '"]');
  if (!choiceTarget) {
    // Try matching by option text
    const allQOpts = Array.from(document.querySelectorAll('[data-mimir-q="%d"][data-mimir-opt]'));
    choiceTarget = allQOpts.find(el => {
      const t = (el.innerText || el.getAttribute('aria-label') || el.value || '').toLowerCase().trim();
      return t.length > 0 && (t === ans.toLowerCase().trim() || ans.toLowerCase().includes(t));
    });
  }

  if (choiceTarget) {
    const input = choiceTarget.tagName === 'INPUT' ? choiceTarget : (choiceTarget.querySelector('input') || choiceTarget);
    input.scrollIntoView({ block: 'nearest', behavior: 'instant' });
    input.focus();
    input.click();
    if (input.type === 'radio' || input.type === 'checkbox') input.checked = true;
    if (input.getAttribute('role') === 'radio' || input.getAttribute('role') === 'checkbox') input.setAttribute('aria-checked', 'true');
    input.dispatchEvent(new Event('input', { bubbles: true, cancelable: true }));
    input.dispatchEvent(new Event('change', { bubbles: true, cancelable: true }));
    return true;
  }

  // 2. Try Dropdown match (Google Forms listbox or select)
  const listbox = document.querySelector('[role="listbox"][data-mimir-q="%d"], select[data-mimir-q="%d"]');
  if (listbox) {
    if (listbox.tagName === 'SELECT') {
      const matchOpt = Array.from(listbox.options).find(o => 
        o.value === ans || o.text.trim().toLowerCase() === ans.toLowerCase() || (ans.length === 1 && o.text.startsWith(ans))
      );
      if (matchOpt) {
        listbox.value = matchOpt.value;
        listbox.dispatchEvent(new Event('change', { bubbles: true }));
        return true;
      }
    } else {
      // Google Forms [role="listbox"]
      const card = listbox.closest('.Qr7Oae, [role="listitem"]') || listbox.parentElement;
      const opts = Array.from(card ? card.querySelectorAll('[role="option"]') : document.querySelectorAll('[role="option"]'));

      let targetOpt = opts.find(o => o.getAttribute('data-mimir-opt') === ans);
      if (!targetOpt) {
        targetOpt = opts.find(o => {
          const val = (o.getAttribute('data-value') || o.innerText || '').trim().toLowerCase();
          const target = ans.toLowerCase().trim();
          return val.length > 0 && (val === target || target.includes(val) || val.includes(target));
        });
      }

      if (targetOpt) {
        const chosenText = (targetOpt.getAttribute('data-value') || targetOpt.innerText || '').trim();

        // Update aria-selected on options
        opts.forEach(o => {
          o.setAttribute('aria-selected', 'false');
          o.classList.remove('KKjvXb', 'DEh1R');
          o.classList.add('OIC90c');
        });
        targetOpt.setAttribute('aria-selected', 'true');
        targetOpt.classList.add('KKjvXb', 'DEh1R');
        targetOpt.classList.remove('OIC90c');

        // Update visible text
        const displaySpan = listbox.querySelector('[jsname="d9BH4c"] .vRMGwf') || listbox.querySelector('.vRMGwf');
        if (displaySpan && chosenText) {
          displaySpan.innerText = chosenText;
        }

        // Sync hidden entry
        syncGoogleFormEntry(listbox, chosenText);
        return true;
      }
    }
  }

  // 3. Try Text Input / Textarea match: [data-mimir-q="Q"]
  const textTarget = document.querySelector('[data-mimir-q="%d"]');
  if (textTarget) {
    const input = (textTarget.tagName === 'INPUT' || textTarget.tagName === 'TEXTAREA') ? textTarget : textTarget.querySelector('input, textarea');
    if (input) {
      input.scrollIntoView({ block: 'nearest', behavior: 'instant' });
      input.focus();
      input.select && input.select();
      document.execCommand && document.execCommand('selectAll', false, null);
      document.execCommand && document.execCommand('delete', false, null);
      const inserted = document.execCommand && document.execCommand('insertText', false, ans);
      if (!inserted || input.value !== ans) {
        input.value = ans;
      }
      input.setAttribute('data-initial-value', ans);
      input.setAttribute('badinput', 'false');
      input.dispatchEvent(new Event('input', { bubbles: true, cancelable: true }));
      input.dispatchEvent(new Event('change', { bubbles: true, cancelable: true }));
      input.dispatchEvent(new Event('blur', { bubbles: true, cancelable: true }));

      syncGoogleFormEntry(input, ans);
      return true;
    }
  }

  return false;
})()
`, string(b), qIndex, qIndex, qIndex, qIndex, qIndex)
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
