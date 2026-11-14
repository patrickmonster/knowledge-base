'use strict';

const ruleCategoryLabels={commit:'커밋',work:'작업',other:'기타'};
const ruleCategoryOptions=[['commit','커밋 규칙'],['work','작업 규칙'],['other','기타']];

// Turns "커밋: 메시지는 한국어로" style lines into project rules for project creation.
function parseRuleLines(text){
  return text.split('\n').map(line=>line.trim()).filter(Boolean).map(line=>{
    let category='work';
    const m=line.match(/^(커밋|commit|작업|work|기타|other)\s*[:：]\s*(.+)$/i);
    if(m){category={커밋:'commit',commit:'commit',작업:'work',work:'work',기타:'other',other:'other'}[m[1].toLowerCase()];line=m[2].trim();}
    const title=[...line].length>60?[...line].slice(0,60).join('')+'…':line;
    return {category,title,body:line};
  });
}

// Remembers the selected tab (project | personal) while the page re-renders.
let rulesTab='project';

async function rulesPage(){
  const root=el('div');
  const project=projectList.find(p=>p.id===currentProjectID);
  if(!project){const c=card('작업 규칙');c.append(el('p','먼저 프로젝트에 참여하고 사이드바에서 현재 프로젝트를 선택해 주세요.','empty'));root.append(c);return root;}
  const data=await tool('list_rules',{project_id:project.id});
  const reload=()=>render('rules');
  // list_rules returns only the caller's own personal rules.
  const tabs={
    project:{label:'소유자 규칙',rules:data.project_rules,canEdit:data.can_manage_project_rules,
      description:data.can_manage_project_rules?'모든 참여자에게 적용됩니다. 소유자·관리자로서 이 프로젝트의 규칙을 관리할 수 있습니다.':'모든 참여자에게 적용됩니다. 소유자 또는 관리자만 바꿀 수 있으며, 변경이 필요하면 이슈·질의로 요청하세요.'},
    personal:{label:'개별 규칙',rules:data.personal_rules,canEdit:true,
      description:'내가 작성한 규칙만 표시됩니다. 나에게만 적용되며, 소유자 규칙과 충돌하면 소유자 규칙을 따릅니다.'},
  };
  if(!tabs[rulesTab])rulesTab='project';
  const c=el('section',undefined,'card rules-card'),bar=el('div',undefined,'rules-bar'),list=el('div',undefined,'rules-tabs');
  list.setAttribute('role','tablist');list.setAttribute('aria-label','규칙 종류');
  const panel=el('div');panel.setAttribute('role','tabpanel');
  const addSlot=el('div',undefined,'rules-add-slot');
  const buttons={};
  const show=scope=>{
    rulesTab=scope;const t=tabs[scope];
    for(const [k,b] of Object.entries(buttons)){const on=k===scope;b.classList.toggle('active',on);b.setAttribute('aria-selected',String(on));b.tabIndex=on?0:-1;}
    panel.replaceChildren(el('p',t.description,'small muted'));
    if(!t.rules.length)panel.append(el('p','등록된 규칙이 없습니다.','empty'));
    else panel.append(ruleTable(project,scope,t.rules,t.canEdit,c,reload));
    addSlot.replaceChildren();
    if(t.canEdit)addSlot.append(btn('규칙 추가',()=>openRuleDialog(c,t.label+' 추가','추가',{category:'work',title:'',body:''},async d=>{
      await tool('save_rule',{project_id:project.id,scope,...d});notify('규칙을 추가했습니다.');await reload();
    }),'rule-add'));
  };
  for(const [scope,t] of Object.entries(tabs)){
    const b=el('button',t.label+' ('+t.rules.length+')','rules-tab');b.type='button';b.setAttribute('role','tab');
    b.addEventListener('click',()=>show(scope));
    b.addEventListener('keydown',e=>{if(e.key!=='ArrowLeft'&&e.key!=='ArrowRight')return;const keys=Object.keys(tabs),next=keys[(keys.indexOf(scope)+(e.key==='ArrowRight'?1:keys.length-1))%keys.length];show(next);buttons[next].focus();});
    buttons[scope]=b;list.append(b);
  }
  bar.append(list,addSlot);c.append(bar,panel);root.append(c);
  show(rulesTab);
  return root;
}

// Lists rules as a table; 수정·삭제 sit in the rightmost column when the caller can edit.
function ruleTable(project,scope,rules,canEdit,parent,reload){
  const wrap=el('div',undefined,'table-wrap rule-table'),tbl=el('table'),thead=el('thead'),head=el('tr'),body=el('tbody');
  for(const h of ['종류','규칙','버전 · 수정일'])head.append(el('th',h));
  if(canEdit){const th=el('th','관리','rule-actions-col');head.append(th);}
  thead.append(head);
  for(const r of rules)body.append(ruleRow(project,scope,r,canEdit,parent,reload));
  tbl.append(thead,body);wrap.append(tbl);
  return wrap;
}

function ruleRow(project,scope,r,canEdit,parent,reload){
  const row=el('tr'),main=el('td',undefined,'rule-main');
  main.append(el('strong',r.title,'rule-title'),el('p',r.body,'rule-body'));
  const kind=el('td');kind.append(el('span',ruleCategoryLabels[r.category]||r.category,'pill'));
  const meta=el('td',undefined,'small muted rule-meta');
  meta.append(el('div','버전 '+r.revision),el('div',new Date(r.updated_at).toLocaleString()));
  row.append(kind,main,meta);
  if(canEdit){
    const acts=el('td',undefined,'rule-actions');
    acts.append(btn('수정',()=>openRuleDialog(parent,'규칙 수정','저장',r,async d=>{
      await tool('save_rule',{project_id:project.id,scope,rule_id:r.id,expected_revision:r.revision,...d});notify('규칙을 저장했습니다.');await reload();
    })),btn('삭제',async()=>{
      if(!confirm('“'+r.title+'” 규칙을 삭제할까요?'))return;
      await tool('delete_rule',{project_id:project.id,rule_id:r.id});notify('규칙을 삭제했습니다.');await reload();
    },'danger-strong'));
    row.append(acts);
  }
  return row;
}

// Opens the add/edit rule form in a modal dialog; it closes on success, Esc, 취소 or a backdrop click.
function openRuleDialog(parent,heading,submitTitle,rule,onSubmit){
  const dialog=el('dialog',undefined,'rule-dialog');
  const form=ruleForm(rule,submitTitle,async d=>{await onSubmit(d);dialog.close();},()=>dialog.close());
  dialog.append(el('h2',heading),form);
  dialog.addEventListener('close',()=>dialog.remove(),{once:true});
  dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();});
  parent.append(dialog);dialog.showModal();form.querySelector('input[name="title"]')?.focus();
}

function ruleForm(r,submitTitle,onSubmit,onCancel){
  const f=el('form',undefined,'rule-form');
  const category=selectField(f,'종류','category',ruleCategoryOptions);category.value=r.category;
  const title=field(f,'제목','title','text',r.title);title.maxLength=200;
  const label=el('label','내용'),body=el('textarea');body.name='body';body.rows=4;body.required=true;body.maxLength=4000;body.value=r.body;label.append(body);f.append(label);
  formSubmit(f,submitTitle,onSubmit);
  if(onCancel)f.append(btn('취소',onCancel));
  return f;
}
