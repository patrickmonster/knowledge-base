# 문서 속 디자인 시안 미리보기

지식 문서에 적은 HTML(또는 JSX) 코드 블록을 웹 화면에서 실제 렌더링된 시안으로 볼 수 있습니다.

## 쓰는 법

코드 블록 언어 뒤에 `preview`를 붙입니다.

````markdown
```html preview css=site.css width=768 theme=dark
<header class="fixed top-0 w-full ...">…</header>
```
````

| 속성 | 뜻 |
| --- | --- |
| `preview` | 미리보기로 표시합니다(없으면 보통 코드 블록). |
| `css=<파일명>` | 같은 프로젝트에 올린 `.css` 첨부(준비 완료, 같은 이름이 여럿이면 최신)를 페이지에 넣습니다. Tailwind는 빌드된 CSS(커스텀 색·테마 포함)를 첨부로 올려 지정합니다. 2 MiB 이하. |
| `width=375\|768\|1280` | 처음 보일 화면 폭. 화면의 버튼으로 바꿀 수 있습니다. |
| `theme=dark` | `<html class="dark">`로 시작합니다(Tailwind `darkMode: 'class'`). 버튼으로 전환합니다. |

- 언어는 `html`·`htm`·`jsx`·`tsx`. JSX는 `className`→`class`, `htmlFor`→`for`, `{/* */}` 주석 제거, ``className={`a ${조건}`}``의 표현식 제거만 하는 단순 변환입니다. 컴포넌트(`<Logo />` 등)·상태·이벤트는 그리지 않으므로 정적 마크업으로 적습니다.
- 블록이 화면에 보일 때 불러오고, 미리보기/코드 탭, 폭(전체·1280·768·375), 라이트/다크, 새로고침 버튼이 있습니다. 미리보기 영역은 아래 모서리를 끌어 높이를 바꿀 수 있습니다.
- 지식 문서 보기, 검토 대기, 지식 검색, 벡터 탐색, 첨부 추출 본문에서 동작합니다. 이슈 본문에서는 `css=`를 쓸 수 없습니다.

## 보안

- 브라우저가 블록 원문을 같은 출처 폼 POST(`/api/design-preview`, CSRF 토큰은 폼 필드)로 보내고, 서버가 감싼 페이지를 iframe에 띄웁니다. 웹 세션만 받으며 MCP 키는 받지 않습니다. 요청 출처는 `Origin`이 공개 주소와 같거나, `Referrer-Policy: no-referrer` 때문에 `Origin: null`이면 `Sec-Fetch-Site: same-origin`일 때만 받습니다.
- 응답 CSP: `default-src 'none'; style-src 'unsafe-inline'; img-src data: https:; font-src data: https:; sandbox; base-uri 'none'; form-action 'none'; frame-ancestors 'self'`. iframe에도 `sandbox`(허용 없음)를 겁니다. 스크립트·이벤트 핸들러는 실행되지 않고, 문서 페이지·쿠키·API에 접근할 수 없으며, 같은 출처에서만 프레임으로 넣을 수 있습니다(이 응답만 `X-Frame-Options: SAMEORIGIN`, 나머지는 기존 `DENY` 유지).
- 이미지는 `data:`와 `https:`만 불러옵니다. 상대 경로(`logo.png`)는 표시되지 않습니다. 외부 https 이미지를 넣으면 보는 사람의 브라우저가 그 주소에 접속합니다.
- 코드 블록은 256 KiB 이하. CSS 첨부는 프로젝트 읽기 권한을 확인하고 `design_preview_css` 감사 이벤트를 남깁니다. 요청 한도는 웹 API와 같은 사용자당 분당 240회입니다.
