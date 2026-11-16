'use strict';
// Mock API answers for web-smoke.test.js. Shapes follow the JSON the server
// sends (struct tags in internal/kb/*.go); keep them in step when a response
// changes. Two variants:
//   full   every list has rows and optional fields are set
//   empty  lists are empty, optional fields are null, and fields the server
//          drops when empty (Go `omitempty`) are missing
const T='2026-11-15T06:08:01.051878+00:00';
const ME={id:'user-1',name:'홍길동',email:'me@example.com',global_role:'admin',disabled:false};
const OTHER={id:'user-2',name:'김철수',email:'other@example.com',global_role:'member',disabled:false};
const PROJECT={id:'project-1',slug:'demo',name:'데모 프로젝트',description:'스모크 테스트용',owner_id:ME.id,role:'owner',created_at:T,repositories:['github.com/example/demo']};

const attachment=(over={})=>({id:'att-1',project_id:PROJECT.id,document_id:'doc-1',document_revision:2,issue_id:null,filename:'보고서.pdf',size_bytes:123456,sha256:'ab'.repeat(32),status:'ready',text_status:'extracted',text_document_id:'doc-3',created_by:ME.id,created_at:T,updated_at:T,...over});
const document=(over={})=>({id:'doc-1',project_id:PROJECT.id,title:'시스템 구조',kind:'reference',content:'# 시스템 구조\n\n본문 **강조** `code`\n\n- 항목 1\n- 항목 2\n\n| a | b |\n|---|---|\n| 1 | 2 |\n',tags:['구조','reference'],status:'active',source_url:'https://example.com/doc',revision:2,created_by:ME.id,updated_by:ME.id,created_at:T,updated_at:T,...over});
const linked=(id,title)=>({document_id:id,title,kind:'decision',status:'active',revision:1,note:'메모',created_at:T});
const links=full=>full?{supersedes:[linked('doc-2','이전 결정')],superseded_by:[],implements:[],implemented_by:[linked('doc-4','구현 이력')],related:[linked('doc-5','관련 문서')]}:{supersedes:[],superseded_by:[],implements:[],implemented_by:[],related:[]};
const recheck={needs_recheck:true,days_since_update:120,stale_after_days:90,message:'90일 넘게 갱신되지 않았습니다.'};
const rule=(id,scope,over={})=>({id,project_id:PROJECT.id,scope,category:'work',title:'작업마다 개별 브랜치',body:'최신 main에서 새 브랜치를 만든다.',enabled:true,revision:1,created_by:ME.id,updated_by:ME.id,created_at:T,updated_at:T,...over});
const pull=(state='merged')=>({repository:'example/demo',number:12,url:'https://github.com/example/demo/pull/12',state});
const todo=(id,status,priority,over={})=>({id,project_id:PROJECT.id,title:'할 일 '+id,body:'본문',status,priority,category:'Work',progress:null,position:1024,revision:1,created_by:ME.id,created_by_name:ME.name,updated_by:ME.id,created_at:T,updated_at:T,task_count:0,open_task_count:0,open_issue_count:0,blocking_issue_count:0,pull_requests:[],...over});
const issue=(id,over={})=>({id,project_id:PROJECT.id,kind:'question',title:'질의 '+id,body:'질문 본문',status:'open',blocking:false,area_id:null,area_name:null,assignee_id:null,assignee_name:null,tags:[],answer:'',answered_by:null,answered_by_name:null,answered_at:null,resolution:'',document_id:null,task_id:null,todo_id:null,knowledge_document_id:null,request_key:null,source:null,source_key:null,source_url:null,source_state:null,source_updated_at:null,created_by:ME.id,created_by_name:ME.name,created_at:T,updated_at:T,comment_count:0,attachment_count:0,citation:'kb://'+PROJECT.id+'/issues/'+id,...over});
const answeredIssue=()=>issue('issue-2',{kind:'issue',status:'answered',blocking:true,area_id:'area-1',area_name:'배포',assignee_id:OTHER.id,assignee_name:OTHER.name,tags:['배포','github'],answer:'답변 본문',answered_by:OTHER.id,answered_by_name:OTHER.name,answered_at:T,todo_id:'todo-1',todo:{id:'todo-1',title:'할 일 todo-1',status:'in_progress'},knowledge_document_id:'doc-1',source:'github',source_key:'example/demo#7',source_url:'https://github.com/example/demo/issues/7',source_state:'open',source_updated_at:T,comment_count:2,attachment_count:1});
const comment=(id,over={})=>({id,issue_id:'issue-2',author_id:ME.id,author_name:ME.name,body:'댓글 '+id,created_at:T,...over});
const hit=(over={})=>({document_id:'doc-1',project_id:PROJECT.id,title:'시스템 구조',kind:'reference',status:'active',revision:2,updated_by:ME.id,updated_at:T,source_url:null,position:0,excerpt:'발췌 본문 '.repeat(60),score:0.0312,citation:'kb://'+PROJECT.id+'/documents/doc-1?revision=2#chunk-0',project_slug:PROJECT.slug,project_name:PROJECT.name,...over});
const ruleSet=full=>({project_rules:full?[rule('rule-1','project'),rule('rule-2','project',{category:'commit',enabled:false})]:[],personal_rules:full?[rule('rule-3','personal',{user_id:ME.id})]:[],can_manage_project_rules:true,guidance:'안내'});
const taskContext=full=>({project:PROJECT,task:'작업 설명',retrieval:'hybrid',sources:full?[hit()]:[],recent_decisions:[],excerpts_truncated:false,guidance:'안내',unindexed_chunks:0,
  ...(full?{warning:'임베딩 대기 중',rules:ruleSet(true),issues:{assigned_to_you:[{id:'issue-1',kind:'question',title:'질의',status:'open',blocking:true,area_name:null,assignee_id:ME.id,assignee_name:ME.name,created_by_name:OTHER.name,updated_at:T,citation:'kb://x'}],your_questions:[],related:[],shared_areas:[],blocking_open:1,guidance:'안내'},todos:{linked:{id:'todo-1',title:'할 일',status:'in_progress',priority:'high',progress:null,open_issues:0,blocking_issues:0},candidates:[],guidance:'안내'}}:{})});
const completion=()=>({task_id:'run-2',outcome:'completed',documents:[{id:'doc-4',kind:'history',title:'작업 이력',status:'active',revision:1,citation:'kb://x'}],guidance:'안내',pull_requests:['https://github.com/example/demo/pull/12']});
const taskRun=(id,over={})=>({id,project_id:PROJECT.id,created_by:ME.id,created_by_name:ME.name,request_key:'key-'+id,title:'작업 '+id,task:'작업 설명',excerpt_budget:8000,status:'open',completion:null,started_at:T,completed_at:null,age_days:0,open_issues:0,pull_requests:[],todo:null,...over});
const taskRuns=()=>[taskRun('run-1',{age_days:9,open_issues:2,todo:{id:'todo-1',title:'할 일 todo-1',status:'in_progress'}}),taskRun('run-2',{status:'completed',completed_at:T,feedback_at:T,feedback_note:'배포 절차가 없음',completion:completion(),pull_requests:[{...pull('deployed'),task_id:'run-2',project_id:PROJECT.id,created_at:T}]}),taskRun('run-3',{status:'abandoned',closed_at:T,closed_reason:'중단 사유',closed_by:ME.id,closed_by_name:ME.name})];
const webhook=()=>({id:'hook-1',project_id:PROJECT.id,repository:'example/demo',import_issues:true,issue_kind:'issue',area_id:'area-1',assignee_id:OTHER.id,tags:['github'],track_pull_requests:true,enabled:true,created_by:ME.id,created_by_name:ME.name,created_at:T,updated_at:T,last_delivery_at:T,last_event:'issues',last_outcome:'processed',last_error:null,payload_url:'http://localhost/api/webhooks/github/hook-1',secret_set:true,recent:[{delivery_id:'d-1',event:'ping',outcome:'processed',detail:null,received_at:T},{delivery_id:'d-2',event:'issues',outcome:'failed',detail:'오류',received_at:T}]});
const channel=()=>({id:'channel-1',project_id:PROJECT.id,kind:'webhook',name:'Discord',target_hint:'https://discord.com/…',events:['issue.assigned','issue.answered'],enabled:true,created_by:ME.id,created_at:T,updated_at:T,last_status:'failed',last_error:'HTTP 500',last_event_at:T,pending:1});
const usageStats=full=>({project_id:PROJECT.id,days:30,timezone:'Asia/Seoul',from:'2026-10-17',to:'2026-11-15',
  totals:{calls:full?120:0,denied:1,errors:2,active_users:2,searches:30,document_reads:40,document_writes:12,documents_new:5,tasks_started:8,tasks_completed:6,tasks_abandoned:1,issues_opened:3,search_misses:2},
  inventory:{documents:full?50:0,by_kind:full?[{key:'history',count:30},{key:'decision',count:20}]:[],by_status:full?[{key:'active',count:45},{key:'draft',count:5}]:[],open_tasks:1,open_issues:2,attachments:3,attachment_bytes:1048576,members:2,embeddings_ready:48,embeddings_total:50,embeddings_failed:1},
  daily:full?[{date:'2026-11-14',calls:60,searches:10,document_writes:5,tasks_started:3,active_users:2},{date:'2026-11-15',calls:60,searches:20,document_writes:7,tasks_started:5,active_users:1}]:[],
  top_actions:full?[{key:'search_knowledge',count:30},{key:'get_document',count:40}]:[],
  top_users:full?[{user_id:ME.id,name:ME.name,calls:100,document_writes:10,tasks_started:7,last_active_at:T},{user_id:OTHER.id,name:OTHER.name,calls:20,document_writes:2,tasks_started:1,last_active_at:null}]:[],
  top_documents:full?[{document_id:'doc-1',title:'시스템 구조',kind:'reference',status:'active',reads:12}]:[],
  search_misses:full?[{query:'없는 검색어',count:2,last_at:T}]:[],
  search_latency:full?[{retrieval:'hybrid',searches:20,p50_ms:12.5,p95_ms:40.1,max_ms:80,avg_embed_ms:8.2,p95_vector_ms:3.4,avg_lexical_ms:1.1,max_scanned_chunks:900,last_at:T},{retrieval:'lexical',searches:10,p50_ms:2,p95_ms:4,max_ms:5,avg_embed_ms:null,p95_vector_ms:null,avg_lexical_ms:1.5,max_scanned_chunks:0,last_at:T}]:[],
  hnsw_review:{thresholds:{project_id:PROJECT.id,vector_p95_ms:200,vector_p95_ms_source:full?'project':'default',scanned_chunks:50000,scanned_chunks_source:'default',server_default:{vector_p95_ms:200,scanned_chunks:50000},...(full?{updated_at:T}:{})},vector_searches:full?20:0,p95_vector_ms:full?3.4:null,max_scanned_chunks:full?900:0,vector_exceeded:false,chunks_exceeded:false,status:full?'ok':'insufficient_data',min_samples:20},
  kind_growth:full?[{kind:'history',start:20,added:10,total:30,cumulative:[25,30]},{kind:'decision',start:15,added:5,total:20,cumulative:[18,20]}]:[],
  guidance:'안내'});
const retrievalMetrics=full=>({project_id:PROJECT.id,days:30,tasks_started:full?8:0,tasks_with_feedback:full?5:0,offered:40,offered_used:12,used_total:14,used_not_offered:2,misleading:1,precision:full?0.3:null,coverage:full?0.85:null,hit_rate:full?0.8:null,mean_reciprocal_rank:full?0.6:null,
  most_used:full?[{document_id:'doc-1',title:'시스템 구조',kind:'reference',offered:8,used:6,misleading:0}]:[],offered_never_used:full?[{document_id:'doc-2',title:'이전 결정',kind:'decision',offered:5,used:0,misleading:0}]:[],misleading_sources:full?[{document_id:'doc-5',title:'관련 문서',kind:'brief',offered:3,used:1,misleading:1}]:[],recent_missing:full?['배포 절차가 없음']:[],guidance:'안내'});
const recheckAssignment=over=>({document_id:'doc-1',project_id:PROJECT.id,owner_id:OTHER.id,owner_name:OTHER.name,interval_days:30,note:'배포 주소 확인',last_checked_at:null,last_checked_by:null,check_note:'',created_by:ME.id,created_at:T,updated_at:T,next_due_at:'2026-08-01T00:00:00Z',...over});
const recheckDue=over=>({document_id:'doc-1',title:'시스템 구조',kind:'reference',status:'active',revision:2,owner_id:OTHER.id,owner_name:OTHER.name,interval_days:30,note:'배포 주소 확인',last_verified_at:T,days_overdue:12,citation:'kb://project-1/documents/doc-1?revision=2',...over});
const recheckDigest=full=>({project_id:PROJECT.id,generated_at:T,by_owner:full?[{owner_id:OTHER.id,owner_name:OTHER.name,total:3,due:[recheckDue()]}]:[],unassigned_total:full?2:0,unassigned:[],summary:full?'## 재확인 요약\n':'## 재확인 요약\n\n재확인할 문서가 없습니다.\n',guidance:'안내'});
const vectorPoints=()=>Array.from({length:12},(_,i)=>({id:'chunk-'+i,document_id:'doc-'+(i%3+1),title:'문서 '+(i%3+1),kind:['reference','decision','history'][i%3],position:i,revision:1,excerpt:'발췌 '+i,x:Math.cos(i),y:Math.sin(i),z:Math.cos(i*2)}));
const revisions=()=>[{document_id:'doc-1',revision:2,reason:'내용 보완',actor_id:ME.id,created_at:T},{document_id:'doc-1',revision:1,reason:'',actor_id:OTHER.id,created_at:T}];

// mockAPI returns the respond function for openConsole.
function mockAPI(variant){
  const full=variant==='full';
  const list=rows=>full?rows:[];
  const tools={
    list_projects:b=>b.offset?[]:[PROJECT],
    count_documents:()=>({count:full?3:0}),
    list_project_members:()=>[{user_id:ME.id,name:ME.name,role:'owner',disabled:false},...list([{user_id:OTHER.id,name:OTHER.name,role:'editor',disabled:false},{user_id:'user-3',name:'퇴사자',role:'viewer',disabled:true}])],
    list_rules:()=>ruleSet(full),
    list_documents:b=>b.offset?[]:list([{...document(),content:undefined,needs_recheck:true,days_since_update:120,stale_after_days:90},{...document({id:'doc-2',title:'이전 결정',kind:'decision',status:b.status||'draft',tags:[],source_url:null,revision:1}),content:undefined,needs_recheck:false,days_since_update:1,stale_after_days:90}]),
    get_document:b=>b.revision&&b.revision<2?document({id:b.document_id,revision:b.revision,content:'# 이전 본문\n',tags:['구조']}):document({id:b.document_id,...(full?{links:links(true),recheck}:{})}),
    get_document_history:()=>full?revisions():revisions().slice(0,1),
    get_document_links:()=>links(full),
    search_across_projects:()=>({hits:list([hit({recheck,superseded_by:[{document_id:'doc-9',title:'새 결정'}],truncated:true}),hit({document_id:'doc-2',title:'이전 결정',kind:'decision'})]),retrieval:'hybrid',unindexed_chunks:full?2:0,projects:[{id:PROJECT.id,slug:PROJECT.slug,name:PROJECT.name,role:'owner'}],...(full?{expanded_terms:['동의어'],warning:'임베딩 대기 중',omitted_projects:1}:{})}),
    list_todos:()=>list([todo('todo-1','in_progress','high',{progress:40,assignee_id:OTHER.id,assignee_name:OTHER.name,due_date:'2026-11-30',task_count:2,open_task_count:1,open_issue_count:1,blocking_issue_count:1,pull_requests:[pull('open')]}),todo('todo-2','not_started','medium',{category:'Ops'}),todo('todo-5','in_review','medium',{pull_requests:[pull('open')]}),todo('todo-3','completed','low',{completed_at:T,pull_requests:[pull('deployed')]}),todo('todo-4','idea','low',{category:''})]),
    list_task_runs:b=>b.offset?[]:list(b.status==='open'?taskRuns().slice(0,1):taskRuns()),
    get_task_run:b=>({...taskRun(b.task_id,full?{status:'completed',completed_at:T,completion:completion()}:{}),context:taskContext(full)}),
    list_issues:b=>b.offset?[]:list([issue('issue-1'),answeredIssue(),issue('issue-3',{status:'resolved',resolution:'해결 내용',answer:'답변',answered_by:ME.id,answered_by_name:ME.name,answered_at:T})]),
    // The server drops comments and attachments when there are none.
    get_issue:b=>b.issue_id==='issue-2'&&full?{...answeredIssue(),comments:[comment('c-1',{attachments:[attachment({id:'att-3',issue_id:'issue-2',document_id:null,document_revision:null,filename:'화면.png'})]}),comment('c-2')],attachments:[attachment({id:'att-2',issue_id:'issue-2',document_id:null,document_revision:null,filename:'캡처.png',text_status:'none'})],answer_attachments:[attachment({id:'att-4',issue_id:'issue-2',document_id:null,document_revision:null,filename:'답변.png'})]}:issue(b.issue_id),
    list_work_areas:()=>list([{id:'area-1',project_id:PROJECT.id,name:'배포',description:'Railway 배포',owner_id:OTHER.id,owner_name:OTHER.name,created_by:ME.id,created_at:T,updated_at:T,open_issues:1,owner_active:true},{id:'area-2',project_id:PROJECT.id,name:'문서',description:'',owner_id:'user-3',owner_name:'퇴사자',created_by:ME.id,created_at:T,updated_at:T,open_issues:0,owner_active:false}]),
    list_attachments:b=>b.offset?[]:list([attachment(),attachment({id:'att-5',filename:'표.xlsx',document_id:null,document_revision:null,sha256:null,status:'pending',text_status:'failed',text_error:'추출 실패',text_document_id:undefined}),attachment({id:'att-6',filename:'긴 문서.docx',text_truncated:true})]),
    get_attachment_usage:()=>({project_id:PROJECT.id,used_bytes:full?5242880:0,quota_bytes:full?1073741824:0,files:full?3:0,malware_scanner:full?'connected':'disabled',executables_blocked:true}),
    list_github_webhooks:()=>({webhooks:list([webhook()]),token_configured:full}),
    list_notification_channels:()=>list([channel(),{...channel(),id:'channel-2',kind:'email',name:'메일',enabled:false,last_status:null,last_error:null,last_event_at:null,pending:0}]),
    get_agents_template:()=>({filename:'AGENTS.md',project_id:PROJECT.id,project_slug:PROJECT.slug,content:'# 작업 지침\n',notes:full?['사용자 수준 지침에 넣으세요.']:[]}),
    get_usage_stats:()=>usageStats(full),
    get_retrieval_metrics:()=>retrievalMetrics(full),
    list_recheck_assignments:()=>list([recheckAssignment()]),
    get_recheck_digest:()=>recheckDigest(full),
    // Writes: answers are only as detailed as the pages read them.
    create_project:()=>PROJECT,
    add_project_repository:()=>({project_id:PROJECT.id,repositories:PROJECT.repositories,changed:true}),
    remove_project_repository:()=>({project_id:PROJECT.id,repositories:[],changed:true}),
    set_project_member:()=>({}),
    transfer_project_owner:()=>({}),
    save_rule:()=>rule('rule-9','personal'),
    delete_rule:()=>({deleted:true}),
    set_rule_enabled:()=>rule('rule-1','project',{enabled:false}),
    review_document:b=>document({id:b.document_id}),
    link_documents:()=>({link:{},created:true}),
    unlink_documents:()=>({removed:true}),
    save_todo:b=>todo(b.todo_id||'todo-9',b.status||'not_started',b.priority||'medium'),
    move_todo:b=>todo(b.todo_id,b.status,b.priority),
    delete_todo:()=>({deleted:true}),
    link_task_todo:()=>({}),
    link_issue_todo:()=>issue('issue-1'),
    close_task_run:b=>taskRun(b.task_id,{status:'abandoned'}),
    cleanup_stale_task_runs:b=>{const tasks=list([{id:'run-1',title:'작업 run-1',created_by:ME.id,created_by_name:ME.name,request_key:'key',started_at:T,age_days:9}]);return{older_than_days:b.older_than_days||7,total_open_stale:tasks.length,tasks,applied:!!b.apply,closed:b.apply?tasks.length:0,guidance:'안내'};},
    create_issue:()=>issue('issue-9'),
    answer_issue:()=>answeredIssue(),
    comment_issue:()=>({...answeredIssue(),created_comment_id:'c-9'}),
    update_issue:()=>issue('issue-1',{status:'resolved'}),
    set_work_area:()=>({id:'area-9'}),
    delete_work_area:()=>({deleted:true,moved_issues:1,detached_issues:0}),
    import_github_issues:()=>({repository:'example/demo',fetched:2,created:1,updated:1,unchanged:0,skipped_pull_requests:0,authenticated:false,issues:[]}),
    prepare_attachment_upload:()=>({attachment:attachment({status:'pending'}),upload_path:'/api/attachments/att-9/content'}),
    delete_attachment:()=>({deleted:true}),
    get_attachment_download:()=>({url:'http://localhost/download/att-1',expires_in_seconds:300}),
    set_github_webhook:b=>b.remove?{removed:true,webhook_id:'hook-1'}:{webhook:webhook(),webhook_id:'hook-1',payload_url:webhook().payload_url,secret:'whsec-mock'},
    set_notification_channel:()=>channel(),
    test_notification_channel:()=>({queued:true}),
    set_search_thresholds:()=>usageStats(true).hnsw_review.thresholds,
    assign_recheck_owner:b=>b.remove?{document_id:b.document_id,removed:true}:recheckAssignment({document_id:b.document_id,owner_id:b.owner_id,interval_days:b.interval_days,note:b.note||''}),
    confirm_recheck:b=>recheckAssignment({document_id:b.document_id,last_checked_at:T,check_note:b.note}),
  };
  const routes={
    'GET /api/me':()=>({user:ME,csrf_token:'csrf-mock',mcp_url:'http://localhost/mcp'}),
    'GET /api/setup':()=>({required:false}),
    'GET /api/keys':()=>list([{id:'key-1',user_id:ME.id,user_name:ME.name,name:'노트북',access:'read_write',project_id:null,expires_at:'2099-01-01T00:00:00Z',revoked_at:null,created_at:T,last_used_at:T},{id:'key-2',user_id:OTHER.id,user_name:OTHER.name,name:'영구',access:'read_only',project_id:PROJECT.id,expires_at:null,revoked_at:T,created_at:T,last_used_at:null}]),
    'POST /api/keys':()=>({token:'kb_mock_token',never_expires:false}),
    'POST /api/keys/revoke':()=>({}),
    'GET /api/mfa':()=>({enabled:full,available:true,enabled_at:full?T:null,recovery_codes_left:full?8:0}),
    'POST /api/mfa/setup':()=>({secret:'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567',otpauth_uri:'otpauth://totp/Knowledge%20Base:me%40example.com?secret=ABCDEFGHIJKLMNOPQRSTUVWXYZ234567&issuer=Knowledge+Base',expires_in_minutes:10}),
    'POST /api/mfa/enable':()=>({recovery_codes:['aaaa-bbbb','cccc-dddd']}),
    'POST /api/mfa/disable':()=>({}),
    'POST /api/mfa/recovery-codes':()=>({recovery_codes:['aaaa-bbbb','cccc-dddd']}),
    'GET /api/admin/stats':()=>({active_users:2,projects:1,active_keys:2,requests_24h:full?120:0,denied_24h:1,errors_24h:0,login_failures_24h:0,average_duration_ms:12}),
    'POST /api/admin/retention':()=>({}),
    'GET /api/admin/retention':()=>({sources:{audit_days:full?'admin':'server',revision_days:'server',revision_keep_latest:'server'},defaults:{audit_days:0,revision_days:0,revision_keep_latest:20},policy:{audit_days:full?365:0,revision_days:full?180:0,revision_keep_latest:5},data:{audit_events:full?1000:0,oldest_audit_event:full?T:null,revisions:full?200:0,oldest_revision:full?T:null},last_purge:full?{at:T,details:{audit_events:3,revisions:1}}:null}),
    'GET /api/discord/link':()=>({enabled:true,linked:full,linked_at:full?T:undefined}),
    'POST /api/discord/link-code':()=>({code:'ABCD-EFGH',expires_at:T}),
    'POST /api/discord/unlink':()=>({unlinked:true}),
    'GET /api/admin/discord':()=>({enabled:full,public_key:full?'a'.repeat(64):'',allowed_guild_ids:full?['123456789012345678']:[],endpoint_url:'https://kb.example.com/api/discord/interactions',updated_at:full?T:undefined}),
    'POST /api/admin/discord':()=>({}),
    'GET /api/admin/users':()=>[{...ME,activated:true,mfa_enabled:full,created_at:T},...list([{...OTHER,activated:true,mfa_enabled:true,created_at:T},{id:'user-3',name:'퇴사자',email:'gone@example.com',global_role:'member',disabled:true,activated:false,mfa_enabled:false,created_at:T}])],
    'POST /api/admin/users':()=>({}),
    'POST /api/admin/users/mfa-reset':()=>({}),
    'POST /api/admin/invite':()=>({invitation_url:'http://localhost/#invite=mock'}),
    'POST /api/admin/audit':()=>list([{id:2,actor_id:ME.id,actor_name:ME.name,action:'search_knowledge',project_id:PROJECT.id,target_id:null,outcome:'success',error_code:null,duration_ms:12,created_at:T},{id:1,actor_id:null,actor_name:null,action:'login',project_id:null,target_id:null,outcome:'denied',error_code:'UNAUTHORIZED',duration_ms:3,created_at:T}]),
    'GET /api/attachments/storage':()=>({state:full?'connected':'not_configured',scanner:full?'connected':'disabled',checked_at:T}),
    'POST /api/vectors/map':()=>({points:list(vectorPoints()),model:'bge-m3',eligible:full?12:0,limit:2000,explained_variance:full?0.42:0,component_variance:full?[0.2,0.14,0.08]:[0,0,0],embedding_status:{enabled:true,model:'bge-m3',total:full?12:0,ready:full?12:0,pending:0,processing:0,failed:0}}),
    'POST /api/vectors/neighbors':()=>list([{id:'chunk-1',document_id:'doc-2',title:'문서 2',kind:'decision',position:1,revision:1,excerpt:'발췌 1',similarity:0.91},{id:'chunk-2',document_id:'doc-3',title:'문서 3',kind:'history',position:2,revision:1,excerpt:'발췌 2',similarity:0.83}]),
    'POST /api/password':()=>({}),
    'POST /api/logout':()=>({}),
  };
  return({method,path,body})=>{
    const toolName=path.startsWith('/api/tools/')?path.slice('/api/tools/'.length):null;
    const handler=toolName?tools[toolName]:routes[method+' '+path.split('?')[0]];
    if(method==='PUT'&&path.startsWith('/api/attachments/'))return{status:'ready',sha256:'ab'.repeat(32),text_status:'extracted'};
    if(!handler)throw Object.assign(Error(`web-fixtures.js has no answer for ${method} ${path}`),{status:404,code:'NO_FIXTURE'});
    return handler(body||{});
  };
}

module.exports={mockAPI,ME,PROJECT};
