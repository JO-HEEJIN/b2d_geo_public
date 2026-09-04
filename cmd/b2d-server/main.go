// b2d-server는 위치·토지 인텔리전스 REST API와 내장 웹 UI를 제공한다.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JO-HEEJIN/b2d_geo_public/internal/api"
)

// initSentry는 SENTRY_DSN이 있을 때만 Sentry를 초기화한다. 없거나 실패하면
// false — 서버는 기존 로그만으로 정상 동작한다(에러 추적은 X-Request-Id 로그).
// 비밀값(MOLIT 키)은 BeforeSend 스크러버가 이벤트에서 치환한다.
func initSentry() bool {
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return false
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      os.Getenv("SENTRY_ENVIRONMENT"),
		Release:          vcsRevision(),
		AttachStacktrace: true,
		BeforeSend:       api.SentryScrubber(os.Getenv("MOLIT_API_KEY")),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sentry init failed (continuing without):", err)
		return false
	}
	fmt.Println("sentry enabled, environment:", os.Getenv("SENTRY_ENVIRONMENT"))
	// 운영자가 연동을 즉시 확인할 수 있는 부팅 핑 (기본 꺼짐).
	if os.Getenv("B2D_SENTRY_BOOT_PING") == "true" {
		sentry.CaptureMessage("b2d-server boot ping")
	}
	return true
}

// vcsRevision은 빌드에 박힌 git 커밋(짧은 형태)을 돌려준다. 없으면 "".
func vcsRevision() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 12 {
				return s.Value[:12]
			}
		}
	}
	return ""
}

func main() {
	dsn := os.Getenv("B2D_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://b2d:b2d_local_dev@localhost:5433/b2d_geo"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect failed:", err)
		os.Exit(1)
	}
	defer pool.Close()

	if initSentry() {
		// 종료 시 큐에 남은 이벤트를 최대 2초 기다려 전송 (베스트에포트).
		defer sentry.Flush(2 * time.Second)
	}

	addr := os.Getenv("B2D_LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	h, closeMeter := api.New(pool, os.Getenv("MOLIT_API_KEY"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: addr, Handler: h}
	go func() {
		fmt.Println("b2d-server listening on", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	// 종료 순서: HTTP 먼저 드레인(진행 중 핸들러 완료) → 계측기 드레인.
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		fmt.Fprintln(os.Stderr, "shutdown:", err)
	}
	closeMeter()
}
