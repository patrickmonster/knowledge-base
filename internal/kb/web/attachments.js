'use strict';

// Preview: what the 첨부 문서 page can show without downloading. The server
// decides again from the file's bytes (GET /api/attachments/{id}/preview), so
// a renamed file is refused there and the dialog falls back to a message.
const attachmentPreviewKinds={png:'image',jpg:'image',jpeg:'image',gif:'image',webp:'image',pdf:'pdf',html:'html',htm:'html',txt:'text',md:'text',csv:'text',tsv:'text',json:'text',log:'text',yaml:'text',yml:'text'};
function attachmentPreviewKind(f){const m=/\.([^.]+)$/.exec(f.filename||'');return m?attachmentPreviewKinds[m[1].toLowerCase()]||'':'';}
function attachmentPreviewURL(f){return `/api/attachments/${encodeURIComponent(f.id)}/preview?project_id=${encodeURIComponent(f.project_id)}`;}
function attachmentHasPreview(f){return f.status==='ready'&&(!!attachmentPreviewKind(f)||(f.text_status==='extracted'&&!!f.text_document_id));}

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
      list.append(table(['파일','크기 / 상태','연결 문서 · 이슈','관리'],items.map(f=>{
        const actions=el('div',undefined,'actions');
        if(attachmentHasPreview(f))actions.append(btn('미리보기',()=>openPreviewDialog(f)));
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
        if(f.text_document_id)name.append(el('p',`본문 문서 ${f.text_document_id}`,'small muted'));
        const text={extracted:f.text_truncated?' · 본문 색인됨(앞부분만)':' · 본문 색인됨',empty:' · 추출할 본문 없음(스캔본은 OCR 필요)',failed:` · 본문 추출 실패(${f.text_error||'오류'})`}[f.text_status]||'';
        return [name,`${(f.size_bytes/1024).toFixed(1)} KB · ${labels[f.status]||f.status}${text}`,f.document_id?`${f.document_id} · v${f.document_revision}`:f.issue_id?`이슈 사진 · ${f.issue_id}`:'프로젝트 첨부',actions];
      })));
      const nav=el('div',undefined,'actions');
      const prev=btn('이전',async()=>{offset=Math.max(0,offset-25);await refresh();});prev.disabled=offset===0;
      const next=btn('다음',async()=>{offset+=25;await refresh();});next.disabled=items.length<25;
      nav.append(prev,next,btn('새로고침',refresh));list.append(nav);
    } catch(e) {if(ticket===generation)list.replaceChildren(listHead(),el('p',e.message,'muted'),btn('다시 불러오기',refresh));}
  }
  // Shows the file in a modal: images inline, text files as plain text, HTML
  // as a page in a sandboxed frame (no script, nothing loaded from elsewhere), and
  // for PDF·Word·Excel the extracted text (PDF also opens the original in a
  // new tab, in the browser's own viewer). Nothing here uses a download link.
  function openPreviewDialog(f) {
    const dialog=el('dialog',undefined,'attachment-dialog attachment-preview'), kind=attachmentPreviewKind(f), url=attachmentPreviewURL(f), projectID=f.project_id;
    const body=el('div',undefined,'attachment-preview-body');
    const actions=el('div',undefined,'actions');
    let open=true;
    const failed=message=>el('p',message+' 다운로드해서 확인해 주세요.','muted');
    if(kind==='html'){
      const a=el('a','새 탭에서 크게 보기','button-link');a.href=url;a.target='_blank';a.rel='noopener';actions.append(a);
    }
    if(kind==='pdf'){
      const a=el('a','원본 PDF 새 탭에서 열기','button-link');a.href=url;a.target='_blank';a.rel='noopener';actions.append(a);
    }
    actions.append(btn('다운로드',async()=>{
      const result=await tool('get_attachment_download',{project_id:projectID,attachment_id:f.id});
      location.assign(result.url);
    }),btn('닫기',()=>dialog.close()));
    if(kind==='image'){
      const img=el('img',undefined,'attachment-preview-image');img.alt=f.filename;
      img.addEventListener('error',()=>img.replaceWith(failed('이미지를 표시하지 못했습니다.')),{once:true});
      img.src=url;body.append(img);
    } else if(kind==='html'){
      // Checked with fetch first so an error shows as a message, not as a
      // blocked frame; the frame then reuses the cached response.
      const status=el('p','불러오는 중…','muted');body.append(status);
      (async()=>{
        try{
          const response=await fetch(url);
          if(!response.ok){let message='미리보기를 불러오지 못했습니다.';try{message=(await response.json()).error?.message||message;}catch{}throw Error(message);}
          await response.arrayBuffer();
          if(!open)return;
          const frame=el('iframe',undefined,'attachment-preview-frame');
          frame.setAttribute('sandbox','');frame.referrerPolicy='no-referrer';frame.title=f.filename+' 미리보기';frame.src=url;
          status.replaceWith(el('p','스크립트는 실행하지 않고 외부 이미지·글꼴·스타일은 불러오지 않습니다. 파일 안에 넣은(data:) 이미지와 인라인 스타일만 표시됩니다.','small muted'),frame);
        }catch(e){if(open)status.replaceWith(failed(e.message));}
      })();
    } else if(kind==='text'){
      const status=el('p','불러오는 중…','muted');body.append(status);
      (async()=>{
        try{
          const response=await fetch(url);
          if(!response.ok){let message='미리보기를 불러오지 못했습니다.';try{message=(await response.json()).error?.message||message;}catch{}throw Error(message);}
          const text=await response.text();
          if(!open)return;
          const pre=el('pre',text,'attachment-preview-text');
          body.replaceChildren(...(response.headers.get('X-Preview-Truncated')?[el('p','파일이 커서 앞부분 1 MiB만 표시합니다.','small muted')]:[]),pre);
        }catch(e){if(open)status.replaceWith(failed(e.message));}
      })();
    }
    if(f.text_status==='extracted'&&f.text_document_id){
      const section=el('div',undefined,'attachment-preview-extracted');
      section.append(el('h3','추출 본문'),el('p','서식·이미지·차트는 빠진 텍스트입니다. 정확한 모양과 수치는 원본으로 확인하세요.'+(f.text_truncated?' 본문이 길어 앞부분만 담겨 있습니다.':''),'small muted'));
      const status=el('p','불러오는 중…','muted');section.append(status);body.append(section);
      tool('get_document',{project_id:projectID,document_id:f.text_document_id}).then(doc=>{
        if(open)status.replaceWith(renderMarkdown(doc.content,'vector-content'));
      }).catch(e=>{if(open)status.textContent='추출 본문을 불러오지 못했습니다: '+e.message;});
    } else if(kind==='pdf'){
      const why={empty:'추출할 본문이 없는 PDF입니다(스캔본일 수 있습니다).',failed:'본문 추출에 실패한 PDF입니다.'}[f.text_status]||'본문이 추출되지 않은 PDF입니다.';
      body.append(el('p',why+' 위의 “원본 PDF 새 탭에서 열기”로 확인해 주세요.','muted'));
    }
    dialog.append(el('h2',f.filename),el('p',`${(f.size_bytes/1024).toFixed(1)} KB · ${f.id}`,'small muted'),actions,body);
    dialog.addEventListener('close',()=>{open=false;dialog.remove();},{once:true});
    dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();});
    root.append(dialog);dialog.showModal();
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
