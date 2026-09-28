(async () => {
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  const visible = el => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
  const text = el => el ? (el.innerText || el.textContent || '').trim().replace(/\s+/g, ' ') : '';
  const labelled = el => (el.getAttribute('aria-labelledby') || '').split(/\s+/).map(id => text(document.getElementById(id))).filter(Boolean).join(' ') || el.getAttribute('aria-label') || '';
  const choiceSelector = 'input[type="radio"], input[type="checkbox"], [role="radio"], [role="checkbox"]';
  const controlSelector = choiceSelector + ', select, [role="listbox"], [role="combobox"], textarea, input[type="text"], input:not([type]), [role="textbox"]';
  const cards = '.Qr7Oae, .freebirdFormviewerComponentsQuestionBaseRoot, fieldset, .question, .quiz-card, .form-group, [role="listitem"]';
  const usable = el => !el.closest('#commentform, .comment-form, #comments, footer, [id*="cookie"]') && (visible(el) || (el.matches('input') && visible(el.closest('label'))));
  const stamp = (el, index, label) => {
    el.setAttribute('data-mimir-q', index);
    el.setAttribute('data-mimir-target', runID + index);
    if (label) el.setAttribute('data-mimir-opt', label);
  };
  document.querySelectorAll('[data-mimir-q]').forEach(el => {
    ['data-mimir-q','data-mimir-opt','data-mimir-target'].forEach(attr => el.removeAttribute(attr));
  });
  const runID = "mimir-" + (Date.now().toString(36) + Math.random().toString(36).slice(2)) + "-";
  const questions = [];
  const claimed = new Set();
  function prompt(container, control) {
    const heading = container && container.querySelector('[role="heading"], legend, .M7eMe, h1, h2, h3, h4, .prompt, [class*="stem"]');
    const label = control.labels && control.labels[0];
    return (text(heading) || labelled(control) || text(label) || control.placeholder || '').replace(/\s*\d+\s*points?\s*$/i, '').trim();
  }
  function addChoices(inputs, question, row) {
    const index = questions.length;
    const choices = inputs.map((el, i) => {
      const label = String.fromCharCode(65 + i);
      stamp(el, index, label); claimed.add(el);
      const value = el.getAttribute('data-answer-value') || el.getAttribute('data-value') || labelled(el).replace(/, response for .+$/, '') || text(el.labels && el.labels[0]) || text(el.closest('label')) || el.value;
      return {label, text:(value || '').trim()};
    });
    if (!question || choices.some(c => !c.text)) return;
    questions.push({index, target_id:runID + index, type:inputs.some(el => el.type === 'checkbox' || el.getAttribute('role') === 'checkbox') ? 'checkbox' : 'choice', text:row ? question + ' — ' + row : question, context:question, row:row || '', choices});
  }
  // Group each grid row independently; never flatten a matrix into one answer.
  const containers = Array.from(document.querySelectorAll(cards)).filter(visible);
  for (const card of containers) {
    const inputs = Array.from(card.querySelectorAll(choiceSelector)).filter(usable).filter(el => !claimed.has(el) && el.closest(cards) === card);
    if (!inputs.length) continue;
    // Prefer the nearest question card, avoiding outer layout fieldsets.
    if (inputs.every(el => el.closest(cards) !== card)) continue;
    const question = prompt(card, inputs[0]);
    const groups = new Map();
    for (const el of inputs) {
      const row = el.closest('[role="radiogroup"], [role="row"], tr, .EzyPc, [role="group"]');
      const key = row && card.contains(row) ? row : (el.name || card);
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(el);
    }
    for (const [group, controls] of groups) {
      let row = '';
      if (group instanceof Element && (groups.size > 1 || group.matches('tr, [role="row"], .EzyPc'))) {
        row = group.getAttribute('aria-label') || text(group.querySelector('[role="rowheader"], th, .wzWPxe')) || (controls[0].getAttribute('aria-label') || '').split(', response for ')[1] || '';
      }
      addChoices(controls, question, row);
    }
  }
  // Flat groups can coexist with structured cards.
  const flatGroups = new Map();
  for (const el of document.querySelectorAll(choiceSelector)) {
    if (claimed.has(el) || !usable(el)) continue;
    const key = el.closest('[role="radiogroup"], [role="group"]') || el.name || el.parentElement;
    if (!flatGroups.has(key)) flatGroups.set(key, []);
    flatGroups.get(key).push(el);
  }
  for (const [group, inputs] of flatGroups) {
    const container = group instanceof Element ? group : inputs[0].parentElement.parentElement;
    addChoices(inputs, prompt(container, inputs[0]), '');
  }
  const placeholder = value => /^(?:choose|select)(?:\s+(?:an?|the))?(?:\s+(?:option|answer|item))?\s*(?:\.\.\.|…)?$/i.test(value) || /^--.*--$/.test(value);
  for (const control of document.querySelectorAll(controlSelector)) {
    if (claimed.has(control) || !usable(control) || control.matches(choiceSelector)) continue;
    // A popup listbox belongs to its combobox and is not another question.
    if (control.id && Array.from(document.querySelectorAll('[role="combobox"]')).some(el => (el.getAttribute('aria-controls') || el.getAttribute('aria-owns') || '').split(/\s+/).includes(control.id))) continue;
    const card = control.closest(cards);
    const question = prompt(card, control);
    if (!question) continue;
    const index = questions.length;
    stamp(control, index); claimed.add(control);
    if (control.matches('select, [role="listbox"], [role="combobox"]')) {
      let options;
      if (control.tagName === 'SELECT') options = Array.from(control.options).filter(el => !el.disabled).map(el => el.text.trim());
      else {
        const optionsForControl = () => {
          const ids = (control.getAttribute('aria-controls') || control.getAttribute('aria-owns') || '').split(/\s+/);
          const roots = [control, ...ids.map(id => document.getElementById(id)).filter(Boolean)];
          return roots.flatMap(root => Array.from(root.querySelectorAll('[role="option"]')));
        };
        let opts = optionsForControl();
        let opened = false;
        if (!opts.length && !control.disabled && control.getAttribute('aria-disabled') !== 'true') {
          control.click(); opened = true;
          for (let i=0; i<12 && !opts.length; i++) { await pause(100); opts = optionsForControl(); }
        }
        options = opts.filter(el => el.getAttribute('aria-disabled') !== 'true').map(el => el.getAttribute('data-value') || text(el));
        if (opened) {
          control.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape', code:'Escape', bubbles:true}));
          if (control.getAttribute('aria-expanded') === 'true') control.click();
        }
      }
      const choices = [...new Set(options)].filter(value => value && !placeholder(value)).map((value, i) => ({label:String.fromCharCode(65+i), text:value}));
      questions.push({index, target_id:runID+index, type:'dropdown', text:question, context:question, choices});
    } else {
      questions.push({index, target_id:runID+index, type:control.matches('textarea, [aria-multiline="true"]') ? 'paragraph' : 'text', text:question, context:question, choices:[]});
    }
  }
  const frame = Array.from(document.querySelectorAll('iframe')).find(el => /docs.google.com\/forms|forms.office.com|typeform.com/.test(el.src));
  return JSON.stringify({questions, is_login:!!document.querySelector('input[type="password"]') && !questions.length, embedded_quiz_url:frame ? frame.src : ''});
})()
