# 지식 품질과 AI 작업 흐름 (3단계)

MCP 도구와 기존 작업 흐름(`start_task`·`finish_task`)에 붙는 품질 기능입니다. 모든 제안은 **제안일 뿐**이며 서버가 문서를 자동으로 연결·승격·저장하지 않습니다. 새 버전과 `012_knowledge_quality.sql` 마이그레이션을 배포한 뒤 MCP 클라이언트의 도구 목록을 새로 연결해야 합니다.

## 1. 중복·충돌 결정 감지

- `create_document`·`create_templated_document`·`finish_task`(documents)로 **decision**을 저장하면 응답에 `similar_decisions`가 붙습니다. 같은 프로젝트의 active·draft 결정 중 표현이 비슷한 문서입니다.
- 각 항목에는 `similarity`(0–1), `shared_terms`, `suggested_relation`(`supersedes` 0.4 이상, 그 외 `related`), 안내 문구가 있습니다. 기준값은 0.18 이상입니다.
- 이미 링크된 문서는 제외합니다. 기존 결정에 대해서는 `find_similar_decisions(document_id)`로 다시 확인할 수 있습니다.
- 판단 방식: 키워드 색인으로 후보 30개를 고른 뒤, 제목(40%)과 본문(60%)의 고유 어휘 Jaccard 유사도를 계산합니다. 양식 제목(결정 배경·선택 이유 등)과 `kb://` 인용은 제외합니다. 의미가 같아도 표현이 전혀 다르면 놓칠 수 있습니다.
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
- **주기 알림**: `start_task`의 `context.rechecks`에 호출자가 담당한 기한 지난 문서(최대 10건)가 표시됩니다. 담당자가 작업을 시작할 때마다 알림을 받는 방식입니다. 이메일·웹훅 발송은 아직 없으며(로드맵 11·17번) 필요하면 예약 작업이 `get_recheck_digest`를 주기적으로 호출해 `summary`를 전달합니다.
- history 문서는 시점 기록이므로 담당자를 지정할 수 없습니다.

## 3. start_task 맥락 활용도 피드백

- `finish_task`에 `used_sources`(실제로 근거로 쓴 문서 ID, 최대 50)와 `missing_knowledge`(찾지 못한 지식)를 함께 보냅니다. 별도로 `record_context_feedback`(misleading_sources 포함)으로 기록하거나 고칠 수 있습니다. 다시 기록하면 해당 작업의 피드백을 교체합니다.
- `start_task`가 제공했지만 목록에 없는 출처는 `unused`로 기록합니다. 나중에 찾아 쓴 문서는 `offered=false`로 남습니다.
- `get_retrieval_metrics(days=30)`:
  - `precision` = 제공한 출처 중 사용한 비율
  - `coverage` = 사용한 근거 중 start_task가 제공했던 비율
  - `hit_rate` = 제공 출처를 하나 이상 사용한 작업 비율, `mean_reciprocal_rank`
  - `most_used`, `offered_never_used`(2회 이상 제공, 사용 0), `misleading_sources`, 최근 `recent_missing`
- 피드백은 작업을 시작한 사용자만 기록합니다. 기록하지 않은 작업은 지표에서 빠지므로 표본 수(`tasks_with_feedback`)를 함께 보세요.

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
- `inventory`: 현재 문서 수(종류·상태별), 열린 작업·이슈, 준비된 첨부 수·용량, 참여자, 임베딩 준비·실패 청크 수.
- 호출·검색·열람·활성 사용자는 감사 로그 기준이라 `AUDIT_RETENTION_DAYS`가 남긴 기록만 집계됩니다. 작성·수정은 문서 버전(revisions) 기준이며 `REVISION_RETENTION_DAYS`로 정리된 버전은 빠집니다. 통계를 조회한 그 요청은 집계에 들어가지 않습니다.
- 웹 '사용 통계' 화면은 이 도구와 `get_retrieval_metrics`를 함께 보여 줍니다. 기간은 7·30·90일·1년이며 90일을 넘으면 차트를 주 단위로 묶습니다(활성 사용자는 주 중 일 최대값). 일별 추이 카드의 '표로 보기'로 일별 값을 확인할 수 있습니다.

## 마이그레이션

`015_search_misses.sql`은 결과 0건 검색어 테이블 `search_misses`를 추가합니다(스키마 버전 15).

`012_knowledge_quality.sql`은 documents의 kind 제약에 glossary를 추가하고, `recheck_assignments`·`task_source_feedback` 테이블과 task_runs의 피드백·닫힘 열과 `abandoned` 상태를 추가합니다. `migrationScripts`에서 011(`011_team_rollout.sql`) 다음, 스키마 버전 12로 적용됩니다.
