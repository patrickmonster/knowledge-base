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

async function rulesPage(){
  const root=el('div');
  const intro=card('작업 규칙');
  intro.append(el('p','AI가 이 프로젝트에서 커밋하거나 작업할 때 따를 규칙입니다. 작업을 시작할 때(start_task) 규칙이 함께 전달되고, list_rules로 다시 읽을 수 있습니다.','muted'));
  intro.append(el('p','소유자 규칙은 소유자와 관리자가 웹 화면에서만 바꿀 수 있고 모든 참여자에게 적용됩니다. 개별 규칙은 나에게만 보이며 자유롭게 추가·수정할 수 있습니다. 두 규칙이 충돌하면 소유자 규칙을 따릅니다.','small muted'));
  root.append(intro);
  const project=projectList.find(p=>p.id===currentProjectID);
  if(!project){intro.append(el('p','먼저 프로젝트에 참여하고 사이드바에서 현재 프로젝트를 선택해 주세요.','empty'));return root;}
  const data=await tool('list_rules',{project_id:project.id});
  const reload=()=>render('rules');
  root.append(
    ruleSection(project,'소유자 규칙','project',data.project_rules,data.can_manage_project_rules,
      data.can_manage_project_rules?'소유자·관리자로서 이 프로젝트의 규칙을 관리할 수 있습니다.':'소유자 또는 관리자만 바꿀 수 있습니다. 변경이 필요하면 이슈·질의로 요청하세요.',reload),
    ruleSection(project,'개별 규칙','personal',data.personal_rules,true,'나에게만 적용되는 규칙입니다. 다른 참여자에게는 보이지 않습니다.',reload));
  return root;
}

function ruleSection(project,title,scope,rules,canEdit,description,reload){
  const c=card(title+' ('+rules.length+')');
  c.append(el('p',description,'small muted'));
  if(!rules.length)c.append(el('p','등록된 규칙이 없습니다.','empty'));
  for(const r of rules)c.append(ruleRow(project,scope,r,canEdit,reload));
  if(canEdit){
    const details=el('details',undefined,'rule-add'),summary=el('summary','규칙 추가');details.append(summary);
    details.append(ruleForm({category:'work',title:'',body:''},'추가',async d=>{
      await tool('save_rule',{project_id:project.id,scope,...d});notify('규칙을 추가했습니다.');await reload();
    }));
    c.append(details);
  }
  return c;
}

function ruleRow(project,scope,r,canEdit,reload){
  const row=el('div',undefined,'rule-row'),head=el('div',undefined,'rule-head');
  head.append(el('span',ruleCategoryLabels[r.category]||r.category,'pill'),el('strong',r.title));
  row.append(head,el('p',r.body,'rule-body'),el('p','버전 '+r.revision+' · 수정 '+new Date(r.updated_at).toLocaleString(),'small muted'));
  if(canEdit){
    const acts=el('div',undefined,'actions');
    acts.append(btn('수정',async()=>{
      row.replaceChildren(ruleForm(r,'저장',async d=>{
        await tool('save_rule',{project_id:project.id,scope,rule_id:r.id,expected_revision:r.revision,...d});notify('규칙을 저장했습니다.');await reload();
      },reload));
    }),btn('삭제',async()=>{
      if(!confirm('“'+r.title+'” 규칙을 삭제할까요?'))return;
      await tool('delete_rule',{project_id:project.id,rule_id:r.id});notify('규칙을 삭제했습니다.');await reload();
    }));
    row.append(acts);
  }
  return row;
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
