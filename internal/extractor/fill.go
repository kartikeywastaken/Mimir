package extractor

import (
	"encoding/json"
	"fmt"
)

// FillResult is returned by the page after an attempted answer write. A field is
// only counted as marked when the browser can read the requested value back.
type FillResult struct {
	OK     bool     `json:"ok"`
	Values []string `json:"values,omitempty"`
	Error  string   `json:"error,omitempty"`
}

type fillPayload struct {
	Index    int               `json:"index"`
	TargetID string            `json:"target_id"`
	Type     QuestionType      `json:"type"`
	Values   []string          `json:"values"`
	Choices  map[string]string `json:"choices"`
}

// FillAnswerJS writes one structured answer and verifies the resulting DOM
// state. It never submits or navigates the form.
func FillAnswerJS(q Question, values []string) string {
	choices := make(map[string]string, len(q.Choices))
	for _, choice := range q.Choices {
		choices[choice.Label] = choice.Text
	}
	payload, _ := json.Marshal(fillPayload{
		Index: q.Index, TargetID: q.TargetID, Type: q.Type, Values: values, Choices: choices,
	})

	return fmt.Sprintf(`
(async () => {
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  const visible = el => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
  const request = %s;
  const wanted = Array.isArray(request.values) ? request.values.map(String) : [];
  const wantedSet = new Set(wanted);
  const selector = '[data-mimir-target="' + CSS.escape(request.target_id || ("mimir-q-" + request.index)) + '"]';
  const optionSelector = selector + '[data-mimir-opt]';
  const answerText = value => request.choices[value] || value;
  const fail = error => JSON.stringify({ok:false, error});
  const pass = values => JSON.stringify({ok:true, values});

  function fire(el) {
    el.dispatchEvent(new Event('input', {bubbles:true, cancelable:true}));
    el.dispatchEvent(new Event('change', {bubbles:true, cancelable:true}));
  }

  if (request.type === 'choice' || request.type === 'checkbox') {
    const raw = Array.from(document.querySelectorAll(optionSelector));
    const controls = raw.map(el => el.tagName === 'INPUT' ? el : (el.querySelector('input') || el))
	  .filter(el => el.matches('input, [role="radio"], [role="checkbox"]'))
      .filter((el, i, all) => all.indexOf(el) === i);
    if (!controls.length) return fail('answer controls not found');

    if (!wanted.length || wanted.some(value => !controls.some(el => el.getAttribute('data-mimir-opt') === value))) return fail('unknown answer option');
    if (controls.some(el => el.disabled || el.getAttribute('aria-disabled') === 'true')) return fail('answer controls disabled');
    for (const control of controls) {
      const owner = control.matches('[data-mimir-opt]') ? control : control.closest('[data-mimir-opt]');
      const label = (owner && owner.getAttribute('data-mimir-opt')) || control.getAttribute('data-mimir-opt');
      const shouldSelect = wantedSet.has(label);
      const selected = control.checked === true || control.getAttribute('aria-checked') === 'true';
      if (request.type === 'checkbox') {
        if (selected !== shouldSelect) control.click();
      } else if (shouldSelect && !selected) {
        control.click();
      }
    }

    await pause(200);
    if (controls.some(control => !control.isConnected)) return fail('answer controls replaced during fill');
    const actual = controls.filter(control => control.checked === true || control.getAttribute('aria-checked') === 'true')
      .map(control => {
        const owner = control.matches('[data-mimir-opt]') ? control : control.closest('[data-mimir-opt]');
        return (owner && owner.getAttribute('data-mimir-opt')) || control.getAttribute('data-mimir-opt');
      }).filter(Boolean).sort();
    const expected = [...wanted].sort();
    return JSON.stringify(actual.length === expected.length && actual.every((v, i) => v === expected[i])
      ? {ok:true, values:actual}
      : {ok:false, values:actual, error:'answer readback mismatch'});
  }

  if (request.type === 'dropdown') {
    const listbox = document.querySelector('[role="listbox"]' + selector + ', [role="combobox"]' + selector + ', select' + selector);
    if (!listbox || wanted.length !== 1) return fail('dropdown not found or invalid answer');
    if (listbox.disabled || listbox.getAttribute('aria-disabled') === 'true') return fail('dropdown disabled');
    const wantedLabel = wanted[0];
    const wantedText = answerText(wantedLabel).trim();
    if (listbox.tagName === 'SELECT') {
      const matches = Array.from(listbox.options).filter(o => o.text.trim() === wantedText);
      if (matches.length !== 1) return fail('dropdown option missing or ambiguous');
      const option = matches[0];
      if (!option) return fail('dropdown option not found');
      if (option.disabled) return fail('dropdown option disabled');
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value').set.call(listbox, option.value);
      fire(listbox);
      await pause(200);
      return listbox.isConnected && listbox.value === option.value ? pass([wantedLabel]) : fail('dropdown readback mismatch');
    }
    const optionText = el => (el.getAttribute('data-value') || el.innerText || el.textContent || '').trim();
    const options = () => {
      const ids = (listbox.getAttribute('aria-controls') || listbox.getAttribute('aria-owns') || '').split(/\s+/);
      return [listbox, ...ids.map(id => document.getElementById(id)).filter(Boolean)]
        .flatMap(root => Array.from(root.querySelectorAll('[role="option"]')));
    };
    listbox.scrollIntoView({block:'center'});
    listbox.focus();
    listbox.click();
    let option;
    for (let i=0; i<25; i++) {
      option = options().find(el => visible(el) && el.getAttribute('aria-disabled') !== 'true' && optionText(el) === wantedText);
      if (option) break;
      await pause(100);
    }
    if (!option) return fail('visible dropdown option not found after opening');
    option.click();
    for (let i=0; i<20; i++) {
      await pause(100);
      if (!listbox.isConnected) return fail('dropdown replaced during fill');
      const selected = options().filter(el => el.getAttribute('aria-selected') === 'true');
      // Never accept text from the whole listbox: that includes unselected options.
      const display = listbox.querySelector('[jsname="d9BH4c"]');
      const displayed = display && (display.innerText || '').trim();
      if ((selected.length === 1 && optionText(selected[0]) === wantedText) ||
          (listbox.getAttribute('aria-expanded') !== 'true' && displayed === wantedText) ||
          (listbox.matches('input') && listbox.value === wantedText && listbox.getAttribute('aria-expanded') === 'false')) return pass([wantedLabel]);
    }
    return fail('dropdown readback mismatch');
  }

  if (request.type === 'text' || request.type === 'paragraph') {
    const candidate = document.querySelector(selector);
    const input = candidate && (candidate.matches('input, textarea, [contenteditable="true"]')
      ? candidate : candidate.querySelector('input, textarea, [contenteditable="true"]'));
    if (!input || wanted.length !== 1) return fail('text field not found or invalid answer');
    if (input.disabled || input.readOnly || input.getAttribute('aria-disabled') === 'true') return fail('text field disabled');
    const value = wanted[0];
    if (input.isContentEditable) {
      input.textContent = value;
    } else {
      const proto = input.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      const setter = Object.getOwnPropertyDescriptor(proto, 'value');
      if (setter && setter.set) setter.set.call(input, value); else input.value = value;
    }
    fire(input);
    input.dispatchEvent(new Event('blur', {bubbles:true}));
    await pause(200);
    const actual = input.isContentEditable ? input.textContent : input.value;
    return input.isConnected && actual === value ? pass([actual]) : fail('text readback mismatch');
  }

  return fail('unsupported question type');
})()
`, string(payload))
}

func ParseFillResult(raw string) (FillResult, error) {
	var inner string
	if err := json.Unmarshal([]byte(raw), &inner); err == nil {
		raw = inner
	}
	var result FillResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return FillResult{}, fmt.Errorf("parse fill result: %w", err)
	}
	return result, nil
}

// AdvancePageJS clicks only an explicitly labelled page-navigation control.
// Generic submit controls and final-action labels are always rejected.
const AdvancePageJS = `
(() => {
  const visible = el => {
    if (!el) return false;
    const style = getComputedStyle(el);
    return style.display !== 'none' && style.visibility !== 'hidden' && !el.disabled;
  };
  const text = el => (el.innerText || el.value || el.getAttribute('aria-label') || '').trim().toLowerCase();
  const finalLabels = /^(submit|send|finish|done|complete|turn in)$/i;
  const nextLabels = /^(next|continue|next page|continue to next page)$/i;
  const controls = Array.from(document.querySelectorAll('button, [role="button"], input[type="button"], input[type="submit"]')).filter(visible);
  const submitsForm = el => el.matches('input[type="submit"]') ||
    (el.tagName === 'BUTTON' && el.type === 'submit') || el.hasAttribute('formaction');
  const next = controls.find(el => nextLabels.test(text(el)) && !finalLabels.test(text(el)) && !submitsForm(el));
  if (!next) return JSON.stringify({advanced:false, final:true, reason:'no safe next control'});
  next.click();
  return JSON.stringify({advanced:true, final:false, label:text(next)});
})()
`

type AdvanceResult struct {
	Advanced bool   `json:"advanced"`
	Final    bool   `json:"final"`
	Label    string `json:"label,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func ParseAdvanceResult(raw string) (AdvanceResult, error) {
	var inner string
	if err := json.Unmarshal([]byte(raw), &inner); err == nil {
		raw = inner
	}
	var result AdvanceResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return AdvanceResult{}, fmt.Errorf("parse advance result: %w", err)
	}
	return result, nil
}
