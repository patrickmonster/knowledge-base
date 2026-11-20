'use strict';
// Smoke tests for the web console (internal/kb/web): every page is opened
// against a mock API (web-fixtures.js) and its buttons are clicked, and the
// test fails on any uncaught error, console error or error notice.
// Run: npm ci --prefix scripts && node --test scripts/
const test=require('node:test');
const assert=require('node:assert');
const {openConsole}=require('./web-harness.js');
const {mockAPI,PROJECT}=require('./web-fixtures.js');

const pages=['projects','search','rules','prompts','documents','todos','todosDone','tasks','reviews','issues','attachments','vectors','usage','rechecks','keys','account','overview','users','audit'];

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

test('task start sources list upstream and related projects apart, by project',async t=>{
  const c=await open(t,'full');
  await show(c,'tasks');
  const card=c.document.querySelector('#page .task-card');
  const start=[...card.querySelectorAll('button')].find(b=>b.textContent==='시작 자료 보기');
  press(c,start);await c.settle();check(c,'task › start sources');
  const dialog=c.document.querySelector('dialog.review-dialog[open]');
  const heads=[...dialog.querySelectorAll('h3')].map(n=>n.textContent);
  assert.ok(heads.includes('상위 프로젝트 자료 1건'),heads.join(' | '));
  assert.ok(heads.includes('관련 프로젝트 자료 2건'),heads.join(' | '));
  const groups=[...dialog.querySelectorAll('.task-project-sources > li')].map(li=>li.textContent);
  assert.deepStrictEqual(groups,['디자인 시스템버튼 규격 (버전 2)','결제 서비스결제 재시도 (버전 2)환불 정책 (버전 5)','빈 프로젝트 · 맞는 자료 없음']);
  assert.match(dialog.textContent,/열람 권한이 없는 프로젝트의 자료 1건은 표시하지 않습니다/);
  assert.match(dialog.textContent,/관련 임베딩 대기 중/);
});

test('completed to-dos live on their own page; only in-progress, in-review and pending-release cards can be ticked',async t=>{
  const c=await open(t,'full');
  await show(c,'todos');
  const heads=[...c.document.querySelectorAll('.todo-col-head .todo-status')].map(n=>n.textContent);
  assert.deepStrictEqual(heads,['시작 전','진행 중','검토대기','반영 대기','아이디어']);
  assert.strictEqual(c.document.querySelector('.todo-card[data-id="todo-3"]'),null,'completed card is not on the board');
  const ticked=[...c.document.querySelectorAll('.todo-card')].filter(n=>n.querySelector('.todo-done')).map(n=>n.dataset.id).sort();
  assert.deepStrictEqual(ticked,['todo-1','todo-5','todo-6','todo-7']);
  assert.match(c.document.querySelector('.todo-done-link').textContent,/완료된 할 일 2건/);
  await show(c,'todosDone');
  const titles=[...c.document.querySelectorAll('.todo-done-title')].map(n=>n.textContent).sort();
  assert.deepStrictEqual(titles,['할 일 todo-3','할 일 todo-8']);
  check(c,'todosDone');
});

test('a completed to-do with an unmerged pull request is flagged on the board and the completed page',async t=>{
  const c=await open(t,'full');
  await show(c,'todos');
  // todo-8's latest PR is deployed, an older one is still open: every PR counts.
  const link=c.document.querySelector('.todo-done-link');
  assert.match(link.textContent,/완료된 할 일 2건 · ⚠ 미병합 PR 1/);
  assert.ok(link.classList.contains('todo-done-mismatch'));
  assert.strictEqual(c.document.querySelector('.todo-card .pr-mismatch'),null,'cards still on the board are not flagged');
  await show(c,'todosDone');
  const row=title=>[...c.document.querySelectorAll('#page tr')].find(r=>r.querySelector('.todo-done-title')?.textContent===title);
  const warn=row('할 일 todo-8').querySelector('.pr-mismatch');
  assert.strictEqual(warn.textContent,'⚠ 미병합 PR 1');
  assert.match(warn.title,/#13 PR 열림/);
  assert.doesNotMatch(warn.title,/#14/);
  assert.strictEqual(row('할 일 todo-3').querySelector('.pr-mismatch'),null,'a deployed PR is not flagged');
  const filter=c.document.querySelector('.todo-mismatch-filter');
  assert.strictEqual(filter.hidden,false);
  assert.match(filter.textContent,/미병합 PR 있는 것만 \(1\)/);
  const box=filter.querySelector('input');box.checked=true;box.dispatchEvent(new c.window.Event('change'));await c.settle();
  assert.deepStrictEqual([...c.document.querySelectorAll('.todo-done-title')].map(n=>n.textContent),['할 일 todo-8']);
  check(c,'todosDone › unmerged PR');
});

test('pending-release cards show their work branch and the dialog edits it',async t=>{
  const c=await open(t,'full');
  await show(c,'todos');
  const branch=id=>c.document.querySelector(`.todo-card[data-id="${id}"] .todo-branch`);
  assert.strictEqual(branch('todo-6').querySelector('code').textContent,'feat/todo-pending-release');
  assert.match(branch('todo-7').textContent,/브랜치 미입력/);
  assert.strictEqual(branch('todo-1'),null,'cards without a branch outside 반영 대기 show nothing');
  c.document.querySelector('.todo-card[data-id="todo-6"]').click();await c.settle();
  const input=c.document.querySelector('.todo-dialog input[name="branch"]');
  assert.strictEqual(input.value,'feat/todo-pending-release');
  check(c,'todos › pending release');
});

test('a closed task can drop its to-do link, an open one changes it',async t=>{
  const c=await open(t,'full');
  await show(c,'tasks');
  const status=c.document.querySelector('#page select[name="status"]');
  status.value='';status.dispatchEvent(new c.window.Event('change'));await c.settle();
  const buttons=[...c.document.querySelectorAll('.task-card .actions button')].map(b=>b.textContent);
  assert.ok(buttons.includes('할 일 변경'),buttons.join(' | '));
  assert.ok(buttons.includes('할 일 연결 해제'),buttons.join(' | '));
  check(c,'tasks › unlink');
});

test('to-dos idle for a while carry a badge and can be shown alone',async t=>{
  const c=await open(t,'full');
  await show(c,'todos');
  const badges=[...c.document.querySelectorAll('.todo-card .todo-stale')].map(n=>[n.closest('.todo-card').dataset.id,n.textContent]);
  assert.deepStrictEqual(badges,[['todo-2','21일 정체']]);
  const label=c.document.querySelector('.todo-stale-filter');
  assert.ok(!label.hidden,'filter shows when some to-dos are stale');
  assert.match(label.textContent,/14일 넘게 멈춘 것만 \(1\)/);
  const box=label.querySelector('input');
  box.checked=true;box.dispatchEvent(new c.window.Event('change'));await c.settle();
  assert.deepStrictEqual([...c.document.querySelectorAll('.todo-card')].map(n=>n.dataset.id),['todo-2']);
  check(c,'todos › stale only');
});

test('the stale filter stays hidden when nothing is stale',async t=>{
  const c=await open(t,'empty');
  await show(c,'todos');
  assert.ok(c.document.querySelector('.todo-stale-filter').hidden);
  assert.strictEqual(c.document.querySelector('.todo-stale'),null);
});

test('upstream page picks the upstream from a table of the caller\'s other projects',async t=>{
  const base=mockAPI('full');
  const mine=[PROJECT,{...PROJECT,id:'project-2',slug:'design',name:'디자인 시안',role:'viewer'},{...PROJECT,id:'project-3',slug:'app',name:'서비스 앱',role:'editor'},{...PROJECT,id:'project-4',slug:'spec',name:'공통 명세',description:'API 규격',role:'admin'}];
  const c=await openConsole(r=>r.path==='/api/tools/list_projects'?(r.body.offset?[]:mine):base(r));
  t.after(()=>c.close());
  await c.call('upstreams',PROJECT);await c.settle();check(c,'upstreams');
  const rows=()=>[...c.document.querySelectorAll('.upstream-candidates tbody tr')];
  assert.deepStrictEqual(rows().map(r=>r.cells[1].textContent),['spec'],'self, linked upstream and downstream are left out');
  assert.strictEqual(rows()[0].cells[2].textContent,'관리자');
  c.window.confirm=()=>true;
  press(c,rows()[0].querySelector('button'));await c.settle();check(c,'upstreams › request');
  const sent=c.requests.filter(r=>r.path==='/api/tools/request_project_upstream').map(r=>r.body);
  assert.deepStrictEqual(sent,[{project_id:PROJECT.id,upstream_project_id:'project-4'}]);
  assert.ok(c.document.querySelector('.upstream-by-id input[name="upstream_project_id"]'),'ID fallback stays available');
});

test('search results mark documents that need a recheck',async t=>{
  const c=await open(t,'full');
  await show(c,'search');
  c.document.querySelector('#page form').dispatchEvent(new c.window.Event('submit',{bubbles:true,cancelable:true}));
  await c.settle();check(c,'search');
  const hits=[...c.document.querySelectorAll('#page .search-hit')];
  assert.strictEqual(hits.filter(n=>n.classList.contains('stale')).length,1,'only the stale hit is marked');
  assert.ok(hits[0].classList.contains('stale'),'the stale hit keeps its rank');
  assert.match(hits[0].querySelector('.pill.stale').title,/120일 동안 갱신되지 않았습니다 \(기준 90일\)/);
  assert.match(c.document.querySelector('#page [role="status"]').textContent,/재확인 필요 1건/);
  // The hit's modal repeats the warning as a sentence.
  press(c,hits[0]);await c.settle();check(c,'search › hit');
  assert.match(c.document.querySelector('dialog.search-dialog[open] .vector-warning').textContent,/재확인 필요 — 120일/);
});

test('the board search box keeps only cards holding every word',async t=>{
  const c=await open(t,'full');
  await show(c,'todos');
  const ids=()=>[...c.document.querySelectorAll('.todo-card')].map(n=>n.dataset.id).sort();
  const before=ids();
  assert.ok(before.length>1,'the board starts with several cards');
  const box=c.document.querySelector('#page .todo-search');
  const type=async value=>{box.value=value;box.dispatchEvent(new c.window.Event('input',{bubbles:true}));await c.settle();};
  c.requests.length=0;
  await type('todo-2');
  assert.deepStrictEqual(ids(),['todo-2'],'only the matching card stays');
  assert.ok(!c.requests.some(r=>r.path==='/api/tools/list_todos'),'filtering reuses the cards already loaded');
  await type('todo-2 없는말');
  assert.deepStrictEqual(ids(),[],'every word must match');
  assert.match(c.document.querySelector('#page .todo-board').textContent,/조건에 맞는 할 일이 없습니다/);
  await type('');
  assert.deepStrictEqual(ids(),before,'clearing the box brings the board back');
  check(c,'todos › search');
});

test('an administrator can change the question score floor from 운영 현황',async t=>{
  const c=await open(t,'full');
  await show(c,'overview');
  const panel=c.document.querySelector('#page .search-settings-card');
  assert.ok(panel,'the search settings card is shown');
  assert.match(panel.textContent,/질의·이슈 결과 점수 하한/);
  assert.match(panel.textContent,/0\.55 · 관리자 설정/);
  const input=panel.querySelector('input[name="issue_score_floor"]');
  assert.strictEqual(input.value,'0.55');
  input.value='0.7';
  c.requests.length=0;
  panel.querySelector('form').dispatchEvent(new c.window.Event('submit',{bubbles:true,cancelable:true}));
  await c.settle();
  const sent=c.requests.filter(r=>r.path==='/api/admin/search-settings'&&r.body);
  assert.deepStrictEqual(sent.map(r=>r.body),[{issue_score_floor:0.7}]);
  check(c,'overview › search settings');
});

test('usage statistics show to-do flow, lead time and backlog per priority',async t=>{
  const c=await open(t,'full');
  await show(c,'usage');check(c,'usage');
  const box=c.document.querySelector('#page .usage-todos');
  assert.ok(box,'the to-do card is shown');
  const kpis=[...box.querySelectorAll('.usage-kpi')].map(n=>n.textContent);
  assert.match(kpis[0],/완료4기간 중 추가 6건/);
  assert.match(kpis[1],/평균 소요2.5일중앙값 2일/);
  const tables=box.querySelectorAll('table');
  assert.strictEqual(tables[0].querySelectorAll('tbody tr').length,2,'one row per week');
  const rows=[...tables[1].querySelectorAll('tbody tr')].map(r=>[...r.cells].map(x=>x.textContent));
  assert.deepStrictEqual(rows.map(r=>r[0]),['높음','보통','낮음']);
  assert.deepStrictEqual(rows[0].slice(-2),['2','12일']);

  // With nothing on the board the lead time is a dash and empty priorities show no age.
  const e=await open(t,'empty');
  await show(e,'usage');check(e,'usage empty');
  const empty=e.document.querySelector('#page .usage-todos');
  assert.match(empty.querySelectorAll('.usage-kpi')[1].textContent,/평균 소요—/);
  assert.deepStrictEqual([...empty.querySelectorAll('table')[1].querySelectorAll('tbody tr')].map(r=>r.cells[6].textContent),['—','—','—']);
});
