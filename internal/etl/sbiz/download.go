package sbiz

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	datasetPageURL   = "https://www.data.go.kr/data/15083033/fileData.do"
	fileDownloadBase = "https://www.data.go.kr/cmm/cmm/fileDownload.do"
	// data.go.kr은 봇 UA를 거르므로 브라우저 UA로 요청한다.
	browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
)

// atchFileId는 분기마다 갱신되므로 하드코딩하지 않고 매번 페이지에서 파싱한다.
var atchFileRe = regexp.MustCompile(`atchFileId=(FILE_\d+)&fileDetailSn=(\d+)`)

// Download는 데이터셋 페이지에서 현재 atchFileId를 파싱해 전국 ZIP을 destDir에
// 내려받는다. 저장한 파일 경로를 반환한다.
func Download(ctx context.Context, destDir string) (string, error) {
	atchFileID, fileDetailSn, err := fetchDownloadParams(ctx)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s?atchFileId=%s&fileDetailSn=%s", fileDownloadBase, atchFileID, fileDetailSn)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", datasetPageURL)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("다운로드 실패: HTTP %d", resp.StatusCode)
	}

	name := filenameFromDisposition(resp.Header.Get("Content-Disposition"))
	if name == "" {
		name = "sbiz_상가상권정보.zip"
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
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

func fetchDownloadParams(ctx context.Context) (atchFileID, fileDetailSn string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, datasetPageURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", browserUA)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("페이지 조회 실패: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	m := atchFileRe.FindSubmatch(body)
	if m == nil {
		return "", "", fmt.Errorf("페이지에서 atchFileId 파싱 실패")
	}
	return string(m[1]), string(m[2]), nil
}

// filenameFromDisposition은 Content-Disposition에서 filename을 추출한다.
func filenameFromDisposition(cd string) string {
	if cd == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(cd); err == nil {
		if fn := params["filename"]; fn != "" {
			return filepath.Base(fn)
		}
	}
	// mime 파싱 실패 시 단순 폴백.
	if i := strings.Index(cd, "filename="); i >= 0 {
		fn := strings.Trim(cd[i+len("filename="):], `"`)
		if j := strings.IndexByte(fn, ';'); j >= 0 {
			fn = fn[:j]
		}
		return filepath.Base(strings.TrimSpace(fn))
	}
	return ""
}
