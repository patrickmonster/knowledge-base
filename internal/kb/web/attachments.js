'use strict';

async function attachmentsPage() {
  const root = el('div'), connection = card('버킷 연결 상태');
  const connectionState = el('p','연결 확인 중…');
  connectionState.setAttribute('role','status');
  const connectionHelp = el('p','','small muted'), scannerState = el('p','','small muted'), checkedAt = el('p','','small muted');
  // storageReady gates the upload button; syncUpload is replaced once the list card exists.
  let storageReady=false, uploadBlockedReason='버킷 연결이 확인된 뒤 업로드할 수 있습니다.', syncUpload=()=>{};
  const checkButton=btn('연결 다시 확인',checkStorage);
  connection.append(connectionState,connectionHelp,scannerState,checkedAt,checkButton);
  root.append(connection);
  async function checkStorage() {
    checkButton.disabled=true;
    storageReady=false;syncUpload();
    connectionState.textContent='연결 확인 중…';
    connectionState.className='';
    connectionHelp.textContent='';scannerState.textContent='';checkedAt.textContent='';
    uploadBlockedReason='버킷 연결이 확인된 뒤 업로드할 수 있습니다.';
    try {
      const result=await api('/api/attachments/storage');
      const states={
        connected:['연결 확인됨','서버에서 Railway 버킷에 접근할 수 있습니다. 파일 저장 성공 여부는 업로드 후 목록에서 확인하세요.'],
        not_configured:['버킷 연결 설정 없음','서버에 버킷 연결 정보를 적용한 뒤 앱을 다시 배포해 주세요.'],
        unavailable:['버킷에 접근할 수 없음','버킷 연결 정보와 접근 권한, 네트워크 상태를 확인한 뒤 다시 시도해 주세요.']
      };
      const state=states[result.state];
      if(!state)throw Error('연결 상태를 확인하지 못했습니다.');
      connectionState.textContent=state[0];connectionHelp.textContent=state[1];
      connectionState.className=result.state==='connected'?'pill':'pill error';
      checkedAt.textContent='마지막 확인: '+new Date(result.checked_at).toLocaleString();
      // A configured but unreachable scanner rejects every upload (fail closed).
      const scanners={
        connected:'악성 파일 검사(ClamAV): 연결됨',
        not_configured:'악성 파일 검사: 설정 안 됨(실행 파일 차단만 적용)',
        unavailable:'악성 파일 검사(ClamAV): 응답 없음 — 서비스가 시작 중이거나 중지되어 업로드가 거부됩니다.'
      };
      scannerState.textContent=scanners[result.scanner]??'';
      scannerState.className=result.scanner==='unavailable'?'pill error':'small muted';
      storageReady=result.state==='connected'&&result.scanner!=='unavailable';
      if(result.state==='connected'&&!storageReady)uploadBlockedReason='악성 파일 검사 서비스가 응답한 뒤 업로드할 수 있습니다.';
      syncUpload();
    } catch(e) {
      connectionState.textContent='연결 상태 확인 실패';connectionState.className='pill error';
      connectionHelp.textContent=e.message+' 연결 다시 확인을 눌러 재시도해 주세요.';
    } finally {checkButton.disabled=false;}
  }
  // Render project files independently of a slow or unavailable bucket.
  void checkStorage();
  const projects = [];
  for (let offset=0; offset<=100000; offset+=50) {
    const batch=await tool('list_projects',{limit:50,offset});
    projects.push(...batch); if(batch.length<50)break;
  }
  const list=card('첨부 목록');
  root.append(list);
  if (!projects.length) { list.append(el('p','먼저 프로젝트에 참여해 주세요.','empty')); return root; }
  const project = projectSelect(projects);
  let offset=0, generation=0;
  function currentProject() { return projects.find(p=>p.id===project.value); }
  // The upload button sits at the top right of the list card and opens the upload form in a modal.
  const uploadButton=el('button','업로드');uploadButton.type='button';
  uploadButton.addEventListener('click',openUploadDialog);
  syncUpload=()=>{
    uploadButton.hidden=currentProject().role==='viewer';
    uploadButton.disabled=!storageReady;
    uploadButton.title=storageReady?'':uploadBlockedReason;
  };
  function listHead() { const head=el('div',undefined,'attachments-head');head.append(el('h2','첨부 목록'),uploadButton);return head; }
  async function refresh() {
    const ticket=++generation, projectID=project.value;
    syncUpload();
    list.replaceChildren(listHead(),el('p','불러오는 중…','muted'));
    try {
      const [items,usage]=await Promise.all([tool('list_attachments',{project_id:projectID,limit:25,offset}),tool('get_attachment_usage',{project_id:projectID}).catch(()=>null)]);
      if(ticket!==generation)return;
      const labels={ready:'저장 완료',pending:'업로드 대기',uploading:'전송 중 또는 중단됨'};
      list.replaceChildren(listHead());
      if(usage){const mb=n=>(n/1048576).toFixed(1)+' MB';list.append(el('p',`사용량 ${mb(usage.used_bytes)} / ${usage.quota_bytes?mb(usage.quota_bytes):'제한 없음'} · 악성 파일 검사 ${usage.malware_scanner==='none'?'없음':usage.malware_scanner}${usage.executables_blocked?' · 실행 파일 차단':''}`,'small muted'));}
      if(!items.length)list.append(el('p',offset===0?'이 프로젝트에 등록된 첨부 파일이 없습니다. 파일을 업로드하면 여기에 표시됩니다.':'마지막 페이지입니다. 이전 페이지로 돌아가 주세요.','empty'));
      if(items.length)
      list.append(table(['파일','크기 / 상태','연결 문서','관리'],items.map(f=>{
        const actions=el('div',undefined,'actions');
        if(f.status==='ready')actions.append(btn('다운로드',async()=>{
          const result=await tool('get_attachment_download',{project_id:projectID,attachment_id:f.id});
          location.assign(result.url);
        }));
        if(currentProject().role!=='viewer'&&f.status!=='uploading')actions.append(btn('삭제',async()=>{
          if(!confirm('이 첨부 파일을 삭제할까요?'))return;
          await tool('delete_attachment',{project_id:projectID,attachment_id:f.id});
          if(project.value===projectID)await refresh();
        },'danger'));
        const name=el('div');name.append(el('strong',f.filename),el('p',f.id,'small muted'));
        return [name,`${(f.size_bytes/1024).toFixed(1)} KB · ${labels[f.status]||f.status}`,f.document_id?`${f.document_id} · v${f.document_revision}`:'프로젝트 첨부',actions];
      })));
      const nav=el('div',undefined,'actions');
      const prev=btn('이전',async()=>{offset=Math.max(0,offset-25);await refresh();});prev.disabled=offset===0;
      const next=btn('다음',async()=>{offset+=25;await refresh();});next.disabled=items.length<25;
      nav.append(prev,next,btn('새로고침',refresh));list.append(nav);
    } catch(e) {if(ticket===generation)list.replaceChildren(listHead(),el('p',e.message,'muted'),btn('다시 불러오기',refresh));}
  }
  // Closes on success, Esc, 취소 or a backdrop click, but stays open while a file is being sent.
  function openUploadDialog() {
    const dialog=el('dialog',undefined,'attachment-dialog'), form=el('form');
    const fields=el('fieldset',undefined,'attachment-fields');
    const documentID=field(fields,'연결할 지식 문서 ID (선택)','document_id');
    documentID.required=false;
    const file=field(fields,'첨부 파일 · 최대 25MB','file','file');
    form.append(fields);
    const status=el('p','','muted');status.setAttribute('role','status');
    const cancel=btn('취소',()=>dialog.close());
    let busy=false;
    formSubmit(form,'업로드',async()=>{
      const selected=file.files[0], projectID=project.value;
      if(!selected||selected.size<1||selected.size>25*1024*1024)throw Error('1바이트 이상, 25MB 이하 파일을 선택해 주세요.');
      busy=true;project.disabled=true;fields.disabled=true;cancel.disabled=true;
      try {
        status.textContent='파일을 저장하고 있습니다…';
        const prepared=await tool('prepare_attachment_upload',{project_id:projectID,document_id:documentID.value.trim(),filename:selected.name,size_bytes:selected.size});
        const response=await fetch(prepared.upload_path,{method:'PUT',headers:{'Content-Type':'application/octet-stream','X-CSRF-Token':csrf},body:selected});
        const body=await response.json();
        if(!response.ok)throw Error(body.error?.message||'파일 업로드에 실패했습니다.');
        offset=0;busy=false;dialog.close();notify(selected.name+' 파일을 저장했습니다.');
      } catch(e) {status.textContent='저장을 완료하지 못했습니다. 목록에서 상태를 확인한 뒤 새로 업로드해 주세요.';throw e;}
      finally {busy=false;project.disabled=false;fields.disabled=false;cancel.disabled=false;if(project.value===projectID)await refresh();}
    });
    const actions=el('div',undefined,'actions');actions.append(form.querySelector('button'),cancel);
    form.append(actions,status);
    dialog.append(el('h2','파일 업로드'),el('p',currentProject().name+' 프로젝트에 파일을 추가합니다.','small muted'),form);
    dialog.addEventListener('cancel',e=>{if(busy)e.preventDefault();});
    dialog.addEventListener('close',()=>dialog.remove(),{once:true});
    dialog.addEventListener('click',e=>{if(busy||e.target!==dialog)return;const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();});
    root.append(dialog);dialog.showModal();file.focus();
  }
  project.addEventListener('change',()=>{offset=0;refresh();});
  await refresh();return root;
}
