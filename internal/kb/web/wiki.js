'use strict';

// wikiSegments splits Markdown at every heading outside fenced code. Each
// segment runs from its heading (or the start, for the lead) to the next
// heading of any level; level is 0 for the lead.
function wikiSegments(markdown) {
  const segments=[], lines=markdown.split('\n');
  let offset=0, fence=null, current={level:0,title:'',start:0};
  for (const line of lines) {
    const opening=line.match(markdownPatterns.fence);
    if (fence) {
      if (new RegExp('^ {0,3}'+fence[0]+'{'+fence.length+',}\\s*$').test(line)) fence=null;
    } else if (opening) {
      fence=opening[1];
    } else {
      const heading=line.match(markdownPatterns.heading);
      if (heading) {
        if (offset>current.start||current.level) segments.push({...current,end:offset});
        current={level:heading[1].length,title:heading[2].trim(),start:offset};
      }
    }
    offset+=line.length+1;
  }
  segments.push({...current,end:markdown.length});
  return segments.filter(seg=>seg.level||markdown.slice(seg.start,seg.end).trim());
}

// wikiSectionRange is the text a heading's edit covers: the heading and its
// subsections, up to the next heading of the same or a higher level.
function wikiSectionRange(segments, index) {
  const first=segments[index];
  let last=index;
  if (first.level) while (last+1<segments.length&&segments[last+1].level>first.level) last++;
  return {first:index,last,start:first.start,end:segments[last].end};
}

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
  let areaList=null;
  const loadAreas=async()=>areaList??=await tool('list_work_areas',{project_id:projectID}).catch(()=>[]);
  const areaName=id=>areaList?.find(a=>a.id===id)?.name;
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
    const results=card(text?'검색 결과':'위키 글');
    const message=el('p','불러오는 중…','small muted');
    message.setAttribute('role','status');
    results.append(message);
    view.replaceChildren(results);
    const filter={project_id:projectID,kind:'wiki',status:selectedStatus};
    try {
      if (text) {
        const out=await tool('search_knowledge',{...filter,query:text,limit:40});
        if (ticket!==generation) return;
        const list=el('ul',undefined,'wiki-article-list');
        list.append(...out.hits.map(articleRow));
        results.append(list);
        message.textContent=out.hits.length?(out.warning||''):'검색 결과가 없습니다.';
        message.hidden=!message.textContent;
        return;
      }
      // The index groups every article by work area, so load them all (up to 1000).
      const [areas,docs]=await Promise.all([loadAreas(),(async()=>{
        const all=[];
        for (let offset=0;offset<1000;offset+=100) {
          const page=await tool('list_documents',{...filter,sort:'title',order:'asc',limit:100,offset});
          all.push(...page);
          if (page.length<100) break;
        }
        return all;
      })()]);
      if (ticket!==generation) return;
      message.textContent=docs.length?(docs.length>=1000?'글이 많아 앞의 1000개만 표시합니다.':''):'이 상태의 위키 글이 없습니다.';
      message.hidden=!message.textContent;
      const groups=new Map(areas.map(a=>[a.id,{area:a,docs:[]}]));
      const unassigned={area:null,docs:[]};
      for (const doc of docs) (groups.get(doc.area_id)||unassigned).docs.push(doc);
      const shown=[...groups.values(),unassigned].filter(g=>g.docs.length||(g.area&&canEdit));
      if (!docs.length&&!areas.length) return;
      if (shown.length>1) {
        const nav=el('nav',undefined,'wiki-area-nav');
        nav.setAttribute('aria-label','작업 영역');
        shown.forEach((g,i)=>nav.append(btn(`${g.area?.name||'영역 없음'} (${g.docs.length})`,()=>{
          const target=results.querySelector(`[data-area-group="${i}"] h3`);
          target.focus({preventScroll:true});
          target.scrollIntoView?.({block:'start'});
        })));
        results.append(nav);
      }
      shown.forEach((g,i)=>{
        const section=el('section',undefined,'wiki-area');
        section.dataset.areaGroup=String(i);
        const head=el('div',undefined,'wiki-area-head');
        const heading=el('h3',g.area?.name||'영역 없음');
        heading.tabIndex=-1;
        head.append(heading);
        if (g.area) head.append(el('span',`담당 ${g.area.owner_name||'—'}${g.area.owner_active===false?' (확인 필요)':''}`,'small muted'));
        if (canEdit) head.append(btn(g.area?'이 영역에 글 쓰기':'글 쓰기',()=>editArticle(null,g.area?.id||'')));
        section.append(head);
        if (g.area?.description) section.append(el('p',g.area.description,'small muted'));
        if (g.docs.length) {
          const list=el('ul',undefined,'wiki-article-list');
          list.append(...g.docs.map(articleRow));
          section.append(list);
        } else section.append(el('p','아직 글이 없습니다.','small muted'));
        results.append(section);
      });
    } catch (e) { if (ticket===generation) { message.textContent=e.message; message.hidden=false; } }
  }

  async function showArticle(id) {
    const ticket=++generation;
    const [doc]=await Promise.all([tool('get_document',{project_id:projectID,document_id:id}),loadAreas()]);
    if (ticket!==generation) return;
    const article=card(doc.title);
    const actions=el('div',undefined,'actions');
    actions.append(btn('목록으로',showIndex));
    if (canEdit) actions.append(btn('최신 버전 수정',()=>editArticle(doc),''));
    const area=doc.area_id?(areaName(doc.area_id)||'삭제된 영역'):'영역 없음';
    article.append(actions,el('p',`${area} · ${doc.status} · 버전 ${doc.revision}`,'small muted'));
    if (canEdit) article.append(el('p','제목에 마우스를 올리면 그 섹션만 바로 편집할 수 있습니다.','small muted wiki-edit-hint'));

    const content=el('div');
    // showBody renders the article one heading segment at a time so each
    // section can be edited in place. Older revisions are read-only.
    function showBody(markdown,editable=false) {
      const segments=wikiSegments(markdown);
      const body=el('div',undefined,'wiki-body');
      const blocks=segments.map((seg,index)=>{
        const block=el('div',undefined,'wiki-section');
        block.append(renderMarkdown(markdown.slice(seg.start,seg.end),'vector-content',{projectId:projectID}));
        if (editable) {
          const title=seg.level?seg.title:'도입부';
          const edit=btn('편집',()=>editSection(index),'secondary wiki-section-edit');
          edit.setAttribute('aria-label',`'${title}' 섹션 편집`);
          edit.title=`'${title}' 섹션 편집`;
          const heading=seg.level&&block.querySelector('h1,h2,h3,h4,h5,h6');
          if (heading) heading.append(edit);
          else { block.classList.add('wiki-lead'); block.prepend(edit); }
        }
        body.append(block);
        return block;
      });
      let open=null;
      function editSection(index) {
        if (open) open();
        const range=wikiSectionRange(segments,index);
        const original=markdown.slice(range.start,range.end);
        const trailing=original.match(/\s*$/)[0];
        const title=segments[index].level?segments[index].title:'도입부';
        const form=el('form',undefined,'wiki-section-form');
        form.append(el('strong',`'${title}' 섹션 편집`));
        const label=el('label','본문 (Markdown)');
        const text=el('textarea');
        text.name='content';
        text.rows=Math.min(24,Math.max(6,original.split('\n').length+1));
        text.value=original.slice(0,original.length-trailing.length);
        label.append(text);
        form.append(label);
        const reason=field(form,'수정 이유 (비우면 자동)','reason');
        reason.required=false;
        reason.maxLength=1000;
        reason.placeholder=`섹션 '${title}' 수정`;
        const preview=el('div');
        const row=el('div',undefined,'actions');
        row.append(btn('미리보기',()=>preview.replaceChildren(renderMarkdown(text.value,'vector-content',{projectId:projectID}))));
        const save=el('button','섹션 저장');
        save.type='submit';
        row.append(save);
        row.append(btn('취소',()=>close()));
        form.append(row,preview);
        const hidden=blocks.slice(range.first,range.last+1);
        hidden.forEach(b=>b.hidden=true);
        body.insertBefore(form,hidden[0]);
        function close() { form.remove(); hidden.forEach(b=>b.hidden=false); open=null; }
        open=close;
        form.addEventListener('submit',async e=>{
          e.preventDefault();
          save.disabled=true;
          try {
            const value=text.value.replace(/\s+$/,'');
            const replacement=value?value+(trailing||(range.end<markdown.length?'\n':'')):'';
            const next=markdown.slice(0,range.start)+replacement+markdown.slice(range.end);
            if (!next.trim()) throw Error('글 본문을 모두 지울 수는 없습니다. 글 전체 수정이나 보관을 이용해 주세요.');
            if (next===markdown) { close(); return; }
            const ref={project_id:projectID,document_id:doc.id,expected_revision:doc.revision,reason:reason.value.trim()||reason.placeholder};
            // Send only the changed section when it is unique; otherwise the whole body.
            const unique=markdown.indexOf(original)===markdown.lastIndexOf(original);
            const saved=unique&&original.length<=20000&&replacement.length<=20000
              ?await tool('update_document',{...ref,edits:[{old_str:original,new_str:replacement}]})
              :await tool('update_document',{...ref,patch:{content:next}});
            linkDocumentCache.delete(projectID);
            notify(`'${title}' 섹션을 저장했습니다.`);
            await showArticle(saved.id);
          } catch (err) {
            if (String(err.message).includes('REVISION_CONFLICT')) {
              notify('다른 사람이 먼저 이 글을 고쳤습니다. 입력한 내용을 복사해 두고 최신 버전을 불러와 다시 편집해 주세요.',true);
              if (!form.querySelector('.wiki-reload')) row.append(btn('최신 버전 불러오기',()=>showArticle(doc.id),'secondary wiki-reload'));
            } else notify(err.message,true);
          } finally { save.disabled=false; }
        });
        text.focus();
      }
      const headings=[...body.querySelectorAll('h1,h2,h3,h4,h5,h6')];
      content.replaceChildren();
      if (headings.length>1) {
        const nav=el('nav',undefined,'wiki-contents');
        nav.setAttribute('aria-label','글 목차');
        nav.append(el('strong','목차'));
        const list=el('ul');
        for (const heading of headings) {
          const item=el('li');
          const label=[...heading.childNodes].filter(n=>!n.classList?.contains('wiki-section-edit')).map(n=>n.textContent).join('');
          const link=btn(label,()=>{
            heading.focus({preventScroll:true});
            heading.scrollIntoView?.({block:'start'});
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
          if (selected===revision.value&&ticket===generation) showBody(version.content,canEdit&&version.revision===doc.revision);
        } catch (e) { notify(e.message,true); }
      });
    }
    showBody(doc.content,canEdit);
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

  async function editArticle(doc,presetArea='') {
    const ticket=++generation;
    const areas=await loadAreas();
    if (ticket!==generation) return;
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
    const areaSelect=selectField(edit,'작업 영역','area_id',[['','영역 없음'],...areas.map(a=>[a.id,a.name])]);
    areaSelect.value=doc?(doc.area_id||''):presetArea;
    if (doc?.area_id&&areaSelect.value!==doc.area_id) areaSelect.value='';
    if (doc) field(edit,'수정 이유','reason').maxLength=1000;
    const preview=el('div');
    edit.append(btn('미리보기',()=>preview.replaceChildren(renderMarkdown(content.value,'vector-content',{projectId:projectID}))));
    formSubmit(edit,'저장',async data=>{
      const patch={title:data.title.trim(),content:data.content.trim(),status:data.status,area_id:data.area_id};
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
