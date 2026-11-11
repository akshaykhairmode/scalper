/**
 * Scalper Settings Management UI
 */
(function () {
  'use strict';

  // State
  let currentConfig = null;
  let currentYAML = '';
  let savedYAML = '';
  let isDirty = false;
  let activeTabId = 'tab-trading';

  // DOM Elements
  const el = (id: string): any => document.getElementById(id);
  const validateBtn = el('validate-btn');
  const saveBtn = el('save-btn');
  const saveSpinner = el('save-spinner');
  const saveLabel = el('save-label');
  const unsavedBadge = el('unsaved-badge');
  const alertBanner = el('alert-banner');
  const alertTitle = el('alert-title');
  const alertMessage = el('alert-message');
  const alertCloseBtn = el('alert-close-btn');
  const toastContainer = el('toast-container');
  const yamlEditor = el('yaml-editor');
  const lineNumbers = el('editor-line-numbers');
  const yamlStatusBadge = el('yaml-status-badge');
  const yamlCursorPos = el('yaml-cursor-pos');
  const yamlCopyBtn = el('yaml-copy-btn');
  const yamlRevertBtn = el('yaml-revert-btn');
  const tabBtns = document.querySelectorAll('.tab-btn');
  const tabPanels = document.querySelectorAll('.tab-panel');

  // Modal Elements
  const indexModal = el('index-modal');
  const indexModalTitle = el('index-modal-title');
  const indexModalForm = el('index-modal-form');
  const indexModalClose = el('index-modal-close');
  const indexModalCancel = el('index-modal-cancel');
  const addIndexBtn = el('add-index-btn');

  const commodityModal = el('commodity-modal');
  const commodityModalTitle = el('commodity-modal-title');
  const commodityModalForm = el('commodity-modal-form');
  const commodityModalClose = el('commodity-modal-close');
  const commodityModalCancel = document.getElementById('commodity-modal-cancel');
  const addCommodityBtn = document.getElementById('add-commodity-btn');

  // Initialize
  function init() {
    setupTabs();
    setupEditor();
    setupFormInputs();
    setupModals();
    setupActions();
    setupDevices();
    loadStorageInfo();
    document.getElementById('db-size-refresh')?.addEventListener('click', loadStorageInfo);

    // Check embedded initial data first for instantaneous render
    const initialConfigEl = document.getElementById('initial-config-data');
    const initialYamlEl = document.getElementById('initial-yaml-data');

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
        console.warn('Failed to parse initial config JSON:', err);
      }
    }

    // Also fetch fresh from API
    fetchConfig();
  }

  // Fetch live config from server
  async function fetchConfig() {
    try {
      const res = await fetch('/api/config');
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
      showToast('error', `Failed to load settings: ${err.message}`);
    }
  }

  // Tab Navigation
  function setupTabs() {
    tabBtns.forEach(btn => {
      btn.addEventListener('click', async () => {
        const targetPanelId = btn.getAttribute('aria-controls');
        const prevTab = activeTabId;

        // If leaving YAML tab, validate syntax and sync to form
        if (prevTab === 'tab-yaml' && btn.id !== 'tab-yaml') {
          currentYAML = yamlEditor.value;
          const valid = await validateAndSyncFromYAML();
          if (!valid) {
            // Stay on YAML tab to let user fix syntax
            return;
          }
        }

        switchTab(btn.id, targetPanelId);
      });
    });
  }

  function switchTab(btnId, panelId) {
    activeTabId = btnId;
    tabBtns.forEach(b => {
      const isActive = b.id === btnId;
      b.classList.toggle('active', isActive);
      b.setAttribute('aria-selected', isActive ? 'true' : 'false');
    });

    tabPanels.forEach(p => {
      const isTarget = p.id === panelId;
      p.classList.toggle('active', isTarget);
    });

    if (panelId === 'panel-yaml') {
      yamlEditor.value = currentYAML;
      updateLineNumbers();
      yamlEditor.focus();
    }

    if (panelId === 'panel-devices') {
      loadSettingsDevices();
    }
  }

  function setupDevices() {
    document.getElementById('settings-register-device-btn')?.addEventListener('click', registerSettingsDevice);
  }

  function bufferToBase64Url(buffer) {
    const bytes = new Uint8Array(buffer);
    let str = '';
    for (let i = 0; i < bytes.byteLength; i++) {
      str += String.fromCharCode(bytes[i]);
    }
    return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function base64UrlToBuffer(base64Url) {
    if (!base64Url) return new ArrayBuffer(0);
    const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
    const pad = base64.length % 4;
    const padded = pad ? base64 + '='.repeat(4 - pad) : base64;
    const binary = atob(padded);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes.buffer;
  }

  async function loadSettingsDevices() {
    const tbody = document.getElementById('settings-devices-tbody');
    if (!tbody) return;
    try {
      const res = await fetch('/api/devices');
      const data = await res.json().catch(() => ({}));
      const devices = data.devices || [];
      if (devices.length === 0) {
        tbody.innerHTML = '<tr><td colspan="4" style="color:var(--text-muted, #888); text-align:center; padding:16px;">No biometric devices enrolled yet.</td></tr>';
        return;
      }
      tbody.innerHTML = devices.map(d => {
        const lastUsed = d.last_used_at ? new Date(d.last_used_at).toLocaleDateString('en-IN', {
          month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'
        }) : '—';
        return `
          <tr>
            <td><strong>📱 ${d.device_name || 'Passkey Device'}</strong></td>
            <td>${lastUsed}</td>
            <td>${d.sign_count || 0}</td>
            <td style="text-align:right;">
              <button type="button" class="btn danger small btn-revoke-setting-dev" data-id="${d.id}">Revoke</button>
            </td>
          </tr>
        `;
      }).join('');
      tbody.querySelectorAll('.btn-revoke-setting-dev').forEach(b => {
        b.addEventListener('click', async () => {
          b.disabled = true;
          b.textContent = 'Revoking...';
          await fetch(`/api/devices/${encodeURIComponent(b.dataset.id)}`, { method: 'DELETE' });
          showToast('Device revoked', 'info');
          loadSettingsDevices();
        });
      });
    } catch (err) {
      tbody.innerHTML = '<tr><td colspan="4" style="color:#ef4444; text-align:center; padding:16px;">Error loading devices</td></tr>';
    }
  }

  async function registerSettingsDevice() {
    if (!window.PublicKeyCredential) {
      showToast('WebAuthn not supported by this browser', 'error');
      return;
    }
    const btn = document.getElementById('settings-register-device-btn');
    if (btn) { btn.disabled = true; btn.textContent = 'Enrolling...'; }
    try {
      const beginRes = await fetch('/api/auth/webauthn/register/begin', { method: 'POST' });
      const data = await beginRes.json();
      if (!data.ok) throw new Error(data.error || 'Failed to begin registration');
      const opts = data.options || data;
      const publicKey = {
        challenge: base64UrlToBuffer(opts.challenge),
        rp: opts.rp,
        user: {
          id: base64UrlToBuffer(opts.user.id),
          name: opts.user.name,
          displayName: opts.user.displayName,
        },
        pubKeyCredParams: opts.pubKeyCredParams,
        authenticatorSelection: opts.authenticatorSelection || { userVerification: 'preferred' },
        timeout: opts.timeout || 60000,
        attestation: opts.attestation || 'none',
      };
      const cred: any = await navigator.credentials.create({ publicKey });
      if (!cred) throw new Error('Cancelled');
      const finRes = await fetch('/api/auth/webauthn/register/finish', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          deviceName: (window.navigator as any)?.userAgentData?.platform || 'Desktop Browser',
          id: cred.id,
          rawId: bufferToBase64Url(cred.rawId),
          type: cred.type,
          response: {
            clientDataJSON: bufferToBase64Url(cred.response.clientDataJSON),
            attestationObject: bufferToBase64Url(cred.response.attestationObject),
          },
        }),
      });
      const finData = await finRes.json();
      if (!finData.ok) throw new Error(finData.error || 'Verification failed');
      if (finData.device?.id) localStorage.setItem('scalper_device_id', finData.device.id);
      if (finData.device?.device_name) localStorage.setItem('scalper_device_name', finData.device.device_name);
      if (finData.token) localStorage.setItem('scalper_serve_session', finData.token);
      showToast('Device registered successfully!', 'success');
      loadSettingsDevices();
    } catch (err) {
      showToast(err.message || 'Registration failed', 'error');
    } finally {
      if (btn) { btn.disabled = false; btn.textContent = '⚡ Register This Device'; }
    }
  }

  // Populate form controls from config JSON
  function populateForm(cfg) {
    if (!cfg) return;

    // Trading
    const t = cfg.trading || {};
    const q = t.quantity || {};
    const r = t.risk || {};
    const s = t.session || {};
    const m = t.margin || {};

    setVal('f-default-product', t.default_product || 'MIS');
    setVal('f-default-order-type', t.default_order_type || 'MARKET');
    setVal('f-default-lots', q.default_lots || 1);
    setVal('f-default-unit', q.default_unit || 'lots');
    setChecked('f-allow-shares', !!q.allow_shares);
    setChecked('f-confirm-live-orders', !!t.confirm_live_orders);

    setVal('f-max-daily-loss', r.max_daily_loss_inr !== undefined ? r.max_daily_loss_inr : 5000);
    setVal('f-max-orders-min', r.max_orders_per_minute !== undefined ? r.max_orders_per_minute : 6);
    setVal('f-max-exit-sec', r.max_exit_orders_per_second !== undefined ? r.max_exit_orders_per_second : 4);
    setVal('f-stale-timeout', r.block_on_data_stale_ms !== undefined ? r.block_on_data_stale_ms : 5000);
    setVal('f-max-lots-order', r.max_lots_per_order !== undefined ? r.max_lots_per_order : 0);
    setVal('f-max-open-lots', r.max_open_lots_per_index !== undefined ? r.max_open_lots_per_index : 0);
    setVal('f-max-order-val', r.max_order_value_inr !== undefined ? r.max_order_value_inr : 0);

    setVal('f-session-open', s.open || '09:15');
    setVal('f-session-close', s.close || '15:30');
    setChecked('f-allow-outside-hours', !!s.allow_outside_hours);
    setChecked('f-margin-check', !!m.check_before_order);
    setChecked('f-margin-fail-closed', !!m.fail_closed);

    // Market
    const mkt = cfg.market || {};
    const d = mkt.depth || {};
    const c = mkt.chain || {};
    const vix = mkt.india_vix || {};

    setVal('f-data-mode', mkt.data_mode || 'websocket');
    setVal('f-depth-mode', d.mode || 'poll');
    setChecked('f-depth-enabled', !!d.enabled);
    setVal('f-visible-poll', c.visible_poll_ms || 1000);
    setVal('f-full-poll', c.full_poll_ms || 5000);
    setVal('f-chain-batch-size', c.batch_size || 40);
    setVal('f-chain-premium-cutoff', c.premium_cutoff !== undefined ? c.premium_cutoff : 30);

    const bg = mkt.background_collection || {};
    setChecked('f-bg-enabled', !!bg.enabled);
    setVal('f-bg-interval', bg.interval_seconds !== undefined ? bg.interval_seconds : 1);
    setVal('f-bg-strike-window', bg.strike_window !== undefined ? bg.strike_window : 1);
    setVal('f-bg-underlyings', (bg.underlyings || []).join(', '));
    setChecked('f-bg-include-positions', bg.include_open_positions !== undefined ? !!bg.include_open_positions : true);
    setChecked('f-bg-persist', bg.persist_last_selection !== undefined ? !!bg.persist_last_selection : true);

    setChecked('f-vix-enabled', !!vix.enabled);
    setVal('f-vix-segment', vix.exchange_segment || 'nse_cm');
    setVal('f-vix-symbol', vix.symbol || 'INDIA VIX');
    setVal('f-vix-scrip', vix.scrip_name || 'INDIA VIX');

    renderIndicesTable(mkt.indices || []);
    renderCommoditiesTable(mkt.commodities || []);

    // Logging
    const lg = cfg.logging || {};
    setVal('f-log-level', lg.level || 'info');
    setVal('f-log-format', lg.format || 'text');
    setVal('f-log-file', lg.file || './logs/scalper.log');
    setVal('f-max-size-mb', lg.max_size_mb || 100);
    setVal('f-max-backups', lg.max_backups || 10);
    setVal('f-retention-days', lg.retention_days || 14);
    setVal('f-tick-sample', lg.tick_sample_seconds !== undefined ? lg.tick_sample_seconds : 1);
    setChecked('f-log-redact', true);

    // AI
    const ai = cfg.ai || {};
    const jev = ai.jev || {};
    const ag = ai.agent || {};

    setChecked('f-ai-enabled', !!ai.enabled);
    setChecked('f-ai-shadow-mode', ai.shadow_mode !== undefined ? !!ai.shadow_mode : true);
    setChecked('f-ai-periodic-scan', !!ai.periodic_scan);
    setVal('f-ai-scan-interval', ai.scan_interval_minutes !== undefined ? ai.scan_interval_minutes : 15);
    setVal('f-ai-target-pct', ai.target_pct !== undefined ? ai.target_pct : 8.0);
    setVal('f-ai-stop-loss-pct', ai.stop_loss_pct !== undefined ? ai.stop_loss_pct : 5.0);
    setVal('f-ai-min-confidence', ai.min_confidence !== undefined ? ai.min_confidence : 0.60);
    setVal('f-ai-min-score', ai.min_score !== undefined ? ai.min_score : 3);
    setVal('f-ai-eval-cadence', ai.eval_cadence_seconds !== undefined ? ai.eval_cadence_seconds : 15);
    setVal('f-ai-max-hold', ai.max_hold_minutes !== undefined ? ai.max_hold_minutes : 120);
    setVal('f-ai-cooldown', ai.cooldown_minutes !== undefined ? ai.cooldown_minutes : 15);
    setVal('f-ai-cost-pct', ai.round_trip_cost_pct !== undefined ? ai.round_trip_cost_pct : 0.10);

    setVal('f-jev-base-url', jev.base_url || 'https://openrouter.ai/api/v1');
    setVal('f-jev-model', jev.model || '~typesafe/jev-latest');
    setVal('f-jev-api-key', jev.api_key || '');
    setVal('f-jev-timeout-ms', jev.timeout_ms || 8000);

    setVal('f-agent-provider', ag.provider || 'openrouter');
    setVal('f-agent-base-url', ag.base_url || 'https://openrouter.ai/api/v1');
    setVal('f-agent-model', ag.model || 'nvidia/nemotron-3.5-lightning:free');
    setVal('f-agent-api-key', ag.api_key || '');
    setVal('f-agent-timeout-ms', ag.timeout_ms || 90000);

    renderScannedUnderlyings(cfg);
  }

  function setVal(id, val) {
    const el = document.getElementById(id);
    if (el) el.value = val;
  }

  function setChecked(id, checked) {
    const el = document.getElementById(id);
    if (el) el.checked = checked;
  }

  // Setup form inputs to sync changes into currentYAML and state
  function setupFormInputs() {
    const map = [
      // Trading
      { id: 'f-default-product', path: ['trading', 'default_product'], type: 'string' },
      { id: 'f-default-order-type', path: ['trading', 'default_order_type'], type: 'string' },
      { id: 'f-default-lots', path: ['trading', 'quantity', 'default_lots'], type: 'int' },
      { id: 'f-default-unit', path: ['trading', 'quantity', 'default_unit'], type: 'string' },
      { id: 'f-allow-shares', path: ['trading', 'quantity', 'allow_shares'], type: 'bool' },
      { id: 'f-confirm-live-orders', path: ['trading', 'confirm_live_orders'], type: 'bool' },
      { id: 'f-max-daily-loss', path: ['trading', 'risk', 'max_daily_loss_inr'], type: 'int' },
      { id: 'f-max-orders-min', path: ['trading', 'risk', 'max_orders_per_minute'], type: 'int' },
      { id: 'f-max-exit-sec', path: ['trading', 'risk', 'max_exit_orders_per_second'], type: 'int' },
      { id: 'f-stale-timeout', path: ['trading', 'risk', 'block_on_data_stale_ms'], type: 'int' },
      { id: 'f-max-lots-order', path: ['trading', 'risk', 'max_lots_per_order'], type: 'int' },
      { id: 'f-max-open-lots', path: ['trading', 'risk', 'max_open_lots_per_index'], type: 'int' },
      { id: 'f-max-order-val', path: ['trading', 'risk', 'max_order_value_inr'], type: 'int' },
      { id: 'f-session-open', path: ['trading', 'session', 'open'], type: 'string' },
      { id: 'f-session-close', path: ['trading', 'session', 'close'], type: 'string' },
      { id: 'f-allow-outside-hours', path: ['trading', 'session', 'allow_outside_hours'], type: 'bool' },
      { id: 'f-margin-check', path: ['trading', 'margin', 'check_before_order'], type: 'bool' },
      { id: 'f-margin-fail-closed', path: ['trading', 'margin', 'fail_closed'], type: 'bool' },

      // Market
      { id: 'f-data-mode', path: ['market', 'data_mode'], type: 'string' },
      { id: 'f-depth-mode', path: ['market', 'depth', 'mode'], type: 'string' },
      { id: 'f-depth-enabled', path: ['market', 'depth', 'enabled'], type: 'bool' },
      { id: 'f-visible-poll', path: ['market', 'chain', 'visible_poll_ms'], type: 'int' },
      { id: 'f-full-poll', path: ['market', 'chain', 'full_poll_ms'], type: 'int' },
      { id: 'f-chain-batch-size', path: ['market', 'chain', 'batch_size'], type: 'int' },
      { id: 'f-chain-premium-cutoff', path: ['market', 'chain', 'premium_cutoff'], type: 'float' },
      { id: 'f-bg-enabled', path: ['market', 'background_collection', 'enabled'], type: 'bool' },
      { id: 'f-bg-interval', path: ['market', 'background_collection', 'interval_seconds'], type: 'int' },
      { id: 'f-bg-strike-window', path: ['market', 'background_collection', 'strike_window'], type: 'int' },
      { id: 'f-bg-include-positions', path: ['market', 'background_collection', 'include_open_positions'], type: 'bool' },
      { id: 'f-bg-persist', path: ['market', 'background_collection', 'persist_last_selection'], type: 'bool' },
      { id: 'f-vix-enabled', path: ['market', 'india_vix', 'enabled'], type: 'bool' },
      { id: 'f-vix-segment', path: ['market', 'india_vix', 'exchange_segment'], type: 'string' },
      { id: 'f-vix-symbol', path: ['market', 'india_vix', 'symbol'], type: 'string' },
      { id: 'f-vix-scrip', path: ['market', 'india_vix', 'scrip_name'], type: 'string' },

      // Logging
      { id: 'f-log-level', path: ['logging', 'level'], type: 'string' },
      { id: 'f-log-format', path: ['logging', 'format'], type: 'string' },
      { id: 'f-log-file', path: ['logging', 'file'], type: 'string' },
      { id: 'f-max-size-mb', path: ['logging', 'max_size_mb'], type: 'int' },
      { id: 'f-max-backups', path: ['logging', 'max_backups'], type: 'int' },
      { id: 'f-retention-days', path: ['logging', 'retention_days'], type: 'int' },
      { id: 'f-tick-sample', path: ['logging', 'tick_sample_seconds'], type: 'int' },

      // AI & Models
      { id: 'f-ai-enabled', path: ['ai', 'enabled'], type: 'bool' },
      { id: 'f-ai-shadow-mode', path: ['ai', 'shadow_mode'], type: 'bool' },
      { id: 'f-ai-periodic-scan', path: ['ai', 'periodic_scan'], type: 'bool' },
      { id: 'f-ai-scan-interval', path: ['ai', 'scan_interval_minutes'], type: 'int' },
      { id: 'f-ai-target-pct', path: ['ai', 'target_pct'], type: 'float' },
      { id: 'f-ai-stop-loss-pct', path: ['ai', 'stop_loss_pct'], type: 'float' },
      { id: 'f-ai-min-confidence', path: ['ai', 'min_confidence'], type: 'float' },
      { id: 'f-ai-min-score', path: ['ai', 'min_score'], type: 'int' },
      { id: 'f-ai-eval-cadence', path: ['ai', 'eval_cadence_seconds'], type: 'int' },
      { id: 'f-ai-max-hold', path: ['ai', 'max_hold_minutes'], type: 'int' },
      { id: 'f-ai-cooldown', path: ['ai', 'cooldown_minutes'], type: 'int' },
      { id: 'f-ai-cost-pct', path: ['ai', 'round_trip_cost_pct'], type: 'float' },
      { id: 'f-jev-base-url', path: ['ai', 'jev', 'base_url'], type: 'string' },
      { id: 'f-jev-model', path: ['ai', 'jev', 'model'], type: 'string' },
      { id: 'f-jev-api-key', path: ['ai', 'jev', 'api_key'], type: 'string' },
      { id: 'f-jev-timeout-ms', path: ['ai', 'jev', 'timeout_ms'], type: 'int' },
      { id: 'f-agent-provider', path: ['ai', 'agent', 'provider'], type: 'string' },
      { id: 'f-agent-base-url', path: ['ai', 'agent', 'base_url'], type: 'string' },
      { id: 'f-agent-model', path: ['ai', 'agent', 'model'], type: 'string' },
      { id: 'f-agent-api-key', path: ['ai', 'agent', 'api_key'], type: 'string' },
      { id: 'f-agent-timeout-ms', path: ['ai', 'agent', 'timeout_ms'], type: 'int' }
    ];

    map.forEach(item => {
      const el: any = document.getElementById(item.id);
      if (!el) return;

      const eventName = el.type === 'checkbox' ? 'change' : (el.tagName === 'SELECT' ? 'change' : 'input');
      el.addEventListener(eventName, () => {
        let val;
        if (item.type === 'bool') {
          val = el.checked;
        } else if (item.type === 'int') {
          val = parseInt(el.value, 10);
          if (isNaN(val)) val = 0;
        } else if (item.type === 'float') {
          val = parseFloat(el.value);
          if (isNaN(val)) val = 0.0;
        } else {
          val = el.value.trim();
        }

        // Clear field error
        clearFieldError(item.path.join('.'));

        // Update YAML string
        currentYAML = setYamlScalar(currentYAML, item.path, val, item.type);
        yamlEditor.value = currentYAML;
        markDirty(true);
      });
    });

    const bgUnderlyings = document.getElementById('f-bg-underlyings') as HTMLInputElement | null;
    if (bgUnderlyings) {
      bgUnderlyings.addEventListener('input', () => {
        const values = bgUnderlyings.value.split(',').map((value) => value.trim()).filter(Boolean);
        currentYAML = setYamlBackgroundUnderlyings(currentYAML, values);
        yamlEditor.value = currentYAML;
        markDirty(true);
      });
    }
  }

  // Indices Table Rendering
  function renderIndicesTable(indices) {
    const tbody = document.getElementById('indices-table-body');
    if (!tbody) return;
    tbody.innerHTML = '';

    if (!indices || indices.length === 0) {
      tbody.innerHTML = `<tr><td colspan="8" style="text-align:center; color:var(--text-dim); padding:16px;">No indices configured. Click "+ Add Index" to add one.</td></tr>`;
      return;
    }

    indices.forEach(idx => {
      const tr = document.createElement('tr');
      tr.innerHTML = `
        <td><strong>${escapeHtml(idx.key)}</strong></td>
        <td>${escapeHtml(idx.display_name || idx.key)}</td>
        <td><code>${escapeHtml(idx.exchange_segment || '')}</code></td>
        <td><code>${escapeHtml(idx.index_segment || '')}</code></td>
        <td>${escapeHtml(idx.index_symbol || '')}</td>
        <td><code>${escapeHtml(idx.expiry_mode || 'both')}</code></td>
        <td><code>${escapeHtml(idx.default_expiry || 'nearest_weekly')}</code></td>
        <td style="text-align:center;">
          <div class="action-btns">
            <button type="button" class="btn-icon edit-idx-btn" title="Edit Index" data-key="${escapeHtml(idx.key)}">✏️</button>
            <button type="button" class="btn-icon delete delete-idx-btn" title="Delete Index" data-key="${escapeHtml(idx.key)}">🗑️</button>
          </div>
        </td>
      `;
      tbody.appendChild(tr);
    });

    // Edit/Delete handlers
    tbody.querySelectorAll('.edit-idx-btn').forEach(btn => {
      btn.addEventListener('click', () => openEditIndexModal(btn.dataset.key));
    });
    tbody.querySelectorAll('.delete-idx-btn').forEach(btn => {
      btn.addEventListener('click', () => deleteIndex(btn.dataset.key));
    });
  }

  // Commodities Table Rendering
  function renderCommoditiesTable(commodities) {
    const tbody = document.getElementById('commodities-table-body');
    if (!tbody) return;
    tbody.innerHTML = '';

    if (!commodities || commodities.length === 0) {
      tbody.innerHTML = `<tr><td colspan="5" style="text-align:center; color:var(--text-dim); padding:16px;">No commodities configured. Click "+ Add Commodity" to add one.</td></tr>`;
      return;
    }

    commodities.forEach(cmd => {
      const tr = document.createElement('tr');
      tr.innerHTML = `
        <td><strong>${escapeHtml(cmd.key)}</strong></td>
        <td>${escapeHtml(cmd.display_name || cmd.key)}</td>
        <td><code>${escapeHtml(cmd.exchange_segment || 'mcx_fo')}</code></td>
        <td>${escapeHtml(cmd.scrip_name || cmd.key)}</td>
        <td style="text-align:center;">
          <div class="action-btns">
            <button type="button" class="btn-icon edit-cmd-btn" title="Edit Commodity" data-key="${escapeHtml(cmd.key)}">✏️</button>
            <button type="button" class="btn-icon delete delete-cmd-btn" title="Delete Commodity" data-key="${escapeHtml(cmd.key)}">🗑️</button>
          </div>
        </td>
      `;
      tbody.appendChild(tr);
    });

    tbody.querySelectorAll('.edit-cmd-btn').forEach(btn => {
      btn.addEventListener('click', () => openEditCommodityModal(btn.dataset.key));
    });
    tbody.querySelectorAll('.delete-cmd-btn').forEach(btn => {
      btn.addEventListener('click', () => deleteCommodity(btn.dataset.key));
    });
  }

  // Modals Setup
  function setupModals() {
    // Index Modal
    addIndexBtn.addEventListener('click', () => openAddIndexModal());
    indexModalClose.addEventListener('click', () => closeIndexModal());
    indexModalCancel.addEventListener('click', () => closeIndexModal());
    indexModalForm.addEventListener('submit', (e) => {
      e.preventDefault();
      saveIndexModal();
    });

    // Commodity Modal
    addCommodityBtn.addEventListener('click', () => openAddCommodityModal());
    commodityModalClose.addEventListener('click', () => closeCommodityModal());
    commodityModalCancel.addEventListener('click', () => closeCommodityModal());
    commodityModalForm.addEventListener('submit', (e) => {
      e.preventDefault();
      saveCommodityModal();
    });

    // Close on backdrop click
    indexModal.addEventListener('click', (e) => {
      if (e.target === indexModal) closeIndexModal();
    });
    commodityModal.addEventListener('click', (e) => {
      if (e.target === commodityModal) closeCommodityModal();
    });
  }

  function openAddIndexModal() {
    indexModalTitle.textContent = 'Add Underlying Index';
    document.getElementById('idx-edit-orig-key').value = '';
    document.getElementById('idx-key').value = '';
    document.getElementById('idx-display-name').value = '';
    document.getElementById('idx-exchange-segment').value = 'nse_fo';
    document.getElementById('idx-index-segment').value = 'nse_cm';
    document.getElementById('idx-symbol').value = '';
    document.getElementById('idx-scrip-name').value = '';
    document.getElementById('idx-expiry-mode').value = 'both';
    document.getElementById('idx-default-expiry').value = 'nearest_weekly';
    indexModal.hidden = false;
    document.getElementById('idx-key').focus();
  }

  function openEditIndexModal(key) {
    if (!currentConfig || !currentConfig.market || !currentConfig.market.indices) return;
    const idx = currentConfig.market.indices.find(i => i.key === key);
    if (!idx) return;

    indexModalTitle.textContent = 'Edit Underlying Index';
    document.getElementById('idx-edit-orig-key').value = idx.key;
    document.getElementById('idx-key').value = idx.key;
    document.getElementById('idx-display-name').value = idx.display_name || idx.key;
    document.getElementById('idx-exchange-segment').value = idx.exchange_segment || 'nse_fo';
    document.getElementById('idx-index-segment').value = idx.index_segment || 'nse_cm';
    document.getElementById('idx-symbol').value = idx.index_symbol || '';
    document.getElementById('idx-scrip-name').value = idx.scrip_name || '';
    document.getElementById('idx-expiry-mode').value = idx.expiry_mode || 'both';
    document.getElementById('idx-default-expiry').value = idx.default_expiry || 'nearest_weekly';
    indexModal.hidden = false;
    document.getElementById('idx-display-name').focus();
  }

  function closeIndexModal() {
    indexModal.hidden = true;
  }

  function saveIndexModal() {
    const origKey = document.getElementById('idx-edit-orig-key').value;
    const newIdx = {
      key: document.getElementById('idx-key').value.trim(),
      display_name: document.getElementById('idx-display-name').value.trim(),
      exchange_segment: document.getElementById('idx-exchange-segment').value,
      index_segment: document.getElementById('idx-index-segment').value,
      index_symbol: document.getElementById('idx-symbol').value.trim(),
      scrip_name: document.getElementById('idx-scrip-name').value.trim(),
      expiry_mode: document.getElementById('idx-expiry-mode').value,
      default_expiry: document.getElementById('idx-default-expiry').value
    };

    if (!newIdx.key) {
      alert('Key is required');
      return;
    }

    if (!currentConfig) currentConfig = {};
    if (!currentConfig.market) currentConfig.market = {};
    if (!currentConfig.market.indices) currentConfig.market.indices = [];

    const indices = currentConfig.market.indices;
    if (origKey) {
      const idx = indices.findIndex(i => i.key === origKey);
      if (idx !== -1) indices[idx] = newIdx;
    } else {
      // Check duplicate key
      if (indices.some(i => i.key === newIdx.key)) {
        alert(`An index with key "${newIdx.key}" already exists`);
        return;
      }
      indices.push(newIdx);
    }

    renderIndicesTable(indices);
    renderScannedUnderlyings(currentConfig);
    currentYAML = setYamlIndices(currentYAML, indices);
    yamlEditor.value = currentYAML;
    markDirty(true);
    closeIndexModal();
  }

  function deleteIndex(key) {
    if (!currentConfig || !currentConfig.market || !currentConfig.market.indices) return;
    if (currentConfig.market.indices.length <= 1) {
      showToast('warn', 'At least one index is required by the application.');
      return;
    }
    if (!confirm(`Are you sure you want to remove index "${key}"?`)) return;

    currentConfig.market.indices = currentConfig.market.indices.filter(i => i.key !== key);
    renderIndicesTable(currentConfig.market.indices);
    renderScannedUnderlyings(currentConfig);
    currentYAML = setYamlIndices(currentYAML, currentConfig.market.indices);
    yamlEditor.value = currentYAML;
    markDirty(true);
  }

  // Commodity Modal Functions
  function openAddCommodityModal() {
    commodityModalTitle.textContent = 'Add Commodity';
    document.getElementById('cmd-edit-orig-key').value = '';
    document.getElementById('cmd-key').value = '';
    document.getElementById('cmd-display-name').value = '';
    document.getElementById('cmd-exchange-segment').value = 'mcx_fo';
    document.getElementById('cmd-scrip-name').value = '';
    commodityModal.hidden = false;
    document.getElementById('cmd-key').focus();
  }

  function openEditCommodityModal(key) {
    if (!currentConfig || !currentConfig.market || !currentConfig.market.commodities) return;
    const cmd = currentConfig.market.commodities.find(c => c.key === key);
    if (!cmd) return;

    commodityModalTitle.textContent = 'Edit Commodity';
    document.getElementById('cmd-edit-orig-key').value = cmd.key;
    document.getElementById('cmd-key').value = cmd.key;
    document.getElementById('cmd-display-name').value = cmd.display_name || cmd.key;
    document.getElementById('cmd-exchange-segment').value = cmd.exchange_segment || 'mcx_fo';
    document.getElementById('cmd-scrip-name').value = cmd.scrip_name || '';
    commodityModal.hidden = false;
    document.getElementById('cmd-display-name').focus();
  }

  function closeCommodityModal() {
    commodityModal.hidden = true;
  }

  function saveCommodityModal() {
    const origKey = document.getElementById('cmd-edit-orig-key').value;
    const newCmd = {
      key: document.getElementById('cmd-key').value.trim(),
      display_name: document.getElementById('cmd-display-name').value.trim(),
      exchange_segment: document.getElementById('cmd-exchange-segment').value,
      scrip_name: document.getElementById('cmd-scrip-name').value.trim()
    };

    if (!newCmd.key) {
      alert('Key is required');
      return;
    }

    if (!currentConfig) currentConfig = {};
    if (!currentConfig.market) currentConfig.market = {};
    if (!currentConfig.market.commodities) currentConfig.market.commodities = [];

    const commodities = currentConfig.market.commodities;
    if (origKey) {
      const idx = commodities.findIndex(c => c.key === origKey);
      if (idx !== -1) commodities[idx] = newCmd;
    } else {
      if (commodities.some(c => c.key === newCmd.key)) {
        alert(`A commodity with key "${newCmd.key}" already exists`);
        return;
      }
      commodities.push(newCmd);
    }

    renderCommoditiesTable(commodities);
    renderScannedUnderlyings(currentConfig);
    currentYAML = setYamlCommodities(currentYAML, commodities);
    yamlEditor.value = currentYAML;
    markDirty(true);
    closeCommodityModal();
  }

  function deleteCommodity(key) {
    if (!currentConfig || !currentConfig.market || !currentConfig.market.commodities) return;
    if (!confirm(`Are you sure you want to remove commodity "${key}"?`)) return;

    currentConfig.market.commodities = currentConfig.market.commodities.filter(c => c.key !== key);
    renderCommoditiesTable(currentConfig.market.commodities);
    renderScannedUnderlyings(currentConfig);
    currentYAML = setYamlCommodities(currentYAML, currentConfig.market.commodities);
    yamlEditor.value = currentYAML;
    markDirty(true);
  }

  // Scanned Underlyings Checkboxes Rendering
  function renderScannedUnderlyings(cfg: any) {
    const container = document.getElementById('ai-underlyings-list');
    if (!container) return;
    container.innerHTML = '';

    const indices = (cfg.market && cfg.market.indices) || [];
    const commodities = (cfg.market && cfg.market.commodities) || [];
    const scanned: string[] = (cfg.ai && cfg.ai.scanned_underlyings && cfg.ai.scanned_underlyings.length > 0)
      ? cfg.ai.scanned_underlyings
      : ['NIFTY', 'SENSEX'];

    interface UnderlyingOption {
      key: string;
      displayName: string;
      segment: string;
      isMCX: boolean;
    }

    const items: UnderlyingOption[] = [];
    indices.forEach((idx: any) => {
      items.push({
        key: idx.key,
        displayName: idx.display_name || idx.key,
        segment: idx.exchange_segment || 'nse_fo',
        isMCX: false,
      });
    });
    commodities.forEach((cmd: any) => {
      items.push({
        key: cmd.key,
        displayName: cmd.display_name || cmd.key,
        segment: cmd.exchange_segment || 'mcx_fo',
        isMCX: true,
      });
    });

    if (items.length === 0) {
      container.innerHTML = '<span style="color:var(--text-dim);font-size:12px;">No indices or commodities configured.</span>';
      return;
    }

    items.forEach(item => {
      const isChecked = scanned.includes(item.key);
      const hoursBadge = item.isMCX ? 'MCX · 09:00–23:55' : 'NSE/BSE · 09:15–15:30';
      const label = document.createElement('label');
      label.className = 'checkbox-label scanned-underlying-item';
      label.innerHTML = `
        <input type="checkbox" value="${escapeHtml(item.key)}" ${isChecked ? 'checked' : ''}>
        <span class="checkbox-custom"></span>
        <span class="checkbox-text">
          <strong>${escapeHtml(item.key)}</strong>
          <span class="form-help">${escapeHtml(item.displayName)} <code style="font-size:10px;margin-left:4px;opacity:0.8;">${hoursBadge}</code></span>
        </span>
      `;

      const input = label.querySelector('input')!;
      input.addEventListener('change', () => {
        const selected: string[] = [];
        container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]').forEach(cb => {
          if (cb.checked && cb.value) selected.push(cb.value);
        });
        if (currentConfig && currentConfig.ai) {
          currentConfig.ai.scanned_underlyings = selected;
        }
        currentYAML = setYamlScannedUnderlyings(currentYAML, selected);
        yamlEditor.value = currentYAML;
        markDirty(true);
      });

      container.appendChild(label);
    });
  }

  // Setup YAML Editor
  function setupEditor() {
    yamlEditor.addEventListener('input', () => {
      currentYAML = yamlEditor.value;
      updateLineNumbers();
      updateCursorPos();
      markDirty(true);
      checkBasicYamlSyntax();
    });

    yamlEditor.addEventListener('scroll', () => {
      lineNumbers.scrollTop = yamlEditor.scrollTop;
    });

    yamlEditor.addEventListener('keydown', (e) => {
      // Support Tab and Shift+Tab indentation (2 spaces)
      if (e.key === 'Tab') {
        e.preventDefault();
        const start = yamlEditor.selectionStart;
        const end = yamlEditor.selectionEnd;

        if (e.shiftKey) {
          // Dedent
          const val = yamlEditor.value;
          const lineStart = val.lastIndexOf('\n', start - 1) + 1;
          if (val.slice(lineStart, lineStart + 2) === '  ') {
            yamlEditor.value = val.slice(0, lineStart) + val.slice(lineStart + 2);
            yamlEditor.selectionStart = Math.max(lineStart, start - 2);
            yamlEditor.selectionEnd = Math.max(lineStart, end - 2);
          }
        } else {
          // Indent 2 spaces
          yamlEditor.setRangeText('  ', start, end, 'end');
        }
        currentYAML = yamlEditor.value;
        updateLineNumbers();
        markDirty(true);
      }
    });

    yamlEditor.addEventListener('click', updateCursorPos);
    yamlEditor.addEventListener('keyup', updateCursorPos);

    // Copy YAML
    yamlCopyBtn.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(yamlEditor.value);
        showToast('success', 'YAML configuration copied to clipboard');
      } catch (err) {
        showToast('error', 'Failed to copy to clipboard');
      }
    });

    // Revert YAML
    yamlRevertBtn.addEventListener('click', () => {
      if (!confirm('Revert all unsaved changes to the last saved configuration?')) return;
      currentYAML = savedYAML;
      yamlEditor.value = currentYAML;
      updateLineNumbers();
      markDirty(false);
      clearErrors();
      if (currentConfig) {
        populateForm(currentConfig);
      }
      showToast('info', 'Reverted to last saved configuration');
    });
  }

  function updateLineNumbers() {
    const lines = yamlEditor.value.split('\n').length;
    let numbers = '';
    for (let i = 1; i <= lines; i++) {
      numbers += i + '\n';
    }
    lineNumbers.textContent = numbers;
  }

  function updateCursorPos() {
    const pos = yamlEditor.selectionStart;
    const text = yamlEditor.value.slice(0, pos);
    const lines = text.split('\n');
    const line = lines.length;
    const col = lines[lines.length - 1].length + 1;
    yamlCursorPos.textContent = `Ln ${line}, Col ${col}`;
  }

  function checkBasicYamlSyntax() {
    // Quick heuristic client-side syntax check
    const text = yamlEditor.value;
    const lines = text.split('\n');
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (line.includes('\t')) {
        setYamlStatus(false, `Tabs not allowed in YAML (line ${i + 1})`);
        return;
      }
    }
    setYamlStatus(true, 'Valid YAML syntax');
  }

  function setYamlStatus(isValid, msg) {
    yamlStatusBadge.className = `badge-status ${isValid ? 'ok' : 'error'}`;
    yamlStatusBadge.textContent = msg;
  }

  // Header Actions (Validate & Save)
  function setupActions() {
    validateBtn.addEventListener('click', () => validateConfig(false));
    saveBtn.addEventListener('click', () => saveConfig());
    alertCloseBtn.addEventListener('click', () => hideAlert());

    // Warn before unload if dirty
    window.addEventListener('beforeunload', (e) => {
      if (isDirty) {
        e.preventDefault();
        e.returnValue = 'You have unsaved configuration changes.';
      }
    });
  }

  // Validate configuration against backend
  async function validateConfig(silentSuccess = false) {
    clearErrors();
    const yamlToSend = (activeTabId === 'tab-yaml') ? yamlEditor.value : currentYAML;

    try {
      const res = await fetch('/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ yaml: yamlToSend, validate_only: true })
      });

      const data = await res.json();
      if (!res.ok || !data.ok) {
        handleValidationError(data);
        return false;
      }

      setYamlStatus(true, 'Valid YAML');
      if (data.config) {
        currentConfig = data.config;
      }

      if (!silentSuccess) {
        showToast('success', 'Configuration syntax and rules are completely valid!');
      }
      return true;
    } catch (err) {
      showToast('error', `Validation network error: ${err.message}`);
      return false;
    }
  }

  // Validate and parse when switching out of YAML tab
  async function validateAndSyncFromYAML() {
    clearErrors();
    try {
      const res = await fetch('/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ yaml: currentYAML, validate_only: true })
      });

      const data = await res.json();
      if (!res.ok || !data.ok) {
        handleValidationError(data);
        showToast('error', 'Cannot switch tabs: Please fix YAML syntax errors first');
        return false;
      }

      if (data.config) {
        currentConfig = data.config;
        populateForm(currentConfig);
      }
      return true;
    } catch (err) {
      showToast('error', `Failed to sync YAML: ${err.message}`);
      return false;
    }
  }

  // Save configuration to disk
  async function saveConfig() {
    clearErrors();
    const yamlToSend = (activeTabId === 'tab-yaml') ? yamlEditor.value : currentYAML;

    saveBtn.disabled = true;
    saveSpinner.hidden = false;
    saveLabel.textContent = 'Saving...';

    // Never leave the button stuck on "Saving..." if the server wedges.
    const controller = new AbortController();
    const timeoutId = window.setTimeout(() => controller.abort(), 90000);

    try {
      const res = await fetch('/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ yaml: yamlToSend, validate_only: false }),
        signal: controller.signal,
      });

      const data = await res.json();
      if (!res.ok || !data.ok) {
        handleValidationError(data);
        return;
      }

      // Success. Prefer the server's redacted YAML so newly entered secrets are
      // replaced by the mask instead of remaining visible in the editor.
      savedYAML = data.yaml || yamlToSend;
      currentYAML = savedYAML;
      yamlEditor.value = currentYAML;
      markDirty(false);

      if (data.config) {
        currentConfig = data.config;
        populateForm(currentConfig);
      }

      const backupMsg = data.backup ? ` (Backup created at ${data.backup})` : '';
      showToast('success', `Configuration saved successfully!${backupMsg}`);
      if (Array.isArray(data.restart_required) && data.restart_required.length) {
        showToast('warn', `Restart required to apply: ${data.restart_required.join(', ')}`, 8000);
      }
    } catch (err) {
      const message = err && err.name === 'AbortError'
        ? 'Request timed out. The server may still be applying changes — reload the page to check.'
        : err.message;
      showToast('error', `Failed to save configuration: ${message}`);
    } finally {
      window.clearTimeout(timeoutId);
      saveBtn.disabled = false;
      saveSpinner.hidden = true;
      saveLabel.textContent = 'Save Configuration';
    }
  }

  // Error Handling
  function handleValidationError(data) {
    const errorMsg = data.message || data.error || 'Validation failed';
    const field = data.field || '';

    showAlert(field ? `Error in ${field}` : 'Validation Error', errorMsg);
    setYamlStatus(false, errorMsg);

    if (field) {
      highlightField(field, errorMsg);
    }
    showToast('error', `Validation Error: ${errorMsg}`);
  }

  function highlightField(fieldPath, msg) {
    const group = document.querySelector(`[data-field="${fieldPath}"]`);
    if (group) {
      group.classList.add('has-error');
      const errEl = group.querySelector('.field-error');
      if (errEl) errEl.textContent = msg;

      // Find which tab this field is in and switch to it if needed
      const panel = group.closest('.tab-panel');
      if (panel && panel.id !== activeTabId) {
        const tabBtn = document.querySelector(`[aria-controls="${panel.id}"]`);
        if (tabBtn) {
          switchTab(tabBtn.id, panel.id);
        }
      }

      group.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
  }

  function clearFieldError(fieldPath) {
    const group = document.querySelector(`[data-field="${fieldPath}"]`);
    if (group) {
      group.classList.remove('has-error');
      const errEl = group.querySelector('.field-error');
      if (errEl) errEl.textContent = '';
    }
  }

  function clearErrors() {
    hideAlert();
    document.querySelectorAll('.form-group.has-error').forEach(g => {
      g.classList.remove('has-error');
      const errEl = g.querySelector('.field-error');
      if (errEl) errEl.textContent = '';
    });
  }

  function showAlert(title: string, msg: string) {
    alertTitle.textContent = title ? `${title}:` : '';
    alertMessage.textContent = msg;
    alertBanner.hidden = false;
    alertBanner.style.display = 'flex';
  }

  function hideAlert() {
    alertBanner.hidden = true;
    alertBanner.style.display = 'none';
    alertTitle.textContent = '';
    alertMessage.textContent = '';
  }

  function showToast(type: string, msg: string, duration = 4500) {
    const toast = document.createElement('div');
    toast.className = `toast ${type}`;
    const icon = type === 'success' ? '✓' : type === 'warn' ? '⚠️' : '✗';
    toast.innerHTML = `
      <span>${icon}</span>
      <div class="toast-msg">${escapeHtml(msg)}</div>
      <button type="button" class="toast-close">&times;</button>
    `;
    toast.querySelector('.toast-close').addEventListener('click', () => toast.remove());
    toastContainer.appendChild(toast);

    setTimeout(() => {
      toast.style.transition = 'opacity 0.4s ease';
      toast.style.opacity = '0';
      setTimeout(() => toast.remove(), 400);
    }, 4500);
  }

  function markDirty(dirty) {
    isDirty = dirty;
    unsavedBadge.hidden = !dirty;
  }

  // --- YAML Manipulation Helpers ---

  /**
   * Updates a scalar value in YAML while preserving indentation and comments.
   */
  function setYamlScalar(yaml, path, newValue, type) {
    const lines = yaml.split('\n');
    let lineIdx = findFieldLine(lines, path);

    let formattedVal;
    if (type === 'string') {
      formattedVal = `"${newValue}"`;
    } else if (type === 'bool') {
      formattedVal = newValue ? 'true' : 'false';
    } else {
      formattedVal = String(newValue);
    }

    if (lineIdx !== -1) {
      // Replace existing line
      const line = lines[lineIdx];
      const colonIdx = line.indexOf(':');
      const indentAndKey = line.slice(0, colonIdx + 1);
      const afterColon = line.slice(colonIdx + 1);

      // Check for inline comment
      const commentIdx = afterColon.indexOf('#');
      let comment = '';
      if (commentIdx !== -1) {
        comment = ' ' + afterColon.slice(commentIdx).trim();
      }

      lines[lineIdx] = `${indentAndKey} ${formattedVal}${comment}`;
      return lines.join('\n');
    }

    // Key not found, insert under parent section
    return insertYamlField(yaml, path, formattedVal);
  }

  function findFieldLine(lines, path) {
    let currentDepth = 0;
    let sectionIndent = -1;

    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#')) continue;

      const indent = line.search(/\S/);
      const colonIdx = line.indexOf(':');
      if (colonIdx === -1) continue;

      const key = line.slice(indent, colonIdx).trim();

      if (key === path[currentDepth]) {
        if (currentDepth === path.length - 1) {
          return i;
        }
        currentDepth++;
        sectionIndent = indent;
      } else if (indent <= sectionIndent && currentDepth > 0) {
        // Exited parent section
        currentDepth = 0;
        sectionIndent = -1;
      }
    }
    return -1;
  }

  function insertYamlField(yaml, path, formattedVal) {
    const lines = yaml.split('\n');

    // Find deepest existing ancestor in path
    let deepestDepth = 0;
    let insertLineIdx = -1;
    let currentIndent = 0;

    for (let depth = path.length - 1; depth >= 1; depth--) {
      const subPath = path.slice(0, depth);
      const idx = findFieldLine(lines, subPath);
      if (idx !== -1) {
        deepestDepth = depth;
        insertLineIdx = idx;
        const line = lines[idx];
        currentIndent = line.search(/\S/);
        break;
      }
    }

    if (insertLineIdx !== -1) {
      let targetIdx = insertLineIdx + 1;
      while (targetIdx < lines.length) {
        const line = lines[targetIdx];
        const trimmed = line.trim();
        if (trimmed && !trimmed.startsWith('#')) {
          const indent = line.search(/\S/);
          if (indent <= currentIndent) {
            break;
          }
        }
        targetIdx++;
      }

      const newLines: string[] = [];
      let indentLevel = currentIndent + 2;
      for (let d = deepestDepth; d < path.length - 1; d++) {
        newLines.push(`${' '.repeat(indentLevel)}${path[d]}:`);
        indentLevel += 2;
      }
      newLines.push(`${' '.repeat(indentLevel)}${path[path.length - 1]}: ${formattedVal}`);

      lines.splice(targetIdx, 0, ...newLines);
      return lines.join('\n');
    }

    // Root insert
    const newLines: string[] = [];
    let indentLevel = 0;
    for (let d = 0; d < path.length - 1; d++) {
      newLines.push(`${' '.repeat(indentLevel)}${path[d]}:`);
      indentLevel += 2;
    }
    newLines.push(`${' '.repeat(indentLevel)}${path[path.length - 1]}: ${formattedVal}`);

    lines.push(...newLines);
    return lines.join('\n');
  }

  function setYamlIndices(yaml, indices) {
    const lines = yaml.split('\n');
    let mktIdx = -1;
    let idxStart = -1;
    let idxEnd = -1;

    for (let i = 0; i < lines.length; i++) {
      if (lines[i].startsWith('market:')) {
        mktIdx = i;
        break;
      }
    }

    if (mktIdx !== -1) {
      for (let i = mktIdx + 1; i < lines.length; i++) {
        const line = lines[i];
        if (line.startsWith('  indices:')) {
          idxStart = i;
          // Find end of indices section
          for (let j = i + 1; j < lines.length; j++) {
            const l = lines[j];
            const trimmed = l.trim();
            if (trimmed && !trimmed.startsWith('#')) {
              const indent = l.search(/\S/);
              if (indent <= 2) {
                idxEnd = j;
                break;
              }
            }
          }
          if (idxEnd === -1) idxEnd = lines.length;
          break;
        } else if (line.length > 0 && !line.startsWith(' ') && !line.startsWith('#')) {
          break;
        }
      }
    }

    // Format new indices
    const newLines = ['  indices:'];
    indices.forEach(idx => {
      newLines.push(`    - key: "${idx.key}"`);
      newLines.push(`      display_name: "${idx.display_name || idx.key}"`);
      newLines.push(`      exchange_segment: "${idx.exchange_segment || 'nse_fo'}"`);
      newLines.push(`      index_segment: "${idx.index_segment || 'nse_cm'}"`);
      newLines.push(`      index_symbol: "${idx.index_symbol || idx.key}"`);
      if (idx.scrip_name) {
        newLines.push(`      scrip_name: "${idx.scrip_name}"`);
      }
      newLines.push(`      expiry_mode: "${idx.expiry_mode || 'both'}"`);
      newLines.push(`      default_expiry: "${idx.default_expiry || 'nearest_weekly'}"`);
    });

    if (idxStart !== -1 && idxEnd !== -1) {
      lines.splice(idxStart, idxEnd - idxStart, ...newLines);
      return lines.join('\n');
    } else if (mktIdx !== -1) {
      lines.splice(mktIdx + 1, 0, ...newLines);
      return lines.join('\n');
    }

    return yaml + '\nmarket:\n' + newLines.join('\n') + '\n';
  }

  function setYamlCommodities(yaml, commodities) {
    const lines = yaml.split('\n');
    let mktIdx = -1;
    let cmdStart = -1;
    let cmdEnd = -1;

    for (let i = 0; i < lines.length; i++) {
      if (lines[i].startsWith('market:')) {
        mktIdx = i;
        break;
      }
    }

    if (mktIdx !== -1) {
      for (let i = mktIdx + 1; i < lines.length; i++) {
        const line = lines[i];
        if (line.startsWith('  commodities:')) {
          cmdStart = i;
          for (let j = i + 1; j < lines.length; j++) {
            const l = lines[j];
            const trimmed = l.trim();
            if (trimmed && !trimmed.startsWith('#')) {
              const indent = l.search(/\S/);
              if (indent <= 2) {
                cmdEnd = j;
                break;
              }
            }
          }
          if (cmdEnd === -1) cmdEnd = lines.length;
          break;
        } else if (line.length > 0 && !line.startsWith(' ') && !line.startsWith('#')) {
          break;
        }
      }
    }

    const newLines = ['  commodities:'];
    commodities.forEach(cmd => {
      newLines.push(`    - key: "${cmd.key}"`);
      newLines.push(`      display_name: "${cmd.display_name || cmd.key}"`);
      newLines.push(`      exchange_segment: "${cmd.exchange_segment || 'mcx_fo'}"`);
      newLines.push(`      scrip_name: "${cmd.scrip_name || cmd.key}"`);
    });

    if (cmdStart !== -1 && cmdEnd !== -1) {
      lines.splice(cmdStart, cmdEnd - cmdStart, ...newLines);
      return lines.join('\n');
    } else if (mktIdx !== -1) {
      lines.splice(mktIdx + 1, 0, ...newLines);
      return lines.join('\n');
    }

    return yaml + '\nmarket:\n' + newLines.join('\n') + '\n';
  }

  function setYamlScannedUnderlyings(yaml: string, underlyings: string[]): string {
    const lines = yaml.split('\n');
    let aiIdx = -1;
    let suStart = -1;
    let suEnd = -1;

    for (let i = 0; i < lines.length; i++) {
      if (lines[i].startsWith('ai:')) {
        aiIdx = i;
        break;
      }
    }

    if (aiIdx !== -1) {
      for (let i = aiIdx + 1; i < lines.length; i++) {
        const line = lines[i];
        if (line.startsWith('  scanned_underlyings:')) {
          suStart = i;
          for (let j = i + 1; j < lines.length; j++) {
            const l = lines[j];
            const trimmed = l.trim();
            if (trimmed && !trimmed.startsWith('#')) {
              const indent = l.search(/\S/);
              if (indent <= 2) {
                suEnd = j;
                break;
              }
            }
          }
          if (suEnd === -1) suEnd = lines.length;
          break;
        } else if (line.length > 0 && !line.startsWith(' ') && !line.startsWith('#')) {
          break;
        }
      }
    }

    const newLines = ['  scanned_underlyings:'];
    underlyings.forEach(u => {
      newLines.push(`    - "${u}"`);
    });

    if (suStart !== -1 && suEnd !== -1) {
      lines.splice(suStart, suEnd - suStart, ...newLines);
      return lines.join('\n');
    } else if (aiIdx !== -1) {
      lines.splice(aiIdx + 1, 0, ...newLines);
      return lines.join('\n');
    }

    return yaml + '\nai:\n' + newLines.join('\n') + '\n';
  }

  function setYamlBackgroundUnderlyings(yaml: string, underlyings: string[]): string {
    const lines = yaml.split('\n');
    let mktIdx = -1;
    let bgIdx = -1;
    let uStart = -1;
    let uEnd = -1;

    for (let i = 0; i < lines.length; i++) {
      if (lines[i].startsWith('market:')) {
        mktIdx = i;
        break;
      }
    }

    if (mktIdx !== -1) {
      for (let i = mktIdx + 1; i < lines.length; i++) {
        const line = lines[i];
        if (line.length > 0 && !line.startsWith(' ') && !line.startsWith('#')) break;
        if (line.trim() === 'background_collection:') {
          bgIdx = i;
          break;
        }
      }
    }

    if (bgIdx !== -1) {
      for (let i = bgIdx + 1; i < lines.length; i++) {
        const line = lines[i];
        const trimmed = line.trim();
        if (trimmed && !trimmed.startsWith('#')) {
          const indent = line.search(/\S/);
          if (indent <= 2) {
            uEnd = i;
            break;
          }
        }
        if (trimmed.startsWith('underlyings:')) {
          uStart = i;
          for (let j = i + 1; j < lines.length; j++) {
            const child = lines[j];
            const childTrimmed = child.trim();
            if (childTrimmed && !childTrimmed.startsWith('#')) {
              const indent = child.search(/\S/);
              if (indent <= 4) {
                uEnd = j;
                break;
              }
            }
          }
          if (uEnd === -1) uEnd = lines.length;
          break;
        }
      }
    }

    const newLines = ['    underlyings:'];
    underlyings.forEach((u) => newLines.push(`      - "${u}"`));

    if (uStart !== -1) {
      lines.splice(uStart, uEnd - uStart, ...newLines);
      return lines.join('\n');
    }
    if (bgIdx !== -1) {
      lines.splice(bgIdx + 1, 0, ...newLines);
      return lines.join('\n');
    }
    if (mktIdx !== -1) {
      lines.splice(mktIdx + 1, 0, '  background_collection:', ...newLines);
      return lines.join('\n');
    }
    return yaml + '\nmarket:\n  background_collection:\n' + newLines.join('\n') + '\n';
  }

  async function loadStorageInfo() {
    const value = document.getElementById('db-size-value');
    const detail = document.getElementById('db-size-detail');
    if (!value) return;
    try {
      const res = await fetch('/api/system/storage');
      const data = await res.json();
      if (!res.ok || !data.ok) throw new Error(data.error || `HTTP ${res.status}`);
      value.textContent = data.total_human || data.size_human || '—';
      const parts: string[] = [];
      if (data.size_human) parts.push(`main ${data.size_human}`);
      if (data.wal_bytes > 0) parts.push(`WAL ${data.wal_human}`);
      if (data.shm_bytes > 0) parts.push(`SHM ${data.shm_human}`);
      if (data.path) parts.push(data.path);
      detail.textContent = parts.join(' · ') || 'Database size unavailable';
    } catch (err: any) {
      value.textContent = '—';
      detail.textContent = `Unable to read database size: ${err.message || err}`;
    }
  }

  function escapeHtml(str) {
    if (!str) return '';
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // Start on DOM ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
