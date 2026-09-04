// 공유: 런타임 설정(API 베이스 + 데모 키)을 서버 /config.json에서 받아, 인증
// 헤더를 붙인 API 호출 헬퍼를 노출한다. 데모 키는 서버 env에서 주입되므로
// 프론트 소스에는 키가 없다.

const B2D_CONFIG = fetch("/config.json")
  .then((r) => r.json())
  .catch(() => ({ apiBase: "/v1", demoKey: "" }));

// api(path, params, opts) → 파싱된 JSON. 실패 시 Error(throw){status, body}.
// opts.method(기본 GET) / opts.body(POST용 객체). GET은 params를 쿼리로 붙인다.
async function api(path, params, opts = {}) {
  const cfg = await B2D_CONFIG;
  const url = new URL(cfg.apiBase + path, location.origin);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== null && v !== undefined && v !== "") url.searchParams.set(k, v);
    }
  }
  const headers = cfg.demoKey ? { Authorization: "Bearer " + cfg.demoKey } : {};
  const init = { method: opts.method || "GET", headers };
  if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(opts.body);
  }
  let res;
  try {
    res = await fetch(url, init);
  } catch (e) {
    console.error(`[b2d] ${init.method} ${url.pathname}${url.search} -> 네트워크 실패`, e);
    throw e;
  }
  // 서버 미들웨어가 요청마다 X-Request-Id를 발급한다. 실패 시 이 ID를 콘솔과
  // 화면에 남겨 서버 로그(`req <id>: ...`)와 대조할 수 있게 한다.
  const reqID = res.headers.get("X-Request-Id") || "";
  const body = await res.json().catch(() => null);
  if (!res.ok) {
    const msg =
      (body && body.error && (body.error.message || body.error)) || res.statusText;
    const err = Object.assign(new Error(msg), {
      status: res.status,
      body,
      requestId: reqID,
    });
    console.error(
      `[b2d] ${init.method} ${url.pathname}${url.search} -> ${res.status}` +
        (reqID ? ` (req ${reqID})` : ""),
      body,
      err
    );
    throw err;
  }
  return body;
}

// 에러 표시용: 메시지 + (있으면) 요청 ID. 사용자가 이 ID로 문의하면 서버 로그와
// 바로 대조된다.
function errText(e) {
  return e.requestId ? `${e.message} [req ${e.requestId}]` : e.message;
}

// 만원 단위 정수 문자열/숫자를 "6억 2,106만원" 형태로.
function formatManwon(manwonStr) {
  const n = Number(String(manwonStr).replace(/,/g, ""));
  if (!isFinite(n) || n <= 0) return "-";
  const eok = Math.floor(n / 10000);
  const man = Math.round(n % 10000);
  let out = "";
  if (eok > 0) out += eok.toLocaleString("ko-KR") + "억 ";
  if (man > 0) out += man.toLocaleString("ko-KR") + "만";
  return (out.trim() || "0") + "원";
}

// 원/㎡ 정수를 천단위 콤마.
function formatWon(n) {
  const v = Number(n);
  return isFinite(v) ? v.toLocaleString("ko-KR") : "-";
}

// ---- 라이트/다크 토글 (뷰어 선택을 root data-theme으로 stamp) ----
(function initTheme() {
  const saved = localStorage.getItem("b2d-theme");
  if (saved) document.documentElement.setAttribute("data-theme", saved);
  window.toggleTheme = function () {
    const cur =
      document.documentElement.getAttribute("data-theme") ||
      (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
    const next = cur === "dark" ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    localStorage.setItem("b2d-theme", next);
  };
})();
