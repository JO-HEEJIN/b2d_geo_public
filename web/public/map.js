// 지도 데모: 주소 검색 또는 지도 클릭 → 그 지점의 필지·공시지가·용도지역·
// 토지특성·주변 상가·표준지를 조회해 지도와 패널에 표시한다. 좌표는 모두
// [lon, lat](GeoJSON 순서); Leaflet에는 [lat, lon]으로 넘긴다.

// Leaflet 기본 마커 아이콘 경로를 vendor로. mergeOptions로 전체 URL을 주면
// Leaflet이 CSS에서 자동 감지한 imagePath를 그 앞에 또 붙여 경로가 중복된다
// (/vendor/images/vendor/images/... 404). imagePath만 지정하면 기본 파일명
// (marker-icon.png 등)에 이 경로가 붙는다.
L.Icon.Default.imagePath = "/vendor/images/";

const map = L.map("map", { zoomControl: true }).setView([37.5665, 126.978], 11);
L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
  maxZoom: 19,
  attribution: '© OpenStreetMap',
}).addTo(map);

const results = document.getElementById("results");
let pinMarker = null;
let parcelLayer = null;
let poiLayer = L.layerGroup().addTo(map);
let lotsLayer = L.layerGroup().addTo(map);
let lastPoint = null; // {lat, lon}

const EXAMPLES = [
  "경기도 하남시 대청로 10",
  "서울특별시 중구 세종대로 110",
  "경기도 성남시 분당구 판교역로 235",
];

// ---------- 조회 진입점 ----------
async function queryPoint(lat, lon, opts = {}) {
  lastPoint = { lat, lon };
  setPin(lat, lon);
  if (!opts.keepView) map.setView([lat, lon], Math.max(map.getZoom(), 17));
  results.innerHTML = `<div class="loading"><span class="spin"></span>토지 정보 조회 중…</div>`;

  // 1) 역지오코딩 + 필지(폴리곤 포함)를 병렬로.
  const [rev, parcel] = await Promise.allSettled([
    api("/reverse", { lat, lon }),
    api("/parcel/by-point", { lat, lon, geometry: "true" }),
  ]);

  let html = "";
  html += renderAddress(rev, parcel);

  const parcelData = parcel.status === "fulfilled" ? parcel.value : null;
  if (parcelData && parcelData.geometry) drawParcel(parcelData.geometry);
  else clearParcel();

  results.innerHTML = html + `<div class="loading" id="more"><span class="spin"></span>부동산 필드 조회 중…</div>`;

  // 2) pnu가 있으면 감정평가 필드들을 병렬 조회.
  if (parcelData && parcelData.pnu) {
    const pnu = parcelData.pnu;
    const [price, landUse, features] = await Promise.allSettled([
      api("/appraisal/official-price", { pnu }),
      api("/appraisal/land-use", { pnu }),
      api("/appraisal/land-features", { pnu }),
    ]);
    let more = "";
    more += renderParcelCard(parcelData);
    more += renderPrice(price);
    more += renderFeatures(features);
    more += renderLandUse(landUse);
    more += renderDeep(parcelData);
    document.getElementById("more").outerHTML = more;
    wireDeep(parcelData);
  } else {
    document.getElementById("more").outerHTML =
      `<div class="card"><div class="body muted">이 지점에서 필지를 찾지 못했습니다. 건물·도로·하천 위이거나 적재되지 않은 지역일 수 있습니다.</div></div>`;
  }

  refreshOverlays();
}

async function searchAddress(q) {
  if (!q || q.length < 5) return;
  results.innerHTML = `<div class="loading"><span class="spin"></span>주소 검색 중…</div>`;
  try {
    const r = await api("/geocode", { q });
    if (r.match === "ambiguous") {
      results.innerHTML =
        `<div class="card"><header>검색 결과</header><div class="body">` +
        `<div class="muted">여러 후보가 있습니다. 더 구체적으로 입력하세요.</div>` +
        (r.candidates || []).map((c) => `<div class="poi-item"><span class="pname">${esc(c)}</span></div>`).join("") +
        `</div></div>`;
      return;
    }
    if (!r.point) throw new Error("좌표를 찾지 못했습니다");
    const [lon, lat] = r.point;
    await queryPoint(lat, lon);
  } catch (e) {
    results.innerHTML = `<div class="card"><div class="body err-line">검색 실패: ${esc(errText(e))}</div></div>`;
  }
}

// ---------- 렌더러 ----------
function renderAddress(rev, parcel) {
  const road =
    (rev.status === "fulfilled" && rev.value.road_address) ||
    (parcel.status === "fulfilled" && parcel.value.address_name) ||
    "주소 미상";
  const sub =
    (rev.status === "fulfilled" && rev.value.address_name) ||
    (parcel.status === "fulfilled" &&
      `${parcel.value.address_name || ""} ${parcel.value.jibun || ""}`) ||
    "";
  return `<div class="card"><header>위치</header><div class="body">
    <div class="addr-line">${esc(road)}</div>
    <div class="addr-sub">${esc(sub.trim())}</div>
  </div></div>`;
}

function renderParcelCard(p) {
  return `<div class="card"><header>필지 <span class="tag mono">${esc(p.pnu)}</span></header><div class="body">
    <dl class="kv">
      <dt>지번</dt><dd>${esc(p.jibun || "-")}</dd>
      <dt>지목</dt><dd>${esc(p.land_category || "-")}</dd>
      <dt>법정동</dt><dd>${esc(p.address_name || "-")}</dd>
      <dt>기준</dt><dd class="mono">${esc(p.source_version || "-")}</dd>
    </dl>
  </div></div>`;
}

function renderPrice(price) {
  if (price.status !== "fulfilled") return card404("공시지가", "해당 지역 미적재");
  const p = price.value;
  const typeLabel = p.price_type === "INDIVIDUAL" ? "개별공시지가" : "표준지 기준 추정";
  return `<div class="card"><header>공시지가 <span class="tag">${p.base_year}년</span></header><div class="body">
    <div class="price">${formatWon(p.price_per_sqm)}<span class="unit">원/㎡</span></div>
    <div class="price-type">${typeLabel}</div>
  </div></div>`;
}

function renderFeatures(f) {
  if (f.status !== "fulfilled") return "";
  const v = f.value;
  return `<div class="card"><header>토지특성 <span class="tag">${v.base_year || ""}</span></header><div class="body">
    <dl class="kv">
      <dt>이용상황</dt><dd>${esc(v.land_use || "-")}</dd>
      <dt>지형고저</dt><dd>${esc(v.terrain_height || "-")}</dd>
      <dt>형상</dt><dd>${esc(v.terrain_shape || "-")}</dd>
      <dt>도로접면</dt><dd>${esc(v.road_side || "-")}</dd>
    </dl>
  </div></div>`;
}

function renderLandUse(lu) {
  if (lu.status !== "fulfilled" || !lu.value.zones || !lu.value.zones.length) return "";
  const zones = lu.value.zones;
  const shown = zones.slice(0, 6);
  const rows = shown
    .map(
      (z) => `<div class="zone">
        <span class="rel ${relClass(z.relation)}">${esc(z.relation || "")}</span>
        <span class="zname">${esc(z.zone_name)}</span>
        <span class="zcode">${esc(z.zone_code)}</span>
      </div>`
    )
    .join("");
  const more = zones.length > shown.length ? `<div class="zone-more">외 ${zones.length - shown.length}개 지역·지구</div>` : "";
  return `<div class="card"><header>용도지역·지구 <span class="tag">${zones.length}건</span></header><div class="body">
    <div class="zones">${rows}${more}</div>
  </div></div>`;
}

function card404(title, msg) {
  return `<div class="card"><header>${esc(title)}</header><div class="body muted">${esc(msg)}</div></div>`;
}

// ---------- 심화 데이터: 실거래 + 지가변동률 ----------
const ZONE_CLASSES = ["주거지역", "상업지역", "공업지역", "녹지지역"];

function renderDeep(p) {
  const sgg = (p.bcode || "").slice(0, 5);
  if (sgg.length !== 5) return "";
  const zopts = ZONE_CLASSES.map((z) => `<option value="${z}">${z}</option>`).join("");
  return `<div class="card"><header>실거래 <span class="tag mono">시군구 ${esc(sgg)}</span></header><div class="body">
      <div class="deep-ctl">
        <input id="salesYm" value="202401" maxlength="6" size="6" inputmode="numeric" aria-label="거래월 YYYYMM">
        <button class="btn" id="salesGo">조회</button>
      </div>
      <div id="salesOut" class="muted">거래월(YYYYMM)을 입력하고 조회하세요.</div>
    </div></div>
    <div class="card"><header>지가변동률 <span class="tag mono">${esc(sgg)}</span></header><div class="body">
      <div class="deep-ctl">
        <select id="ziClass" aria-label="용도지역">${zopts}</select>
        <span class="muted">월별 변동률(%)</span>
      </div>
      <div id="ziOut" class="muted"><span class="spin"></span></div>
    </div></div>`;
}

function wireDeep(p) {
  const sgg = (p.bcode || "").slice(0, 5);
  if (sgg.length !== 5) return;
  const salesGo = document.getElementById("salesGo");
  if (salesGo) salesGo.addEventListener("click", () => loadSales(sgg));
  const ziClass = document.getElementById("ziClass");
  if (ziClass) {
    ziClass.addEventListener("change", () => loadPriceIndex(sgg, ziClass.value));
    loadPriceIndex(sgg, ziClass.value); // 최초 자동 로드
  }
}

async function loadSales(sgg) {
  const ym = (document.getElementById("salesYm").value || "").trim();
  const box = document.getElementById("salesOut");
  if (!/^\d{6}$/.test(ym)) { box.innerHTML = `<span class="err-line">YYYYMM 형식으로 입력하세요.</span>`; return; }
  box.innerHTML = `<span class="spin"></span>조회 중…`;
  try {
    const r = await api("/appraisal/sales", { lawd_cd: sgg, deal_ymd: ym });
    const items = r.items || [];
    if (!items.length) { box.innerHTML = `<span class="muted">${ym}에 조회된 실거래가 없습니다.</span>`; return; }
    const rows = items.slice(0, 8).map((it) =>
      `<div class="sales-row">
        <span class="amt">${formatManwon(it.deal_amount_manwon)}</span>
        <span>${esc(it.jimok || "")} ${esc(it.deal_area_m2 || "")}㎡</span>
        <span class="meta">${esc(it.umd_nm || "")} · ${it.deal_month}/${it.deal_day}</span>
      </div>`).join("");
    const more = items.length > 8 ? `<div class="zone-more">외 ${items.length - 8}건</div>` : "";
    box.innerHTML = `<div class="muted" style="margin-bottom:6px">${items.length}건</div>${rows}${more}`;
  } catch (e) {
    box.innerHTML = `<span class="err-line">${esc(errText(e))}</span>`;
  }
}

async function loadPriceIndex(sgg, cls) {
  const box = document.getElementById("ziOut");
  box.innerHTML = `<span class="spin"></span>`;
  try {
    const r = await api("/appraisal/price-index", { sigungu: sgg, use_zone_class: cls });
    const series = (r.series || []).filter((s) => s.rate_pct != null);
    if (!series.length) { box.innerHTML = `<span class="muted">해당 용도지역 데이터가 없습니다.</span>`; return; }
    box.innerHTML = sparkline(series);
  } catch (e) {
    box.innerHTML = `<span class="err-line">${esc(errText(e))}</span>`;
  }
}

// 의존성 없는 SVG 스파크라인(월별 변동률%). 0선 기준 area+line.
function sparkline(series) {
  const W = 320, H = 56, pad = 4;
  const vals = series.map((s) => s.rate_pct);
  const min = Math.min(0, ...vals), max = Math.max(0, ...vals);
  const span = max - min || 1;
  const x = (i) => pad + (i * (W - 2 * pad)) / Math.max(1, series.length - 1);
  const y = (v) => H - pad - ((v - min) / span) * (H - 2 * pad);
  const pts = series.map((s, i) => `${x(i).toFixed(1)},${y(s.rate_pct).toFixed(1)}`);
  const area = `M${pts[0]} L${pts.join(" L")} L${x(series.length - 1).toFixed(1)},${y(min).toFixed(1)} L${x(0).toFixed(1)},${y(min).toFixed(1)} Z`;
  const last = series[series.length - 1];
  return `<div class="spark-wrap">
    <svg class="spark" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-label="지가변동률 추이">
      <path class="ar" d="${area}"/>
      <polyline class="ln" points="${pts.join(" ")}"/>
      <circle class="dot" cx="${x(series.length - 1).toFixed(1)}" cy="${y(last.rate_pct).toFixed(1)}" r="2.5"/>
    </svg>
    <div class="spark-cap"><span>${esc(fmtMonth(series[0].month))}</span>
      <span>최근 ${last.rate_pct > 0 ? "+" : ""}${last.rate_pct}% (${esc(fmtMonth(last.month))})</span></div>
  </div>`;
}
function fmtMonth(m) { const s = String(m); return s.length === 6 ? `${s.slice(0, 4)}.${s.slice(4)}` : s; }

// ---------- 오버레이(주변 상가 / 표준지) ----------
async function refreshOverlays() {
  if (!lastPoint) return;
  const { lat, lon } = lastPoint;
  poiLayer.clearLayers();
  lotsLayer.clearLayers();
  if (document.getElementById("poiToggle").checked) loadPOI(lat, lon);
  if (document.getElementById("lotsToggle").checked) loadLots(lat, lon);
}

async function loadPOI(lat, lon) {
  try {
    // q 없이 lat/lon만 주면 서버가 반경 내 근접순 브라우즈 모드로 응답한다.
    const r = await api("/places/search", { lat, lon, radius: 500, limit: 50 });
    (r.items || []).slice(0, 60).forEach((it) => {
      if (!it.point) return;
      const [plon, plat] = it.point;
      L.circleMarker([plat, plon], { radius: 4, color: "#0f766e", weight: 1, fillOpacity: 0.7 })
        .bindPopup(`<b>${esc(it.name)}</b><br><span class="mono">${esc(it.category_code || "")}</span><br>${esc(it.road_address || "")}`)
        .addTo(poiLayer);
    });
  } catch (e) {
    /* 상가 검색은 q가 필요할 수 있음 — 실패해도 조용히 무시 */
  }
}

async function loadLots(lat, lon) {
  try {
    const r = await api("/appraisal/standard-lots", { lat, lng: lon, radius: 1000 });
    (r.standard_lots || []).forEach((s) => {
      L.circleMarker([s.lat, s.lng], { radius: 5, color: "#a45a09", weight: 1.5, fillColor: "#eab34d", fillOpacity: 0.8 })
        .bindPopup(
          `<b>표준지</b> <span class="mono">${esc(s.pnu)}</span><br>` +
            `${formatWon(s.price_per_sqm)}원/㎡<br>${esc(s.use_zone || "")} · ${esc(s.land_use || "")}<br>` +
            `<span class="mono">${s.distance_m}m</span>`
        )
        .addTo(lotsLayer);
    });
  } catch (e) {
    /* 미적재 지역이면 조용히 무시 */
  }
}

// ---------- 지도 요소 ----------
function setPin(lat, lon) {
  if (pinMarker) pinMarker.setLatLng([lat, lon]);
  else pinMarker = L.marker([lat, lon]).addTo(map);
}
function drawParcel(geometry) {
  clearParcel();
  parcelLayer = L.geoJSON(geometry, {
    style: { color: "#0f766e", weight: 2, fillColor: "#0f766e", fillOpacity: 0.12 },
  }).addTo(map);
  try {
    map.fitBounds(parcelLayer.getBounds(), { maxZoom: 18, padding: [30, 30] });
  } catch (e) {}
}
function clearParcel() {
  if (parcelLayer) { map.removeLayer(parcelLayer); parcelLayer = null; }
}

// ---------- 유틸 ----------
function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])
  );
}
function relClass(rel) {
  if (rel === "포함") return "포함";
  if (rel === "저촉") return "저촉";
  return "접함";
}

// ---------- 이벤트 ----------
document.getElementById("go").addEventListener("click", () => searchAddress(document.getElementById("q").value.trim()));
document.getElementById("q").addEventListener("keydown", (e) => {
  if (e.key === "Enter") searchAddress(e.target.value.trim());
});
map.on("click", (e) => queryPoint(e.latlng.lat, e.latlng.lng, { keepView: true }));
document.getElementById("poiToggle").addEventListener("change", refreshOverlays);
document.getElementById("lotsToggle").addEventListener("change", refreshOverlays);

const exWrap = document.getElementById("examples");
EXAMPLES.forEach((ex) => {
  const b = document.createElement("button");
  b.className = "chip";
  b.textContent = ex;
  b.onclick = () => { document.getElementById("q").value = ex; searchAddress(ex); };
  exWrap.appendChild(b);
});

// 공유 가능한 딥링크: /map.html?q=주소  또는  ?lat=..&lon=..
(function fromURL() {
  const p = new URLSearchParams(location.search);
  const q = p.get("q");
  const lat = parseFloat(p.get("lat"));
  const lon = parseFloat(p.get("lon"));
  if (q) { document.getElementById("q").value = q; searchAddress(q); }
  else if (isFinite(lat) && isFinite(lon)) queryPoint(lat, lon);
})();
