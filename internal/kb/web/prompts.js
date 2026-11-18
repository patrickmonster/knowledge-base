'use strict';

// 작업 프롬프트: work instructions the owner or admins save so a member can ask an
// AI for them by name or trigger phrase ("배포 점검해줘"). Every member reads them;
// only an owner or admin in this console changes them.

// "env*: 대상 환경" lines ⇄ [{name:'env',description:'대상 환경',required:true}].
function parsePromptArguments(text){
  return text.split('\n').map(l=>l.trim()).filter(Boolean).map(line=>{
    const m=line.match(/^([^:：]+?)\s*(\*)?\s*(?:[:：]\s*(.*))?$/);
    const name=(m?m[1]:line).trim();
    return {name,description:(m&&m[3]||'').trim(),required:!!(m&&m[2])};
  });
}
function formatPromptArguments(args){
  return (args||[]).map(a=>a.name+(a.required?'*':'')+(a.description?': '+a.description:'')).join('\n');
}
const promptLines=text=>text.split('\n').map(l=>l.trim()).filter(Boolean);

async function promptsPage(){
  const root=el('div');
  const project=projectList.find(p=>p.id===currentProjectID);
  if(!project){const c=card('작업 프롬프트');c.append(el('p','먼저 프로젝트에 참여하고 사이드바에서 현재 프로젝트를 선택해 주세요.','empty'));root.append(c);return root;}
  const data=await tool('list_task_prompts',{project_id:project.id,include_disabled:true,include_body:true});
  const reload=()=>render('prompts');
  const c=el('section',undefined,'card rules-card'),bar=el('div',undefined,'rules-bar'),slot=el('div',undefined,'rules-add-slot');
  const intro=el('div');
  intro.append(el('h2','작업 프롬프트 ('+data.prompts.length+')'),el('p','AI에게 이름이나 호출 문구로 "~해줘"라고 요청하면 AI가 여기 저장된 지시를 가져와 그대로 진행합니다. 본문의 {{인자}} 자리는 요청할 때 받은 값으로 채워집니다. '+(data.can_manage?'소유자·관리자로서 프롬프트를 관리할 수 있습니다.':'소유자 또는 관리자만 바꿀 수 있습니다.')+' 미사용 프롬프트는 AI가 실행하지 않습니다.','small muted'));
  if(data.can_manage)slot.append(btn('프롬프트 추가',()=>openPromptDialog(c,'작업 프롬프트 추가','추가',{name:'',title:'',description:'',triggers:[],body:'',arguments:[]},async d=>{
    await tool('save_task_prompt',{project_id:project.id,...d});notify('작업 프롬프트를 추가했습니다.');await reload();
  }),'rule-add'));
  bar.append(intro,slot);c.append(bar);
  if(!data.prompts.length)c.append(el('p','등록된 작업 프롬프트가 없습니다.','empty'));
  else c.append(promptTable(project,data.prompts,data.can_manage,c,reload));
  root.append(c);
  return root;
}

function promptTable(project,prompts,canEdit,parent,reload){
  const wrap=el('div',undefined,'table-wrap rule-table prompt-table'),tbl=el('table'),thead=el('thead'),head=el('tr'),body=el('tbody');
  for(const h of ['이름','프롬프트','호출 문구','사용','버전 · 수정일'])head.append(el('th',h));
  if(canEdit)head.append(el('th','관리','rule-actions-col'));
  thead.append(head);
  for(const p of prompts)body.append(promptRow(project,p,canEdit,parent,reload));
  tbl.append(thead,body);wrap.append(tbl);
  return wrap;
}

function promptRow(project,p,canEdit,parent,reload){
  const row=el('tr',undefined,p.enabled?'':'rule-off');
  const name=el('td');name.append(el('code',p.name));
  const main=el('td',undefined,'rule-main');
  main.append(el('strong',p.title,'rule-title'));
  if(p.description)main.append(el('p',p.description,'small muted'));
  if(p.arguments&&p.arguments.length)main.append(el('p','인자: '+p.arguments.map(a=>a.name+(a.required?' (필수)':'')).join(', '),'small muted'));
  const details=el('details'),summary=el('summary','본문 보기');details.append(summary,el('p',p.body||'','rule-body'));main.append(details);
  const triggers=el('td');
  if(p.triggers&&p.triggers.length)for(const t of p.triggers)triggers.append(el('span',t,'pill'));
  else triggers.append(el('span','이름으로만','small muted'));
  const use=el('td',undefined,'rule-use');
  use.append(ruleSwitch(p,canEdit,async on=>{
    await tool('set_task_prompt_enabled',{project_id:project.id,prompt_id:p.id,enabled:on});
    notify(on?'작업 프롬프트를 사용합니다.':'작업 프롬프트를 사용하지 않습니다.');await reload();
  }));
  const meta=el('td',undefined,'small muted rule-meta');
  meta.append(el('div','버전 '+p.revision),el('div',new Date(p.updated_at).toLocaleString()));
  row.append(name,main,triggers,use,meta);
  if(canEdit){
    const acts=el('td',undefined,'rule-actions');
    acts.append(btn('수정',()=>openPromptDialog(parent,'작업 프롬프트 수정','저장',p,async d=>{
      await tool('save_task_prompt',{project_id:project.id,prompt_id:p.id,expected_revision:p.revision,...d});notify('작업 프롬프트를 저장했습니다.');await reload();
    })),btn('삭제',async()=>{
      if(!confirm('“'+p.title+'” 작업 프롬프트를 삭제할까요?'))return;
      await tool('delete_task_prompt',{project_id:project.id,prompt_id:p.id});notify('작업 프롬프트를 삭제했습니다.');await reload();
    },'danger-strong'));
    row.append(acts);
  }
  return row;
}

function openPromptDialog(parent,heading,submitTitle,p,onSubmit){
  const dialog=el('dialog',undefined,'rule-dialog');
  const form=promptForm(p,submitTitle,async d=>{await onSubmit(d);dialog.close();},()=>dialog.close());
  dialog.append(el('h2',heading),form);
  dialog.addEventListener('close',()=>dialog.remove(),{once:true});
  dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();});
  parent.append(dialog);dialog.showModal();form.querySelector('input[name="name"]')?.focus();
}

function promptForm(p,submitTitle,onSubmit,onCancel){
  const f=el('form',undefined,'rule-form');
  const name=field(f,'이름 (영문 소문자·숫자·하이픈, 예: deploy-check)','name','text',p.name);name.maxLength=63;name.pattern='[a-z0-9][a-z0-9\\-]{0,62}';
  const title=field(f,'제목','title','text',p.title);title.maxLength=200;
  const description=field(f,'설명 (선택)','description','text',p.description||'');description.required=false;description.maxLength=1000;
  const area=(label,name,rows,value,max)=>{const l=el('label',label),t=el('textarea');t.name=name;t.rows=rows;t.value=value;if(max)t.maxLength=max;l.append(t);f.append(l);return t;};
  area('호출 문구 (한 줄에 하나, 최대 10개 · 예: 배포 점검)','triggers',3,(p.triggers||[]).join('\n'));
  area('인자 (한 줄에 하나 · "이름*: 설명", *는 필수 · 본문에서 {{이름}}으로 사용)','arguments',3,formatPromptArguments(p.arguments));
  const body=area('본문 (AI가 따를 지시)','body',10,p.body||'',20000);body.required=true;
  formSubmit(f,submitTitle,d=>onSubmit({name:d.name.trim(),title:d.title,description:d.description||'',triggers:promptLines(d.triggers||''),arguments:parsePromptArguments(d.arguments||''),body:d.body}));
  if(onCancel)f.append(btn('취소',onCancel));
  return f;
}
