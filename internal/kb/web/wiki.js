'use strict';

async function wikiPage() {
  const focus=takePageFocus('wiki');
  const project=projectList.find(p=>p.id===currentProjectID);
  const root=el('div',undefined,'wiki-page');
  if (!project) {
    root.append(el('p','먼저 프로젝트에 참여해 주세요.','empty'));
    return root;
  }
  const projectID=project.id;
  const canEdit=['owner','admin','editor'].includes(project.role);
  const controls=card();
  const form=el('form',undefined,'actions wiki-search');
  const query=field(form,'위키 검색','query','search');
  query.required=false;
  query.maxLength=1000;
  const status=selectField(form,'상태','status',[['active','확정된 글'],['draft','검토 대기'],['archived','보관']]);
  formSubmit(form,'검색',()=>showIndex());
  controls.append(form);
  if (canEdit) {
    const actions=el('div',undefined,'actions');
    actions.append(btn('위키 글 쓰기',()=>editArticle(),''),btn('원본 문서 등록',()=>editOriginal()),btn('원본 파일 올리기',()=>render('attachments')));
    controls.append(actions);
  }
  const view=el('div');
  root.append(controls,view);
  let generation=0;

  function articleRow(doc) {
    const row=el('li');
    row.append(btn(doc.title,()=>showArticle(doc.document_id||doc.id)));
    if (doc.excerpt) row.append(el('p',doc.excerpt.slice(0,310),'small muted'));
    return row;
  }

  async function showIndex() {
    const ticket=++generation;
    const text=query.value.trim(), selectedStatus=status.value;
    const list=el('ul',undefined,'wiki-article-list');
    const results=card(text?'검색 결과':'위키 글');
    const message=el('p','불러오는 중…','small muted');
    message.setAttribute('role','status');
    results.append(message,list);
    view.replaceChildren(results);
    let offset=0;
    const more=btn('더 보기',()=>load());
    more.hidden=true;
    results.append(more);
    async function load() {
      const filter={project_id:projectID,kind:'wiki',status:selectedStatus};
      const out=text?await tool('search_knowledge',{...filter,query:text,limit:40}):null;
      const docs=out?out.hits:await tool('list_documents',{...filter,sort:'title',order:'asc',limit:30,offset});
      if (ticket!==generation) return;
      offset+=docs.length;
      list.append(...docs.map(articleRow));
      more.hidden=!!out||docs.length<30;
      message.textContent=offset?(out?.warning||''):(text?'검색 결과가 없습니다.':'이 상태의 위키 글이 없습니다.');
      message.hidden=!message.textContent;
    }
    try { await load(); }
    catch (e) { message.textContent=e.message; }
  }

  async function showArticle(id) {
    const ticket=++generation;
    const doc=await tool('get_document',{project_id:projectID,document_id:id});
    if (ticket!==generation) return;
    const article=card(doc.title);
    const actions=el('div',undefined,'actions');
    actions.append(btn('목록으로',showIndex));
    if (canEdit) actions.append(btn('최신 버전 수정',()=>editArticle(doc),''));
    article.append(actions,el('p',`${doc.status} · 버전 ${doc.revision}`,'small muted'));

    const content=el('div');
    function showBody(markdown) {
      const body=renderMarkdown(markdown,'vector-content',{projectId:projectID});
      const headings=[...body.querySelectorAll('h1,h2,h3,h4,h5,h6')];
      content.replaceChildren();
      if (headings.length>1) {
        const nav=el('nav',undefined,'wiki-contents');
        nav.setAttribute('aria-label','글 목차');
        nav.append(el('strong','목차'));
        const list=el('ul');
        for (const heading of headings) {
          const item=el('li');
          const link=btn(heading.textContent,()=>{
            heading.focus({preventScroll:true});
            heading.scrollIntoView({block:'start'});
          });
          heading.tabIndex=-1;
          item.append(link);
          list.append(item);
        }
        nav.append(list);
        content.append(nav);
      }
      content.append(body);
    }

    if (doc.revision>1) {
      const history=await tool('get_document_history',{project_id:projectID,document_id:id});
      if (ticket!==generation) return;
      const revision=selectField(article,'문서 버전','revision',history.map(h=>[
        String(h.revision),`버전 ${h.revision}${h.revision===doc.revision?' (현재)':''} · ${h.reason||new Date(h.created_at).toLocaleDateString()}`
      ]));
      revision.value=String(doc.revision);
      revision.addEventListener('change',async()=>{
        const selected=revision.value;
        try {
          const version=Number(selected)===doc.revision?doc:await tool('get_document',{project_id:projectID,document_id:id,revision:Number(selected)});
          if (selected===revision.value&&ticket===generation) showBody(version.content);
        } catch (e) { notify(e.message,true); }
      });
    }
    showBody(doc.content);
    article.append(content,linksPanel(doc,canEdit,other=>other.kind==='wiki'?
      showArticle(other.document_id):openOriginal(other.document_id)));
    view.replaceChildren(article);
  }

  async function openOriginal(id) {
    const ref={project_id:projectID,document_id:id};
    const doc=await tool('get_document',ref);
    if (!root.isConnected) return;
    const dialog=el('dialog',undefined,'document-dialog');
    const close=btn('닫기',()=>dialog.close());
    const body=el('div',undefined,'card');
    body.append(el('h2',doc.title),close,el('p',`원본 문서 · 버전 ${doc.revision}`,'small muted'),
      renderMarkdown(doc.content,'vector-content',{projectId:projectID}));
    const files=el('div',undefined,'actions');
    body.append(files);
    if (canEdit) files.append(btn('이 문서에 원본 파일 올리기',()=>{
      dialog.close();
      return openPage('attachments',ref);
    }));
    dialog.append(body);
    dialog.addEventListener('close',()=>dialog.remove(),{once:true});
    root.append(dialog);
    dialog.showModal();
    close.focus();
    try {
      const attachments=await tool('list_attachments',{...ref,limit:100});
      for (const file of attachments) {
        if (file.status!=='ready') continue;
        files.append(btn(`${file.filename} 다운로드`,async()=>{
          const download=await tool('get_attachment_download',{project_id:projectID,attachment_id:file.id});
          location.assign(download.url);
        }));
      }
    } catch (e) { files.append(el('p',e.message,'small muted')); }
  }

  function editOriginal() {
    ++generation;
    const editor=card('원본 문서 등록');
    const form=el('form');
    field(form,'제목','title').maxLength=200;
    const label=el('label','원본 텍스트 또는 첨부할 파일의 설명');
    const content=el('textarea');
    content.name='content';
    content.rows=10;
    content.maxLength=100000;
    content.required=true;
    label.append(content);
    form.append(label);
    formSubmit(form,'원본 문서 저장',async data=>{
      const source=await tool('create_document',{
        project_id:projectID,kind:'brief',title:data.title.trim(),content:data.content, tags:['위키 원본']
      });
      linkDocumentCache.delete(projectID);
      notify('원본 문서를 저장했습니다.');
      await showIndex();
      await openOriginal(source.id);
    });
    form.append(btn('취소',showIndex));
    editor.append(form);
    view.replaceChildren(editor);
    form.elements.title.focus();
  }

  function editArticle(doc) {
    ++generation;
    const editor=card(doc?'위키 글 수정':'위키 글 쓰기');
    const edit=el('form');
    const title=field(edit,'제목','title','text',doc?.title||'');
    title.maxLength=200;
    const label=el('label','본문 (Markdown)');
    const content=el('textarea');
    content.name='content';
    content.rows=16;
    content.maxLength=100000;
    content.required=true;
    content.value=doc?.content||'';
    label.append(content);
    edit.append(label);
    const state=selectField(edit,'상태','status',[['draft','검토 대기'],['active','확정'],['archived','보관']]);
    state.value=doc?.status||'draft';
    if (doc) field(edit,'수정 이유','reason').maxLength=1000;
    const preview=el('div');
    edit.append(btn('미리보기',()=>preview.replaceChildren(renderMarkdown(content.value,'vector-content',{projectId:projectID}))));
    formSubmit(edit,'저장',async data=>{
      const patch={title:data.title.trim(),content:data.content.trim(),status:data.status};
      const saved=doc?await tool('update_document',{
        project_id:projectID,document_id:doc.id,expected_revision:doc.revision,reason:data.reason.trim(),patch
      }):await tool('create_document',{project_id:projectID,kind:'wiki',...patch});
      linkDocumentCache.delete(projectID);
      refreshReviewBadge();
      notify('위키 글을 저장했습니다.');
      query.value='';
      status.value=saved.status;
      await showArticle(saved.id);
    });
    edit.append(btn('취소',()=>doc?showArticle(doc.id):showIndex()));
    editor.append(edit,preview);
    view.replaceChildren(editor);
    title.focus();
  }

  if (focus?.document_id) await showArticle(focus.document_id);
  else await showIndex();
  return root;
}
