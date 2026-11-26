'use strict';

const vectorKinds = {brief:['기획','#3d7963'],decision:['결정','#667bc0'],history:['히스토리','#c18a43'],artifact:['작업물','#ae6b96'],reference:['참고자료','#4697a2'],glossary:['용어집','#7a8a3c'],wiki:['위키','#527084']};

async function vectorPage() {
  const root=el('div',undefined,'vector-page'),toolbar=card(),controls=el('div',undefined,'vector-controls');
  const projects=[];
  for (let offset=0; offset<=100000; offset+=50) {
    const batch=await tool('list_projects',{limit:50,offset});
    projects.push(...batch); if(batch.length<50)break;
  }
  root.append(el('p','문서 조각이 어떻게 연결되어 있는지 살펴보세요. 참여한 프로젝트의 자료만 표시합니다.','muted'));
  if(!projects.length){root.append(card('먼저 프로젝트에 참여하세요'));return root;}
  const project=projectSelect(projects);
  const status=selectField(controls,'문서 상태','status',[['active','활성 문서'],['draft','초안'],['archived','보관 문서']]);
  const refresh=btn('새로고침',()=>load(),'secondary');
  controls.append(refresh);toolbar.append(controls);root.append(toolbar);
  const metrics=el('div',undefined,'vector-metrics'),body=el('div');root.append(metrics,body);
  let generation=0,selectionGeneration=0,data,selectedID='',visible=[],mapScale=1,view={x:0,y:0,w:1000,h:600};
  let svg,pointsLayer,detail,list,summary,filter,kind,selectionNote, circles=new Map(),neighborIDs=new Set();
  // 3D view: PCA components 1-3, rotated in the browser. mode survives refreshes.
  const homeRotation={yaw:-.65,pitch:.32};
  let mode='3d',rotation={...homeRotation},depthZoom=1,scale3=1,frame=0,spinning=false;
  let axes2d,frame3d,cubeEdges=[],axisLines=[],axisLabels=[],hint,caption,spinButton,modeButtons=[];
  project.addEventListener('change',()=>load().catch(e=>notify(e.message,true)));
  status.addEventListener('change',()=>load().catch(e=>notify(e.message,true)));

  function svgEl(tag,attributes={}){const node=document.createElementNS('http://www.w3.org/2000/svg',tag);for(const [key,value]of Object.entries(attributes))node.setAttribute(key,value);return node;}
  function updateView(){svg.setAttribute('viewBox',mode==='3d'?'0 0 1000 600':`${view.x} ${view.y} ${view.w} ${view.h}`);}
  function zoom(factor,cx=view.x+view.w/2,cy=view.y+view.h/2){
    if(mode==='3d'){depthZoom=Math.max(.4,Math.min(6,depthZoom/factor));schedule();return;}
    const w=Math.max(100,Math.min(2000,view.w*factor)),ratio=w/view.w;
    view={x:cx+(view.x-cx)*ratio,y:cy+(view.y-cy)*ratio,w,h:w*.6};updateView();
  }
  // Rotate around the vertical axis (yaw), then tilt (pitch), then apply a weak
  // perspective so nearer points are larger. Coordinates are scaled so the
  // largest absolute component is 1; relative axis lengths stay truthful.
  function project3(x,y,z){
    const cy=Math.cos(rotation.yaw),sy=Math.sin(rotation.yaw),cp=Math.cos(rotation.pitch),sp=Math.sin(rotation.pitch);
    const x1=x*cy+z*sy,z1=-x*sy+z*cy,y2=y*cp-z1*sp,z2=y*sp+z1*cp,perspective=3.2/(3.2-z2),k=perspective*depthZoom*180;
    return {sx:500+x1*k,sy:300-y2*k,perspective,z:z2};
  }
  function setLine(line,a,b){line.setAttribute('x1',a.sx.toFixed(1));line.setAttribute('y1',a.sy.toFixed(1));line.setAttribute('x2',b.sx.toFixed(1));line.setAttribute('y2',b.sy.toFixed(1));}
  function render(){
    frame=0;if(!data||!svg)return;
    const flat=mode==='2d',order=[];
    for(const p of data.points){
      const circle=circles.get(p.id),selected=p.id===selectedID,neighbor=neighborIDs.has(p.id),base=selected?10:neighbor?7:5;
      circle.classList.toggle('selected',selected);circle.classList.toggle('neighbor',neighbor);
      let depth=0,shade=1;
      if(flat){circle.setAttribute('cx',(500+p.x/mapScale*245).toFixed(1));circle.setAttribute('cy',(300-p.y/mapScale*245).toFixed(1));circle.setAttribute('r',base);}
      else{
        const q=project3(p.x/scale3,p.y/scale3,(p.z||0)/scale3);depth=q.z;shade=Math.max(.35,Math.min(1,.62+q.z*.24));
        circle.setAttribute('cx',q.sx.toFixed(1));circle.setAttribute('cy',q.sy.toFixed(1));circle.setAttribute('r',(base*q.perspective).toFixed(2));
      }
      circle.style.setProperty('--depth',shade.toFixed(2));
      // Painter's order: far points first; the selection and its neighbors stay on top.
      order.push([depth+(selected?100:neighbor?50:0),circle]);
    }
    order.sort((a,b)=>a[0]-b[0]);for(const [,circle]of order)pointsLayer.append(circle);
    axes2d.style.display=flat?'':'none';frame3d.style.display=flat?'none':'';
    if(!flat){
      for(const [line,a,b]of cubeEdges)setLine(line,project3(...a),project3(...b));
      axisLines.forEach((line,i)=>{const a=[0,0,0],b=[0,0,0];a[i]=-1.12;b[i]=1.12;setLine(line,project3(...a),project3(...b));});
      axisLabels.forEach((label,i)=>{const at=[0,0,0];at[i]=1.24;const q=project3(...at);label.setAttribute('x',q.sx.toFixed(1));label.setAttribute('y',q.sy.toFixed(1));});
    }
  }
  function schedule(){if(!frame)frame=requestAnimationFrame(render);}
  function highlight(){render();}
  function stopSpin(){spinning=false;spinButton?.setAttribute('aria-pressed','false');}
  function spin(version){
    if(!spinning||version!==generation||mode!=='3d'||!svg?.isConnected){stopSpin();return;}
    rotation.yaw+=.004;render();requestAnimationFrame(()=>spin(version));
  }
  function describe(){
    const shares=data.component_variance||[data.explained_variance,0,0],pct=v=>(v*100).toFixed(1)+'%';
    if(mode==='3d'){
      caption.textContent=`1,024차원 벡터를 3차원으로 압축한 PCA 분포입니다. 표현된 분산 ${pct(shares[0]+shares[1]+shares[2])} (PC1 ${pct(shares[0])} · PC2 ${pct(shares[1])} · PC3 ${pct(shares[2])}). 축 길이는 각 성분의 실제 퍼짐을 그대로 반영하며, 회전하면 2D에서 겹쳐 보이던 점이 분리될 수 있습니다. 가까운 점이 항상 더 유사한 것은 아니며, 새로고침이나 문서 상태 변경 시 배치가 달라질 수 있습니다.`;
      hint.textContent='점을 선택 · 드래그로 회전 · 휠로 확대 · 방향키로 회전';
      svg.setAttribute('aria-label','문서 조각의 3차원 벡터 분포. 방향키로 회전할 수 있으며, 아래 조각 목록으로도 선택할 수 있습니다.');
    }else{
      caption.textContent=`1,024차원 벡터를 2차원으로 압축한 PCA 분포입니다. 표현된 분산 ${pct(shares[0]+shares[1])}. 가까운 점이 항상 더 유사한 것은 아니며, 같은 위치의 점은 겹칠 수 있습니다. 새로고침이나 문서 상태 변경 시 배치가 달라질 수 있습니다.`;
      hint.textContent='점을 선택 · 드래그로 이동 · 휠로 확대';
      svg.setAttribute('aria-label','문서 조각의 2차원 벡터 분포. 아래 조각 목록으로도 선택할 수 있습니다.');
    }
    for(const button of modeButtons)button.setAttribute('aria-pressed',String(button.dataset.mode===mode));
    spinButton.hidden=mode!=='3d';
  }
  function setMode(next){mode=next;stopSpin();svg.classList.toggle('is-3d',mode==='3d');updateView();describe();render();}
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
      detail.append(el('p',`${vectorKinds[p.kind]?.[0]||p.kind} · 조각 ${p.position+1} · 버전 ${p.revision}`,'small muted'),renderMarkdown(p.content,'vector-content',{projectId:projectID}));
      detail.append(btn('문서 원문 보기',async()=>{
        const doc=await tool('get_document',{project_id:projectID,document_id:p.document_id,revision:p.revision});
        if(!root.isConnected||!me||version!==generation||ticket!==selectionGeneration)return;
        const dialog=el('dialog',undefined,'vector-document'),close=btn('닫기',()=>dialog.close());
        dialog.append(el('h2',doc.title),el('p',`버전 ${doc.revision}`,'small muted'),renderMarkdown(doc.content,'vector-content',{projectId:doc.project_id}),close);
        dialog.addEventListener('close',()=>dialog.remove(),{once:true});dialog.addEventListener('click',e=>{if(e.target!==dialog)return;const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();});root.append(dialog);dialog.showModal();close.focus();
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
      const filters=el('div',undefined,'vector-controls vector-filters');
      kind=selectField(filters,'문서 종류','kind',[['','모든 종류'],...Object.entries(vectorKinds).map(([key,v])=>[key,v[0]])]);kind.parentElement.classList.add('vector-kind-filter');
      filter=field(filters,'조각 찾기','query','search');filter.required=false;filter.placeholder='제목 또는 발췌문';mapCard.append(filters);
      filter.addEventListener('input',renderList);kind.addEventListener('change',renderList);
      if(data.eligible>data.limit)mapCard.append(el('p',`최근 수정한 문서 순으로 최대 ${data.limit}개 조각을 표시합니다. 전체 ${data.eligible}개 중 일부이며, 유사한 조각 목록은 전체 준비된 벡터에서 찾습니다.`,'vector-warning'));
      const legend=el('div',undefined,'vector-legend');for(const [key,[label,color]]of Object.entries(vectorKinds)){const item=el('span',label);item.className='vector-kind-'+key;legend.append(item);}mapCard.append(legend);
      const plot=el('div',undefined,'vector-plot');svg=svgEl('svg',{viewBox:'0 0 1000 600',role:'group',tabindex:'0'});
      view={x:0,y:0,w:1000,h:600};circles=new Map();mapScale=Math.max(...data.points.flatMap(p=>[Math.abs(p.x),Math.abs(p.y)]),.000001);
      scale3=Math.max(...data.points.flatMap(p=>[Math.abs(p.x),Math.abs(p.y),Math.abs(p.z||0)]),.000001);
      axes2d=svgEl('g',{class:'vector-axes'});axes2d.append(svgEl('line',{x1:50,y1:300,x2:950,y2:300}),svgEl('line',{x1:500,y1:30,x2:500,y2:570}));svg.append(axes2d);
      // The 3D frame is the bounding cube of the scaled sample plus the three principal axes.
      frame3d=svgEl('g',{class:'vector-frame'});cubeEdges=[];axisLines=[];axisLabels=[];
      const corners=[];for(const x of [-1,1])for(const y of [-1,1])for(const z of [-1,1])corners.push([x,y,z]);
      for(let i=0;i<corners.length;i++)for(let j=i+1;j<corners.length;j++)if(corners[i].filter((v,k)=>v!==corners[j][k]).length===1){const line=svgEl('line',{class:'vector-cube'});cubeEdges.push([line,corners[i],corners[j]]);frame3d.append(line);}
      ['PC1','PC2','PC3'].forEach((name,i)=>{const line=svgEl('line',{class:'vector-axis vector-axis-'+(i+1)}),label=svgEl('text',{class:'vector-axis-label','text-anchor':'middle','dominant-baseline':'middle'});label.textContent=name;axisLines.push(line);axisLabels.push(label);frame3d.append(line,label);});
      svg.append(frame3d);
      pointsLayer=svgEl('g');svg.append(pointsLayer);
      for(const p of data.points){
        const circle=svgEl('circle',{r:5,fill:vectorKinds[p.kind]?.[1]||'#70806f',class:'vector-dot'});
        const title=svgEl('title');title.textContent=`${p.title} · 조각 ${p.position+1}`;circle.append(title);
        circle.addEventListener('click',()=>{if(!dragged)selectPoint(p.id);});circles.set(p.id,circle);pointsLayer.append(circle);
      }
      let drag=null,dragged=false;
      svg.addEventListener('pointerdown',e=>{if(e.button!==0)return;dragged=false;stopSpin();drag={x:e.clientX,y:e.clientY,view:{...view},rotation:{...rotation},id:e.pointerId};});
      svg.addEventListener('pointermove',e=>{
        if(!drag)return;const dx=e.clientX-drag.x,dy=e.clientY-drag.y;if(Math.hypot(dx,dy)>4)dragged=true;if(!dragged)return;
        svg.setPointerCapture(e.pointerId);const rect=svg.getBoundingClientRect();
        if(mode==='3d'){const speed=3.2/Math.max(rect.width,1);rotation={yaw:drag.rotation.yaw+dx*speed,pitch:Math.max(-1.45,Math.min(1.45,drag.rotation.pitch+dy*speed))};schedule();return;}
        const scale=Math.max(drag.view.w/rect.width,drag.view.h/rect.height);view={...drag.view,x:drag.view.x-dx*scale,y:drag.view.y-dy*scale};updateView();
      });
      for(const event of ['pointerup','pointercancel','lostpointercapture'])svg.addEventListener(event,()=>{drag=null;});
      svg.addEventListener('wheel',e=>{e.preventDefault();stopSpin();zoom(e.deltaY>0?1.15:1/1.15);},{passive:false});
      svg.addEventListener('keydown',e=>{
        if(mode!=='3d')return;const step=e.shiftKey?.3:.1,keys={ArrowLeft:[-step,0],ArrowRight:[step,0],ArrowUp:[0,-step],ArrowDown:[0,step]};
        if(keys[e.key]){e.preventDefault();stopSpin();rotation={yaw:rotation.yaw+keys[e.key][0],pitch:Math.max(-1.45,Math.min(1.45,rotation.pitch+keys[e.key][1]))};schedule();}
        else if(e.key==='+'||e.key==='='){e.preventDefault();zoom(.85);}else if(e.key==='-'){e.preventDefault();zoom(1/.85);}
      });
      plot.append(svg);mapCard.append(plot);
      const tools=el('div',undefined,'vector-map-tools'),modes=el('div',undefined,'vector-mode');modes.setAttribute('role','group');modes.setAttribute('aria-label','보기 방식');
      modeButtons=[['3d','3D'],['2d','2D']].map(([key,label])=>{const button=btn(label,()=>setMode(key));button.dataset.mode=key;modes.append(button);return button;});
      spinButton=btn('자동 회전',()=>{if(spinning){stopSpin();return;}spinning=true;spinButton.setAttribute('aria-pressed','true');spin(version);});spinButton.setAttribute('aria-pressed','false');
      hint=el('span','','small muted');
      tools.append(modes,btn('확대',()=>zoom(.75)),btn('축소',()=>zoom(1/.75)),btn('처음 위치',()=>{stopSpin();view={x:0,y:0,w:1000,h:600};rotation={...homeRotation};depthZoom=1;updateView();render();}),spinButton,hint);mapCard.append(tools);
      caption=el('p','','small muted vector-caption');mapCard.append(caption);
      stopSpin();svg.classList.toggle('is-3d',mode==='3d');updateView();describe();render();
      selectionNote=el('p',undefined,'small muted');mapCard.append(selectionNote);
      const listCard=card('지도에 표시된 조각');listCard.classList.add('vector-list-card');mapCard.classList.add('vector-map-card');list=el('div');listCard.append(el('p','키보드로 목록의 버튼을 선택해도 상세 내용을 볼 수 있습니다.','small muted'),list);
      detail=card();detail.classList.add('vector-detail');detail.append(el('p','CHUNK INSPECTOR','eyebrow'),el('h2','문서 조각을 선택하세요'),el('p','분포도의 점이나 아래 목록을 선택하면 본문과 유사한 조각을 확인할 수 있습니다.','muted'));
      layout.append(mapCard,detail,listCard);body.append(layout);renderList();
    }catch(e){if(version===generation){metrics.replaceChildren();body.replaceChildren(el('p','벡터를 불러오지 못했습니다. 접근 권한과 서버 상태를 확인한 뒤 다시 시도하세요.','card empty'));}throw e;}
    finally{if(version===generation)refresh.disabled=false;}
  }
  await load();return root;
}
