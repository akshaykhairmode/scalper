// web/src/settings.ts
(function() {
  "use strict";
  let currentConfig = null;
  let currentYAML = "";
  let savedYAML = "";
  let isDirty = false;
  let activeTabId = "tab-trading";
  const el = (id) => document.getElementById(id);
  const validateBtn = el("validate-btn");
  const saveBtn = el("save-btn");
  const saveSpinner = el("save-spinner");
  const saveLabel = el("save-label");
  const unsavedBadge = el("unsaved-badge");
  const alertBanner = el("alert-banner");
  const alertTitle = el("alert-title");
  const alertMessage = el("alert-message");
  const alertCloseBtn = el("alert-close-btn");
  const toastContainer = el("toast-container");
  const yamlEditor = el("yaml-editor");
  const lineNumbers = el("editor-line-numbers");
  const yamlStatusBadge = el("yaml-status-badge");
  const yamlCursorPos = el("yaml-cursor-pos");
  const yamlCopyBtn = el("yaml-copy-btn");
  const yamlRevertBtn = el("yaml-revert-btn");
  const tabBtns = document.querySelectorAll(".tab-btn");
  const tabPanels = document.querySelectorAll(".tab-panel");
  const indexModal = el("index-modal");
  const indexModalTitle = el("index-modal-title");
  const indexModalForm = el("index-modal-form");
  const indexModalClose = el("index-modal-close");
  const indexModalCancel = el("index-modal-cancel");
  const addIndexBtn = el("add-index-btn");
  const commodityModal = el("commodity-modal");
  const commodityModalTitle = el("commodity-modal-title");
  const commodityModalForm = el("commodity-modal-form");
  const commodityModalClose = el("commodity-modal-close");
  const commodityModalCancel = document.getElementById("commodity-modal-cancel");
  const addCommodityBtn = document.getElementById("add-commodity-btn");
  function init() {
    setupTabs();
    setupEditor();
    setupFormInputs();
    setupModals();
    setupActions();
    setupDevices();
    const initialConfigEl = document.getElementById("initial-config-data");
    const initialYamlEl = document.getElementById("initial-yaml-data");
    if (initialYamlEl && initialYamlEl.textContent.trim()) {
      currentYAML = initialYamlEl.textContent;
      savedYAML = currentYAML;
      yamlEditor.value = currentYAML;
      updateLineNumbers();
    }
    if (initialConfigEl && initialConfigEl.textContent.trim()) {
      try {
        currentConfig = JSON.parse(initialConfigEl.textContent);
        populateForm(currentConfig);
      } catch (err) {
        console.warn("Failed to parse initial config JSON:", err);
      }
    }
    fetchConfig();
  }
  async function fetchConfig() {
    try {
      const res = await fetch("/api/config");
      if (!res.ok) {
        const errData = await res.json().catch(() => ({}));
        throw new Error(errData.error || `HTTP ${res.status}`);
      }
      const data = await res.json();
      if (data.yaml) {
        currentYAML = data.yaml;
        savedYAML = currentYAML;
        yamlEditor.value = currentYAML;
        updateLineNumbers();
        markDirty(false);
      }
      if (data.config) {
        currentConfig = data.config;
        populateForm(currentConfig);
      }
    } catch (err) {
      showToast("error", `Failed to load settings: ${err.message}`);
    }
  }
  function setupTabs() {
    tabBtns.forEach((btn) => {
      btn.addEventListener("click", async () => {
        const targetPanelId = btn.getAttribute("aria-controls");
        const prevTab = activeTabId;
        if (prevTab === "tab-yaml" && btn.id !== "tab-yaml") {
          currentYAML = yamlEditor.value;
          const valid = await validateAndSyncFromYAML();
          if (!valid) {
            return;
          }
        }
        switchTab(btn.id, targetPanelId);
      });
    });
  }
  function switchTab(btnId, panelId) {
    activeTabId = btnId;
    tabBtns.forEach((b) => {
      const isActive = b.id === btnId;
      b.classList.toggle("active", isActive);
      b.setAttribute("aria-selected", isActive ? "true" : "false");
    });
    tabPanels.forEach((p) => {
      const isTarget = p.id === panelId;
      p.classList.toggle("active", isTarget);
    });
    if (panelId === "panel-yaml") {
      yamlEditor.value = currentYAML;
      updateLineNumbers();
      yamlEditor.focus();
    }
    if (panelId === "panel-devices") {
      loadSettingsDevices();
    }
  }
  function setupDevices() {
    document.getElementById("settings-register-device-btn")?.addEventListener("click", registerSettingsDevice);
  }
  function bufferToBase64Url(buffer) {
    const bytes = new Uint8Array(buffer);
    let str = "";
    for (let i = 0; i < bytes.byteLength; i++) {
      str += String.fromCharCode(bytes[i]);
    }
    return btoa(str).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }
  function base64UrlToBuffer(base64Url) {
    if (!base64Url) return new ArrayBuffer(0);
    const base64 = base64Url.replace(/-/g, "+").replace(/_/g, "/");
    const pad = base64.length % 4;
    const padded = pad ? base64 + "=".repeat(4 - pad) : base64;
    const binary = atob(padded);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes.buffer;
  }
  async function loadSettingsDevices() {
    const tbody = document.getElementById("settings-devices-tbody");
    if (!tbody) return;
    try {
      const res = await fetch("/api/devices");
      const data = await res.json().catch(() => ({}));
      const devices = data.devices || [];
      if (devices.length === 0) {
        tbody.innerHTML = '<tr><td colspan="4" style="color:var(--text-muted, #888); text-align:center; padding:16px;">No biometric devices enrolled yet.</td></tr>';
        return;
      }
      tbody.innerHTML = devices.map((d) => {
        const lastUsed = d.last_used_at ? new Date(d.last_used_at).toLocaleDateString("en-IN", {
          month: "short",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit"
        }) : "\u2014";
        return `
          <tr>
            <td><strong>\u{1F4F1} ${d.device_name || "Passkey Device"}</strong></td>
            <td>${lastUsed}</td>
            <td>${d.sign_count || 0}</td>
            <td style="text-align:right;">
              <button type="button" class="btn danger small btn-revoke-setting-dev" data-id="${d.id}">Revoke</button>
            </td>
          </tr>
        `;
      }).join("");
      tbody.querySelectorAll(".btn-revoke-setting-dev").forEach((b) => {
        b.addEventListener("click", async () => {
          b.disabled = true;
          b.textContent = "Revoking...";
          await fetch(`/api/devices/${encodeURIComponent(b.dataset.id)}`, { method: "DELETE" });
          showToast("Device revoked", "info");
          loadSettingsDevices();
        });
      });
    } catch (err) {
      tbody.innerHTML = '<tr><td colspan="4" style="color:#ef4444; text-align:center; padding:16px;">Error loading devices</td></tr>';
    }
  }
  async function registerSettingsDevice() {
    if (!window.PublicKeyCredential) {
      showToast("WebAuthn not supported by this browser", "error");
      return;
    }
    const btn = document.getElementById("settings-register-device-btn");
    if (btn) {
      btn.disabled = true;
      btn.textContent = "Enrolling...";
    }
    try {
      const beginRes = await fetch("/api/auth/webauthn/register/begin", { method: "POST" });
      const data = await beginRes.json();
      if (!data.ok) throw new Error(data.error || "Failed to begin registration");
      const opts = data.options || data;
      const publicKey = {
        challenge: base64UrlToBuffer(opts.challenge),
        rp: opts.rp,
        user: {
          id: base64UrlToBuffer(opts.user.id),
          name: opts.user.name,
          displayName: opts.user.displayName
        },
        pubKeyCredParams: opts.pubKeyCredParams,
        authenticatorSelection: opts.authenticatorSelection || { userVerification: "preferred" },
        timeout: opts.timeout || 6e4,
        attestation: opts.attestation || "none"
      };
      const cred = await navigator.credentials.create({ publicKey });
      if (!cred) throw new Error("Cancelled");
      const finRes = await fetch("/api/auth/webauthn/register/finish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          deviceName: window.navigator?.userAgentData?.platform || "Desktop Browser",
          id: cred.id,
          rawId: bufferToBase64Url(cred.rawId),
          type: cred.type,
          response: {
            clientDataJSON: bufferToBase64Url(cred.response.clientDataJSON),
            attestationObject: bufferToBase64Url(cred.response.attestationObject)
          }
        })
      });
      const finData = await finRes.json();
      if (!finData.ok) throw new Error(finData.error || "Verification failed");
      if (finData.device?.id) localStorage.setItem("scalper_device_id", finData.device.id);
      if (finData.device?.device_name) localStorage.setItem("scalper_device_name", finData.device.device_name);
      if (finData.token) localStorage.setItem("scalper_serve_session", finData.token);
      showToast("Device registered successfully!", "success");
      loadSettingsDevices();
    } catch (err) {
      showToast(err.message || "Registration failed", "error");
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.textContent = "\u26A1 Register This Device";
      }
    }
  }
  function populateForm(cfg) {
    if (!cfg) return;
    const t = cfg.trading || {};
    const q = t.quantity || {};
    const r = t.risk || {};
    const s = t.session || {};
    const m = t.margin || {};
    setVal("f-default-product", t.default_product || "MIS");
    setVal("f-default-order-type", t.default_order_type || "MARKET");
    setVal("f-default-lots", q.default_lots || 1);
    setVal("f-default-unit", q.default_unit || "lots");
    setChecked("f-allow-shares", !!q.allow_shares);
    setChecked("f-confirm-live-orders", !!t.confirm_live_orders);
    setVal("f-max-daily-loss", r.max_daily_loss_inr !== void 0 ? r.max_daily_loss_inr : 5e3);
    setVal("f-max-orders-min", r.max_orders_per_minute !== void 0 ? r.max_orders_per_minute : 6);
    setVal("f-max-exit-sec", r.max_exit_orders_per_second !== void 0 ? r.max_exit_orders_per_second : 4);
    setVal("f-stale-timeout", r.block_on_data_stale_ms !== void 0 ? r.block_on_data_stale_ms : 5e3);
    setVal("f-max-lots-order", r.max_lots_per_order !== void 0 ? r.max_lots_per_order : 0);
    setVal("f-max-open-lots", r.max_open_lots_per_index !== void 0 ? r.max_open_lots_per_index : 0);
    setVal("f-max-order-val", r.max_order_value_inr !== void 0 ? r.max_order_value_inr : 0);
    setVal("f-session-open", s.open || "09:15");
    setVal("f-session-close", s.close || "15:30");
    setChecked("f-allow-outside-hours", !!s.allow_outside_hours);
    setChecked("f-margin-check", !!m.check_before_order);
    setChecked("f-margin-fail-closed", !!m.fail_closed);
    const mkt = cfg.market || {};
    const d = mkt.depth || {};
    const c = mkt.chain || {};
    const vix = mkt.india_vix || {};
    setVal("f-data-mode", mkt.data_mode || "websocket");
    setVal("f-depth-mode", d.mode || "poll");
    setChecked("f-depth-enabled", !!d.enabled);
    setVal("f-visible-poll", c.visible_poll_ms || 1e3);
    setVal("f-full-poll", c.full_poll_ms || 5e3);
    setVal("f-chain-batch-size", c.batch_size || 40);
    setChecked("f-vix-enabled", !!vix.enabled);
    setVal("f-vix-segment", vix.exchange_segment || "nse_cm");
    setVal("f-vix-symbol", vix.symbol || "INDIA VIX");
    setVal("f-vix-scrip", vix.scrip_name || "INDIA VIX");
    renderIndicesTable(mkt.indices || []);
    renderCommoditiesTable(mkt.commodities || []);
    const lg = cfg.logging || {};
    setVal("f-log-level", lg.level || "info");
    setVal("f-log-format", lg.format || "text");
    setVal("f-log-file", lg.file || "./logs/scalper.log");
    setVal("f-max-size-mb", lg.max_size_mb || 100);
    setVal("f-max-backups", lg.max_backups || 10);
    setVal("f-retention-days", lg.retention_days || 14);
    setVal("f-tick-sample", lg.tick_sample_seconds !== void 0 ? lg.tick_sample_seconds : 1);
    setChecked("f-log-redact", true);
  }
  function setVal(id, val) {
    const el2 = document.getElementById(id);
    if (el2) el2.value = val;
  }
  function setChecked(id, checked) {
    const el2 = document.getElementById(id);
    if (el2) el2.checked = checked;
  }
  function setupFormInputs() {
    const map = [
      // Trading
      { id: "f-default-product", path: ["trading", "default_product"], type: "string" },
      { id: "f-default-order-type", path: ["trading", "default_order_type"], type: "string" },
      { id: "f-default-lots", path: ["trading", "quantity", "default_lots"], type: "int" },
      { id: "f-default-unit", path: ["trading", "quantity", "default_unit"], type: "string" },
      { id: "f-allow-shares", path: ["trading", "quantity", "allow_shares"], type: "bool" },
      { id: "f-confirm-live-orders", path: ["trading", "confirm_live_orders"], type: "bool" },
      { id: "f-max-daily-loss", path: ["trading", "risk", "max_daily_loss_inr"], type: "int" },
      { id: "f-max-orders-min", path: ["trading", "risk", "max_orders_per_minute"], type: "int" },
      { id: "f-max-exit-sec", path: ["trading", "risk", "max_exit_orders_per_second"], type: "int" },
      { id: "f-stale-timeout", path: ["trading", "risk", "block_on_data_stale_ms"], type: "int" },
      { id: "f-max-lots-order", path: ["trading", "risk", "max_lots_per_order"], type: "int" },
      { id: "f-max-open-lots", path: ["trading", "risk", "max_open_lots_per_index"], type: "int" },
      { id: "f-max-order-val", path: ["trading", "risk", "max_order_value_inr"], type: "int" },
      { id: "f-session-open", path: ["trading", "session", "open"], type: "string" },
      { id: "f-session-close", path: ["trading", "session", "close"], type: "string" },
      { id: "f-allow-outside-hours", path: ["trading", "session", "allow_outside_hours"], type: "bool" },
      { id: "f-margin-check", path: ["trading", "margin", "check_before_order"], type: "bool" },
      { id: "f-margin-fail-closed", path: ["trading", "margin", "fail_closed"], type: "bool" },
      // Market
      { id: "f-data-mode", path: ["market", "data_mode"], type: "string" },
      { id: "f-depth-mode", path: ["market", "depth", "mode"], type: "string" },
      { id: "f-depth-enabled", path: ["market", "depth", "enabled"], type: "bool" },
      { id: "f-visible-poll", path: ["market", "chain", "visible_poll_ms"], type: "int" },
      { id: "f-full-poll", path: ["market", "chain", "full_poll_ms"], type: "int" },
      { id: "f-chain-batch-size", path: ["market", "chain", "batch_size"], type: "int" },
      { id: "f-vix-enabled", path: ["market", "india_vix", "enabled"], type: "bool" },
      { id: "f-vix-segment", path: ["market", "india_vix", "exchange_segment"], type: "string" },
      { id: "f-vix-symbol", path: ["market", "india_vix", "symbol"], type: "string" },
      { id: "f-vix-scrip", path: ["market", "india_vix", "scrip_name"], type: "string" },
      // Logging
      { id: "f-log-level", path: ["logging", "level"], type: "string" },
      { id: "f-log-format", path: ["logging", "format"], type: "string" },
      { id: "f-log-file", path: ["logging", "file"], type: "string" },
      { id: "f-max-size-mb", path: ["logging", "max_size_mb"], type: "int" },
      { id: "f-max-backups", path: ["logging", "max_backups"], type: "int" },
      { id: "f-retention-days", path: ["logging", "retention_days"], type: "int" },
      { id: "f-tick-sample", path: ["logging", "tick_sample_seconds"], type: "int" }
    ];
    map.forEach((item) => {
      const el2 = document.getElementById(item.id);
      if (!el2) return;
      const eventName = el2.type === "checkbox" ? "change" : "input";
      el2.addEventListener(eventName, () => {
        let val;
        if (item.type === "bool") {
          val = el2.checked;
        } else if (item.type === "int") {
          val = parseInt(el2.value, 10);
          if (isNaN(val)) val = 0;
        } else {
          val = el2.value.trim();
        }
        clearFieldError(item.path.join("."));
        currentYAML = setYamlScalar(currentYAML, item.path, val, item.type);
        yamlEditor.value = currentYAML;
        markDirty(true);
      });
    });
  }
  function renderIndicesTable(indices) {
    const tbody = document.getElementById("indices-table-body");
    if (!tbody) return;
    tbody.innerHTML = "";
    if (!indices || indices.length === 0) {
      tbody.innerHTML = `<tr><td colspan="8" style="text-align:center; color:var(--text-dim); padding:16px;">No indices configured. Click "+ Add Index" to add one.</td></tr>`;
      return;
    }
    indices.forEach((idx) => {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td><strong>${escapeHtml(idx.key)}</strong></td>
        <td>${escapeHtml(idx.display_name || idx.key)}</td>
        <td><code>${escapeHtml(idx.exchange_segment || "")}</code></td>
        <td><code>${escapeHtml(idx.index_segment || "")}</code></td>
        <td>${escapeHtml(idx.index_symbol || "")}</td>
        <td><code>${escapeHtml(idx.expiry_mode || "both")}</code></td>
        <td><code>${escapeHtml(idx.default_expiry || "nearest_weekly")}</code></td>
        <td style="text-align:center;">
          <div class="action-btns">
            <button type="button" class="btn-icon edit-idx-btn" title="Edit Index" data-key="${escapeHtml(idx.key)}">\u270F\uFE0F</button>
            <button type="button" class="btn-icon delete delete-idx-btn" title="Delete Index" data-key="${escapeHtml(idx.key)}">\u{1F5D1}\uFE0F</button>
          </div>
        </td>
      `;
      tbody.appendChild(tr);
    });
    tbody.querySelectorAll(".edit-idx-btn").forEach((btn) => {
      btn.addEventListener("click", () => openEditIndexModal(btn.dataset.key));
    });
    tbody.querySelectorAll(".delete-idx-btn").forEach((btn) => {
      btn.addEventListener("click", () => deleteIndex(btn.dataset.key));
    });
  }
  function renderCommoditiesTable(commodities) {
    const tbody = document.getElementById("commodities-table-body");
    if (!tbody) return;
    tbody.innerHTML = "";
    if (!commodities || commodities.length === 0) {
      tbody.innerHTML = `<tr><td colspan="5" style="text-align:center; color:var(--text-dim); padding:16px;">No commodities configured. Click "+ Add Commodity" to add one.</td></tr>`;
      return;
    }
    commodities.forEach((cmd) => {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td><strong>${escapeHtml(cmd.key)}</strong></td>
        <td>${escapeHtml(cmd.display_name || cmd.key)}</td>
        <td><code>${escapeHtml(cmd.exchange_segment || "mcx_fo")}</code></td>
        <td>${escapeHtml(cmd.scrip_name || cmd.key)}</td>
        <td style="text-align:center;">
          <div class="action-btns">
            <button type="button" class="btn-icon edit-cmd-btn" title="Edit Commodity" data-key="${escapeHtml(cmd.key)}">\u270F\uFE0F</button>
            <button type="button" class="btn-icon delete delete-cmd-btn" title="Delete Commodity" data-key="${escapeHtml(cmd.key)}">\u{1F5D1}\uFE0F</button>
          </div>
        </td>
      `;
      tbody.appendChild(tr);
    });
    tbody.querySelectorAll(".edit-cmd-btn").forEach((btn) => {
      btn.addEventListener("click", () => openEditCommodityModal(btn.dataset.key));
    });
    tbody.querySelectorAll(".delete-cmd-btn").forEach((btn) => {
      btn.addEventListener("click", () => deleteCommodity(btn.dataset.key));
    });
  }
  function setupModals() {
    addIndexBtn.addEventListener("click", () => openAddIndexModal());
    indexModalClose.addEventListener("click", () => closeIndexModal());
    indexModalCancel.addEventListener("click", () => closeIndexModal());
    indexModalForm.addEventListener("submit", (e) => {
      e.preventDefault();
      saveIndexModal();
    });
    addCommodityBtn.addEventListener("click", () => openAddCommodityModal());
    commodityModalClose.addEventListener("click", () => closeCommodityModal());
    commodityModalCancel.addEventListener("click", () => closeCommodityModal());
    commodityModalForm.addEventListener("submit", (e) => {
      e.preventDefault();
      saveCommodityModal();
    });
    indexModal.addEventListener("click", (e) => {
      if (e.target === indexModal) closeIndexModal();
    });
    commodityModal.addEventListener("click", (e) => {
      if (e.target === commodityModal) closeCommodityModal();
    });
  }
  function openAddIndexModal() {
    indexModalTitle.textContent = "Add Underlying Index";
    document.getElementById("idx-edit-orig-key").value = "";
    document.getElementById("idx-key").value = "";
    document.getElementById("idx-display-name").value = "";
    document.getElementById("idx-exchange-segment").value = "nse_fo";
    document.getElementById("idx-index-segment").value = "nse_cm";
    document.getElementById("idx-symbol").value = "";
    document.getElementById("idx-scrip-name").value = "";
    document.getElementById("idx-expiry-mode").value = "both";
    document.getElementById("idx-default-expiry").value = "nearest_weekly";
    indexModal.hidden = false;
    document.getElementById("idx-key").focus();
  }
  function openEditIndexModal(key) {
    if (!currentConfig || !currentConfig.market || !currentConfig.market.indices) return;
    const idx = currentConfig.market.indices.find((i) => i.key === key);
    if (!idx) return;
    indexModalTitle.textContent = "Edit Underlying Index";
    document.getElementById("idx-edit-orig-key").value = idx.key;
    document.getElementById("idx-key").value = idx.key;
    document.getElementById("idx-display-name").value = idx.display_name || idx.key;
    document.getElementById("idx-exchange-segment").value = idx.exchange_segment || "nse_fo";
    document.getElementById("idx-index-segment").value = idx.index_segment || "nse_cm";
    document.getElementById("idx-symbol").value = idx.index_symbol || "";
    document.getElementById("idx-scrip-name").value = idx.scrip_name || "";
    document.getElementById("idx-expiry-mode").value = idx.expiry_mode || "both";
    document.getElementById("idx-default-expiry").value = idx.default_expiry || "nearest_weekly";
    indexModal.hidden = false;
    document.getElementById("idx-display-name").focus();
  }
  function closeIndexModal() {
    indexModal.hidden = true;
  }
  function saveIndexModal() {
    const origKey = document.getElementById("idx-edit-orig-key").value;
    const newIdx = {
      key: document.getElementById("idx-key").value.trim(),
      display_name: document.getElementById("idx-display-name").value.trim(),
      exchange_segment: document.getElementById("idx-exchange-segment").value,
      index_segment: document.getElementById("idx-index-segment").value,
      index_symbol: document.getElementById("idx-symbol").value.trim(),
      scrip_name: document.getElementById("idx-scrip-name").value.trim(),
      expiry_mode: document.getElementById("idx-expiry-mode").value,
      default_expiry: document.getElementById("idx-default-expiry").value
    };
    if (!newIdx.key) {
      alert("Key is required");
      return;
    }
    if (!currentConfig) currentConfig = {};
    if (!currentConfig.market) currentConfig.market = {};
    if (!currentConfig.market.indices) currentConfig.market.indices = [];
    const indices = currentConfig.market.indices;
    if (origKey) {
      const idx = indices.findIndex((i) => i.key === origKey);
      if (idx !== -1) indices[idx] = newIdx;
    } else {
      if (indices.some((i) => i.key === newIdx.key)) {
        alert(`An index with key "${newIdx.key}" already exists`);
        return;
      }
      indices.push(newIdx);
    }
    renderIndicesTable(indices);
    currentYAML = setYamlIndices(currentYAML, indices);
    yamlEditor.value = currentYAML;
    markDirty(true);
    closeIndexModal();
  }
  function deleteIndex(key) {
    if (!currentConfig || !currentConfig.market || !currentConfig.market.indices) return;
    if (currentConfig.market.indices.length <= 1) {
      showToast("warn", "At least one index is required by the application.");
      return;
    }
    if (!confirm(`Are you sure you want to remove index "${key}"?`)) return;
    currentConfig.market.indices = currentConfig.market.indices.filter((i) => i.key !== key);
    renderIndicesTable(currentConfig.market.indices);
    currentYAML = setYamlIndices(currentYAML, currentConfig.market.indices);
    yamlEditor.value = currentYAML;
    markDirty(true);
  }
  function openAddCommodityModal() {
    commodityModalTitle.textContent = "Add Commodity";
    document.getElementById("cmd-edit-orig-key").value = "";
    document.getElementById("cmd-key").value = "";
    document.getElementById("cmd-display-name").value = "";
    document.getElementById("cmd-exchange-segment").value = "mcx_fo";
    document.getElementById("cmd-scrip-name").value = "";
    commodityModal.hidden = false;
    document.getElementById("cmd-key").focus();
  }
  function openEditCommodityModal(key) {
    if (!currentConfig || !currentConfig.market || !currentConfig.market.commodities) return;
    const cmd = currentConfig.market.commodities.find((c) => c.key === key);
    if (!cmd) return;
    commodityModalTitle.textContent = "Edit Commodity";
    document.getElementById("cmd-edit-orig-key").value = cmd.key;
    document.getElementById("cmd-key").value = cmd.key;
    document.getElementById("cmd-display-name").value = cmd.display_name || cmd.key;
    document.getElementById("cmd-exchange-segment").value = cmd.exchange_segment || "mcx_fo";
    document.getElementById("cmd-scrip-name").value = cmd.scrip_name || "";
    commodityModal.hidden = false;
    document.getElementById("cmd-display-name").focus();
  }
  function closeCommodityModal() {
    commodityModal.hidden = true;
  }
  function saveCommodityModal() {
    const origKey = document.getElementById("cmd-edit-orig-key").value;
    const newCmd = {
      key: document.getElementById("cmd-key").value.trim(),
      display_name: document.getElementById("cmd-display-name").value.trim(),
      exchange_segment: document.getElementById("cmd-exchange-segment").value,
      scrip_name: document.getElementById("cmd-scrip-name").value.trim()
    };
    if (!newCmd.key) {
      alert("Key is required");
      return;
    }
    if (!currentConfig) currentConfig = {};
    if (!currentConfig.market) currentConfig.market = {};
    if (!currentConfig.market.commodities) currentConfig.market.commodities = [];
    const commodities = currentConfig.market.commodities;
    if (origKey) {
      const idx = commodities.findIndex((c) => c.key === origKey);
      if (idx !== -1) commodities[idx] = newCmd;
    } else {
      if (commodities.some((c) => c.key === newCmd.key)) {
        alert(`A commodity with key "${newCmd.key}" already exists`);
        return;
      }
      commodities.push(newCmd);
    }
    renderCommoditiesTable(commodities);
    currentYAML = setYamlCommodities(currentYAML, commodities);
    yamlEditor.value = currentYAML;
    markDirty(true);
    closeCommodityModal();
  }
  function deleteCommodity(key) {
    if (!currentConfig || !currentConfig.market || !currentConfig.market.commodities) return;
    if (!confirm(`Are you sure you want to remove commodity "${key}"?`)) return;
    currentConfig.market.commodities = currentConfig.market.commodities.filter((c) => c.key !== key);
    renderCommoditiesTable(currentConfig.market.commodities);
    currentYAML = setYamlCommodities(currentYAML, currentConfig.market.commodities);
    yamlEditor.value = currentYAML;
    markDirty(true);
  }
  function setupEditor() {
    yamlEditor.addEventListener("input", () => {
      currentYAML = yamlEditor.value;
      updateLineNumbers();
      updateCursorPos();
      markDirty(true);
      checkBasicYamlSyntax();
    });
    yamlEditor.addEventListener("scroll", () => {
      lineNumbers.scrollTop = yamlEditor.scrollTop;
    });
    yamlEditor.addEventListener("keydown", (e) => {
      if (e.key === "Tab") {
        e.preventDefault();
        const start = yamlEditor.selectionStart;
        const end = yamlEditor.selectionEnd;
        if (e.shiftKey) {
          const val = yamlEditor.value;
          const lineStart = val.lastIndexOf("\n", start - 1) + 1;
          if (val.slice(lineStart, lineStart + 2) === "  ") {
            yamlEditor.value = val.slice(0, lineStart) + val.slice(lineStart + 2);
            yamlEditor.selectionStart = Math.max(lineStart, start - 2);
            yamlEditor.selectionEnd = Math.max(lineStart, end - 2);
          }
        } else {
          yamlEditor.setRangeText("  ", start, end, "end");
        }
        currentYAML = yamlEditor.value;
        updateLineNumbers();
        markDirty(true);
      }
    });
    yamlEditor.addEventListener("click", updateCursorPos);
    yamlEditor.addEventListener("keyup", updateCursorPos);
    yamlCopyBtn.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(yamlEditor.value);
        showToast("success", "YAML configuration copied to clipboard");
      } catch (err) {
        showToast("error", "Failed to copy to clipboard");
      }
    });
    yamlRevertBtn.addEventListener("click", () => {
      if (!confirm("Revert all unsaved changes to the last saved configuration?")) return;
      currentYAML = savedYAML;
      yamlEditor.value = currentYAML;
      updateLineNumbers();
      markDirty(false);
      clearErrors();
      if (currentConfig) {
        populateForm(currentConfig);
      }
      showToast("info", "Reverted to last saved configuration");
    });
  }
  function updateLineNumbers() {
    const lines = yamlEditor.value.split("\n").length;
    let numbers = "";
    for (let i = 1; i <= lines; i++) {
      numbers += i + "\n";
    }
    lineNumbers.textContent = numbers;
  }
  function updateCursorPos() {
    const pos = yamlEditor.selectionStart;
    const text = yamlEditor.value.slice(0, pos);
    const lines = text.split("\n");
    const line = lines.length;
    const col = lines[lines.length - 1].length + 1;
    yamlCursorPos.textContent = `Ln ${line}, Col ${col}`;
  }
  function checkBasicYamlSyntax() {
    const text = yamlEditor.value;
    const lines = text.split("\n");
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (line.includes("	")) {
        setYamlStatus(false, `Tabs not allowed in YAML (line ${i + 1})`);
        return;
      }
    }
    setYamlStatus(true, "Valid YAML syntax");
  }
  function setYamlStatus(isValid, msg) {
    yamlStatusBadge.className = `badge-status ${isValid ? "ok" : "error"}`;
    yamlStatusBadge.textContent = msg;
  }
  function setupActions() {
    validateBtn.addEventListener("click", () => validateConfig(false));
    saveBtn.addEventListener("click", () => saveConfig());
    alertCloseBtn.addEventListener("click", () => hideAlert());
    window.addEventListener("beforeunload", (e) => {
      if (isDirty) {
        e.preventDefault();
        e.returnValue = "You have unsaved configuration changes.";
      }
    });
  }
  async function validateConfig(silentSuccess = false) {
    clearErrors();
    const yamlToSend = activeTabId === "tab-yaml" ? yamlEditor.value : currentYAML;
    try {
      const res = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ yaml: yamlToSend, validate_only: true })
      });
      const data = await res.json();
      if (!res.ok || !data.ok) {
        handleValidationError(data);
        return false;
      }
      setYamlStatus(true, "Valid YAML");
      if (data.config) {
        currentConfig = data.config;
      }
      if (!silentSuccess) {
        showToast("success", "Configuration syntax and rules are completely valid!");
      }
      return true;
    } catch (err) {
      showToast("error", `Validation network error: ${err.message}`);
      return false;
    }
  }
  async function validateAndSyncFromYAML() {
    clearErrors();
    try {
      const res = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ yaml: currentYAML, validate_only: true })
      });
      const data = await res.json();
      if (!res.ok || !data.ok) {
        handleValidationError(data);
        showToast("error", "Cannot switch tabs: Please fix YAML syntax errors first");
        return false;
      }
      if (data.config) {
        currentConfig = data.config;
        populateForm(currentConfig);
      }
      return true;
    } catch (err) {
      showToast("error", `Failed to sync YAML: ${err.message}`);
      return false;
    }
  }
  async function saveConfig() {
    clearErrors();
    const yamlToSend = activeTabId === "tab-yaml" ? yamlEditor.value : currentYAML;
    saveBtn.disabled = true;
    saveSpinner.hidden = false;
    saveLabel.textContent = "Saving...";
    try {
      const res = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ yaml: yamlToSend, validate_only: false })
      });
      const data = await res.json();
      if (!res.ok || !data.ok) {
        handleValidationError(data);
        return;
      }
      savedYAML = data.yaml || yamlToSend;
      currentYAML = savedYAML;
      yamlEditor.value = currentYAML;
      markDirty(false);
      if (data.config) {
        currentConfig = data.config;
        populateForm(currentConfig);
      }
      const backupMsg = data.backup ? ` (Backup created at ${data.backup})` : "";
      showToast("success", `Configuration saved successfully!${backupMsg}`);
      if (Array.isArray(data.restart_required) && data.restart_required.length) {
        showToast("warn", `Restart required to apply: ${data.restart_required.join(", ")}`, 8e3);
      }
    } catch (err) {
      showToast("error", `Failed to save configuration: ${err.message}`);
    } finally {
      saveBtn.disabled = false;
      saveSpinner.hidden = true;
      saveLabel.textContent = "Save Configuration";
    }
  }
  function handleValidationError(data) {
    const errorMsg = data.message || data.error || "Validation failed";
    const field = data.field || "";
    showAlert(field ? `Error in ${field}` : "Validation Error", errorMsg);
    setYamlStatus(false, errorMsg);
    if (field) {
      highlightField(field, errorMsg);
    }
    showToast("error", `Validation Error: ${errorMsg}`);
  }
  function highlightField(fieldPath, msg) {
    const group = document.querySelector(`[data-field="${fieldPath}"]`);
    if (group) {
      group.classList.add("has-error");
      const errEl = group.querySelector(".field-error");
      if (errEl) errEl.textContent = msg;
      const panel = group.closest(".tab-panel");
      if (panel && panel.id !== activeTabId) {
        const tabBtn = document.querySelector(`[aria-controls="${panel.id}"]`);
        if (tabBtn) {
          switchTab(tabBtn.id, panel.id);
        }
      }
      group.scrollIntoView({ behavior: "smooth", block: "center" });
    }
  }
  function clearFieldError(fieldPath) {
    const group = document.querySelector(`[data-field="${fieldPath}"]`);
    if (group) {
      group.classList.remove("has-error");
      const errEl = group.querySelector(".field-error");
      if (errEl) errEl.textContent = "";
    }
  }
  function clearErrors() {
    hideAlert();
    document.querySelectorAll(".form-group.has-error").forEach((g) => {
      g.classList.remove("has-error");
      const errEl = g.querySelector(".field-error");
      if (errEl) errEl.textContent = "";
    });
  }
  function showAlert(title, msg) {
    alertTitle.textContent = title ? `${title}:` : "";
    alertMessage.textContent = msg;
    alertBanner.hidden = false;
    alertBanner.style.display = "flex";
  }
  function hideAlert() {
    alertBanner.hidden = true;
    alertBanner.style.display = "none";
    alertTitle.textContent = "";
    alertMessage.textContent = "";
  }
  function showToast(type, msg, duration = 4500) {
    const toast = document.createElement("div");
    toast.className = `toast ${type}`;
    const icon = type === "success" ? "\u2713" : type === "warn" ? "\u26A0\uFE0F" : "\u2717";
    toast.innerHTML = `
      <span>${icon}</span>
      <div class="toast-msg">${escapeHtml(msg)}</div>
      <button type="button" class="toast-close">&times;</button>
    `;
    toast.querySelector(".toast-close").addEventListener("click", () => toast.remove());
    toastContainer.appendChild(toast);
    setTimeout(() => {
      toast.style.transition = "opacity 0.4s ease";
      toast.style.opacity = "0";
      setTimeout(() => toast.remove(), 400);
    }, 4500);
  }
  function markDirty(dirty) {
    isDirty = dirty;
    unsavedBadge.hidden = !dirty;
  }
  function setYamlScalar(yaml, path, newValue, type) {
    const lines = yaml.split("\n");
    let lineIdx = findFieldLine(lines, path);
    let formattedVal;
    if (type === "string") {
      formattedVal = `"${newValue}"`;
    } else if (type === "bool") {
      formattedVal = newValue ? "true" : "false";
    } else {
      formattedVal = String(newValue);
    }
    if (lineIdx !== -1) {
      const line = lines[lineIdx];
      const colonIdx = line.indexOf(":");
      const indentAndKey = line.slice(0, colonIdx + 1);
      const afterColon = line.slice(colonIdx + 1);
      const commentIdx = afterColon.indexOf("#");
      let comment = "";
      if (commentIdx !== -1) {
        comment = " " + afterColon.slice(commentIdx).trim();
      }
      lines[lineIdx] = `${indentAndKey} ${formattedVal}${comment}`;
      return lines.join("\n");
    }
    return insertYamlField(yaml, path, formattedVal);
  }
  function findFieldLine(lines, path) {
    let currentDepth = 0;
    let sectionIndent = -1;
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith("#")) continue;
      const indent = line.search(/\S/);
      const colonIdx = line.indexOf(":");
      if (colonIdx === -1) continue;
      const key = line.slice(indent, colonIdx).trim();
      if (key === path[currentDepth]) {
        if (currentDepth === path.length - 1) {
          return i;
        }
        currentDepth++;
        sectionIndent = indent;
      } else if (indent <= sectionIndent && currentDepth > 0) {
        currentDepth = 0;
        sectionIndent = -1;
      }
    }
    return -1;
  }
  function insertYamlField(yaml, path, formattedVal) {
    const lines = yaml.split("\n");
    const parentKey = path[0];
    const key = path[path.length - 1];
    let parentIdx = -1;
    for (let i = 0; i < lines.length; i++) {
      if (lines[i].startsWith(parentKey + ":")) {
        parentIdx = i;
        break;
      }
    }
    const indent = "  ".repeat(path.length - 1);
    const newLine = `${indent}${key}: ${formattedVal}`;
    if (parentIdx !== -1) {
      lines.splice(parentIdx + 1, 0, newLine);
      return lines.join("\n");
    }
    lines.push(`${parentKey}:`);
    lines.push(newLine);
    return lines.join("\n");
  }
  function setYamlIndices(yaml, indices) {
    const lines = yaml.split("\n");
    let mktIdx = -1;
    let idxStart = -1;
    let idxEnd = -1;
    for (let i = 0; i < lines.length; i++) {
      if (lines[i].startsWith("market:")) {
        mktIdx = i;
        break;
      }
    }
    if (mktIdx !== -1) {
      for (let i = mktIdx + 1; i < lines.length; i++) {
        const line = lines[i];
        if (line.startsWith("  indices:")) {
          idxStart = i;
          for (let j = i + 1; j < lines.length; j++) {
            const l = lines[j];
            const trimmed = l.trim();
            if (trimmed && !trimmed.startsWith("#")) {
              const indent = l.search(/\S/);
              if (indent <= 2) {
                idxEnd = j;
                break;
              }
            }
          }
          if (idxEnd === -1) idxEnd = lines.length;
          break;
        } else if (line.length > 0 && !line.startsWith(" ") && !line.startsWith("#")) {
          break;
        }
      }
    }
    const newLines = ["  indices:"];
    indices.forEach((idx) => {
      newLines.push(`    - key: "${idx.key}"`);
      newLines.push(`      display_name: "${idx.display_name || idx.key}"`);
      newLines.push(`      exchange_segment: "${idx.exchange_segment || "nse_fo"}"`);
      newLines.push(`      index_segment: "${idx.index_segment || "nse_cm"}"`);
      newLines.push(`      index_symbol: "${idx.index_symbol || idx.key}"`);
      if (idx.scrip_name) {
        newLines.push(`      scrip_name: "${idx.scrip_name}"`);
      }
      newLines.push(`      expiry_mode: "${idx.expiry_mode || "both"}"`);
      newLines.push(`      default_expiry: "${idx.default_expiry || "nearest_weekly"}"`);
    });
    if (idxStart !== -1 && idxEnd !== -1) {
      lines.splice(idxStart, idxEnd - idxStart, ...newLines);
      return lines.join("\n");
    } else if (mktIdx !== -1) {
      lines.splice(mktIdx + 1, 0, ...newLines);
      return lines.join("\n");
    }
    return yaml + "\nmarket:\n" + newLines.join("\n") + "\n";
  }
  function setYamlCommodities(yaml, commodities) {
    const lines = yaml.split("\n");
    let mktIdx = -1;
    let cmdStart = -1;
    let cmdEnd = -1;
    for (let i = 0; i < lines.length; i++) {
      if (lines[i].startsWith("market:")) {
        mktIdx = i;
        break;
      }
    }
    if (mktIdx !== -1) {
      for (let i = mktIdx + 1; i < lines.length; i++) {
        const line = lines[i];
        if (line.startsWith("  commodities:")) {
          cmdStart = i;
          for (let j = i + 1; j < lines.length; j++) {
            const l = lines[j];
            const trimmed = l.trim();
            if (trimmed && !trimmed.startsWith("#")) {
              const indent = l.search(/\S/);
              if (indent <= 2) {
                cmdEnd = j;
                break;
              }
            }
          }
          if (cmdEnd === -1) cmdEnd = lines.length;
          break;
        } else if (line.length > 0 && !line.startsWith(" ") && !line.startsWith("#")) {
          break;
        }
      }
    }
    const newLines = ["  commodities:"];
    commodities.forEach((cmd) => {
      newLines.push(`    - key: "${cmd.key}"`);
      newLines.push(`      display_name: "${cmd.display_name || cmd.key}"`);
      newLines.push(`      exchange_segment: "${cmd.exchange_segment || "mcx_fo"}"`);
      newLines.push(`      scrip_name: "${cmd.scrip_name || cmd.key}"`);
    });
    if (cmdStart !== -1 && cmdEnd !== -1) {
      lines.splice(cmdStart, cmdEnd - cmdStart, ...newLines);
      return lines.join("\n");
    } else if (mktIdx !== -1) {
      lines.splice(mktIdx + 1, 0, ...newLines);
      return lines.join("\n");
    }
    return yaml + "\nmarket:\n" + newLines.join("\n") + "\n";
  }
  function escapeHtml(str) {
    if (!str) return "";
    return String(str).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
//# sourceMappingURL=settings.js.map
