// API 문서 + 브라우저 내 실행(try-it-out). 엔드포인트 메타를 정의하고, 각
// 파라미터로 폼을 만들어 same-origin API를 데모 키로 호출한다.

const GROUPS = [
  {
    id: "geo", title: "지오코딩",
    endpoints: [
      { m: "GET", path: "/geocode", desc: "도로명주소 → 좌표",
        params: [{ n: "q", req: true, def: "경기도 하남시 대청로 10", note: "도로명주소(최소 5자)" }] },
      { m: "POST", path: "/geocode/batch", desc: "주소 여러 건 일괄 지오코딩", bodyKey: "addresses",
        body: ["서울특별시 중구 세종대로 110", "경기도 하남시 대청로 10"],
        note: "요청 본문: <code>{\"addresses\": [\"...\", \"...\"]}</code>" },
      { m: "GET", path: "/reverse", desc: "좌표 → 가장 가까운 도로명주소",
        params: [{ n: "lat", req: true, def: "37.5387" }, { n: "lon", req: true, def: "127.2141" }] },
    ],
  },
  {
    id: "places", title: "장소",
    endpoints: [
      { m: "GET", path: "/places/search", desc: "상호 검색(+반경 필터)",
        params: [
          { n: "q", req: true, def: "의원", note: "상호 부분일치" },
          { n: "lat", def: "37.5665" }, { n: "lon", def: "126.9780" },
          { n: "radius", def: "3000", note: "미터" },
        ] },
    ],
  },
  {
    id: "parcel", title: "필지",
    endpoints: [
      { m: "GET", path: "/parcel/by-point", desc: "좌표가 속한 필지",
        params: [{ n: "lat", req: true, def: "37.5387" }, { n: "lon", req: true, def: "127.2141" },
          { n: "geometry", def: "true", note: "true면 GeoJSON 경계 포함" }] },
      { m: "GET", path: "/parcel/by-jibun", desc: "법정동코드+지번으로 필지",
        params: [{ n: "bcode", req: true, def: "4145010600", note: "법정동코드 10자리" },
          { n: "jibun", req: true, def: "520" }, { n: "san", def: "false" }, { n: "geometry", def: "false" }] },
      { m: "GET", path: "/parcel/{pnu}", desc: "PNU로 필지 조회", pathParam: "pnu",
        params: [{ n: "pnu", req: true, def: "4145010600105200000", note: "필지 고유번호 19자리", inPath: true },
          { n: "geometry", def: "true" }] },
    ],
  },
  {
    id: "appraisal", title: "감정평가",
    endpoints: [
      { m: "GET", path: "/appraisal/official-price", desc: "공시지가(표준지 우선, 원/㎡)",
        params: [{ n: "pnu", req: true, def: "4145010600105200000" }] },
      { m: "GET", path: "/appraisal/land-use", desc: "용도지역·지구(사전판정 관계)",
        params: [{ n: "pnu", req: true, def: "4145010600105200000" }] },
      { m: "GET", path: "/appraisal/land-features", desc: "토지특성(이용상황·지형·형상·도로)",
        params: [{ n: "pnu", req: true, def: "4145010600105200000" }] },
      { m: "GET", path: "/appraisal/standard-lots", desc: "주변 표준지공시지가",
        params: [{ n: "pnu", def: "4145010600105200000", note: "pnu 또는 lat+lng" },
          { n: "lat", def: "" }, { n: "lng", def: "" }, { n: "radius", def: "2000", note: "미터" }] },
      { m: "GET", path: "/appraisal/sales", desc: "토지 실거래(시군구·월)",
        params: [{ n: "lawd_cd", req: true, def: "41450", note: "시군구코드 5자리" },
          { n: "deal_ymd", req: true, def: "202401", note: "YYYYMM" }] },
      { m: "GET", path: "/appraisal/price-index", desc: "용도지역별 지가변동률(월)",
        params: [{ n: "sigungu", req: true, def: "41450", note: "시군구코드 5자리" },
          { n: "use_zone_class", req: true, def: "주거지역", note: "주거지역·상업지역·공업지역·녹지지역" }] },
    ],
  },
  {
    id: "meta", title: "기타",
    endpoints: [
      { m: "GET", path: "/health", desc: "상태 확인(인증 불필요)", params: [] },
      { m: "GET", path: "/usage", desc: "이 키의 사용량", params: [] },
    ],
  },
];

const side = document.getElementById("side");
const container = document.getElementById("endpoints");

GROUPS.forEach((g) => {
  const grp = document.createElement("div");
  grp.className = "grp";
  grp.innerHTML = `<div class="grp-t">${g.title}</div>` +
    g.endpoints.map((e) => `<a href="#${epId(e)}">${esc(e.path)}</a>`).join("");
  side.appendChild(grp);

  g.endpoints.forEach((e) => container.appendChild(renderEndpoint(e)));
});

function epId(e) { return e.path.replace(/[^a-z0-9]+/gi, "-").replace(/^-|-$/g, ""); }

function renderEndpoint(e) {
  const el = document.createElement("details");
  el.className = "ep";
  el.id = epId(e);
  const params = e.params || [];
  const paramRows = params.length
    ? `<h4>파라미터</h4><table class="params"><thead><tr><th>이름</th><th>필수</th><th>설명</th></tr></thead><tbody>` +
      params.map((p) => `<tr><td class="pname">${esc(p.n)}</td><td class="req">${p.req ? "필수" : ""}</td><td>${p.note || ""}</td></tr>`).join("") +
      `</tbody></table>`
    : "";
  const bodyNote = e.note ? `<h4>비고</h4><p class="muted">${e.note}</p>` : "";

  el.innerHTML = `
    <summary>
      <span class="method ${e.m}">${e.m}</span>
      <span class="path">${esc(e.path)}</span>
      <span class="ep-desc">${esc(e.desc)}</span>
    </summary>
    <div class="ep-body">
      ${paramRows}
      ${bodyNote}
      <h4>실행</h4>
      <div class="tryit">${renderForm(e)}</div>
    </div>`;
  return el;
}

function renderForm(e) {
  let fieldsHtml = "";
  if (e.bodyKey) {
    fieldsHtml = `<div class="field" style="flex:1 1 100%"><label>요청 본문 (addresses, 줄바꿈 구분)</label>
      <textarea data-body>${(e.body || []).join("\n")}</textarea></div>`;
  } else {
    fieldsHtml = (e.params || [])
      .map((p) => `<div class="field"><label>${esc(p.n)}${p.req ? " *" : ""}</label>
        <input data-p="${esc(p.n)}" value="${esc(p.def ?? "")}" placeholder="${esc(p.n)}"></div>`)
      .join("");
    if (!fieldsHtml) fieldsHtml = `<span class="muted">파라미터 없음</span>`;
  }
  return `${fieldsHtml ? `<div class="fields">${fieldsHtml}</div>` : ""}
    <button class="btn" data-run>실행</button>
    <div class="resp" data-resp></div>`;
}

// 실행 위임
container.addEventListener("click", async (ev) => {
  const btn = ev.target.closest("[data-run]");
  if (!btn) return;
  const ep = btn.closest(".ep");
  const e = findEndpoint(ep.id);
  const respBox = ep.querySelector("[data-resp]");
  respBox.innerHTML = `<span class="muted"><span class="spin"></span>요청 중…</span>`;

  try {
    let out;
    if (e.bodyKey) {
      const raw = ep.querySelector("[data-body]").value.trim();
      const addresses = raw.split("\n").map((s) => s.trim()).filter(Boolean);
      out = await api(e.path, null, { method: "POST", body: { [e.bodyKey]: addresses } });
    } else {
      let path = e.path;
      const params = {};
      ep.querySelectorAll("[data-p]").forEach((inp) => {
        const name = inp.getAttribute("data-p");
        const val = inp.value.trim();
        const def = (e.params || []).find((p) => p.n === name);
        if (def && def.inPath) path = path.replace(`{${name}}`, encodeURIComponent(val));
        else if (val !== "") params[name] = val;
      });
      out = await api(path, params);
    }
    respBox.innerHTML = `<span class="status-pill ok">200 OK</span><pre>${esc(JSON.stringify(out, null, 2))}</pre>`;
  } catch (err) {
    const st = err.status || "ERR";
    respBox.innerHTML = `<span class="status-pill bad">${st}</span><pre>${esc(JSON.stringify(err.body || { error: err.message }, null, 2))}</pre>`;
  }
});

function findEndpoint(id) {
  for (const g of GROUPS) for (const e of g.endpoints) if (epId(e) === id) return e;
  return null;
}

// 공유 가능한 라이브 예제: /docs.html?open=<id>는 해당 엔드포인트를 펼치고,
// ?run=<id>는 펼친 뒤 바로 실행한다.
(function fromURL() {
  const p = new URLSearchParams(location.search);
  const openId = p.get("open") || p.get("run");
  if (!openId) return;
  const ep = document.getElementById(openId);
  if (!ep) return;
  ep.open = true;
  ep.scrollIntoView({ block: "start" });
  if (p.get("run")) ep.querySelector("[data-run]").click();
})();

function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])
  );
}

// ---- 셀프 데모 키 발급(운영자가 켰을 때만 노출) ----
B2D_CONFIG.then((cfg) => {
  if (!cfg.selfServe) return;
  const box = document.getElementById("issueBox");
  box.hidden = false;
  document.getElementById("issueBtn").addEventListener("click", async (ev) => {
    const btn = ev.currentTarget;
    const out = document.getElementById("issueOut");
    btn.disabled = true;
    out.innerHTML = `<span class="muted"><span class="spin"></span>발급 중…</span>`;
    try {
      const res = await fetch("/issue-demo-key", { method: "POST" });
      const body = await res.json();
      if (!res.ok) throw new Error((body && body.error && (body.error.message || body.error)) || "발급 실패");
      out.innerHTML =
        `<div class="issued"><code id="issuedKey">${esc(body.key)}</code>` +
        `<button class="btn ghost" id="copyKey">복사</button></div>` +
        `<p class="muted" style="margin:8px 0 0">${esc(body.note || "")} 이 키는 다시 표시되지 않으니 안전하게 보관하세요.</p>` +
        renderConnect(body.key);
      document.getElementById("copyKey").addEventListener("click", () => {
        navigator.clipboard.writeText(body.key).then(() => {
          document.getElementById("copyKey").textContent = "복사됨 ✓";
        });
      });
      // 연동 블록의 복사 버튼(위임 리스너 — 블록마다 data-copy에 원문 보관)
      out.querySelectorAll("button[data-copy]").forEach((b) =>
        b.addEventListener("click", () => {
          navigator.clipboard.writeText(b.dataset.copy).then(() => { b.textContent = "복사됨 ✓"; });
        })
      );
    } catch (e) {
      out.innerHTML = `<span class="err-line">${esc(e.message)}</span>`;
    } finally {
      btn.disabled = false;
    }
  });
});

// renderConnect는 발급된 키가 이미 박힌 AI 에이전트 연동 블록을 만든다.
// 사용자가 채울 칸이 없어야 복붙 사고가 안 난다. 키가 URL/명령에 노출되므로
// 화면 공유·공개 저장소 커밋 주의 문구를 함께 둔다.
function renderConnect(key) {
  const base = location.origin;
  const cli = `claude mcp add --transport http b2d-geo ${base}/mcp --header "Authorization: Bearer ${key}"`;
  const json = JSON.stringify(
    { mcpServers: { "b2d-geo": { url: `${base}/mcp`, headers: { Authorization: `Bearer ${key}` } } } },
    null, 2
  );
  const keyedURL = `${base}/mcp/${key}`;
  const cursorLink = "cursor://anysphere.cursor-deeplink/mcp/install?name=b2d-geo&config=" +
    encodeURIComponent(btoa(JSON.stringify({ url: `${base}/mcp`, headers: { Authorization: `Bearer ${key}` } })));
  const block = (title, text, hint) =>
    `<div style="margin-top:14px"><b style="font-size:13px">${title}</b>` +
    (hint ? `<span class="muted" style="margin-left:8px">${hint}</span>` : "") +
    `<div class="issued" style="margin-top:6px"><code style="white-space:pre-wrap;word-break:break-all">${esc(text)}</code>` +
    `<button class="btn ghost" data-copy="${esc(text)}">복사</button></div></div>`;
  return (
    `<div style="margin-top:18px"><b>AI 에이전트에 바로 연결하기</b>` +
    `<p class="muted" style="margin:4px 0 0">아래는 방금 발급된 키가 이미 들어간 완성본입니다. 하나 골라 그대로 쓰면 됩니다.</p>` +
    `<p style="margin-top:10px"><a class="btn" href="${cursorLink}">Cursor에 원클릭 추가</a></p>` +
    block("Claude Code (터미널 한 줄)", cli) +
    block("Claude Desktop 등 (설정 JSON)", json) +
    block("URL만 받는 도구", keyedURL, "키가 URL에 들어가니 화면 공유·공개 커밋 주의") +
    `<p class="muted" style="margin-top:10px">예시 질문: "서울 세종대로 110 주변 1km 카페 찾고, 그 필지 정보도 알려줘"</p></div>`
  );
}

// ---- 요금제(문서 하단) ----
document.getElementById("pricing").innerHTML = `
  <h3>요금제</h3>
  <p>원가는 공공데이터라 볼륨 경쟁이 아닌 <b>깊이</b>로 승부합니다. 카카오/네이버가 못 파는 필지·공시지가·감정평가 그래프.</p>
  <p>관리형 API·온프레미스·벌크 배치 안내는 <a href="https://api.birth2death.com/pricing.html" rel="noopener">라이브 서비스</a>에서 제공합니다.</p>
  <p class="muted" style="margin-top:14px">데이터 출처: 국토교통부·행정안전부·한국부동산원·NSDI 등 공공데이터. 공공누리 약관 준수.</p>
`;
