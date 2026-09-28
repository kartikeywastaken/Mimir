package extractor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"mimir/internal/von"
)

func TestBrowserFormControls(t *testing.T) {
	endpoint := os.Getenv("MIMIR_TEST_CDP")
	if endpoint == "" {
		t.Skip("set MIMIR_TEST_CDP to an isolated Chromium CDP endpoint")
	}
	client := von.New(endpoint)
	target, err := client.CreateBackgroundTab("about:blank")
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseTarget(target.ID)
	eval := func(js string) string {
		t.Helper()
		result, err := client.Evaluate(target.WebSocketURL, js)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	html := `<fieldset><legend>Match elements to symbols</legend>
 <div role="group" class="EzyPc"><div class="wzWPxe">Potassium</div><div role="checkbox" aria-label="P, response for Potassium" data-answer-value="P" aria-checked="false"></div><div role="checkbox" aria-label="K, response for Potassium" data-answer-value="K" aria-checked="false"></div></div>
 <div role="group" class="EzyPc"><div class="wzWPxe">Sodium</div><div role="checkbox" data-answer-value="P" aria-checked="false"></div><div role="checkbox" data-answer-value="Na" aria-checked="false"></div></div></fieldset>
 <fieldset><legend>Match scientists to fields</legend>
 <div role="radiogroup" aria-label="Newton"><div role="radio" data-value="Physics" aria-checked="false"></div><div role="radio" data-value="Biology" aria-checked="false"></div></div>
 <div role="radiogroup" aria-label="Darwin"><div role="radio" data-value="Physics" aria-checked="false"></div><div role="radio" data-value="Biology" aria-checked="false"></div></div></fieldset>
 <fieldset><legend>What is the capital of France?</legend><select><option disabled>Choose</option><option>Paris</option><option>Natural selection</option></select></fieldset>
 <fieldset><legend>Choose a planet</legend><div id="combo" role="combobox" aria-controls="popup" aria-expanded="false" tabindex="0">Choose</div></fieldset>
 <label>What gas do humans exhale?<input type="text"></label>
 <fieldset><legend>Broken listbox</legend><div role="listbox" id="broken"><div role="option" aria-selected="false">Earth</div><div role="option" aria-selected="false">Mars</div></div></fieldset>
 <style>[role=radio],[role=checkbox]{display:inline-block;width:25px;height:25px} [role=option]{padding:8px}</style>`
	data, _ := json.Marshal(html)
	eval("document.body.innerHTML=" + string(data))
	eval(`document.querySelectorAll('[role=radio], [role=checkbox]').forEach(el=>el.onclick=()=>{
 if(el.getAttribute('role')==='radio')el.parentElement.querySelectorAll('[role=radio]').forEach(e=>e.setAttribute('aria-checked','false'));
 el.setAttribute('aria-checked',String(el.getAttribute('aria-checked')!=='true'));
 });
 const combo=document.getElementById('combo');
 combo.onclick=()=>{if(document.getElementById('popup'))return;combo.setAttribute('aria-expanded','true');setTimeout(()=>{
 const popup=document.createElement('div');popup.id='popup';popup.setAttribute('role','listbox');
 for(const value of ['Earth','Mars']){const opt=document.createElement('div');opt.setAttribute('role','option');opt.textContent=value;opt.onclick=()=>{combo.textContent=value;combo.setAttribute('aria-expanded','false');popup.querySelectorAll('[role=option]').forEach(o=>o.setAttribute('aria-selected',String(o===opt)));popup.style.display='none'};popup.append(opt)}document.body.append(popup)},100)};
 combo.onkeydown=e=>{if(e.key==='Escape'){document.getElementById('popup')?.remove();combo.setAttribute('aria-expanded','false')}};true`)
	batch, err := ParseBatchResult(eval(ExtractAllJS))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Questions) != 8 {
		t.Fatalf("got %d questions: %+v", len(batch.Questions), batch.Questions)
	}
	for i, want := range []string{"Potassium", "Sodium", "Newton", "Darwin"} {
		q := batch.Questions[i]
		if q.Row != want || len(q.Choices) != 2 {
			t.Fatalf("row %d: %+v", i, q)
		}
	}
	for _, index := range []int{0, 1, 2, 3, 4, 5} {
		q := batch.Questions[index]
		values := []string{"B"}
		if index == 4 {
			values = []string{"A"}
		}
		result, err := ParseFillResult(eval(FillAnswerJS(q, values)))
		if err != nil || !result.OK {
			t.Fatalf("fill %s: %+v %v", q.Text, result, err)
		}
	}
	// Each row retains its own selection after filling the next row.
	if actual := eval(`Array.from(document.querySelectorAll('[aria-checked=true]')).length`); actual != "4" {
		t.Fatalf("grid rows overwritten: %s", actual)
	}
	if len(batch.Questions[4].Choices) != 2 || batch.Questions[4].Choices[1].Text != "Natural selection" {
		t.Fatal("valid option containing select was filtered")
	}
	textQ := batch.Questions[6]
	result, _ := ParseFillResult(eval(FillAnswerJS(textQ, []string{"carbon dioxide"})))
	if !result.OK {
		t.Fatal(result)
	}
	broken := batch.Questions[7]
	result, _ = ParseFillResult(eval(FillAnswerJS(broken, []string{"B"})))
	if result.OK {
		t.Fatal("unselected dropdown text reported as success")
	}
	eval(`document.querySelectorAll('[role=checkbox]').forEach(el=>{el.onclick=null;el.setAttribute('aria-checked','false')})`)
	result, _ = ParseFillResult(eval(FillAnswerJS(batch.Questions[0], []string{"B"})))
	if result.OK {
		t.Fatal("inert checkbox reported as success")
	}
	// A new extraction invalidates prior targets, preventing writes after page changes.
	eval(ExtractAllJS)
	result, _ = ParseFillResult(eval(FillAnswerJS(textQ, []string{"stale"})))
	if result.OK {
		t.Fatal("stale target accepted")
	}
	client.PollShortcut()
	eval(`window.dispatchEvent(new KeyboardEvent('keydown',{key:'p',ctrlKey:true,bubbles:true,cancelable:true}));true`)
	if id := client.PollShortcut(); id != target.ID {
		t.Fatalf("shortcut target=%q want %q", id, target.ID)
	}
	if id := client.PollShortcut(); id != "" {
		t.Fatal("shortcut request repeated")
	}
	client.PinTarget(target.ID)
	active, err := client.ActiveTarget()
	if err != nil || active.ID != target.ID {
		t.Fatal("failed to pin shortcut tab")
	}
}

func TestLiveFormExtraction(t *testing.T) {
	endpoint, form := os.Getenv("MIMIR_TEST_CDP"), os.Getenv("MIMIR_TEST_FORM")
	if endpoint == "" || form == "" {
		t.Skip("optional read-only live form extraction")
	}
	c := von.New(endpoint)
	target, err := c.CreateBackgroundTab(form)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseTarget(target.ID)
	time.Sleep(2 * time.Second)
	_, err = c.Evaluate(target.WebSocketURL, `new Promise(resolve=>{let n=0;const timer=setInterval(()=>{if(document.querySelectorAll('.Qr7Oae').length || ++n>50){clearInterval(timer);resolve(true)}},100)})`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Evaluate(target.WebSocketURL, ExtractAllJS)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := ParseBatchResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]bool{}
	for _, q := range batch.Questions {
		if q.Row != "" {
			rows[q.Row] = true
		}
		if strings.Contains(q.Text, "symbol") && q.Row == "Potassium" {
			if len(q.Choices) != 4 || q.Choices[1].Text != "K" {
				t.Fatal(q)
			}
		}
	}
	for _, row := range []string{"Potassium", "Sodium", "Isaac Newton", "Charles Darwin", "Proton", "Transform"} {
		if !rows[row] {
			t.Fatalf("missing live form row %s: %+v", row, batch.Questions)
		}
	}
	t.Log(fmt.Sprintf("Read %d questions/rows from live form without filling or submitting", len(batch.Questions)))
}
