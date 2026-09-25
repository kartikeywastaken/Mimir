package overlay

import (
	"fmt"
	"mimir/internal/extractor"
	"mimir/internal/solver"
)

// HTML renders the floating card injected into the page
func HTML(q *extractor.Question, r *solver.Result) string {
	confColor := "#4ade80" // green
	if r.Confidence < 70 {
		confColor = "#facc15"
	}
	if r.Confidence < 50 {
		confColor = "#f87171"
	}
	searchBadge := ""
	if r.SearchUsed {
		searchBadge = `<span style="background:#2563eb;color:#fff;padding:2px 6px;border-radius:999px;font-size:10px;margin-left:8px;vertical-align:middle;">[search] background search</span>`
	}
	choicesHTML := ""
	for _, c := range q.Choices {
		isAns := c.Label == r.Answer
		bg := "#1f1f1f"
		border := "#333"
		if isAns {
			bg = "#14532d"
			border = "#22c55e"
		}
		weight := "400"
		if isAns {
			weight = "700"
		}
		choicesHTML += fmt.Sprintf(
			`<div style="padding:6px 10px;margin:4px 0;background:%s;border:1px solid %s;border-radius:8px;font-weight:%s;">%s) %s %s</div>`,
			bg, border, weight, c.Label, c.Text, map[bool]string{true: " v", false: ""}[isAns],
		)
	}

	return fmt.Sprintf(`
<div style="font-family: ui-sans-serif, system-ui; color:#fff; line-height:1.4;">
  <div style="display:flex; align-items:center; justify-content:space-between; margin-bottom:10px;">
    <div style="font-weight:800; font-size:14px; letter-spacing:0.5px;">! MIMIR %s</div>
    <button onclick="document.getElementById('__mimir_overlay').remove()" style="background:#222;color:#aaa;border:1px solid #333;border-radius:6px;padding:2px 8px;cursor:pointer;">x</button>
  </div>
  <div style="background:#0a0a0a; border:1px solid #222; border-radius:8px; padding:10px; margin-bottom:10px; font-size:12px; color:#ccc; max-height:120px; overflow:auto;">%s</div>
  %s
  <div style="margin-top:12px; padding:10px; background:#111; border-radius:8px; border:1px solid #333;">
    <div style="font-size:12px; color:#999;">SUGGESTED ANSWER</div>
    <div style="font-size:28px; font-weight:900; color:%s; margin:4px 0;">%s <span style="font-size:14px; font-weight:600; color:%s;">%d%% confidence</span></div>
    <div style="font-size:12px; color:#ddd; margin-top:6px;">%s</div>
  </div>
  <div style="margin-top:10px; display:flex; gap:8px;">
    <button onclick="document.getElementById('__mimir_overlay').style.display='none'" style="flex:1; background:#222; color:#fff; border:1px solid #333; border-radius:8px; padding:8px; cursor:pointer; font-weight:600;">Dismiss</button>
    <button id="__mimir_auto" data-answer="%s" onclick="(function(){
      const label='%s';
      let el = document.querySelector('input[type=\'radio\'][value=\''+label+'\'], input[type=\'radio\'][id*=\''+label+'\']');
      if(!el){
        const labels=Array.from(document.querySelectorAll('label'));
        const target = labels.find(l=> l.innerText.trim().startsWith(label+')') || l.innerText.trim().startsWith(label+' .'));
        if(target) el = target.querySelector('input') || target;
      }
      if(el){ el.click(); el.dispatchEvent(new Event('change',{bubbles:true})); }
      const ov=document.getElementById('__mimir_overlay');
      if(ov) ov.style.border='2px solid #22c55e';
    })()" style="flex:1; background:#22c55e; color:#000; border:none; border-radius:8px; padding:8px; cursor:pointer; font-weight:800;">Mark %s</button>
  </div>
  <div style="margin-top:8px; font-size:10px; color:#666; text-align:center;">You choose - Mimir never auto-submits without your click</div>
</div>
`, searchBadge, htmlEscape(q.Text), choicesHTML, confColor, r.Answer, confColor, r.Confidence, htmlEscape(r.Reason), r.Answer, r.Answer, r.Answer)
}

func htmlEscape(s string) string {
	out := ""
	for _, ch := range s {
		switch ch {
		case '<':
			out += "&lt;"
		case '>':
			out += "&gt;"
		case '&':
			out += "&amp;"
		case '"':
			out += "&quot;"
		default:
			out += string(ch)
		}
	}
	return out
}
