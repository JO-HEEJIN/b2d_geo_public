// Package api는 HTTP 핸들러를 제공한다. 이 파일은 부동산 특화 최소 서버로,
// 인증/rate limit/사용량 계측 미들웨어는 Phase 4에서 추가한다.
// 절대 경계(Phase 7 지시서): 사실 반환만 — 보정/평가/추천 로직 금지.
package api

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JO-HEEJIN/b2d_geo_public/internal/auth"
	"github.com/JO-HEEJIN/b2d_geo_public/internal/geocode"
	"github.com/JO-HEEJIN/b2d_geo_public/internal/meter"
	"github.com/JO-HEEJIN/b2d_geo_public/internal/parcel"
	"github.com/JO-HEEJIN/b2d_geo_public/internal/places"
	"github.com/JO-HEEJIN/b2d_geo_public/internal/revgeo"
	"github.com/JO-HEEJIN/b2d_geo_public/web"
)

// Server는 최소 API 서버다.
type Server struct {
	Pool     *pgxpool.Pool
	MolitKey string // 토지매매 실거래 (data.go.kr)
	Client   *http.Client
}

// New는 라우팅이 구성된 http.Handler와, 종료 시 계측기를 드레인하는
// close 함수를 돌려준다.
func New(pool *pgxpool.Pool, molitKey string) (http.Handler, func()) {
	s := &Server{Pool: pool, MolitKey: molitKey,
		Client: &http.Client{Timeout: 15 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /v1/usage", s.usageHandler)
	mux.HandleFunc("GET /v1/geocode", s.geocodeHandler)
	mux.HandleFunc("POST /v1/geocode/batch", s.geocodeBatchHandler)
	mux.HandleFunc("GET /v1/reverse", s.reverseHandler)
	mux.HandleFunc("GET /v1/places/search", s.placesSearchHandler)
	mux.HandleFunc("GET /v1/parcel/by-point", s.parcelByPoint)
	mux.HandleFunc("GET /v1/parcel/by-jibun", s.parcelByJibun)
	mux.HandleFunc("GET /v1/parcel/{pnu}", s.parcelByPNU)
	mux.HandleFunc("GET /v1/appraisal/sales", s.appraisalSales)
	mux.HandleFunc("GET /v1/appraisal/price-index", s.appraisalPriceIndex)
	mux.HandleFunc("GET /v1/appraisal/standard-lots", s.appraisalStandardLots)
	mux.HandleFunc("GET /v1/appraisal/official-price", s.appraisalOfficialPrice)
	mux.HandleFunc("GET /v1/appraisal/land-use", s.appraisalLandUse)
	mux.HandleFunc("GET /v1/appraisal/land-features", s.appraisalLandFeatures)

	// Phase 4 미들웨어 체인: IP rate limit → auth → key rate limit → meter
	// → handler. IP 제한이 최외곽이라 미인증 플러드도 막는다 (QA MED-1).
	// (/v1/health는 auth·rate limit 미들웨어 내부에서 제외한다.)
	authr := auth.NewAuthenticator(pool)
	limiter := auth.NewRateLimiter()
	m := meter.New(pool)
	apiChain := limiter.IPMiddleware(authr.Middleware(limiter.Middleware(m.Middleware(mux))))

	// /v1/*는 인증·계측 체인을 타고, 그 외 경로는 임베드된 정적 사이트(랜딩·지도
	// 데모·API 문서)를 same-origin으로 서빙한다. 프론트가 API와 같은 오리진이라
	// CORS가 불필요하고, 바이너리 하나로 사이트+API를 함께 배포한다.
	root := http.NewServeMux()
	// 요청 ID를 최외곽에 두어 rate limit·auth가 만든 에러 응답에도 ID가 붙는다.
	root.Handle("/v1/", requestIDMiddleware(apiChain))
	// 프론트에 API 베이스와 데모 키를 런타임에 주입한다. 데모 키는 저장소가 아닌
	// 환경변수(B2D_DEMO_API_KEY)에 두어 git에 시크릿이 남지 않게 한다. 이 키는
	// 공개 데모용(좁은 scope + 낮은 rate limit)이라 노출을 전제로 운용한다.
	root.HandleFunc("GET /config.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"apiBase":   "/v1",
			"demoKey":   os.Getenv("B2D_DEMO_API_KEY"),
			"selfServe": os.Getenv("B2D_SELF_SERVE_DEMO") == "true",
		})
	})
	// 셀프 데모키 발급(선택 기능). 운영자가 B2D_SELF_SERVE_DEMO=true로 켤 때만
	// 노출한다. geo scope + 낮은 rate의 무료 티어 키를 만들어 개발자 온보딩을 돕는다
	// (감정평가 계열은 realestate scope가 필요한 유료 티어). IP rate limit(최외곽)로
	// 남용을 막지만, 공개 키 발급은 본질적으로 DB에 행을 쌓으므로 데모/미리보기
	// 환경에서만 켜는 것을 전제로 한다.
	if os.Getenv("B2D_SELF_SERVE_DEMO") == "true" {
		root.Handle("POST /issue-demo-key", limiter.IPMiddleware(http.HandlerFunc(s.issueDemoKey)))
	}
	root.Handle("/", http.FileServerFS(web.Assets))
	return secureHeaders(root), m.Close
}

// secureHeaders는 모든 응답(API·정적)에 기본 보안 헤더를 붙인다. 지도 타일(외부
// 이미지)과 index.html의 소형 인라인 스크립트를 깨뜨리지 않도록 엄격한 CSP는 두지
// 않고, 안전한 기본값만 설정한다.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// issueDemoKey는 geo scope + 낮은 rate의 무료 데모 키를 발급한다. 평문은 이 응답에서
// 1회만 노출된다(DB에는 해시만 저장). B2D_SELF_SERVE_DEMO=true일 때만 라우팅된다.
func (s *Server) issueDemoKey(w http.ResponseWriter, r *http.Request) {
	const rateRPS = 2
	id, plaintext, prefix, err := auth.CreateAPIKey(r.Context(), s.Pool, "self-serve-demo", []string{"geo"}, rateRPS, "demo", 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "key issue failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":             id,
		"key":            plaintext,
		"prefix":         prefix,
		"scopes":         []string{"geo"},
		"rate_limit_rps": rateRPS,
		"note":           "geo scope(지오코딩·필지)만 포함. 감정평가 계열은 realestate scope 유료 키가 필요합니다.",
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// hasRegion은 pnu 앞5자리(시군구) 프리픽스가 table에 적재됐는지 확인한다. substr(pnu,1,5)=..
// 는 인덱스를 못 써 부재 지역 조회가 대용량 테이블 전체스캔(타임아웃)이 되므로, pnu PK
// 범위 조건으로 Index Only Scan을 탄다. table은 호출부의 컴파일 상수만 전달한다(SQLi 무관).
func (s *Server) hasRegion(ctx context.Context, table, pnu5 string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE pnu >= $1 AND pnu <= $2)`,
		pnu5+"00000000000000", pnu5+"99999999999999").Scan(&ok)
	return ok, err
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.Pool.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "db unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func wantGeom(r *http.Request) bool { return r.URL.Query().Get("geometry") == "true" }

func (s *Server) respondParcel(w http.ResponseWriter, p *parcel.Parcel, err error) {
	switch {
	case err == pgx.ErrNoRows:
		writeErr(w, http.StatusNotFound, "parcel not found")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "query failed")
	default:
		writeJSON(w, http.StatusOK, p)
	}
}

func (s *Server) parcelByPNU(w http.ResponseWriter, r *http.Request) {
	pnu := r.PathValue("pnu")
	if len(pnu) != 19 {
		writeErr(w, http.StatusBadRequest, "pnu must be 19 digits")
		return
	}
	p, err := parcel.ByPNU(r.Context(), s.Pool, pnu, wantGeom(r))
	s.respondParcel(w, p, err)
}

func (s *Server) parcelByPoint(w http.ResponseWriter, r *http.Request) {
	lon, err1 := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	lat, err2 := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "lon, lat required")
		return
	}
	p, err := parcel.ByPoint(r.Context(), s.Pool, lon, lat, wantGeom(r))
	s.respondParcel(w, p, err)
}

func (s *Server) parcelByJibun(w http.ResponseWriter, r *http.Request) {
	bcode, jibun := r.URL.Query().Get("bcode"), r.URL.Query().Get("jibun")
	if len(bcode) != 10 || jibun == "" {
		writeErr(w, http.StatusBadRequest, "bcode(10 digits), jibun required")
		return
	}
	p, err := parcel.ByJibun(r.Context(), s.Pool, bcode, jibun,
		r.URL.Query().Get("san") == "true", wantGeom(r))
	s.respondParcel(w, p, err)
}

// geocodeHandler는 도로명주소 정확 매칭 v0 (Phase 2에서 파서/폴백 확장).
func (s *Server) geocodeHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 5 {
		writeErr(w, http.StatusBadRequest, "q (road address) required")
		return
	}
	res, err := geocode.Search(r.Context(), s.Pool, q)
	switch {
	case err == geocode.ErrAmbiguous:
		// point 필드 제외: 후보 미확정 상태에 [0,0] 좌표를 내보내지 않는다
		// (환각 좌표 금지 — QA MED-2)
		writeJSON(w, http.StatusOK, map[string]any{
			"match": "ambiguous", "candidates": res.Candidates, "warnings": res.Warnings,
		})
	case err == pgx.ErrNoRows:
		writeErr(w, http.StatusNotFound, "no match")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "query failed")
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

// geocodeBatchHandler는 여러 도로명주소를 한 요청(POST, 최대 50건)으로 지오코딩한다.
// 각 주소를 단건 geocode.Search와 동일 규칙으로 처리하고 입력 순서대로 결과를 돌려준다.
// 한 주소의 실패는 그 항목에만 담고 배치 전체는 계속한다.
func (s *Server) geocodeBatchHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Addresses []string `json:"addresses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, `JSON body {"addresses":[...]} required`)
		return
	}
	if len(body.Addresses) == 0 || len(body.Addresses) > 50 {
		writeErr(w, http.StatusBadRequest, "addresses must have 1..50 entries")
		return
	}
	type item struct {
		Q          string          `json:"q"`
		Match      string          `json:"match,omitempty"`
		Result     *geocode.Result `json:"result,omitempty"`
		Candidates []string        `json:"candidates,omitempty"`
		Error      string          `json:"error,omitempty"`
	}
	items := make([]item, 0, len(body.Addresses))
	for _, q := range body.Addresses {
		it := item{Q: q}
		if len(q) < 5 {
			it.Error = "q (road address) required"
			items = append(items, it)
			continue
		}
		res, err := geocode.Search(r.Context(), s.Pool, q)
		switch {
		case err == geocode.ErrAmbiguous:
			it.Match, it.Candidates = "ambiguous", res.Candidates
		case err == pgx.ErrNoRows:
			it.Error = "no match"
		case err != nil:
			it.Error = "query failed"
		default:
			it.Result = res
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(items), "items": items})
}

// usageHandler는 인증된 키 자신의 usage_log 집계를 엔드포인트별로 돌려준다(자기 키만).
func (s *Server) usageHandler(w http.ResponseWriter, r *http.Request) {
	keyID, ok := auth.APIKeyID(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "auth required")
		return
	}
	rows, err := s.Pool.Query(r.Context(), `
		SELECT endpoint, SUM(count)::bigint, MIN(bucket), MAX(bucket)
		FROM usage_log WHERE api_key_id = $1
		GROUP BY endpoint ORDER BY 2 DESC`, keyID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	type usageItem struct {
		Endpoint  string    `json:"endpoint"`
		Count     int64     `json:"count"`
		FirstSeen time.Time `json:"first_seen"`
		LastSeen  time.Time `json:"last_seen"`
	}
	items := make([]usageItem, 0)
	var total int64
	for rows.Next() {
		var it usageItem
		if err := rows.Scan(&it.Endpoint, &it.Count, &it.FirstSeen, &it.LastSeen); err != nil {
			writeErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		total += it.Count
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	// 티어·월 캡·이달 사용량. 셀프서브 구매 이유가 요금 예측 가능성이므로 잔여량을
	// 함께 노출한다. monthly_cap 0은 캡 없음(remaining 미제공).
	var tier string
	var monthlyCap, monthUsed int64
	err = s.Pool.QueryRow(r.Context(), `
		SELECT k.tier, k.monthly_cap,
		       (SELECT COALESCE(SUM(count), 0) FROM usage_log
		        WHERE api_key_id = k.id AND bucket >= date_trunc('month', now())
		          AND endpoint <> '/v1/usage')
		FROM api_keys k WHERE k.id = $1`, keyID).Scan(&tier, &monthlyCap, &monthUsed)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	resp := map[string]any{
		"total": total, "items": items,
		"tier": tier, "monthly_cap": monthlyCap, "month_used": monthUsed,
	}
	if monthlyCap > 0 {
		remaining := monthlyCap - monthUsed
		if remaining < 0 {
			remaining = 0
		}
		resp["month_remaining"] = remaining
	}
	writeJSON(w, http.StatusOK, resp)
}

// reverseHandler는 좌표 → 최근접 건물 주소 (v0: 반경 200m KNN).
func (s *Server) reverseHandler(w http.ResponseWriter, r *http.Request) {
	lon, err1 := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	lat, err2 := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "lon, lat required")
		return
	}
	res, err := revgeo.ByPoint(r.Context(), s.Pool, lon, lat, 200)
	switch {
	case err == pgx.ErrNoRows:
		writeErr(w, http.StatusNotFound, "no building within 200m")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "query failed")
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

// placesSearchHandler는 상호명 키워드 검색 (v0: trigram). q 없이 lat/lon만 주면
// 반경 내 근접순 목록을 돌려준다 (지도 "주변 상가" 오버레이용 브라우즈 모드).
func (s *Server) placesSearchHandler(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	q := qv.Get("q")
	limit, _ := strconv.Atoi(qv.Get("limit"))
	// lon,lat 둘 다 유효할 때만 반경 필터 활성(기본 1km, 1..20km). 없으면 radius=0.
	lon, errLon := strconv.ParseFloat(qv.Get("lon"), 64)
	lat, errLat := strconv.ParseFloat(qv.Get("lat"), 64)
	var radius float64
	if errLon == nil && errLat == nil {
		radius = float64(clampInt(atoiDefault(qv.Get("radius"), 1000), 1, 20000))
	}
	var out []places.Place
	var err error
	switch {
	case len(q) >= 2:
		out, err = places.Search(r.Context(), s.Pool, q, qv.Get("category"), lon, lat, radius, limit)
	case radius > 0:
		out, err = places.Nearby(r.Context(), s.Pool, qv.Get("category"), lon, lat, radius, limit)
	default:
		writeErr(w, http.StatusBadRequest, "q (2+ chars) or lat+lon required")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(out), "items": out})
}

// ---- 실거래가 (MOLIT RTMSDataSvcLandTrade 사실 반환) ----

type molitItem struct {
	DealYear   string `xml:"dealYear" json:"deal_year"`
	DealMonth  string `xml:"dealMonth" json:"deal_month"`
	DealDay    string `xml:"dealDay" json:"deal_day"`
	DealAmount string `xml:"dealAmount" json:"deal_amount_manwon"`
	DealArea   string `xml:"dealArea" json:"deal_area_m2"`
	Jimok      string `xml:"jimok" json:"jimok"`
	LandUse    string `xml:"landUse" json:"land_use"`
	SggCd      string `xml:"sggCd" json:"sgg_cd"`
	SggNm      string `xml:"sggNm" json:"sgg_nm"`
	UmdNm      string `xml:"umdNm" json:"umd_nm"`
	Jibun      string `xml:"jibun" json:"jibun_masked"`
	DealingGbn string `xml:"dealingGbn" json:"dealing_gbn"`
	ShareGbn   string `xml:"shareDealingType" json:"share_dealing_type"`
}

type molitResp struct {
	Header struct {
		ResultCode string `xml:"resultCode"`
		ResultMsg  string `xml:"resultMsg"`
	} `xml:"header"`
	Body struct {
		Items      []molitItem `xml:"items>item"`
		TotalCount int         `xml:"totalCount"`
	} `xml:"body"`
}

func (s *Server) appraisalSales(w http.ResponseWriter, r *http.Request) {
	lawd, ymd := r.URL.Query().Get("lawd_cd"), r.URL.Query().Get("deal_ymd")
	if !allDigits(lawd) || !allDigits(ymd) || len(lawd) != 5 || len(ymd) != 6 {
		writeErr(w, http.StatusBadRequest, "lawd_cd(5), deal_ymd(YYYYMM) required")
		return
	}
	// 키 미설정이면 상류 호출 없이 비활성 상태를 명시한다. (빈 키로 호출하면
	// data.go.kr이 401을 주고 오해하기 쉬운 502가 됐다 — deploy/OPS.md 4절.)
	if s.MolitKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{
				"code":    "SALES_DISABLED",
				"message": "MOLIT_API_KEY 미설정 — 실거래 조회 비활성 (운영자가 키 투입 후 재시작 필요)",
			},
		})
		return
	}
	u := fmt.Sprintf("https://apis.data.go.kr/1613000/RTMSDataSvcLandTrade/getRTMSDataSvcLandTrade?serviceKey=%s&LAWD_CD=%s&DEAL_YMD=%s&numOfRows=1000",
		url.QueryEscape(s.MolitKey), lawd, ymd)
	body, err := s.fetch(r.Context(), u)
	if err != nil {
		log.Printf("req %s: appraisal/sales upstream error: %v",
			RequestID(r.Context()), redactKey(err, s.MolitKey))
		writeErr(w, http.StatusBadGateway, "upstream unavailable")
		return
	}
	var mr molitResp
	if err := xml.Unmarshal(body, &mr); err != nil || mr.Header.ResultCode != "000" {
		// data.go.kr은 키 오류도 200 + 에러 XML로 주므로 원인 코드를 로그에 남긴다.
		log.Printf("req %s: appraisal/sales molit resultCode=%q msg=%q xmlErr=%v",
			RequestID(r.Context()), mr.Header.ResultCode, mr.Header.ResultMsg, err)
		writeErr(w, http.StatusBadGateway, "upstream molit response invalid")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "rt.molit.go.kr (RTMSDataSvcLandTrade)", "lawd_cd": lawd,
		"deal_ymd": ymd, "total_count": mr.Body.TotalCount, "items": mr.Body.Items,
	})
}

// ---- 지가변동률 (land_price_index = R-ONE 용도지역별 월, 사실 반환) ----

// appraisalPriceIndex는 land_price_index에서 (시군구×용도지역) 월별 변동률을 그대로
// 돌려준다. cumulative=true면 시점수정용 누적률((1+r/100)의 곱)을 산식·기간과 함께 병기한다
// (지시서 허용 계산). 평가·판단·보정 없음.
func (s *Server) appraisalPriceIndex(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sigungu, useZone := q.Get("sigungu"), q.Get("use_zone_class")
	from, to := q.Get("from"), q.Get("to")
	if len(sigungu) != 5 || !allDigits(sigungu) {
		writeErr(w, http.StatusBadRequest, "sigungu(5자리) required")
		return
	}
	if useZone == "" {
		writeErr(w, http.StatusBadRequest, "use_zone_class required")
		return
	}
	if (from != "" && (!allDigits(from) || len(from) != 6)) || (to != "" && (!allDigits(to) || len(to) != 6)) {
		writeErr(w, http.StatusBadRequest, "from/to must be YYYYMM")
		return
	}
	rows, err := s.Pool.Query(r.Context(),
		`SELECT month, rate_pct::float8 FROM land_price_index
		 WHERE sigungu_code=$1 AND use_zone_class=$2
		   AND ($3='' OR month >= $3) AND ($4='' OR month <= $4)
		 ORDER BY month`, sigungu, useZone, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	type point struct {
		Month string  `json:"month"`
		Rate  float64 `json:"rate_pct"`
	}
	series := []point{}
	for rows.Next() {
		var p point
		if err := rows.Scan(&p.Month, &p.Rate); err != nil {
			writeErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		series = append(series, p)
	}
	if rows.Err() != nil {
		writeErr(w, http.StatusInternalServerError, "read failed")
		return
	}
	resp := map[string]any{
		"source":         "R-ONE 용도지역별 지가변동률 (월)",
		"sigungu_code":   sigungu,
		"use_zone_class": useZone,
		"count":          len(series),
		"series":         series,
	}
	if q.Get("cumulative") == "true" && len(series) > 0 {
		prod := 1.0
		for _, p := range series {
			prod *= 1 + p.Rate/100
		}
		resp["cumulative_pct"] = (prod - 1) * 100
		resp["cumulative_formula"] = "∏(1 + rate_pct/100) - 1, 단위 %"
		resp["period"] = map[string]string{"from": series[0].Month, "to": series[len(series)-1].Month}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- 비교표준지 검색 (standard_lots, 앵커 반경 내 거리순 사실 반환) ----

// appraisalStandardLots는 앵커(pnu 또는 lat/lng) 반경 내 표준지를 거리 오름차순으로
// 돌려준다. 정렬·필터는 객관 지표(거리)만 — 유사도/추천/격차 점수 없음(지시서 절대경계).
func (s *Server) appraisalStandardLots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pnu := q.Get("pnu")
	var alon, alat float64
	if pnu != "" {
		if len(pnu) != 19 || !allDigits(pnu) {
			writeErr(w, http.StatusBadRequest, "pnu must be 19 digits")
			return
		}
		err := s.Pool.QueryRow(r.Context(),
			`SELECT ST_X(p), ST_Y(p) FROM (SELECT ST_PointOnSurface(geom) p FROM parcels WHERE pnu=$1) t`,
			pnu).Scan(&alon, &alat)
		if err == pgx.ErrNoRows {
			writeErr(w, http.StatusNotFound, "pnu not found in parcels")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "anchor lookup failed")
			return
		}
	} else {
		var e1, e2 error
		alat, e1 = strconv.ParseFloat(q.Get("lat"), 64)
		alon, e2 = strconv.ParseFloat(q.Get("lng"), 64)
		if e1 != nil || e2 != nil {
			writeErr(w, http.StatusBadRequest, "pnu, or lat+lng required")
			return
		}
		if alat < 33 || alat > 39 || alon < 124 || alon > 132 {
			writeErr(w, http.StatusBadRequest, "lat/lng out of service area (Korea)")
			return
		}
	}
	radius := clampInt(atoiDefault(q.Get("radius"), 2000), 1, 10000)
	limit := clampInt(atoiDefault(q.Get("limit"), 20), 1, 50)
	baseYear := atoiDefault(q.Get("base_year"), 0)

	rows, err := s.Pool.Query(r.Context(), `
		SELECT pnu,
		       round(ST_Distance(geom::geography, ST_SetSRID(ST_MakePoint($1,$2),4326)::geography)::numeric, 1)::float8 AS dist,
		       price_per_sqm, land_category, use_zone, land_use, road_side, terrain_shape,
		       ST_Y(geom), ST_X(geom)
		FROM standard_lots
		WHERE geom && ST_Expand(ST_SetSRID(ST_MakePoint($1,$2),4326), $3/80000.0)
		  AND ST_DWithin(geom::geography, ST_SetSRID(ST_MakePoint($1,$2),4326)::geography, $3)
		  AND ($4 = 0 OR base_year = $4)
		  AND ($5 = '' OR use_zone = $5)
		  AND ($6 = '' OR land_category = $6)
		  AND ($7 = '' OR land_use = $7)
		ORDER BY dist ASC
		LIMIT $8`,
		alon, alat, radius, baseYear, q.Get("use_zone"), q.Get("land_category"), q.Get("land_use"), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	type lot struct {
		PNU      string  `json:"pnu"`
		DistM    float64 `json:"distance_m"`
		Price    int64   `json:"price_per_sqm"`
		LandCat  *string `json:"land_category"`
		UseZone  *string `json:"use_zone"`
		LandUse  *string `json:"land_use"`
		RoadSide *string `json:"road_side"`
		Shape    *string `json:"terrain_shape"`
		Lat      float64 `json:"lat"`
		Lng      float64 `json:"lng"`
	}
	out := []lot{}
	for rows.Next() {
		var l lot
		if err := rows.Scan(&l.PNU, &l.DistM, &l.Price, &l.LandCat, &l.UseZone,
			&l.LandUse, &l.RoadSide, &l.Shape, &l.Lat, &l.Lng); err != nil {
			writeErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, l)
	}
	if rows.Err() != nil {
		writeErr(w, http.StatusInternalServerError, "read failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":        "표준지공시지가 (MOLIT/NSDI)",
		"anchor":        map[string]float64{"lat": alat, "lng": alon},
		"radius_m":      radius,
		"count":         len(out),
		"standard_lots": out,
	})
}

// ---- 공시지가 (개별 우선, 없으면 표준지, 사실 반환) ----

// appraisalOfficialPrice는 PNU의 공시지가를 돌려준다. 개별공시지가(official_land_prices)가
// 있으면 그것(INDIVIDUAL), 없으면 표준지공시지가(standard_lots, STANDARD)를 반환한다.
// base_year 미지정 시 최신 연도. 사실만 — 보정·계산 없음.
func (s *Server) appraisalOfficialPrice(w http.ResponseWriter, r *http.Request) {
	pnu := r.URL.Query().Get("pnu")
	if len(pnu) != 19 || !allDigits(pnu) {
		writeErr(w, http.StatusBadRequest, "pnu must be 19 digits")
		return
	}
	byr := atoiDefault(r.URL.Query().Get("base_year"), 0) // 0 = 최신

	var price int64
	var year int
	emit := func(ptype string) {
		writeJSON(w, http.StatusOK, map[string]any{
			"pnu": pnu, "price_per_sqm": price, "price_type": ptype, "base_year": year,
		})
	}
	// 표준지(STANDARD) 우선 — 전국 필수 적재라 신뢰 소스 (지시서 §4).
	err := s.Pool.QueryRow(r.Context(),
		`SELECT price_per_sqm, base_year FROM standard_lots
		 WHERE pnu=$1 AND ($2=0 OR base_year=$2) ORDER BY base_year DESC LIMIT 1`,
		pnu, byr).Scan(&price, &year)
	if err == nil {
		emit("STANDARD")
		return
	}
	if err != pgx.ErrNoRows {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	// 표준지 없음 → 개별공시지가(INDIVIDUAL, 시군구 선택 적재)
	err = s.Pool.QueryRow(r.Context(),
		`SELECT price_per_sqm, base_year FROM official_land_prices
		 WHERE pnu=$1 AND ($2=0 OR base_year=$2) ORDER BY base_year DESC LIMIT 1`,
		pnu, byr).Scan(&price, &year)
	if err == nil {
		emit("INDIVIDUAL")
		return
	}
	if err != pgx.ErrNoRows {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	// 개별도 없음: 해당 시군구에 개별공시지가가 아예 미적재면 404가 아니라 REGION_NOT_LOADED로
	// 구분한다(선택 적재 정책 — 지시서 §4). 적재됐는데 이 PNU만 없으면 진짜 404.
	loaded, rerr := s.hasRegion(r.Context(), "official_land_prices", pnu[:5])
	if rerr != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !loaded {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]string{
				"code": "REGION_NOT_LOADED", "message": "개별공시지가 미적재 지역 (표준지 없음)",
			},
		})
		return
	}
	writeErr(w, http.StatusNotFound, "no official price for this pnu")
}

// ---- 토지이용계획 (land_use_plan, 필지별 용도지역·지구·구역 사실 반환) ----

// appraisalLandUse는 PNU의 용도지역/지구/구역과 원자료 사전판정 관계(포함/저촉/접함)를
// 그대로 돌려준다. 저촉 면적비(%)는 원자료 미제공 — 우리가 ST_Area로 재판정하지 않는다(사실 반환).
func (s *Server) appraisalLandUse(w http.ResponseWriter, r *http.Request) {
	pnu := r.URL.Query().Get("pnu")
	if len(pnu) != 19 || !allDigits(pnu) {
		writeErr(w, http.StatusBadRequest, "pnu must be 19 digits")
		return
	}
	rows, err := s.Pool.Query(r.Context(),
		`SELECT zone_code, zone_name, relation FROM land_use_plan WHERE pnu=$1 ORDER BY zone_code`, pnu)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	type zone struct {
		Code     string `json:"zone_code"`
		Name     string `json:"zone_name"`
		Relation string `json:"relation"` // 포함 | 저촉 | 접함 (원자료 그대로)
	}
	zones := []zone{}
	for rows.Next() {
		var z zone
		if err := rows.Scan(&z.Code, &z.Name, &z.Relation); err != nil {
			writeErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		zones = append(zones, z)
	}
	if rows.Err() != nil {
		writeErr(w, http.StatusInternalServerError, "read failed")
		return
	}
	if len(zones) == 0 {
		// 미적재 시군구면 404가 아니라 REGION_NOT_LOADED (전국 필수지만 부분 적재 중일 수 있음).
		loaded, rerr := s.hasRegion(r.Context(), "land_use_plan", pnu[:5])
		if rerr != nil {
			writeErr(w, http.StatusInternalServerError, "query failed")
			return
		}
		if !loaded {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error": map[string]string{"code": "REGION_NOT_LOADED", "message": "토지이용계획 미적재 지역"},
			})
			return
		}
		writeErr(w, http.StatusNotFound, "no land use plan for this pnu")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pnu":   pnu,
		"count": len(zones),
		"zones": zones,
		"note":  "관계는 원자료 사전판정(포함/저촉/접함); 저촉 면적비(%)는 원자료 미제공",
	})
}

// ---- 토지특성 (land_features, 필지별 이용상황·지형·도로접면 사실 반환) ----

// appraisalLandFeatures는 PNU의 토지특성(토지이용상황·지형고저·지형형상·도로접면)을
// 원값 라벨 그대로 돌려준다. base_year 미지정 시 최신. 재분류/보정 없음(사실 반환).
func (s *Server) appraisalLandFeatures(w http.ResponseWriter, r *http.Request) {
	pnu := r.URL.Query().Get("pnu")
	if len(pnu) != 19 || !allDigits(pnu) {
		writeErr(w, http.StatusBadRequest, "pnu must be 19 digits")
		return
	}
	baseYear := atoiDefault(r.URL.Query().Get("base_year"), 0)
	var out struct {
		PNU           string `json:"pnu"`
		BaseYear      int    `json:"base_year"`
		LandUse       string `json:"land_use"`       // 토지이용상황 (원값)
		TerrainHeight string `json:"terrain_height"` // 지형높이 (원값)
		TerrainShape  string `json:"terrain_shape"`  // 지형형상 (원값)
		RoadSide      string `json:"road_side"`      // 도로접면 (원값)
		SourceVersion string `json:"source_version"`
	}
	var sv time.Time
	err := s.Pool.QueryRow(r.Context(),
		`SELECT base_year, land_use, terrain_height, terrain_shape, road_side, source_version
		 FROM land_features WHERE pnu=$1 AND ($2=0 OR base_year=$2) ORDER BY base_year DESC LIMIT 1`,
		pnu, baseYear).Scan(&out.BaseYear, &out.LandUse, &out.TerrainHeight, &out.TerrainShape, &out.RoadSide, &sv)
	switch {
	case err == pgx.ErrNoRows:
		// 미적재 시군구면 404가 아니라 REGION_NOT_LOADED (지역선택 적재 중).
		loaded, rerr := s.hasRegion(r.Context(), "land_features", pnu[:5])
		if rerr != nil {
			writeErr(w, http.StatusInternalServerError, "query failed")
			return
		}
		if !loaded {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error": map[string]string{"code": "REGION_NOT_LOADED", "message": "토지특성 미적재 지역"},
			})
			return
		}
		writeErr(w, http.StatusNotFound, "no land features for this pnu")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	out.PNU = pnu
	out.SourceVersion = sv.Format("2006-01-02")
	writeJSON(w, http.StatusOK, out)
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// redactKey는 에러 문자열에서 API 키(원문/URL인코딩)를 가린다.
func redactKey(err error, key string) string {
	msg := err.Error()
	if key == "" {
		return msg // 빈 키면 ReplaceAll이 모든 문자 사이에 삽입되므로 원문 반환
	}
	msg = strings.ReplaceAll(msg, key, "***")
	msg = strings.ReplaceAll(msg, url.QueryEscape(key), "***")
	return msg
}

func (s *Server) fetch(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// data.go.kr 게이트웨이가 기본 UA를 차단함 (실측) — 명시 UA 필수
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; b2d-geo/0.1)")
	res, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	return io.ReadAll(res.Body)
}
