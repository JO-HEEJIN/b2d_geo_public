# b2d-geo MCP adapter

Claude Desktop, Cursor 같은 AI 에이전트에서 "이 주소 주변 상권이랑 실거래가 분석해줘"
한 문장으로 b2d-geo를 쓰게 해주는 MCP adapter다. 도구 7종: geocode, reverse_geocode,
search_places, get_parcel, get_land_value, get_transactions, get_zoning.

## 1. 설치 (복사-붙여넣기)

Go 1.26.4+가 있으면 이 저장소에서 바로 설치할 수 있다:

```bash
go install github.com/JO-HEEJIN/b2d_geo_public/cmd/b2d-mcp@latest
```

설치된 바이너리 이름은 `b2d-mcp`다.

호스팅 API는 발급된 API 키가 필요하다. 운영자가 공개 데모 키를 활성화한 경우
https://api.birth2death.com/docs.html 에서 제한된 범위로 바로 시험할 수 있다.

## 2. 설정 (복사-붙여넣기)

### Claude Desktop

`claude_desktop_config.json`에 추가:

```json
{
  "mcpServers": {
    "b2d-geo": {
      "command": "b2d-mcp",
      "env": { "B2D_API_KEY": "발급받은_키" }
    }
  }
}
```

### Cursor

`.cursor/mcp.json`에 추가:

```json
{
  "mcpServers": {
    "b2d-geo": {
      "command": "b2d-mcp",
      "env": { "B2D_API_KEY": "발급받은_키" }
    }
  }
}
```

설정 후 앱을 재시작하면 도구 7개가 잡힌다.

## 3. 이렇게 물어보면 된다

- "서울특별시 중구 세종대로 110 주변 1km 카페 찾고, 그 필지 공시지가도 알려줘"
- "경기도 하남시 대청로 10 땅의 용도지역이랑 2026년 5월 그 동네 토지 실거래가 보여줘"
- "좌표 126.9779, 37.5663이 어디야? 그 필지의 토지특성(지형, 도로접면)은?"

## 원격 사용 (설치 없이)

Claude Code(터미널)는 한 줄이면 된다:

```bash
claude mcp add --transport http b2d-geo https://api.birth2death.com/mcp --header "Authorization: Bearer 발급받은_키"
```

원격 MCP를 지원하는 다른 클라이언트는 설정에 붙여넣는다:

```json
{
  "mcpServers": {
    "b2d-geo": {
      "url": "https://api.birth2death.com/mcp",
      "headers": { "Authorization": "Bearer 발급받은_키" }
    }
  }
}
```

헤더를 못 붙이고 URL만 받는 도구는 키 내장 URL을 쓴다
(키가 URL에 들어가니 화면 공유·공개 커밋 주의):

```
https://api.birth2death.com/mcp/발급받은_키
```

https://api.birth2death.com/docs.html 은 운영자가 공개 데모 키를 활성화한 경우
위 설정 예시를 채워서 보여준다.

## 참고

- 인증: 로컬(stdio)은 환경변수 `B2D_API_KEY`, 원격(HTTP)은 요청의
  Authorization Bearer 또는 X-API-Key 헤더가 상류 API로 전달된다.
- 자가 호스팅 원격 모드: `b2d-mcp -http :8330` → `POST /mcp` (stateless HTTP).
- MCP 프로토콜: 2025-06-18 리비전 기준 구현 (2024-11-05, 2025-03-26 협상 지원).
- 개별공시지가·토지특성은 서울·경기 우선 적재. 미적재 지역은 도구가 안내 문장을
  돌려주며, 각 도구의 실제 범위는 운영 DB에 적재된 데이터셋을 따른다.
- API 전체 문서: https://api.birth2death.com/docs.html
