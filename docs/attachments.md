# Railway 버킷 첨부 문서

파일 원본은 비공개 Railway 버킷에 저장합니다. PostgreSQL에는 프로젝트·문서 연결, 원래 파일명, 크기, SHA-256, 작성자, 저장 상태를 기록합니다. 최대 25 MiB이며 빈 파일은 받지 않습니다. UI는 편의상 25MB로 표시합니다.

## 화면에서 사용

**첨부 문서** 상단에서 버킷 연결 상태와 마지막 확인 시각을 확인합니다. `연결 다시 확인`은 파일을 만들거나 삭제하지 않고 서버의 버킷 접근 여부를 조회합니다. 연결 설정 없음·접근 실패·조회 실패를 구분하며 연결 확인 전에는 업로드를 비활성화합니다. 연결 확인은 파일 쓰기 성공을 보장하지 않으므로 업로드 후 목록의 `저장 완료`도 확인하세요. 버킷명·접속 주소·자격 증명은 화면에 노출하지 않습니다.

목록에는 이 서비스에서 해당 프로젝트에 등록한 첨부만 표시합니다. 버킷에 직접 업로드한 파일은 자동으로 가져오지 않습니다. 연결 오류가 있어도 기존 첨부 목록은 조회할 수 있습니다.

**첨부 문서** → 프로젝트 선택 → 파일 선택 → 업로드. 기존 지식 문서에 연결하려면 문서 ID를 입력합니다. 생략하면 프로젝트 첨부가 됩니다. 연결 당시 문서 revision을 함께 보관합니다. 원본 문서의 본문·버전은 수정하지 않습니다.

소유자·편집자는 추가·삭제할 수 있고 조회자는 목록·다운로드만 사용할 수 있습니다. 전체 관리자라도 프로젝트 참여 권한이 없으면 파일에 접근할 수 없습니다. 업로드를 예약한 사용자만 해당 예약을 완료할 수 있습니다.

### 미리보기

목록의 **미리보기**는 파일을 내려받지 않고 창에서 보여 줍니다. 조회자를 포함해 프로젝트 참여자 누구나 쓸 수 있습니다.

| 형식 | 표시 |
| --- | --- |
| PNG·JPG·GIF·WEBP | 창 안에 이미지 표시 |
| `.txt`·`.md`·`.csv`·`.tsv`·`.json`·`.log`·`.yaml`·`.yml` | 글자 그대로 표시(Markdown·HTML로 해석하지 않음). 1 MiB를 넘으면 앞부분만 |
| PDF | 추출 본문 표시 + **원본 PDF 새 탭에서 열기**(브라우저 기본 PDF 보기) |
| `.html`·`.htm` | 창 안 샌드박스 프레임에 페이지로 표시 + **새 탭에서 크게 보기**. 스크립트는 실행하지 않고 외부 이미지·글꼴·스타일시트는 불러오지 않음(인라인 스타일과 `data:` 이미지·글꼴만 표시) |
| `.docx`·`.xlsx` | 추출 본문 표시(서식·이미지·차트 제외, 아래 "본문 추출과 검색") |
| 그 밖의 형식, 추출 본문이 없는 Word·Excel | 미리보기 없음 — 다운로드 |

보안 규칙:

- 원본은 `GET /api/attachments/{id}/preview?project_id=…`로만 읽습니다. 로그인 쿠키(웹 세션)와 프로젝트 조회 권한이 필요하고, MCP 키와 업로드 링크 토큰은 받지 않습니다. 주소에 비밀값이 없으므로 주소가 알려져도 권한 없는 사람은 열 수 없습니다. 미리보기는 버킷 다운로드 링크를 발급하지 않습니다(다운로드 링크는 지금처럼 **다운로드**를 누를 때만 5분짜리로 발급).
- 무엇으로 보여 줄지는 파일명이 아니라 **파일 앞부분 바이트**로 서버가 다시 판단합니다. 래스터 이미지, PDF, (텍스트 확장자이면서 실제로 텍스트인) 파일, (`.html`·`.htm` 이름이면서 HTML이나 텍스트로 판정되는) 파일만 내보내고 나머지는 `INVALID_INPUT`으로 거절합니다. `.txt` 등 텍스트 이름으로 올린 HTML은 계속 거절합니다. SVG는 보여 주지 않습니다.
- HTML은 `Content-Type: text/html; charset=utf-8`과 `Content-Security-Policy: sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; frame-ancestors 'self'`로 보냅니다. CSP `sandbox`(허용 항목 없음)로 문서가 고유(opaque) 출처가 되어 이 앱의 쿠키·API·화면에 접근할 수 없고, 스크립트·폼 전송·팝업·상위 창 이동이 막힙니다. 주소를 새 탭에서 직접 열어도 같은 제한이 걸립니다. 어떤 외부 주소에도 요청하지 않으므로 파일이 열람자를 추적할 수 없습니다. 웹 화면은 `sandbox=""` 속성을 단 iframe에 한 번 더 가두고, 이 응답만 `X-Frame-Options: SAMEORIGIN`으로 앱 자신의 화면에서 끼워 넣을 수 있게 합니다. 스크립트가 필요한 대화형 HTML은 미리보기에서 동작하지 않으니 다운로드해서 여세요.
- 응답은 `X-Content-Type-Options: nosniff`, `Content-Disposition: inline`, `Cache-Control: private, max-age=300`입니다. 이미지·텍스트는 `Content-Security-Policy: default-src 'none'; sandbox`, HTML은 위 정책, PDF는 브라우저 PDF 보기가 sandbox에서 동작하지 않아 `default-src 'none'; frame-ancestors 'none'`입니다. 전역 `X-Frame-Options: DENY`가 그대로 적용되어 다른 페이지에 끼워 넣을 수 없으므로 PDF는 창 안이 아니라 새 탭에서 엽니다.
- 추출 본문은 지식 문서와 같은 Markdown 렌더러(DOM만 생성, HTML 미해석)로 표시합니다.
- 미리보기 읽기는 감사 로그에 `preview_attachment`로 남고 사용자당 분당 600회로 제한합니다(이슈 사진과 같은 한도).

## MCP 클라이언트에서 사용

1. `prepare_attachment_upload` 호출:

```json
{"project_id":"PROJECT_UUID","document_id":"OPTIONAL_DOCUMENT_UUID","filename":"기획서.pdf","size_bytes":12345}
```

응답의 `attachment.id`, `upload_url`, `upload_path`를 보관합니다. `document_id`는 생략할 수 있지만, 파일의 목적과 핵심을 요약한 지식 문서에 연결하는 것을 권장합니다. `.pdf`·`.docx`·`.xlsx`는 아래 "본문 추출과 검색"대로 본문이 자동 색인되고, 그 밖의 형식은 본문이 검색되지 않습니다. 예약은 1시간 유효합니다. 파일 내용이나 비밀키를 MCP JSON에 넣지 않습니다.

2. 원본 바이트를 PUT 합니다. 방법은 두 가지입니다.
   - **1회용 업로드 링크 (MCP 키로 예약한 경우):** 응답의 `upload_url`은 `PUBLIC_URL` 기준 절대 주소이며 이 파일 하나에만, 1시간 동안, 한 번만 쓸 수 있습니다. `Authorization` 헤더가 필요 없어서 키 원문을 읽을 수 없는 클라이언트(호스팅 커넥터, 샌드박스)도 올릴 수 있습니다.

     ```sh
     curl --fail-with-body -T 정리표.xlsx -H "Content-Type: application/octet-stream" "<upload_url>"
     ```

     서버에는 링크 토큰의 SHA-256만 저장하며 업로드가 시작되면 지웁니다. 예약한 키가 폐기·만료되거나 계정이 비활성화되면 링크도 무효입니다. 권한·업로더·크기 검사는 Bearer 업로드와 동일합니다. 링크는 응답·로그·지식 문서·커밋에 남기지 마세요. `Authorization` 헤더와 함께 보내면 거절합니다.
   - **Bearer 키:** `upload_path`(상대 경로)에 `Authorization: Bearer <예약에 사용한 사용자의 MCP 키>`로 보냅니다.

   두 방법 모두 `Content-Type: application/octet-stream`이 필요합니다. 웹 화면은 로그인 쿠키와 CSRF 토큰을 사용하며 업로드 링크를 받지 않습니다. 버킷 자격 증명은 반환하지 않습니다.
   - **MCP 조각 업로드 (네트워크가 `upload_url`에 닿지 않는 경우):** claude.ai 채팅 샌드박스처럼 외부 접속이 허용 목록으로 막힌 환경은 PUT 대신 `upload_attachment_chunk`로 MCP 호출만으로 올립니다. 원본을 순서대로 최대 512 KiB(디코딩 기준, `max_chunk_bytes`) 조각으로 나눠 표준 base64로 보냅니다.

     ```json
     {"project_id":"PROJECT_UUID","attachment_id":"ATTACHMENT_UUID","offset":0,"data_base64":"..."}
     ```

     응답의 `next_offset`을 다음 조각의 `offset`으로 씁니다. 마지막 조각이 도착하면 PUT과 같은 크기·실행 파일·악성코드 검사를 거쳐 버킷에 저장하고 `complete: true`와 `attachment`(`status: ready`, `sha256`)를 돌려줍니다. 같은 조각을 같은 데이터로 다시 보내는 것은 안전합니다(마지막 조각 재시도는 저장된 결과를 다시 반환). 순서가 틀리면 `INVALID_INPUT`, 이미 받은 위치에 다른 데이터를 보내면 `CONFLICT`입니다. 첫 조각을 받으면 그 파일의 `upload_url`은 무효가 됩니다. 조각은 저장이 끝나면 지우고, 1시간 안에 끝내지 못한 예약은 정리 작업이 조각과 함께 제거합니다. base64는 모델이 직접 출력해야 하므로 큰 파일에는 느리고 토큰 비용이 큽니다. PUT이 가능하면 PUT을 쓰세요.
3. PUT 응답의 `status: ready`와 SHA-256, 본문 추출 결과(`text_status`, `text_document_id`)를 확인합니다. 응답이 끊겼다면 `list_attachments`로 상태부터 확인하세요. 예약은 한 번만 사용되며 같은 ID에 다시 PUT해서 파일을 덮어쓸 수 없습니다. 실패한 경우 새 예약을 생성합니다. `prepare_attachment_upload` 자체는 멱등 호출이 아니므로 응답이 불확실할 때 목록부터 확인하세요.
4. `list_attachments`로 조회합니다. `project_id`, 선택적 `document_id`, `limit`(최대 100), `offset`을 지원합니다.
5. `get_attachment_download`에 `project_id`, `attachment_id`를 전달하면 5분 유효 다운로드 링크를 받습니다.
6. `delete_attachment`에 같은 식별자를 전달하면 접근을 차단하고 버킷 삭제 대기 상태로 바꿉니다. 진행 중인 업로드는 삭제할 수 없습니다.

## 질의·이슈 사진

`prepare_attachment_upload`의 `issue_id`로 질의·이슈에 이미지(PNG·JPG·GIF·WEBP, 장당 10 MiB)를 연결합니다. 업로드 때 실제 내용이 허용된 이미지인지 확인하고, 웹은 `GET /api/attachments/{id}/image`로 인라인 표시합니다. 자세한 사용법은 [이슈·질의 베이스](issues.md#사진-첨부).

## 저장과 권한

버킷 키는 서버가 만든 UUID 경로로 정하며 사용자 파일명을 경로로 사용하지 않습니다. 파일은 서버 임시 파일에 받아 길이를 확인한 후 전송하며 정상 종료 시 임시 파일을 지웁니다. 최대 업로드 요청 시간은 120초입니다. 업로드 이후 권한을 다시 확인한 다음 저장 완료를 확정합니다. 그 사이 권한이 취소되면 다운로드 가능 상태로 전환하지 않습니다.

원본은 항상 `application/octet-stream`, `Content-Disposition: attachment`로 내려받습니다. HTML 등 능동 콘텐츠를 앱 화면 안에서 실행하지 않습니다(HTML 미리보기는 스크립트 없는 샌드박스 표시만). PDF 미리보기와 OCR은 포함하지 않습니다(악성 파일 검사는 아래 "총량 제한과 악성 파일 검사" 참고). 텍스트 본문 추출은 아래 절을 참고하세요.

다운로드 링크는 유효 기간 동안 링크를 가진 사람이 사용할 수 있습니다. 발급 후 권한을 취소해도 최대 5분 동안 기존 링크가 남을 수 있습니다. 링크를 지식 본문이나 공개 기록에 저장하지 마세요. API 응답과 감사 로그에 버킷 키·비밀번호를 기록하지 않으며 감사 로그에는 파일 식별자와 작업 결과만 남깁니다.

## 실패 복구와 삭제

DB에 예약을 먼저 기록하고 버킷에 전송합니다. 파일 저장 후 서버가 중단되거나 응답을 잃어도 원본을 추적할 수 있습니다. 정리 작업은 매분 삭제 대기 파일과 1시간 이상 지난 미완료 업로드를 제거합니다. 버킷 삭제가 실패하면 DB 기록을 남겨 다음 실행에서 재시도합니다. 완료 파일은 이 정리 대상이 아닙니다. 버킷 연결을 해제하면 정리도 멈춥니다.

DB 트랜잭션과 버킷 PUT은 하나의 원자적 작업이 아닙니다. 중단된 파일은 위 정리 작업으로 수습합니다. 프로세스 강제 종료로 남은 운영체제 임시 파일은 컨테이너 재생성 시 정리됩니다. 서버 운영 시 임시 디스크 사용량을 관리하세요. 프로젝트 총량 제한과 악성 파일 검사는 아래 절을 따릅니다.

## 총량 제한과 악성 파일 검사

| 변수 | 기본값 / 의미 |
| --- | --- |
| `ATTACHMENT_PROJECT_QUOTA_MB` | `1024`. 프로젝트별 첨부 총량(MiB). `0`이면 제한 없음, 지정 시 25~1048576 |
| `CLAMAV_ADDRESS` | 비어 있으면 백신 검사 없음. `host:port`(예: Railway 내부 주소의 clamd 3310)를 지정하면 모든 업로드를 검사 |
| `ATTACHMENT_ALLOW_EXECUTABLES` | `false`. `true`면 아래 실행 파일 차단을 끔 |

- **총량**: 저장 완료 파일과 1시간 안의 진행 중 예약(`pending`·`uploading`)을 합산합니다. `prepare_attachment_upload`에서 초과하면 `QUOTA_EXCEEDED`(HTTP 413)입니다. 같은 프로젝트의 동시 예약은 프로젝트 행 잠금으로 순서대로 처리합니다. 사용량은 `get_attachment_usage`와 웹 첨부 목록에 표시됩니다.
- **실행 파일 차단(기본)**: `.exe .dll .scr .com .msi .msp .bat .cmd .ps1 .psm1 .vbs .vbe .jse .wsf .wsh .hta .lnk .pif .cpl .jar .apk .appx .msix .reg` 이름은 예약 단계에서, 이름을 바꾼 Windows PE·ELF·Mach-O 실행 파일은 업로드 본문 앞부분으로 거부합니다(`BLOCKED_FILE`, HTTP 422).
- **백신 검사(ClamAV)**: 앱이 임시 파일을 clamd `INSTREAM`으로 보내고, 깨끗하다는 응답을 받은 뒤에만 버킷에 저장합니다. 감염이면 `MALWARE_DETECTED`(422), clamd 연결 실패·오류·크기 초과면 `SCAN_UNAVAILABLE`(503)로 **저장하지 않습니다(fail closed)**. clamd의 `StreamMaxLength`는 26M 이상이어야 합니다(현재 clamd 기본값은 100M이며, 제공 설정은 26M으로 고정).
- 거부된 예약은 즉시 삭제 대기로 바뀌어 총량에서 빠집니다. 감사 로그 `attachment_rejected`에 사유와 백신 시그니처 이름만 남기며 파일명·내용은 남기지 않습니다.
- 검사는 새 업로드에만 적용됩니다. 기능 도입 전 저장된 파일은 다시 검사하지 않습니다.
- **검사 서비스 상태**: 첨부 화면의 연결 상태 카드와 `GET /api/attachments/storage`의 `scanner`가 clamd `PING` 결과를 `connected`·`not_configured`·`unavailable`로 보여 줍니다(주소는 응답하지 않음). `unavailable`이면 업로드 버튼이 비활성화됩니다.

### ClamAV 서비스 구성

| 환경 | 방법 |
| --- | --- |
| Railway | `.railway/deployment.local.json` 환경에 `"clamav": {"name": "ClamAV", "volumeName": "clamav-signatures"}`를 추가하거나 설정 생성기에 `--clamav`를 붙입니다. `railway config plan`/`apply` 시 clamd 서비스·1 GiB 시그니처 볼륨이 생기고 앱에 `CLAMAV_ADDRESS=${{ClamAV.RAILWAY_PRIVATE_DOMAIN}}:3310`이 연결됩니다. |
| 로컬 Compose | `.env`에 `CLAMAV_ADDRESS=clamav:3310`을 넣고 `docker compose --profile clamav up`. |

- 이미지 `clamav/clamav:1.4_base`(시그니처 미포함). 첫 시작 때 시그니처를 볼륨에 내려받은 뒤 clamd가 열리므로 **수 분간 `SCAN_UNAVAILABLE`** 입니다. 이후 재시작은 볼륨의 시그니처를 사용합니다.
- 설정은 이미지 진입점의 `CLAMD_CONF_<키>` 환경변수로 넣습니다: `TCPAddr ::`(Railway 내부망 IPv6), `StreamMaxLength 26M`, `ConcurrentDatabaseReload no`. freshclam은 하루 12회(`FRESHCLAM_CHECKS`) 갱신을 확인합니다.
- 메모리: 엔진 약 1.2~1.5 GiB. `ConcurrentDatabaseReload no`로 시그니처 갱신 중 메모리 2배 사용을 피하는 대신, 갱신 적용 동안(수십 초) 검사가 대기하며 앱 검사 제한 60초를 넘으면 `SCAN_UNAVAILABLE`입니다. Railway 사용량 요금이 늘어납니다.
- 이미지 진입점은 clamd가 비정상 종료돼도 컨테이너를 유지합니다. 첨부 화면 검사 상태가 계속 `unavailable`이면 ClamAV 서비스를 재배포합니다.
- 공개 도메인·TCP 프록시를 만들지 않습니다. clamd에는 인증이 없으므로 내부망에만 둡니다.

## 본문 추출과 검색

`.pdf`·`.docx`·`.xlsx` 파일은 버킷 저장 직후 서버가 본문 텍스트를 추출해 업로드한 사용자 이름으로 `kind: artifact`, `status: active` 문서 `첨부 본문: <파일명>`(태그 `첨부본문`, 형식)을 만듭니다. 이 문서는 일반 문서와 같은 권한·버전·청크·임베딩 대기열·검색(`search_knowledge`, `start_task`)을 그대로 사용합니다. 첨부에는 아래 값이 붙습니다(`018_attachment_text.sql`).

| 필드 | 의미 |
| --- | --- |
| `text_status` | `extracted`(문서 생성) · `empty`(추출할 텍스트 없음, 대개 스캔본 PDF) · `failed`(`text_error` 참고) · `none`(지원하지 않는 형식이거나 기능 꺼짐) |
| `text_error` | `INVALID_FILE`(손상·형식 불일치), `ENCRYPTED`(암호 걸린 파일), `TOO_LARGE`(압축 해제 한도·쪽수 초과), `TIMEOUT`(45초 초과), `EXTRACT_FAILED`(추출 프로세스 오류) |
| `text_document_id` | 추출 본문 문서 ID |
| `text_truncated` | 본문이 95,000자를 넘어 앞부분만 담았는지 |

- **형식별 내용**: PDF는 쪽마다 `## p.N` 제목 아래 줄 단위 텍스트, DOCX는 문단과 표(Markdown 표), XLSX는 시트마다 `## 시트: 이름` 아래 Markdown 표(첫 행을 머리글로 표시)입니다. 서식·이미지·차트·주석은 빠집니다. XLSX 수식은 계산된 저장 값으로 나옵니다. 날짜·시각 서식(기본 날짜 서식, `yyyy"년" m"월" d"일"` 같은 한국어 서식, 사용자 지정 서식)이 붙은 숫자 셀은 일련번호 대신 `2026-11-15`, `2026-11-15 14:30`, `09:05:00`처럼 바꿔 표시하며(1904 날짜 체계 포함, 초는 서식에 초가 있을 때만), 그 밖의 숫자 서식(통화·쉼표 등)은 원래 값을 그대로 둡니다. 숨김 시트도 포함합니다. DOCX의 변경 추적 삭제 문구는 제외합니다. 구형 `.doc`·`.xls`·`.hwp`, 스캔본 OCR은 지원하지 않습니다.
- **추출 실패는 업로드 실패가 아닙니다.** 원본은 `ready`로 저장되고 `text_status`만 `failed`/`empty`가 됩니다. 나중에 `reextract_attachment`로 다시 추출할 수 있습니다(아래 '다시 추출').
- **다시 추출**: `reextract_attachment`(편집 권한 필요)는 버킷에서 파일을 다시 읽어 지금의 추출기로 본문을 만듭니다. `attachment_id`를 주면 그 파일 하나(예: 옛 버전이 줄바꿈을 잘못 붙여 추출한 PDF)를, 생략하면 본문이 없거나(`none`: 기능 도입 전·기능을 꺼 둔 동안 올린 파일) `failed`인 `.pdf`·`.docx`·`.xlsx`를 `limit`개(기본 5, 최대 20)까지 오래 손대지 않은 순서로 다시 시도합니다. `more: true`면 같은 입력으로 다시 호출해 이어 갑니다. 본문 문서가 없으면 새로 만들고, 있으면 같은 문서에 새 버전으로 덮어쓰며(내용이 같으면 그대로 둠) 검색 색인은 기존 경로로 갱신됩니다. 다시 시도가 실패하거나 빈 본문이어도 이미 색인된 본문은 지우지 않습니다. 저장된 객체가 기록된 SHA-256·크기와 다르면 색인하지 않고 `CONFLICT`로 멈춥니다. 추출은 업로드와 마찬가지로 DB 트랜잭션 밖에서 하며 `ATTACHMENT_TEXT_EXTRACTION=false`이면 `CONFLICT`입니다. 스캔본 PDF는 여전히 `empty`입니다.
- **요약 문서와 자동 연결**: 파일을 문서에 연결해(`document_id`) 올렸다면 그 문서(요약 문서)와 추출 본문 문서를 `related` 관계(메모 `첨부 본문 자동 연결`, 만든 사람은 업로드·재추출한 사용자)로 묶습니다. 어느 쪽 문서에서든 `get_document`의 `links.related`·`get_document_links`로 서로를 찾을 수 있습니다. 관계는 메타데이터라 두 문서에 새 버전을 만들지 않습니다. 문서에 연결하지 않은 파일, 본문이 없는(`empty`·`failed`·`none`) 파일은 연결하지 않습니다. 같은 두 문서 사이에 이미 `related`가 있으면(방향 무관, 직접 건 것 포함) 그대로 둡니다. 이 기능 전에 추출된 본문은 `reextract_attachment`에 `attachment_id`를 주어 다시 추출하면 연결됩니다(내용이 같아 `unchanged`여도 연결). 재추출은 연결이 없을 때마다 다시 걸므로, 직접 끊은 연결도 그 파일을 재추출하면 되살아납니다.
- **삭제**: `delete_attachment`는 추출 본문 문서를 `archived`로 바꿔(새 버전 기록) 기본 검색에서 빠지게 합니다. 요약 문서와의 `related` 연결은 남겨 두어, 요약 문서에서 보관된 본문 문서(`status: archived`)를 확인할 수 있습니다.
- **격리**: 업로드 파일은 신뢰할 수 없는 입력이므로 추출은 서버 실행 파일을 `extract-text` 하위 명령으로 띄운 자식 프로세스에서 합니다. 자식은 서버 환경변수(DB 주소·버킷 키 등)를 받지 않고 파일 경로만 받으며, 45초가 지나면 강제 종료됩니다. Go 힙 소프트 한도 384 MiB(`GOMEMLIMIT`), 출력 상한, ZIP 항목 수(2만)·항목별 64 MiB·파일당 160 MiB 압축 해제 한도, PDF 3,000쪽 한도를 둡니다. XML 파서는 사용자 정의 엔티티를 확장하지 않고 외부 자원을 읽지 않습니다. 파서가 비정상 종료해도 서버는 영향을 받지 않습니다.
- **끄기**: `ATTACHMENT_TEXT_EXTRACTION=false`(기본 `true`). 기능 도입 전에 올린 파일은 업로드 때 추출하지 않으며 `reextract_attachment`로 나중에 추출합니다.
- 감사 로그·서버 로그에는 추출 결과 코드와 형식만 남기며 파일명·본문은 남기지 않습니다.

## 설치

[Railway 설치 안내](../.railway/README.md)의 버킷 변수와 `006_attachments.sql` 마이그레이션, 새 앱 배포가 필요합니다. 버킷만 만들거나 연결 변수만 넣어서는 기존 앱에 첨부 화면이 나타나지 않습니다. 새 프로젝트 설치용 준비 파일은 실제 배포 시점에 적용합니다.

## 검증 기록 — 2026-11-14

실제 PostgreSQL 통합·동시성·권한·실패 복구 테스트와 전체 `go test -race ./...`, 정적 검사, 빌드가 통과했습니다. 내부 화면을 로컬에서 확인하며 목록 요청의 페이지 크기 오류를 수정했습니다. 사용자 승인 후 운영 Attachments 버킷에 비민감 테스트 파일 하나를 전송하고 내용·다운로드 헤더·삭제 후 404 응답을 확인했습니다. 기존 파일·운영 DB는 검사에서 변경하지 않았습니다. 새 앱의 운영 배포와 실제 사용자 화면에서의 전체 흐름 검사는 별도입니다.
