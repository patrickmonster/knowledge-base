'use strict';
// Smoke tests for the web console (internal/kb/web): every page is opened
// against a mock API (web-fixtures.js) and its buttons are clicked, and the
// test fails on any uncaught error, console error or error notice.
// Run: npm ci --prefix scripts && node --test scripts/
const test=require('node:test');
const assert=require('node:assert');
const {openConsole}=require('./web-harness.js');
const {mockAPI}=require('./web-fixtures.js');

const pages=['projects','search','rules','documents','todos','tasks','reviews','issues','attachments','vectors','usage','rechecks','keys','account','overview','users','audit'];

async function open(t,variant){
  const c=await openConsole(mockAPI(variant));
  t.after(()=>c.close());
  assert.deepStrictEqual(c.problems,[],'sign-in');
  assert.strictEqual(c.document.querySelector('#app-view').hidden,false,'console is shown after sign-in');
  return c;
}
async function show(c,page){
  for(const d of c.document.querySelectorAll('dialog[open]'))d.close();
  await c.call('render',page);
  await c.settle();
}
// Things a user can press on the page or in a dialog opened from it.
const clickable=c=>[...c.document.querySelectorAll('#page button, #page summary, #page a:not([href]), #page [role="button"], #page [tabindex="0"], dialog[open] button, dialog[open] summary')].filter(n=>!n.disabled&&!n.closest('[hidden]'));
const check=(c,where)=>assert.deepStrictEqual([...c.problems,...c.faults()],[],where);
const press=(c,n)=>n.dispatchEvent(new c.window.MouseEvent('click',{bubbles:true,cancelable:true}));
const label=n=>(n.textContent.trim()||n.getAttribute('aria-label')||n.title||n.className||n.tagName).slice(0,40);

test('the page list matches the console',async t=>{
  const c=await open(t,'empty');
  assert.deepStrictEqual(Object.keys(c.get('titles')).sort(),[...pages].sort());
});

for(const variant of ['full','empty']){
  test(`every page opens (${variant} data)`,async t=>{
    const c=await open(t,variant);
    for(const page of pages){
      await show(c,page);
      check(c,page);
      assert.ok(c.document.querySelector('#page').children.length>0,page+' rendered nothing');
    }
  });

  test(`every button can be pressed (${variant} data)`,async t=>{
    const c=await open(t,variant);
    let presses=0;
    for(const page of pages){
      await show(c,page);
      const count=clickable(c).length;
      for(let i=0;i<count;i++){
        await show(c,page);
        const target=clickable(c)[i];
        if(!target)continue;
        const name=`${page} › ${label(target)}`;
        press(c,target);presses++;
        await c.settle();
        check(c,name);
        // One level deeper: whatever the press opened (dialog, inline form, sub-page).
        const opened=clickable(c).filter(n=>n.isConnected);
        for(let j=0;j<opened.length;j++){
          await show(c,page);
          const first=clickable(c)[i];
          if(!first)break;
          press(c,first);
          await c.settle();
          const second=clickable(c)[j];
          if(!second||second===first)continue;
          const deeper=`${name} › ${label(second)}`;
          press(c,second);presses++;
          await c.settle();
          check(c,deeper);
        }
      }
    }
    const endpoints=new Set(c.requests.map(r=>r.method+' '+r.path));
    t.diagnostic(`${presses} presses reached ${endpoints.size} API endpoints`);
    if(process.env.SMOKE_DEBUG)console.error(variant,[...endpoints].sort().join('\n'),'\nNOTICES',[...new Set(c.notices)].join(' | '));
  });
}

test('an API failure is reported, not thrown',async t=>{
  const respond=mockAPI('full');
  let failing=false;
  const c=await openConsole(request=>{
    if(failing&&request.path.startsWith('/api/tools/'))throw Object.assign(Error('서버 오류'),{status:500});
    return respond(request);
  });
  t.after(()=>c.close());
  failing=true;
  for(const page of ['documents','todos','tasks','issues']){
    await assert.rejects(c.call('render',page),/서버 오류/,page);
    await c.settle();
    assert.deepStrictEqual(c.problems,[],page);
  }
});

test('a task links to its questions and history document',async t=>{
  const c=await open(t,'full');
  const cards=async()=>{await show(c,'tasks');return[...c.document.querySelectorAll('#page .task-card')];};
  // A card may read its question again for images, so compare distinct IDs.
  const asked=()=>[...new Set(c.requests.filter(r=>r.path==='/api/tools/get_issue').map(r=>r.body.issue_id))];

  // All questions of the open task: the issue page shows only those, then the full list again.
  let [running]=await cards();
  c.requests.length=0;
  press(c,running.querySelector('button.task-issues'));await c.settle();check(c,'task › questions');
  assert.strictEqual(c.get('pageName'),'issues');
  assert.deepStrictEqual(asked().sort(),['issue-1','issue-2','issue-3']);
  assert.match(c.document.querySelector('.issues-summary').textContent,/작업 “작업 run-1”의 질의·이슈 3건/);
  assert.ok(!c.requests.some(r=>r.path==='/api/tools/list_issues'),'focused view does not list every question');
  press(c,c.document.querySelector('.issues-focus-clear'));await c.settle();check(c,'issues › full list');
  assert.ok(c.requests.some(r=>r.path==='/api/tools/list_issues'),'full list loads after clearing the focus');

  // Unresolved questions get their own shortcut; resolved ones do not.
  [running]=await cards();
  const shortcuts=[...running.querySelectorAll('button.task-issue')];
  assert.deepStrictEqual(shortcuts.map(b=>b.textContent),['이슈 · 질의 issue-2 (답변 대기 · 보류)','질의 · 질의 issue-1 (답변됨)']);
  c.requests.length=0;
  press(c,shortcuts[0]);await c.settle();check(c,'task › one question');
  assert.deepStrictEqual(asked(),['issue-2']);

  // The completed task's history opens in the document page.
  await show(c,'tasks');
  const status=c.document.querySelector('#page select[name="status"]');
  status.value='completed';status.dispatchEvent(new c.window.Event('change'));await c.settle();
  const done=[...c.document.querySelectorAll('#page .task-card')].find(n=>n.querySelector('button.task-doc'));
  c.requests.length=0;
  press(c,done.querySelector('button.task-doc'));await c.settle();check(c,'task › history');
  assert.strictEqual(c.get('pageName'),'documents');
  assert.ok(c.requests.some(r=>r.path==='/api/tools/get_document'&&r.body.document_id==='doc-4'),'history document is read');
  assert.ok(c.document.querySelector('dialog.document-dialog[open]'),'history document dialog is open');

  // Opening the page again from the menu shows the plain list.
  c.requests.length=0;
  await show(c,'documents');
  assert.ok(!c.requests.some(r=>r.path==='/api/tools/get_document'),'the focus is used once');
  assert.strictEqual(c.document.querySelector('dialog.document-dialog[open]'),null);
});
