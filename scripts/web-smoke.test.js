'use strict';
// Smoke tests for the web console (internal/kb/web): every page is opened
// against a mock API (web-fixtures.js) and its buttons are clicked, and the
// test fails on any uncaught error, console error or error notice.
// Run: npm ci --prefix scripts && node --test scripts/
const test=require('node:test');
const assert=require('node:assert');
const {openConsole}=require('./web-harness.js');
const {mockAPI}=require('./web-fixtures.js');

const pages=['projects','search','rules','documents','todos','todosDone','tasks','reviews','issues','attachments','vectors','usage','rechecks','keys','account','overview','users','audit'];

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

test('to-do cards show sub-items and predecessors; the dialog saves only changed relations',async t=>{
  const c=await open(t,'full');
  await show(c,'todos');
  const parent=c.document.querySelector('.todo-card[data-id="todo-1"]'),child=c.document.querySelector('.todo-card[data-id="todo-2"]');
  assert.match(parent.textContent,/하위 0\/1/);
  assert.match(child.querySelector('.todo-parent').textContent,/할 일 todo-1/);
  assert.match(child.textContent,/선행 대기 1/);
  press(c,child);await c.settle();
  const dialog=c.document.querySelector('dialog.todo-dialog[open]');
  assert.strictEqual(dialog.querySelector('select[name="parent_id"]').value,'todo-1');
  const checked=[...dialog.querySelectorAll('.todo-blocker-list input:checked')].length;
  assert.strictEqual(checked,2);
  c.requests.length=0;
  dialog.querySelector('form').dispatchEvent(new c.window.Event('submit',{bubbles:true,cancelable:true}));
  await c.settle();
  let saved=c.requests.find(r=>r.path==='/api/tools/save_todo').body;
  assert.ok(!('parent_id' in saved)&&!('blocked_by' in saved),'unchanged relations are not sent');
  await show(c,'todos');
  press(c,c.document.querySelector('.todo-card[data-id="todo-2"]'));await c.settle();
  const again=c.document.querySelector('dialog.todo-dialog[open]');
  again.querySelector('select[name="parent_id"]').value='';
  const first=again.querySelector('.todo-blocker-list input:checked');first.checked=false;first.dispatchEvent(new c.window.Event('change'));
  c.requests.length=0;
  again.querySelector('form').dispatchEvent(new c.window.Event('submit',{bubbles:true,cancelable:true}));
  await c.settle();
  saved=c.requests.find(r=>r.path==='/api/tools/save_todo').body;
  assert.strictEqual(saved.parent_id,'');
  assert.strictEqual(saved.blocked_by.length,1);
  check(c,'todo relations');
});

test('completed to-dos live on their own page; only in-progress and in-review cards can be ticked',async t=>{
  const c=await open(t,'full');
  await show(c,'todos');
  const heads=[...c.document.querySelectorAll('.todo-col-head .todo-status')].map(n=>n.textContent);
  assert.deepStrictEqual(heads,['시작 전','진행 중','검토대기','아이디어']);
  assert.strictEqual(c.document.querySelector('.todo-card[data-id="todo-3"]'),null,'completed card is not on the board');
  const ticked=[...c.document.querySelectorAll('.todo-card')].filter(n=>n.querySelector('.todo-done')).map(n=>n.dataset.id).sort();
  assert.deepStrictEqual(ticked,['todo-1','todo-5']);
  assert.match(c.document.querySelector('.todo-done-link').textContent,/완료된 할 일 1건/);
  await show(c,'todosDone');
  const titles=[...c.document.querySelectorAll('.todo-done-title')].map(n=>n.textContent);
  assert.deepStrictEqual(titles,['할 일 todo-3']);
  check(c,'todosDone');
});
