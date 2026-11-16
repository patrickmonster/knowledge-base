# 지식 품질과 AI 작업 흐름 (3단계)

MCP 도구와 기존 작업 흐름(`start_task`·`finish_task`)에 붙는 품질 기능입니다. 모든 제안은 **제안일 뿐**이며 서버가 문서를 자동으로 연결·승격·저장하지 않습니다. 새 버전과 `012_knowledge_quality.sql` 마이그레이션을 배포한 뒤 MCP 클라이언트의 도구 목록을 새로 연결해야 합니다.

## 1. 중복·충돌 결정 감지

- `create_document`·`create_templated_document`·`finish_task`(documents)로 **decision**을 저장하면 응답에 `similar_decisions`가 붙습니다. 같은 프로젝트의 active·draft 결정 중 표현이 비슷한 문서입니다.
- 각 항목에는 `similarity`(0–1), `shared_terms`, `suggested_relation`(`supersedes` 0.4 이상, 그 외 `related`), 안내 문구가 있습니다. 기준값은 0.06 이상입니다(아래 '기준값 근거').
- 이미 링크된 문서는 제외합니다. 기존 결정에 대해서는 `find_similar_decisions(document_id)`로 다시 확인할 수 있습니다.
- 판단 방식: 키워드 색인으로 후보 30개를 고른 뒤, 제목(40%)과 본문(60%)의 고유 어휘 Jaccard 유사도를 계산합니다. 양식 제목(결정 배경·선택 이유 등), `kb://` 인용, 커밋·문서 ID와 날짜·숫자, 결정 문서마다 들어가는 기록 문구(`rev`, `draft`, '사용자가 선택했다', '재구성' 등)는 제외합니다. 의미가 같아도 표현이 전혀 다르면 놓칠 수 있습니다.
- 기준값 근거(2026-11-16): 이 프로젝트의 결정 10건·45쌍으로 측정했습니다. 주제가 다른 결정은 최대 0.054, 주제가 이어지는 결정은 0.067–0.105였고(Railway 볼륨 백업↔development 볼륨 삭제 등), 한 결정을 줄이거나 고쳐 쓴 판은 0.69–0.97이었습니다. 그래서 보고 기준은 두 무리 사이의 0.06, `supersedes` 제안 기준은 서로 다른 결정이 닿지 않는 0.4를 유지합니다. 표본이 작으므로 결정이 훨씬 늘면 다시 측정합니다. 측정에서 이미 이어진 것으로 기록된 결정 한 쌍(HNSW 도입 시점↔Ollama·pgvector 검색, 0.057)은 표현이 달라 여전히 놓칩니다.
- AI는 사용자에게 확인한 뒤 `link_documents(source=새 결정, target=기존 결정, relation=supersedes)` 또는 `related`를 호출합니다. 두 결정이 충돌하면 사용자에게 어느 쪽이 유효한지 묻습니다.

## 2. 오래된 지식 재확인 담당자

| 도구 | 역할 | 권한 |
| --- | --- | --- |
| `assign_recheck_owner` | 문서의 재확인 담당자와 주기(`interval_days`, 기본 90일) 지정·변경, `remove=true`로 해제 | 편집자 이상. 담당자는 활성 소유자·편집자 |
| `confirm_recheck` | 내용을 바꾸지 않고 "확인함"을 기록해 주기를 다시 시작 | 편집자 이상 |
| `list_recheck_assignments` | 지정 목록과 `next_due_at` | 참여자 |
| `get_recheck_digest` | 기한이 지난 문서를 담당자별로 묶은 요약(`summary`는 Markdown) | 참여자 |

- 기한은 `max(마지막 수정, 마지막 확인) + interval_days`입니다. 문서를 수정해도 주기가 다시 시작됩니다.
- `include_unassigned=true`이면 담당자가 없고 90일 넘게 수정되지 않은 active reference·brief도 함께 보여 줍니다.
- **주기 알림**: `start_task`의 `context.rechecks`에 호출자가 담당한 기한 지난 문서(최대 10건)가 표시됩니다. 담당자가 작업을 시작할 때마다 알림을 받는 방식입니다. 프로젝트 알림 채널로 보내는 **주기 요약 발송**도 있습니다([아래](#재확인-요약-발송)). 채널을 쓰지 않는다면 예약 작업이 `get_recheck_digest`를 주기적으로 호출해 `summary`를 전달해도 됩니다.
- history 문서는 시점 기록이므로 담당자를 지정할 수 없습니다.
- 웹 **재확인 · 피드백** 화면에서 기준 일수(`stale_days`)별 재확인 필요 문서, 담당자·주기·다음 기한·마지막 확인, 담당자별 기한 지난 문서와 `get_recheck_digest` 요약 복사를 볼 수 있습니다. 담당 지정·변경·해제(`assign_recheck_owner`)와 "확인함" 기록(`confirm_recheck`)은 편집자 이상에게만 버튼이 보이며, 권한 판정은 도구와 같은 서버 규칙입니다. 조회자는 읽기 전용입니다.

### 재확인 요약 발송

기한이 지난 문서 요약(`get_recheck_digest`와 같은 조회)을 이슈 알림과 같은 채널·대기열(`notification_outbox`)·재시도 워커로 보냅니다. 새 채널 종류는 없고, Discord 웹훅 주소는 웹훅 채널에서 자동으로 판별합니다.

- **받을 채널 고르기**: 웹 **담당자 알림**에서 채널을 추가·수정할 때 이벤트 `recheck_digest`를 고릅니다(프로젝트 소유자). 기본 이벤트에는 포함되지 않으므로 기존 채널은 바뀌지 않고, 이 이벤트를 고른 채널만 받습니다. 프로젝트 단위 설정이 따로 필요 없고, 채널을 끄거나 이벤트를 빼면 멈춥니다.
- **주기**: 서버 환경변수 `RECHECK_DIGEST_SCHEDULE`(`weekly` 기본값 · `daily` · `off`)와 `RECHECK_DIGEST_HOUR`(0–23, 한국 시간, 기본 9). `weekly`는 월요일 시작 주마다, `daily`는 매일, 지정한 시각이 지난 뒤 첫 확인 때 한 번 보냅니다. 서버가 그 시각에 꺼져 있었다면 같은 주기 안에서 켜진 뒤 보냅니다. 알림 워커가 1분마다 확인하며 `NOTIFICATIONS_ENABLED=false`면 보내지 않습니다.
- **중복 방지**: 프로젝트·주기마다 `recheck_digest_runs`에 한 줄을 넣는 쪽만 대기열에 쌓습니다. 한 트랜잭션에서 처리하므로 서버가 여러 대여도 한 주기에 한 번만 나가고, 실패한 발송의 재시도는 기존 대기열 규칙(최대 6번)을 따릅니다.
- **기한 지난 문서가 없으면** 보내지 않고 주기도 소모하지 않습니다(그 주기 안에 문서가 기한을 넘기면 다음 확인 때 보냅니다). 담당자가 없는 문서(`include_unassigned`)는 포함하지 않습니다.
- **내용**: 담당자별로 제목·종류·기한이 지난 일수와 웹 콘솔 링크(`/#documents`)만 담습니다. 담당자당 최대 5건, 최대 15명까지 나열하고 나머지는 건수로 알립니다. 문서 본문과 재확인 메모는 보내지 않습니다. Slack·Discord·이메일 본문은 1,800자 안에서 줄이고 남은 건수를 적어, Discord 2,000자 제한에 걸리지 않습니다. 제목의 `<`·`>`는 Slack 멘션 구문이 되지 않도록 바꿉니다. 일반 웹훅은 같은 내용을 JSON(`digest`)으로 받습니다(`X-KB-Event: recheck_digest`).
- **받는 사람**: 웹훅·Slack 채널은 프로젝트 전체 요약을 한 건 받습니다. 이메일 채널은 이슈 알림과 같은 규칙으로 **현재 프로젝트 참여자이고 비활성화되지 않은 담당자**에게만, 각자 담당한 문서만 보냅니다. 프로젝트에서 제외됐거나 비활성화된 담당자의 문서는 이름 없이 "담당자 확인 필요"로 묶여 채널 요약에만 나타나고, 그 사람에게는 보내지 않습니다.
- **선택 이유**: 주기는 서버 전체 운영 값이라 환경변수로, 받을 곳은 기존 채널 구독(`events`)으로 정해 새 설정 테이블 없이 둘을 가장 적게 바꿨습니다. 프로젝트마다 다른 주기가 필요해지면 그때 프로젝트 설정으로 옮깁니다.
- 마이그레이션 `034_recheck_digest_runs.sql`이 필요합니다.

## 3. start_task 맥락 활용도 피드백

- `finish_task`에 `used_sources`(실제로 근거로 쓴 문서 ID, 최대 50)와 `missing_knowledge`(찾지 못한 지식)를 함께 보냅니다. 별도로 `record_context_feedback`(misleading_sources 포함)으로 기록하거나 고칠 수 있습니다. 다시 기록하면 해당 작업의 피드백을 교체합니다. `finish_task`의 `used_sources`에 들어 있는 결정·기획 문서는 히스토리와 `implements`로 자동 연결됩니다(`record_context_feedback`은 연결하지 않음).
- `start_task`가 제공했지만 목록에 없는 출처는 `unused`로 기록합니다. 나중에 찾아 쓴 문서는 `offered=false`로 남습니다.
- `get_retrieval_metrics(days=30)`:
  - `precision` = 제공한 출처 중 사용한 비율
  - `coverage` = 사용한 근거 중 start_task가 제공했던 비율
  - `hit_rate` = 제공 출처를 하나 이상 사용한 작업 비율, `mean_reciprocal_rank`
  - `most_used`, `offered_never_used`(2회 이상 제공, 사용 0), `misleading_sources`, 최근 `recent_missing`
- 피드백은 작업을 시작한 사용자만 기록합니다. 기록하지 않은 작업은 지표에서 빠지므로 표본 수(`tasks_with_feedback`)를 함께 보세요.
- 같은 화면의 **맥락 피드백** 카드는 `get_retrieval_metrics`(7·30·90일·1년)의 정확도·포괄률·적중률·MRR, 사용·미사용·오해 소지 비율(미사용은 제공 건수에서 사용·오해 소지를 뺀 추정치), 많이 쓴 문서·쓰이지 않은 문서·오해 소지 문서, 찾지 못한 지식과 `list_task_runs`의 최근 작업별 피드백 기록을 보여 줍니다. 기록 자체는 웹에서 하지 않는 조회 전용입니다.

## 4. finish_task 결정 후보 추출

- `finish_task`의 `decisions` 본문에서 결정처럼 보이는 문장(결정·하기로·채택·도입·유지·대신·decide·adopt 등)을 최대 5개 찾아 `decision_candidates`로 돌려줍니다. "없음"은 무시합니다.
- 각 후보에는 `create_templated_document`에 그대로 넣을 수 있는 `draft`(kind decision, status draft)가 있습니다. 확인되지 않은 항목은 "미확인 — 작성자가 보완"으로 채웁니다. `이유:`·`—`·`because` 뒤 문장은 선택 이유로 옮깁니다.
- 같은 호출의 `documents`로 이미 저장한 결정과 비슷한 후보는 제외하고, 기존 결정과 비슷하면 `similar_decisions`를 붙입니다.
- 후보는 저장하지 않습니다. 사용자 확인 후 빈 항목을 보완해 draft로 저장합니다.

## 5. 오래 열린 작업 정리

- `list_task_runs(status=open, older_than_days=7)`로 오래 열린 작업을 찾습니다. 결과에 `age_days`가 포함됩니다.
- 웹 **진행 중 작업** 화면에서 같은 목록을 상태(진행 중·완료·중단)와 "내가 시작한 작업만"으로 걸러 봅니다. 7일 넘게 열린 작업에 표시가 붙고, 시작 자료(규칙·참고 자료·질의) 스냅샷과 완료 기록·PR 상태를 열람하며, 시작한 사람이나 소유자는 사유를 적어 중단 처리합니다. 소유자는 같은 화면에서 `cleanup_stale_task_runs` 미리보기·일괄 정리를 실행합니다. 사이드바에 현재 프로젝트의 진행 중 작업 수 배지가 붙습니다.
- 실제로 마친 작업은 `finish_task`로 결과를 기록합니다. 버려진 작업은 `close_task_run(task_id, reason)`으로 닫습니다(시작한 사용자 또는 프로젝트 소유자). 상태는 `abandoned`가 되며 이유·닫은 사람·시각이 남고, 이후 `finish_task`는 CONFLICT입니다.
- `cleanup_stale_task_runs`는 기본이 미리보기입니다. `apply=true`와 `reason`을 주면 프로젝트 소유자가 한 번에 최대 200건을 닫습니다.
- 같은 `request_key`로 `start_task`를 다시 호출하면 닫힌 작업이 그대로 반환됩니다. 다시 하려면 새 `request_key`를 사용합니다.

## 6. 용어집과 동의어 확장

- 문서 종류 `glossary`가 추가되었습니다. 제목이 용어이고(`용어: ` 접두어는 무시) 동의어는 `## 동의어` 절 또는 `동의어: a, b` 줄에 쉼표로 적습니다. `get_document_templates`에 용어집 양식이 있습니다.
- `search_knowledge`·`search_across_projects`·`start_task` 맥락 검색은 질의가 **active** 용어집 항목의 용어나 동의어를 언급하면 같은 항목의 다른 용어를 질의에 덧붙입니다(최대 20개). 결과의 `expanded_terms`로 확인하고 `no_synonyms=true`로 끌 수 있습니다.
- 한글 용어는 조사가 붙어도 일치합니다(결제를 → 결제). 영문·약어는 단어 단위로만 일치합니다(`PG`는 `upgrade`에 일치하지 않음).
- 확장 어휘는 키워드 검색과 질의 임베딩 모두에 사용합니다. 용어집 본문은 앞 8,000자만 읽으며 프로젝트당 최근 500개 항목을 사용합니다.

## 7. 사용 통계 대시보드

- `get_usage_stats(project_id, days=30)`: 프로젝트 참여자(조회자 포함) 누구나 호출할 수 있는 읽기 도구입니다. `days`는 1~365이며 오늘을 포함한 한국 시간(Asia/Seoul) 일 단위로 집계합니다.
- `totals`: 성공한 도구 호출·거부·오류, 활성 사용자, 검색(`search_knowledge`·`get_task_context`·`start_task`), 문서 열람(`get_document`), 문서 작성·수정(새 버전 수), 새 문서, 작업 시작·완료·중단, 새 이슈.
- `daily`: 기간의 모든 날짜를 0으로 채운 일별 호출·활성 사용자·검색·작성·작업 시작. `top_actions`(최대 15), `top_users`(최대 10), `top_documents`(열람 상위 10).
- `kind_growth`: 문서 종류별 기간 시작 수·추가·현재 수와 일별 누적(`cumulative`, `daily`와 같은 날짜). 문서의 현재 종류 기준이며 보관 문서도 포함합니다.
- `search_misses`: 기간 안에 `search_knowledge`가 결과 0건을 돌려준 검색어 상위 20개(횟수·마지막 시각), `totals.search_misses`는 전체 건수입니다. 자주 나오는 검색어는 아직 쓰지 않은 지식이거나 용어집 동의어 후보입니다.
  - 저장 범위: 공백을 하나로 줄이고 소문자로 바꾼 검색어 앞 200자, 검색 방식, 시각만 `search_misses` 테이블에 저장합니다. 검색한 사용자는 저장하지 않습니다. `search_across_projects`, `start_task`, `get_task_context`는 기록하지 않습니다.
  - 보관: 최대 365일, `AUDIT_RETENTION_DAYS`가 더 짧으면 그 기간. 매시간 정리합니다.
- `search_latency`: 검색 방식(`retrieval`: hybrid·semantic·lexical·lexical-fallback)별 검색 시간(ms). 횟수, 전체 p50·p95·최대, 질의 임베딩 평균(`avg_embed_ms`), 벡터 검색 p95(`p95_vector_ms`), 키워드 검색 평균(`avg_lexical_ms`), 한 번의 벡터 검색이 비교한 최대 청크 수(`max_scanned_chunks`), 마지막 시각. 실행하지 않은 단계는 null입니다.
  - 목적: HNSW 같은 근사 벡터 인덱스 도입 시점을 측정으로 정합니다. 현재 의미 검색은 범위 안의 준비된 청크를 모두 비교하는 정확 검색이라 `p95_vector_ms`가 `max_scanned_chunks`에 비례해 커집니다.
  - 저장 범위: `search_knowledge`와 `start_task`·`get_task_context`의 맥락 검색 1회마다 `search_timings`에 출처(`source`), 요청·실제 검색 방식, 단계별 시간, 비교 청크 수, 결과 수, 시각만 저장합니다. 검색어와 사용자는 저장하지 않습니다. `search_across_projects`는 기록하지 않습니다. 오류로 끝난 검색은 트랜잭션과 함께 취소되어 남지 않습니다.
  - 보관: `search_misses`와 같습니다(최대 365일 또는 더 짧은 `AUDIT_RETENTION_DAYS`, 매시간 정리).
- `hnsw_review`: 기간 안의 벡터 검색 전체(검색 방식 구분 없이 `vector_ms`가 있는 검색)를 HNSW 검토 기준과 비교한 판정입니다.
  - 값: `thresholds`(적용 기준값과 출처 `project`·`server`, `server_default`), `vector_searches`, `p95_vector_ms`, `max_scanned_chunks`, `vector_exceeded`, `chunks_exceeded`, `status`, `min_samples`(20).
  - `status`: 비교 청크 수가 기준 이상이거나, 벡터 검색이 20회 이상이면서 p95가 기준 이상이면 `review`. 그 밖에 벡터 검색이 20회 미만이면 `insufficient_data`, 아니면 `ok`. 청크 수는 측정 잡음이 없어 한 번만 넘어도 판정합니다.
  - 기준값: 서버 기본값은 `HNSW_REVIEW_VECTOR_P95_MS`(기본 100 ms), `HNSW_REVIEW_SCANNED_CHUNKS`(기본 20,000개). 프로젝트 소유자·관리자가 `set_search_thresholds(project_id, vector_p95_ms, scanned_chunks)`로 프로젝트별로 덮어씁니다. 호출마다 두 값을 함께 바꾸며 0이나 생략은 서버 기본값입니다. 웹 '사용 통계' → '검색 시간' 카드에서도 바꿀 수 있습니다.
  - 기본값 근거: 2026-11 운영(청크 약 320개)에서 벡터 p95는 13~18 ms였고 hybrid 검색 시간의 대부분은 질의 임베딩이었습니다. 100 ms는 벡터 검색이 체감 지연의 주요 원인이 되기 시작하는 선, 20,000개는 1,024차원 정확 검색이 그 시간에 가까워지는 규모로 잡은 출발점입니다. 운영 측정으로 조정합니다.
  - `review`는 HNSW 작업을 검토하라는 신호일 뿐 인덱스를 자동으로 만들지 않습니다. 인덱스만 추가해서는 현재 쿼리(`vector_search.go`)가 쓰지 않으므로 쿼리 재작성이 함께 필요합니다.
- `inventory`: 현재 문서 수(종류·상태별), 열린 작업·이슈, 준비된 첨부 수·용량, 참여자, 임베딩 준비·실패 청크 수.
- 호출·검색·열람·활성 사용자는 감사 로그 기준이라 `AUDIT_RETENTION_DAYS`가 남긴 기록만 집계됩니다. 작성·수정은 문서 버전(revisions) 기준이며 `REVISION_RETENTION_DAYS`로 정리된 버전은 빠집니다. 통계를 조회한 그 요청은 집계에 들어가지 않습니다.
- 웹 '사용 통계' 화면은 이 도구와 `get_retrieval_metrics`를 함께 보여 줍니다. 기간은 7·30·90일·1년이며 90일을 넘으면 차트를 주 단위로 묶습니다(활성 사용자는 주 중 일 최대값). 일별 추이 카드의 '표로 보기'로 일별 값을 확인할 수 있습니다.

## 마이그레이션

`030_search_thresholds.sql`은 프로젝트별 HNSW 검토 기준 테이블 `project_search_settings`를 추가합니다(스키마 버전 30). 값이 없거나 NULL이면 서버 기본값을 씁니다.

`022_search_timings.sql`은 검색 시간 표본 테이블 `search_timings`를 추가합니다(스키마 버전 22).

`015_search_misses.sql`은 결과 0건 검색어 테이블 `search_misses`를 추가합니다(스키마 버전 15).

`012_knowledge_quality.sql`은 documents의 kind 제약에 glossary를 추가하고, `recheck_assignments`·`task_source_feedback` 테이블과 task_runs의 피드백·닫힘 열과 `abandoned` 상태를 추가합니다. `migrationScripts`에서 011(`011_team_rollout.sql`) 다음, 스키마 버전 12로 적용됩니다.
