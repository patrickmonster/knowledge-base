'use strict';

const vectorKinds = {brief:['기획','#3d7963'],decision:['결정','#667bc0'],history:['히스토리','#c18a43'],artifact:['작업물','#ae6b96'],reference:['참고자료','#4697a2']};

async function vectorPage() {
  const root=el('div',undefined,'vector-page'),toolbar=card(),controls=el('div',undefined,'vector-controls');
  const projects=await tool('list_projects',{limit:50});
  root.append(el('p','문서 조각이 어떻게 연결되어 있는지 살펴보세요. 참여한 프로젝트의 자료만 표시합니다.','muted'));
  if(!projects.length){root.append(card('먼저 프로젝트에 참여하세요'));return root;}
  const project=selectField(controls,'프로젝트','project',projects.map(p=>[p.id,p.name]));
  const status=selectField(controls,'문서 상태','status',[['active','활성 문서'],['draft','초안'],['archived','보관 문서']]);
  const refresh=btn('새로고침',()=>load(),'secondary');
  const moreProjects=btn('프로젝트 더 불러오기',async()=>{
    const next=await tool('list_projects',{limit:50,offset:projects.length});projects.push(...next);
    for(const p of next){const option=el('option',p.name);option.value=p.id;project.append(option);}moreProjects.hidden=next.length<50;
  });moreProjects.hidden=projects.length<50;
  controls.append(refresh,moreProjects);toolbar.append(controls);root.append(toolbar);
  const metrics=el('div',undefined,'vector-metrics'),body=el('div');root.append(metrics,body);
  let generation=0,selectionGeneration=0,data,selectedID='',visible=[],mapScale=1,view={x:0,y:0,w:1000,h:600};
  let svg,pointsLayer,detail,list,summary,filter,kind,selectionNote, circles=new Map(),neighborIDs=new Set();
  project.addEventListener('change',()=>load().catch(e=>notify(e.message,true)));
  status.addEventListener('change',()=>load().catch(e=>notify(e.message,true)));

  function svgEl(tag,attributes={}){const node=document.createElementNS('http://www.w3.org/2000/svg',tag);for(const [key,value]of Object.entries(attributes))node.setAttribute(key,value);return node;}
  function updateView(){svg.setAttribute('viewBox',`${view.x} ${view.y} ${view.w} ${view.h}`);}
  function zoom(factor,cx=view.x+view.w/2,cy=view.y+view.h/2){
    const w=Math.max(100,Math.min(2000,view.w*factor)),ratio=w/view.w;
    view={x:cx+(view.x-cx)*ratio,y:cy+(view.y-cy)*ratio,w,h:w*.6};updateView();
  }
  function highlight(){for(const [id,circle]of circles){circle.classList.toggle('selected',id===selectedID);circle.classList.toggle('neighbor',neighborIDs.has(id));circle.setAttribute('r',id===selectedID?'10':neighborIDs.has(id)?'7':'5');}}
  function renderList(){
    const focusedID=list.contains(document.activeElement)?document.activeElement.dataset.chunkId:null;
    const query=filter.value.trim().toLocaleLowerCase();
    visible=data.points.filter(p=>(!kind.value||p.kind===kind.value)&&(!query||(p.title+' '+p.excerpt).toLocaleLowerCase().includes(query)));
    for(const p of data.points){const c=circles.get(p.id);c.style.display=visible.includes(p)?'':'none';}
    summary.textContent=`표시 ${visible.length.toLocaleString()}개 / 준비된 조각 ${data.eligible.toLocaleString()}개`;
    list.replaceChildren();let count=0;
    const rows=el('div',undefined,'vector-list-rows');
    const more=btn('조각 더 보기',append);
    function append(){for(const p of visible.slice(count,count+30)){
      const row=btn('',()=>selectPoint(p.id),'vector-list-item');row.setAttribute('aria-label',`${p.title}, 조각 ${p.position+1}`);
      row.append(el('strong',p.title),el('span',`${vectorKinds[p.kind]?.[0]||p.kind} · 조각 ${p.position+1} · v${p.revision}`,'small muted'));
      row.dataset.chunkId=p.id;row.classList.toggle('is-selected',p.id===selectedID);rows.append(row);
    }count+=30;more.hidden=count>=visible.length;}
    list.append(rows,more);append();
    if(focusedID){while(count<visible.length&&!rows.querySelector(`[data-chunk-id="${focusedID}"]`))append();rows.querySelector(`[data-chunk-id="${focusedID}"]`)?.focus({preventScroll:true});}
    if(!visible.length)rows.append(el('p','조건에 맞는 조각이 없습니다.','empty'));
    selectionNote.textContent=selectedID&&!visible.some(p=>p.id===selectedID)?'선택한 조각은 현재 지도 범위 또는 필터 밖에 있습니다.':'';
  }
  async function selectPoint(id){
    const ticket=++selectionGeneration,version=generation,projectID=project.value,docStatus=status.value;
    selectedID=id;neighborIDs.clear();highlight();renderList();detail.replaceChildren(el('p','조각을 불러오는 중…','muted'));
    try{
      const result=await api('/api/vectors/neighbors',{project_id:projectID,chunk_id:id,status:docStatus});
      if(ticket!==selectionGeneration||version!==generation)return;
      const p=result.selected;neighborIDs=new Set(result.neighbors.map(n=>n.id));highlight();
      detail.replaceChildren(el('p','SELECTED CHUNK','eyebrow'),el('h2',p.title));
      detail.append(el('p',`${vectorKinds[p.kind]?.[0]||p.kind} · 조각 ${p.position+1} · 버전 ${p.revision}`,'small muted'),el('p',p.content,'vector-content'));
      detail.append(btn('문서 원문 보기',async()=>{
        const doc=await tool('get_document',{project_id:projectID,document_id:p.document_id,revision:p.revision});
        if(!root.isConnected||!me||version!==generation||ticket!==selectionGeneration)return;
        const dialog=el('dialog',undefined,'vector-document'),close=btn('닫기',()=>dialog.close());
        dialog.append(el('h2',doc.title),el('p',`버전 ${doc.revision}`,'small muted'),el('pre',doc.content,'vector-content'),close);
        dialog.addEventListener('close',()=>dialog.remove(),{once:true});root.append(dialog);dialog.showModal();close.focus();
      }));
      detail.append(el('h3','유사한 조각'),el('p','같은 프로젝트·문서 상태에서 계산한 실제 코사인 유사도입니다. 1에 가까울수록 유사하며, 지도상의 거리와는 다를 수 있습니다.','small muted'));
      if(!result.neighbors.length)detail.append(el('p','비교할 다른 조각이 없습니다.','muted small'));
      for(const n of result.neighbors){
        const row=btn('',()=>selectPoint(n.id),'vector-neighbor');
        row.append(el('strong',n.title),el('span',`조각 ${n.position+1} · 유사도 ${n.similarity.toFixed(3)}`,'small muted'));detail.append(row);
      }
    }catch(e){if(ticket===selectionGeneration&&version===generation)detail.replaceChildren(el('p','조각을 불러오지 못했습니다. 문서가 변경되었거나 접근 권한이 바뀌었을 수 있습니다. 새로고침해 주세요.','muted'),btn('다시 불러오기',()=>load()));}
  }
  async function load(){
    const version=++generation;++selectionGeneration;selectedID='';neighborIDs.clear();refresh.disabled=true;metrics.replaceChildren();body.replaceChildren(el('p','벡터 분포를 계산하는 중…','empty'));
    try{
      const result=await api('/api/vectors/map',{project_id:project.value,status:status.value});
      if(version!==generation)return;data=result;
      const state=data.embedding_status;
      for(const [label,value]of [['전체 조각',state.total],['임베딩 완료',state.ready],['대기·처리 중',state.pending+state.processing],['실패',state.failed]]){
        const metric=card();metric.append(el('p',label,'small muted'),el('strong',value.toLocaleString(),'vector-value'));metrics.append(metric);
      }
      body.replaceChildren(el('p',`위 현황은 프로젝트 전체 기준입니다. ${data.model?'모델: '+data.model:'임베딩 생성이 비활성화되어 있습니다.'}`,'small muted'));
      if(state.failed)body.append(el('p',`실패한 조각 ${state.failed}개가 있습니다. 모델 서버 상태와 재시도 결과를 확인하세요. 오류: ${(state.error_codes||[]).join(', ')}`,'vector-warning'));
      if(!data.points.length){body.append(el('div',data.model?'아직 표시할 벡터가 없습니다. 선택한 상태의 문서가 임베딩을 마치면 새로고침해 주세요.':'임베딩을 활성화하고 모델을 준비하면 벡터 분포를 확인할 수 있습니다.','card empty'));return;}
      const layout=el('div',undefined,'vector-layout'),mapCard=card(),mapHeader=el('div',undefined,'vector-map-header');
      summary=el('strong');mapHeader.append(el('h2','문서 조각의 분포'),summary);mapCard.append(mapHeader);
      const filters=el('div',undefined,'vector-controls');filter=field(filters,'조각 찾기','query','search');filter.required=false;filter.placeholder='제목 또는 발췌문';
      kind=selectField(filters,'문서 종류','kind',[['','모든 종류'],...Object.entries(vectorKinds).map(([key,v])=>[key,v[0]])]);mapCard.append(filters);
      filter.addEventListener('input',renderList);kind.addEventListener('change',renderList);
      if(data.eligible>data.limit)mapCard.append(el('p',`최근 수정한 문서 순으로 최대 ${data.limit}개 조각을 표시합니다. 전체 ${data.eligible}개 중 일부이며, 유사한 조각 목록은 전체 준비된 벡터에서 찾습니다.`,'vector-warning'));
      const legend=el('div',undefined,'vector-legend');for(const [key,[label,color]]of Object.entries(vectorKinds)){const item=el('span',label);item.className='vector-kind-'+key;legend.append(item);}mapCard.append(legend);
      const plot=el('div',undefined,'vector-plot');svg=svgEl('svg',{viewBox:'0 0 1000 600',role:'group','aria-label':'문서 조각의 2차원 벡터 분포. 아래 조각 목록으로도 선택할 수 있습니다.'});
      view={x:0,y:0,w:1000,h:600};circles=new Map();mapScale=Math.max(...data.points.flatMap(p=>[Math.abs(p.x),Math.abs(p.y)]),.000001);
      const axes=svgEl('g',{class:'vector-axes'});axes.append(svgEl('line',{x1:50,y1:300,x2:950,y2:300}),svgEl('line',{x1:500,y1:30,x2:500,y2:570}));svg.append(axes);
      pointsLayer=svgEl('g');svg.append(pointsLayer);
      for(const p of data.points){
        const circle=svgEl('circle',{cx:500+p.x/mapScale*245,cy:300-p.y/mapScale*245,r:5,fill:vectorKinds[p.kind]?.[1]||'#70806f',class:'vector-dot'});
        const title=svgEl('title');title.textContent=`${p.title} · 조각 ${p.position+1}`;circle.append(title);
        circle.addEventListener('click',()=>{if(!dragged)selectPoint(p.id);});circles.set(p.id,circle);pointsLayer.append(circle);
      }
      let drag=null,dragged=false;
      svg.addEventListener('pointerdown',e=>{if(e.button!==0)return;dragged=false;drag={x:e.clientX,y:e.clientY,view:{...view},id:e.pointerId};});
      svg.addEventListener('pointermove',e=>{if(!drag)return;const dx=e.clientX-drag.x,dy=e.clientY-drag.y;if(Math.hypot(dx,dy)>4)dragged=true;if(dragged){svg.setPointerCapture(e.pointerId);const rect=svg.getBoundingClientRect(),scale=Math.max(drag.view.w/rect.width,drag.view.h/rect.height);view={...drag.view,x:drag.view.x-dx*scale,y:drag.view.y-dy*scale};updateView();}});
      for(const event of ['pointerup','pointercancel','lostpointercapture'])svg.addEventListener(event,()=>{drag=null;});
      svg.addEventListener('wheel',e=>{e.preventDefault();zoom(e.deltaY>0?1.15:1/1.15);},{passive:false});
      plot.append(svg);mapCard.append(plot);
      const tools=el('div',undefined,'vector-map-tools');tools.append(btn('확대',()=>zoom(.75)),btn('축소',()=>zoom(1/.75)),btn('처음 위치',()=>{view={x:0,y:0,w:1000,h:600};updateView();}),el('span','점을 선택 · 드래그로 이동 · 휠로 확대','small muted'));mapCard.append(tools);
      mapCard.append(el('p',`1,024차원 벡터를 2차원으로 압축한 PCA 분포입니다. 표현된 분산 ${(data.explained_variance*100).toFixed(1)}%. 가까운 점이 항상 더 유사한 것은 아니며, 같은 위치의 점은 겹칠 수 있습니다. 새로고침이나 문서 상태 변경 시 배치가 달라질 수 있습니다.`,'small muted vector-caption'));
      selectionNote=el('p',undefined,'small muted');mapCard.append(selectionNote);
      const listCard=card('지도에 표시된 조각');listCard.classList.add('vector-list-card');mapCard.classList.add('vector-map-card');list=el('div');listCard.append(el('p','키보드로 목록의 버튼을 선택해도 상세 내용을 볼 수 있습니다.','small muted'),list);
      detail=card();detail.classList.add('vector-detail');detail.append(el('p','CHUNK INSPECTOR','eyebrow'),el('h2','문서 조각을 선택하세요'),el('p','분포도의 점이나 아래 목록을 선택하면 본문과 유사한 조각을 확인할 수 있습니다.','muted'));
      layout.append(mapCard,detail,listCard);body.append(layout);renderList();
    }catch(e){if(version===generation){metrics.replaceChildren();body.replaceChildren(el('p','벡터를 불러오지 못했습니다. 접근 권한과 서버 상태를 확인한 뒤 다시 시도하세요.','card empty'));}throw e;}
    finally{if(version===generation)refresh.disabled=false;}
  }
  await load();return root;
}
