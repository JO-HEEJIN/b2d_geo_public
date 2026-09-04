package juso

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 도로명주소 한글DB 다운로드 엔드포인트. business.juso.go.kr은 SPA로 개편되어
// 과거 *.do 경로는 404다. 데이터/목록은 아래 JSON API로 제공된다.
// 근거: docs/dataspec/juso.md §4.
const (
	apiListURL      = "https://business.juso.go.kr/api/jst/selectAttrbDBDwldList"
	apiDownloadBase = "https://business.juso.go.kr/api/jst/download"
	// 도로명주소 한글 RTL_DTA_DTL_SN.
	rnaddrkorDtlSn = "1"
	// SPA가 봇 UA를 거르므로 브라우저 UA로 요청한다.
	browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
)

// manualGuide는 익명 다운로드가 막혔을 때(로그인 필요) 사용자에게 안내하는 문구다.
// 정책상 쿠키/세션을 코드로 다루지 않는다. 사용자가 브라우저로 직접 받은 zip 경로를
// Load에 넘긴다.
const manualGuide = "실데이터(전체분 zip) 다운로드는 로그인이 필요하다. " +
	"business.juso.go.kr 로그인 후 '도로명주소 한글 > 전체분'을 브라우저로 내려받아, " +
	"그 zip(또는 압축 해제한 txt 디렉터리) 경로를 juso.Load에 직접 전달할 것"

// monthFile은 selectAttrbDBDwldList 응답의 전체분 파일 항목이다.
type monthFile struct {
	CrtrYm     string `json:"crtrYm"`     // 기준연월 (예: "202605")
	FileNm     string `json:"fileNm"`     // 표시 파일명(한글)
	TmprFileNm string `json:"tmprFileNm"` // 실제 파일 식별자(예: "RNADDR_KOR_2605.zip")
	FileTypeNm string `json:"fileTypeNm"` // reqType (예: "ALLRNADR_KOR")
	CtpvClsfCd string `json:"ctpvClsfCd"` // 시도구분코드 (전국="00")
	IsExist    string `json:"isExist"`    // "Y"/"N"
}

type listResponse struct {
	Results struct {
		AllMonthFileList []monthFile `json:"allMonthFileList"`
	} `json:"results"`
}

// Download는 도로명주소 한글 최신 전체분 ZIP을 익명으로 시도해 destDir에 저장하고
// 경로를 반환한다. 실데이터 전송은 로그인 세션이 필요하므로 익명 요청은 서버가 ZIP
// 대신 JSON 오류를 준다. 이 경우 수동 다운로드 안내와 함께 에러를 반환한다(정책상
// 쿠키를 코드로 다루지 않는다). 이미 받은 파일이 있으면 Load에 그 경로를 넘기면 된다.
func Download(ctx context.Context, destDir string) (string, error) {
	mf, err := latestFullFile(ctx)
	if err != nil {
		return "", fmt.Errorf("%w (%s)", err, manualGuide)
	}

	q := url.Values{}
	q.Set("rtlDtaDtlSn", rnaddrkorDtlSn)
	q.Set("rtlDtaDtlNm", "도로명주소 한글")
	q.Set("reqType", mf.FileTypeNm)
	q.Set("ctprvnCd", mf.CtpvClsfCd)
	q.Set("stdde", mf.CrtrYm)
	q.Set("fileName", mf.FileNm)
	q.Set("realFileName", mf.TmprFileNm)
	q.Set("intFileNo", "0")
	q.Set("intNum", "0")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiDownloadBase+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", "https://business.juso.go.kr/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// 로그인 미인증이면 서버가 application/json 오류를 준다(ZIP은 application/zip).
	if strings.Contains(resp.Header.Get("Content-Type"), "json") || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("전체분 자동 다운로드 불가(HTTP %d). %s", resp.StatusCode, manualGuide)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	name := mf.TmprFileNm
	if name == "" {
		name = "rnaddrkor.zip"
	}
	dest := filepath.Join(destDir, name)
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("본문 저장: %w", err)
	}
	return dest, nil
}

// latestFullFile은 최신(가장 큰 기준연월) 전체분 파일 항목을 조회한다.
func latestFullFile(ctx context.Context) (monthFile, error) {
	year := time.Now().Year()
	// 최신 전체분이 아직 안 올라온 연초 대비로 올해와 작년을 훑는다.
	for _, y := range []int{year, year - 1} {
		files, err := listFullFiles(ctx, y)
		if err != nil {
			return monthFile{}, err
		}
		var best monthFile
		for _, f := range files {
			if f.IsExist == "Y" && f.TmprFileNm != "" && f.CrtrYm > best.CrtrYm {
				best = f
			}
		}
		if best.CrtrYm != "" {
			return best, nil
		}
	}
	return monthFile{}, fmt.Errorf("이용 가능한 도로명주소 한글 전체분 없음")
}

// listFullFiles는 해당 연도의 전체분 파일 목록을 조회한다.
func listFullFiles(ctx context.Context, year int) ([]monthFile, error) {
	body, _ := json.Marshal(map[string]any{
		"rtlDtaDtlSn": rnaddrkorDtlSn,
		"year":        fmt.Sprintf("%d", year),
		"month":       "12",
		"expand":      "Y",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiListURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", browserUA)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("파일목록 조회 실패: HTTP %d", resp.StatusCode)
	}
	var lr listResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("파일목록 파싱: %w", err)
	}
	return lr.Results.AllMonthFileList, nil
}
