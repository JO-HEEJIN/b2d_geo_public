// b2d-mcp는 b2d_geo HTTP API를 MCP(Model Context Protocol) 도구로 노출하는
// 얇은 래퍼다. 비즈니스 로직은 없다: 파라미터 검증과 기존 API 호출 전달이 전부이고,
// 판정은 전부 서버(https://api.birth2death.com)가 한다.
//
// 실행: B2D_API_KEY=<키> b2d-mcp            (stdio, Claude Desktop/Cursor용)
//
//	B2D_API_KEY=<키> b2d-mcp -http :8330 (streamable HTTP, POST /mcp)
//
// 프로토콜은 JSON-RPC 2.0이며 stdio 전송은 줄 단위 JSON이다. 외부 의존성 없이
// 표준 라이브러리만 쓴다(단순성 원칙 1.6 — MCP 서버 subset은 initialize,
// tools/list, tools/call, ping뿐이라 SDK 없이 충분하다).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultAPIBase = "https://api.birth2death.com/v1"
	serverName     = "b2d-geo"
	serverVersion  = "0.1.0"
	pricingURL     = "https://api.birth2death.com/pricing.html"
)

// supportedProtocols는 응답 가능한 MCP 프로토콜 버전이다. 클라이언트가 이 중
// 하나를 요청하면 그대로 돌려주고, 모르는 버전이면 최신을 제안한다.
// 구현 기준 리비전: 2025-06-18 스펙(stdio 줄 단위 + streamable HTTP).
// 클라이언트 호환 문제가 실제로 발생하면 공식 SDK 전환 검토(OPS.md 부채 기록).
var supportedProtocols = map[string]bool{
	"2024-11-05": true, "2025-03-26": true, "2025-06-18": true,
}

const latestProtocol = "2025-06-18"

func main() {
	httpAddr := flag.String("http", "", "streamable HTTP 리슨 주소 (예 :8330). 비면 stdio")
	flag.Parse()
	log.SetOutput(os.Stderr) // stdout은 프로토콜 전용

	s := &mcpServer{
		apiBase: strings.TrimRight(envOr("B2D_API_BASE", defaultAPIBase), "/"),
		apiKey:  os.Getenv("B2D_API_KEY"),
		client:  &http.Client{Timeout: 15 * time.Second},
	}
	if s.apiKey == "" {
		log.Println("warning: B2D_API_KEY is empty; API calls will fail with 401")
	}
	if *httpAddr != "" {
		s.serveHTTP(*httpAddr)
		return
	}
	s.serveStdio()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type mcpServer struct {
	apiBase string
	apiKey  string
	client  *http.Client
}

// ---- JSON-RPC 배선 ----

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// serveStdio는 stdin에서 줄 단위 JSON-RPC를 읽어 stdout으로 응답한다.
func (s *mcpServer) serveStdio() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if resp := s.handleMessage([]byte(line), s.apiKey); resp != nil {
			if err := out.Encode(resp); err != nil {
				log.Println("stdout write failed:", err)
				return
			}
		}
	}
}

// serveHTTP는 streamable HTTP 전송이다. POST /mcp에 JSON-RPC 메시지 하나를 받아
// JSON으로 응답한다(알림이면 202). 세션 상태는 없다(stateless).
// 인증은 호출자의 Authorization(Bearer) 또는 X-API-Key 헤더를 상류 API에 그대로
// 전달한다 — 공개 원격 엔드포인트가 서버 env 키를 대신 쓰면 인증 우회가 되므로,
// env B2D_API_KEY는 헤더가 없을 때의 폴백(사설 배치 전용)일 뿐이다.
func (s *mcpServer) serveHTTP(addr string) {
	mux := http.NewServeMux()
	// /mcp/{key}는 키 내장 URL이다: 헤더를 못 붙이는 클라이언트가 URL 하나로
	// 연결한다. 키 우선순위는 헤더 > 경로 > env(폴백). 키가 URL에 실리므로
	// 프록시(Caddy)에서 /mcp 계열 접근 로그를 남기지 않는 것이 전제다.
	handler := func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
		if err != nil {
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}
		key := s.apiKey
		if p := r.PathValue("key"); p != "" {
			key = p
		}
		if h := r.Header.Get("X-API-Key"); h != "" {
			key = h
		} else if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			key = strings.TrimSpace(a[len("Bearer "):])
		}
		resp := s.handleMessage(body, key)
		if resp == nil { // 알림: 응답 본문 없음
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
	mux.HandleFunc("POST /mcp", handler)
	mux.HandleFunc("POST /mcp/{key}", handler)
	log.Println("b2d-mcp streamable http listening on", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// handleMessage는 JSON-RPC 메시지 하나를 처리한다. 알림(id 없음)이면 nil을 돌려준다.
// apiKey는 이 메시지의 상류 호출에 쓸 키다(stdio는 env, HTTP는 요청 헤더).
func (s *mcpServer) handleMessage(raw []byte, apiKey string) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}
	}
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil
	}
	var result any
	var rerr *rpcError
	switch req.Method {
	case "initialize":
		result = s.initialize(req.Params)
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": toolDefs}
	case "tools/call":
		result, rerr = s.toolsCall(req.Params, apiKey)
	default:
		rerr = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	if isNotification {
		return nil
	}
	if rerr != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rerr}
	}
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (s *mcpServer) initialize(params json.RawMessage) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	json.Unmarshal(params, &p)
	ver := latestProtocol
	if supportedProtocols[p.ProtocolVersion] {
		ver = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": ver,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
	}
}

// ---- 도구 정의 ----
// description은 에이전트가 읽는 텍스트다: 어떤 질문에 이 도구를 쓰는지 한국어+영어 병기.

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func schema(required []string, props map[string]any) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

var toolDefs = []toolDef{
	{
		Name: "geocode",
		Description: "한국 주소(도로명 또는 지번)를 좌표로 변환한다. \"이 주소가 어디야?\", \"주소를 좌표로\" 류 질문에 사용. " +
			"Geocode a Korean address (road or jibun) to WGS84 coordinates. Returns matched address, coordinates [lon, lat], and bcode.",
		InputSchema: schema([]string{"query"}, map[string]any{
			"query": map[string]any{"type": "string", "description": "주소 문자열, 예: 서울특별시 중구 세종대로 110 / Korean address string"},
		}),
	},
	{
		Name: "reverse_geocode",
		Description: "좌표를 가장 가까운 도로명주소로 변환한다. \"이 좌표가 어디야?\" 류 질문에 사용. " +
			"Reverse-geocode WGS84 coordinates to the nearest Korean road address.",
		InputSchema: schema([]string{"lon", "lat"}, map[string]any{
			"lon": map[string]any{"type": "number", "description": "경도 / longitude"},
			"lat": map[string]any{"type": "number", "description": "위도 / latitude"},
		}),
	},
	{
		Name: "search_places",
		Description: "상호명으로 장소(상가)를 검색한다. 초성 검색 지원(예: ㅅㅌㅂㅅ). 좌표+반경을 주면 주변 검색. " +
			"\"근처 카페 찾아줘\", \"OO역 주변 상권\" 류 질문에 사용. " +
			"Search Korean places (stores) by name, with choseong (initial-consonant) support and optional lon/lat/radius filter.",
		InputSchema: schema(nil, map[string]any{
			"query":    map[string]any{"type": "string", "description": "상호명 또는 초성 / place name or choseong"},
			"category": map[string]any{"type": "string", "description": "업종 카테고리 필터 / category filter"},
			"lon":      map[string]any{"type": "number"},
			"lat":      map[string]any{"type": "number"},
			"radius":   map[string]any{"type": "integer", "description": "반경 m / radius in meters"},
			"limit":    map[string]any{"type": "integer"},
		}),
	},
	{
		Name: "get_parcel",
		Description: "필지(토지) 정보를 조회한다. pnu(19자리) / 좌표(lon,lat) / 법정동코드+지번(bcode,jibun) 중 하나로 조회. " +
			"\"이 땅의 지목·면적은?\", \"이 좌표의 필지는?\" 류 질문에 사용. 반환된 pnu는 공시지가·용도지역·토지특성 조회에 쓴다. " +
			"Look up a land parcel by 19-digit PNU, by point (lon/lat), or by legal-dong code + jibun. The returned pnu feeds the land value, zoning, and features tools.",
		InputSchema: schema(nil, map[string]any{
			"pnu":   map[string]any{"type": "string", "description": "19자리 필지고유번호 / 19-digit parcel id"},
			"lon":   map[string]any{"type": "number"},
			"lat":   map[string]any{"type": "number"},
			"bcode": map[string]any{"type": "string", "description": "법정동코드 10자리 / 10-digit legal dong code"},
			"jibun": map[string]any{"type": "string", "description": "지번, 예: 110-1 / jibun number"},
			"san":   map[string]any{"type": "boolean", "description": "산 지번 여부 / mountain jibun"},
		}),
	},
	{
		Name: "get_land_value",
		Description: "필지의 공시지가(표준지 우선, 없으면 개별)와 토지특성(이용상황·지형·도로접면)을 함께 조회한다. " +
			"\"이 땅 공시지가 얼마야?\", \"토지 특성은?\" 류 질문에 사용. 개별공시지가·토지특성은 서울·경기 우선 적재. " +
			"Get official land value (standard lot first, individual fallback) and land features for a parcel. Individual prices and features currently cover Seoul and Gyeonggi first.",
		InputSchema: schema([]string{"pnu"}, map[string]any{
			"pnu":       map[string]any{"type": "string", "description": "19자리 필지고유번호 / 19-digit parcel id"},
			"base_year": map[string]any{"type": "integer", "description": "기준연도(선택) / base year (optional)"},
		}),
	},
	{
		Name: "get_transactions",
		Description: "토지 실거래가를 조회한다(국토교통부). pnu 또는 시군구코드(lawd_cd 5자리)와 거래연월(deal_ymd, YYYYMM)로 조회. " +
			"\"이 동네 실거래가\", \"최근 거래\" 류 질문에 사용. " +
			"Get real land transaction prices (MOLIT). Query by pnu or 5-digit district code plus deal month (YYYYMM).",
		InputSchema: schema([]string{"deal_ymd"}, map[string]any{
			"pnu":      map[string]any{"type": "string", "description": "19자리 필지고유번호(앞 5자리를 시군구코드로 사용) / pnu, first 5 digits used as district"},
			"lawd_cd":  map[string]any{"type": "string", "description": "시군구코드 5자리 / 5-digit district code"},
			"deal_ymd": map[string]any{"type": "string", "description": "거래연월 YYYYMM / deal month"},
		}),
	},
	{
		Name: "get_zoning",
		Description: "필지의 용도지역·지구·구역(토지이용계획)을 조회한다. 포함/저촉/접함 관계를 원자료 그대로 반환. " +
			"\"이 땅 용도지역이 뭐야?\", \"개발 가능한 땅이야?\"의 사실 근거 조회에 사용. " +
			"Get zoning (use zones, districts) for a parcel from the national land-use plan, with inclusion/conflict/adjacency relations as recorded.",
		InputSchema: schema([]string{"pnu"}, map[string]any{
			"pnu": map[string]any{"type": "string", "description": "19자리 필지고유번호 / 19-digit parcel id"},
		}),
	},
}

// ---- 도구 실행 ----

type toolResult struct {
	Content []map[string]any `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

func textResult(text string, isErr bool) *toolResult {
	return &toolResult{Content: []map[string]any{{"type": "text", "text": text}}, IsError: isErr}
}

func (s *mcpServer) toolsCall(params json.RawMessage, apiKey string) (any, *rpcError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	a := args(p.Arguments)
	switch p.Name {
	case "geocode":
		if a.str("query") == "" {
			return textResult("query가 필요합니다. 예: 서울특별시 중구 세종대로 110", true), nil
		}
		return s.get(apiKey, "/geocode", url.Values{"q": {a.str("query")}}), nil
	case "reverse_geocode":
		if !a.has("lon") || !a.has("lat") {
			return textResult("lon, lat가 필요합니다.", true), nil
		}
		return s.get(apiKey, "/reverse", url.Values{"lon": {a.num("lon")}, "lat": {a.num("lat")}}), nil
	case "search_places":
		q := url.Values{}
		a.copyStr(q, "query", "q")
		a.copyStr(q, "category", "category")
		a.copyNum(q, "lon", "lon")
		a.copyNum(q, "lat", "lat")
		a.copyNum(q, "radius", "radius")
		a.copyNum(q, "limit", "limit")
		if len(q) == 0 {
			return textResult("query 또는 좌표(lon, lat)가 필요합니다.", true), nil
		}
		return s.get(apiKey, "/places/search", q), nil
	case "get_parcel":
		switch {
		case a.str("pnu") != "":
			return s.get(apiKey, "/parcel/"+url.PathEscape(a.str("pnu")), nil), nil
		case a.has("lon") && a.has("lat"):
			return s.get(apiKey, "/parcel/by-point", url.Values{"lon": {a.num("lon")}, "lat": {a.num("lat")}}), nil
		case a.str("bcode") != "" && a.str("jibun") != "":
			q := url.Values{"bcode": {a.str("bcode")}, "jibun": {a.str("jibun")}}
			if a.boolVal("san") {
				q.Set("san", "true")
			}
			return s.get(apiKey, "/parcel/by-jibun", q), nil
		default:
			return textResult("pnu, 좌표(lon+lat), 법정동코드+지번(bcode+jibun) 중 하나가 필요합니다. 주소만 안다면 먼저 geocode로 좌표를 얻으세요.", true), nil
		}
	case "get_land_value":
		pnu := a.str("pnu")
		if pnu == "" {
			return textResult("pnu(19자리)가 필요합니다. get_parcel로 먼저 필지를 찾으세요.", true), nil
		}
		q := url.Values{"pnu": {pnu}}
		a.copyNum(q, "base_year", "base_year")
		price := s.get(apiKey, "/appraisal/official-price", q)
		features := s.get(apiKey, "/appraisal/land-features", q)
		// 두 사실을 한 응답으로 합친다(가공 없음). 한쪽 실패는 그 파트에 그대로 표시.
		combined := "official_price:\n" + firstText(price) + "\n\nland_features:\n" + firstText(features)
		return textResult(combined, price.IsError && features.IsError), nil
	case "get_transactions":
		lawd := a.str("lawd_cd")
		if lawd == "" && len(a.str("pnu")) >= 5 {
			lawd = a.str("pnu")[:5]
		}
		if lawd == "" || a.str("deal_ymd") == "" {
			return textResult("deal_ymd(YYYYMM)와 lawd_cd(시군구 5자리) 또는 pnu가 필요합니다.", true), nil
		}
		return s.get(apiKey, "/appraisal/sales", url.Values{"lawd_cd": {lawd}, "deal_ymd": {a.str("deal_ymd")}}), nil
	case "get_zoning":
		if a.str("pnu") == "" {
			return textResult("pnu(19자리)가 필요합니다. get_parcel로 먼저 필지를 찾으세요.", true), nil
		}
		return s.get(apiKey, "/appraisal/land-use", url.Values{"pnu": {a.str("pnu")}}), nil
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
}

func firstText(r *toolResult) string {
	if len(r.Content) > 0 {
		if t, ok := r.Content[0]["text"].(string); ok {
			return t
		}
	}
	return ""
}

// get은 기존 HTTP API를 호출해 응답 body를 그대로 도구 결과로 만든다.
// 에러 응답은 에이전트가 사용자에게 그대로 전달할 수 있는 문장으로 바꾼다
// (MCP에서는 에러 메시지가 곧 안내 채널이다).
func (s *mcpServer) get(apiKey, path string, q url.Values) *toolResult {
	u := s.apiBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return textResult("요청 생성 실패: "+err.Error(), true)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return textResult("b2d_geo API에 연결하지 못했습니다: "+err.Error(), true)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return textResult("응답 읽기 실패: "+err.Error(), true)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return textResult(string(body), false)
	}
	return textResult(humanError(resp.StatusCode, body), true)
}

// humanError는 API 에러를 사용자 전달용 문장으로 바꾼다.
func humanError(status int, body []byte) string {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(body, &e)
	switch e.Error.Code {
	case "monthly_cap_exceeded":
		return "이번 달 호출 한도에 도달했습니다. 다음 달(UTC 기준)에 자동으로 풀립니다. 업그레이드: " + pricingURL +
			" (This API key reached its monthly quota; it resets next month UTC. Upgrade at the pricing page.)"
	case "rate_limited":
		return "요청 속도 제한에 걸렸습니다. 잠시 후 다시 시도하세요. (Rate limited; retry shortly.)"
	case "REGION_NOT_LOADED":
		return "이 지역의 데이터는 아직 적재되지 않았습니다. 개별공시지가·토지특성은 현재 서울·경기 우선 적재이며, 계약 시 필요 지역을 우선 적재합니다. 문의: " + pricingURL +
			" (Data for this region is not loaded yet; Seoul and Gyeonggi are loaded first. Other data tools still work nationwide.)"
	case "unauthorized":
		return "API 키가 없거나 유효하지 않습니다. 환경변수 B2D_API_KEY를 확인하세요. 키 발급: " + pricingURL +
			" (Missing or invalid B2D_API_KEY.)"
	case "forbidden":
		return "이 도구는 realestate scope가 있는 키가 필요합니다. 키 업그레이드: " + pricingURL +
			" (This tool needs an API key with the realestate scope.)"
	}
	if e.Error.Message != "" {
		return fmt.Sprintf("API 오류 (%d, %s): %s", status, e.Error.Code, e.Error.Message)
	}
	return fmt.Sprintf("API 오류 (%d): %s", status, strings.TrimSpace(string(body)))
}

// ---- 인자 헬퍼 ----

type args map[string]any

func (a args) has(k string) bool { _, ok := a[k]; return ok }

func (a args) str(k string) string {
	if v, ok := a[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// num은 JSON 숫자(float64) 또는 숫자 문자열을 쿼리 문자열로 만든다.
func (a args) num(k string) string {
	switch v := a[k].(type) {
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case string:
		return strings.TrimSpace(v)
	}
	return ""
}

func (a args) boolVal(k string) bool {
	v, _ := a[k].(bool)
	return v
}

func (a args) copyStr(q url.Values, from, to string) {
	if s := a.str(from); s != "" {
		q.Set(to, s)
	}
}

func (a args) copyNum(q url.Values, from, to string) {
	if a.has(from) {
		if s := a.num(from); s != "" {
			q.Set(to, s)
		}
	}
}
