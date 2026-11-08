// src/logs.ts
function initLogsViewer() {
  let allLines = [];
  const container = document.getElementById("log-container");
  const filterInput = document.getElementById("filter-input");
  const linesSelect = document.getElementById("lines-select");
  const autoscrollToggle = document.getElementById("autoscroll-toggle");
  const autorefreshToggle = document.getElementById("autorefresh-toggle");
  const summaryEl = document.getElementById("log-summary");
  const updatedEl = document.getElementById("last-updated");
  const pathEl = document.getElementById("log-path");
  async function fetchLogs() {
    try {
      const linesCount = linesSelect ? linesSelect.value : "500";
      const res = await fetch("/api/logs?lines=" + encodeURIComponent(linesCount));
      if (!res.ok) {
        throw new Error("HTTP " + res.status + " " + (res.statusText || ""));
      }
      const data = await res.json();
      allLines = data.lines || [];
      if (data.path && pathEl) pathEl.textContent = data.path;
      renderLogs();
      if (updatedEl) updatedEl.textContent = "Updated: " + (/* @__PURE__ */ new Date()).toLocaleTimeString();
    } catch (err) {
      console.warn("fetch logs error:", err);
      if (allLines.length === 0 && container) {
        container.innerHTML = '<div style="color:var(--err); text-align:center; padding:40px;">Failed to load logs: ' + (err?.message || err) + "</div>";
        if (summaryEl) summaryEl.textContent = "Error loading logs";
      }
    }
  }
  function renderLogs() {
    if (!container) return;
    const q = filterInput ? filterInput.value.toLowerCase().trim() : "";
    const filtered = q ? allLines.filter((l) => l.toLowerCase().includes(q)) : allLines;
    if (filtered.length === 0) {
      container.innerHTML = '<div style="color:var(--text-dim); text-align:center; padding:40px;">No matching log lines.</div>';
      if (summaryEl) summaryEl.textContent = "Showing 0 / " + allLines.length + " lines";
      return;
    }
    let html = "";
    for (let i = 0; i < filtered.length; i++) {
      const line = filtered[i];
      let cls = "log-info";
      if (/ERROR|FATAL/i.test(line)) cls = "log-error";
      else if (/WARN/i.test(line)) cls = "log-warn";
      const escaped = line.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
      html += '<div class="log-line ' + cls + '"><span class="line-num">' + (i + 1) + "</span>" + escaped + "</div>";
    }
    container.innerHTML = html;
    if (summaryEl) summaryEl.textContent = "Showing " + filtered.length + " of " + allLines.length + " lines";
    if (autoscrollToggle && autoscrollToggle.checked) {
      container.scrollTop = container.scrollHeight;
    }
  }
  if (filterInput) filterInput.addEventListener("input", renderLogs);
  if (linesSelect) linesSelect.addEventListener("change", fetchLogs);
  const refreshBtn = document.getElementById("refresh-btn");
  if (refreshBtn) refreshBtn.addEventListener("click", fetchLogs);
  setInterval(() => {
    if (autorefreshToggle && autorefreshToggle.checked) fetchLogs();
  }, 3e3);
  fetchLogs();
}
if (typeof document !== "undefined") {
  initLogsViewer();
}
//# sourceMappingURL=logs.js.map
