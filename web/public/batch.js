// 배치 지오코딩: 여러 주소 → POST /v1/geocode/batch → 표 + CSV.

const SAMPLE = [
  "서울특별시 중구 세종대로 110",
  "경기도 하남시 대청로 10",
  "경기도 성남시 분당구 판교역로 235",
  "제주특별자치도 제주시 광양11길 10",
];

let lastRows = null; // CSV용

document.getElementById("sample").addEventListener("click", () => {
  document.getElementById("input").value = SAMPLE.join("\n");
});
document.getElementById("run").addEventListener("click", run);
document.getElementById("csv").addEventListener("click", downloadCSV);

async function run() {
  const addresses = document.getElementById("input").value
    .split("\n").map((s) => s.trim()).filter(Boolean);
  const out = document.getElementById("out");
  const csvBtn = document.getElementById("csv");
  csvBtn.disabled = true;
  if (!addresses.length) {
    out.innerHTML = `<div class="batch-empty">주소를 한 줄에 하나씩 입력하세요.</div>`;
    return;
  }
  if (addresses.length > 1000) {
    out.innerHTML = `<div class="batch-empty err-line">한 번에 1,000건까지 처리합니다. (${addresses.length}건 입력됨)</div>`;
    return;
  }
  out.innerHTML = `<div class="loading"><span class="spin"></span>${addresses.length}건 변환 중…</div>`;

  try {
    const r = await api("/geocode/batch", null, { method: "POST", body: { addresses } });
    const items = r.items || [];
    const rows = items.map((it, i) => {
      const res = it.result;
      return {
        idx: i + 1,
        q: it.q,
        match: res ? res.match : "error",
        lon: res && res.point ? res.point[0] : "",
        lat: res && res.point ? res.point[1] : "",
        bcode: res ? res.bcode : "",
        note: it.error || (res && res.match === "ambiguous" ? (res.candidates || []).join(" | ") : ""),
      };
    });
    lastRows = rows;
    renderTable(rows);
    const ok = rows.filter((x) => x.match === "exact").length;
    document.getElementById("count").textContent = `${rows.length}건 중 ${ok}건 매칭`;
    csvBtn.disabled = rows.length === 0;
  } catch (e) {
    out.innerHTML = `<div class="batch-empty err-line">실패: ${esc(e.message)}</div>`;
  }
}

function renderTable(rows) {
  const body = rows
    .map(
      (x) => `<tr>
        <td class="idx">${x.idx}</td>
        <td>${esc(x.q)}</td>
        <td><span class="match-badge ${x.match}">${esc(x.match)}</span></td>
        <td class="mono">${x.lat !== "" ? `${Number(x.lat).toFixed(6)}, ${Number(x.lon).toFixed(6)}` : "-"}</td>
        <td class="mono">${esc(x.bcode || "-")}</td>
        <td class="muted">${esc(x.note || "")}</td>
      </tr>`
    )
    .join("");
  document.getElementById("out").innerHTML = `
    <table class="batch-table">
      <thead><tr><th>#</th><th>주소</th><th>매칭</th><th>좌표 (위도, 경도)</th><th>법정동코드</th><th>비고</th></tr></thead>
      <tbody>${body}</tbody>
    </table>`;
}

function downloadCSV() {
  if (!lastRows) return;
  const header = ["입력주소", "매칭", "위도", "경도", "법정동코드", "비고"];
  const lines = [header.join(",")].concat(
    lastRows.map((x) =>
      [x.q, x.match, x.lat, x.lon, x.bcode, x.note].map(csvCell).join(",")
    )
  );
  const blob = new Blob(["﻿" + lines.join("\r\n")], { type: "text/csv;charset=utf-8" });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = "geocode_batch.csv";
  a.click();
  URL.revokeObjectURL(a.href);
}

function csvCell(v) {
  const s = String(v == null ? "" : v);
  return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
}
function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])
  );
}
