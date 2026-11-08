// Scalper Logs Viewer
function initLogsViewer(): void {
  let allLines: string[] = [];
  let isInitialLoad = true;
  let isUserScrolledUp = false;
  const container = document.getElementById("log-container") as HTMLDivElement | null;
  const filterInput = document.getElementById("filter-input") as HTMLInputElement | null;
  const linesSelect = document.getElementById("lines-select") as HTMLSelectElement | null;
  const autoscrollToggle = document.getElementById("autoscroll-toggle") as HTMLInputElement | null;
  const autorefreshToggle = document.getElementById("autorefresh-toggle") as HTMLInputElement | null;
  const summaryEl = document.getElementById("log-summary") as HTMLElement | null;
  const updatedEl = document.getElementById("last-updated") as HTMLElement | null;
  const pathEl = document.getElementById("log-path") as HTMLElement | null;

  if (container) {
    container.addEventListener("scroll", () => {
      const distanceFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight;
      const atBottom = distanceFromBottom <= 40;
      isUserScrolledUp = !atBottom;

      // When the user scrolls up to inspect logs, pause auto-scroll and uncheck the toggle
      if (autoscrollToggle && isUserScrolledUp && autoscrollToggle.checked) {
        autoscrollToggle.checked = false;
      }
    }, { passive: true });
  }

  if (autoscrollToggle) {
    autoscrollToggle.addEventListener("change", () => {
      if (autoscrollToggle.checked && container) {
        isUserScrolledUp = false;
        container.scrollTop = container.scrollHeight;
      }
    });
  }

  let isFetching = false;
  async function fetchLogs(): Promise<void> {
    if (isFetching) return;
    isFetching = true;
    try {
      const linesCount = linesSelect ? linesSelect.value : "500";
      const res = await fetch('/api/logs?lines=' + encodeURIComponent(linesCount));
      if (!res.ok) {
        throw new Error('HTTP ' + res.status + ' ' + (res.statusText || ''));
      }
      const data = await res.json();
      allLines = data.lines || [];
      if (data.path && pathEl) pathEl.textContent = data.path;
      renderLogs();
      if (updatedEl) updatedEl.textContent = 'Updated: ' + new Date().toLocaleTimeString();
    } catch (err: any) {
      console.warn("fetch logs error:", err);
      if (allLines.length === 0 && container) {
        container.innerHTML = '<div style="color:var(--err); text-align:center; padding:40px;">Failed to load logs: ' +
          (err?.message || err) + '</div>';
        if (summaryEl) summaryEl.textContent = 'Error loading logs';
      }
    } finally {
      isFetching = false;
    }
  }

  function renderLogs(): void {
    if (!container) return;
    const q = filterInput ? filterInput.value.toLowerCase().trim() : '';
    const filtered = q ? allLines.filter(l => l.toLowerCase().includes(q)) : allLines;

    // Capture current scroll offset and bottom proximity before DOM replacement
    const prevScrollTop = container.scrollTop;
    const wasNearBottom = (container.scrollHeight - container.scrollTop - container.clientHeight) <= 40;

    if (filtered.length === 0) {
      container.innerHTML = '<div style="color:var(--text-dim); text-align:center; padding:40px;">No matching log lines.</div>';
      if (summaryEl) summaryEl.textContent = 'Showing 0 / ' + allLines.length + ' lines';
      return;
    }

    let html = '';
    for (let i = 0; i < filtered.length; i++) {
      const line = filtered[i];
      let cls = 'log-info';
      if (/ERROR|FATAL/i.test(line)) cls = 'log-error';
      else if (/WARN/i.test(line)) cls = 'log-warn';
      const escaped = line.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      html += '<div class="log-line ' + cls + '"><span class="line-num">' + (i + 1) + '</span>' + escaped + '</div>';
    }
    container.innerHTML = html;
    if (summaryEl) summaryEl.textContent = 'Showing ' + filtered.length + ' of ' + allLines.length + ' lines';

    if (isInitialLoad) {
      // First load: scroll to bottom so the user sees the newest lines
      container.scrollTop = container.scrollHeight;
      isInitialLoad = false;
    } else if (autoscrollToggle && autoscrollToggle.checked && wasNearBottom && !isUserScrolledUp) {
      // Tail mode: stay at bottom if already at bottom and autoscroll is checked
      container.scrollTop = container.scrollHeight;
    } else {
      // Inspected mode: keep the exact scroll position so lines being read do NOT jump away
      container.scrollTop = prevScrollTop;
    }
  }

  if (filterInput) filterInput.addEventListener('input', renderLogs);
  if (linesSelect) linesSelect.addEventListener('change', fetchLogs);
  const refreshBtn = document.getElementById('refresh-btn');
  if (refreshBtn) refreshBtn.addEventListener('click', fetchLogs);

  setInterval(() => {
    if (autorefreshToggle && autorefreshToggle.checked) fetchLogs();
  }, 3000);

  fetchLogs();
}

if (typeof document !== 'undefined') {
  initLogsViewer();
}
