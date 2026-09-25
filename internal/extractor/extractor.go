package extractor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Question represents what we pulled from the visible tab
type Question struct {
	Text    string   `json:"text"`
	Choices []Choice `json:"choices"`
	RawHTML string   `json:"raw_html,omitempty"`
}

type Choice struct {
	Label string `json:"label"` // A, B, C, D
	Text  string `json:"text"`
}

// JS that runs inside Von's active tab to find the question.
// Heuristic-heavy - covers most quiz/test UIs.
const ExtractJS = `
(() => {
  function visible(el){ const s=getComputedStyle(el); return s.display!=='none' && s.visibility!=='hidden' && el.offsetParent!==null; }
  function text(el){ return (el.innerText||el.textContent||'').trim().replace(/\s+/g,' ').slice(0,1200); }
  // 1) try common selectors
  const qSelectors = [
    '[data-testid*="question"]', '[data-qa*="question"]', '.question', '#question',
    '[role="heading"]', 'h1','h2','h3',
    '[class*="prompt"]','[class*="stem"]'
  ];
  let qEl = null;
  for(const sel of qSelectors){
    const els = Array.from(document.querySelectorAll(sel)).filter(visible);
    // pick largest visible text block that looks like a question
    els.sort((a,b)=> text(b).length - text(a).length);
    for(const e of els){
      const t=text(e);
      if(t.length>20 && (t.includes('?') || t.split(' ').length>6)){
        // ignore choices containers
        if(e.querySelectorAll('input[type="radio"], input[type="checkbox"]').length===0){
          qEl=e; break;
        }
      }
    }
    if(qEl) break;
  }
  // fallback: largest text near radios
  if(!qEl){
    const radios = document.querySelectorAll('input[type="radio"], input[type="checkbox"]');
    if(radios.length>0){
      // walk up from first radio to find heading-like sibling
      let cur=radios[0];
      while(cur && !qEl){
        let prev=cur.previousElementSibling;
        while(prev){ if(visible(prev) && text(prev).length>20){ qEl=prev; break;} prev=prev.previousElementSibling; }
        cur=cur.parentElement;
        if(cur===document.body) break;
      }
    }
  }
  if(!qEl){
    // last resort: biggest h1/h2
    qEl=document.querySelector('h1,h2');
  }
  const bodyText = document.body ? (document.body.innerText || '') : '';
  const questionText = qEl ? text(qEl) : (bodyText.slice(0,800).split('\n').find(l=>l.trim().length>20)||'');

  // choices
  let choices=[];
  const choiceSelectors = 'label, [role="radio"], [role="option"], li, [data-testid*="choice"], [data-testid*="option"], .choice, .option';
  let rawChoices = Array.from(document.querySelectorAll(choiceSelectors)).filter(visible);

  // Filter out choices that contain choices (avoid outer container shells)
  rawChoices = rawChoices.filter(el=>{
    const hasInput = el.querySelector('input[type="radio"], input[type="checkbox"]') !== null;
    const isRole = el.getAttribute('role')==='radio' || el.getAttribute('role')==='option';
    const startsLabel = /^[A-D][\).:-]/.test(text(el));
    if(!hasInput && !isRole && !startsLabel) return false;
    // ensure this is not an ancestor wrapper containing multiple other choice candidates
    const childChoices = el.querySelectorAll(choiceSelectors);
    return childChoices.length <= 1;
  });

  // de-dupe by text
  const seen=new Set();
  rawChoices.forEach((el,i)=>{
    let t=text(el);
    // strip label prefix
    t=t.replace(/^[A-D][\).:-]\s*/,'');
    if(t.length<2 || t.length>400) return;
    if(seen.has(t)) return;
    seen.add(t);
    const label = String.fromCharCode(65 + choices.length);
    choices.push({label, text:t});
  });
  // if still empty, try splitting nearby text by A) B) pattern
  if(choices.length===0 && qEl){
    const sib = qEl.parentElement ? qEl.parentElement.innerText : '';
    const parts = sib.split(/\n|\r/);
    parts.forEach(p=>{
      const m=p.match(/^\s*([A-D])[\).:-]\s*(.+)/);
      if(m) choices.push({label:m[1], text:m[2].trim()});
    });
  }
  return JSON.stringify({text: questionText, choices, raw_html: qEl? qEl.outerHTML.slice(0,1200):''});
})()
`

func ParseResult(jsonStr string) (*Question, error) {
	// CDP returns JSON string inside JSON string - handle both
	var inner string
	// try to unmarshal as string first (double-encoded)
	if err := json.Unmarshal([]byte(jsonStr), &inner); err == nil {
		jsonStr = inner
	}
	var q Question
	if err := json.Unmarshal([]byte(jsonStr), &q); err != nil {
		return nil, fmt.Errorf("parse extractor result: %w | raw: %s", err, jsonStr)
	}
	// normalize labels
	for i := range q.Choices {
		if q.Choices[i].Label == "" {
			q.Choices[i].Label = string(rune('A' + i))
		}
		q.Choices[i].Label = strings.ToUpper(strings.TrimSpace(q.Choices[i].Label))
	}
	return &q, nil
}
