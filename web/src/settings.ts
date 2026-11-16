/**
 * Scalper Settings Management UI
 */
import {
  blockListIndent,
  findFieldLine,
  replaceYamlBlock,
  setYamlList,
  setYamlScalar,
  yamlQuote,
  yamlScalarValue,
  yamlTrailingComment,
  type YamlScalarType,
} from "./settings/yaml";

(function () {
  'use strict';

  // State
  let currentConfig = null;
  let savedConfig = null;
  let currentYAML = '';
  let savedYAML = '';
  let isDirty = false;
  let activePanelId = 'panel-trading';

  // DOM Elements
  const el = (id: string): any => document.getElementById(id);
  const validateBtn = el('validate-btn');
  const revertBtn = el('revert-btn');
  const saveBtn = el('save-btn');
  const saveSpinner = el('save-spinner');
  const saveLabel = el('save-label');
  const unsavedBadge = el('unsaved-badge');
  const alertBanner = el('alert-banner');
  const alertTitle = el('alert-title');
  const alertMessage = el('alert-message');
  const alertCloseBtn = el('alert-close-btn');
  const toastContainer = el('toast-container');
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
    setupFormInputs();
    setupModals();
    setupActions();
    setupDevices();
    setupAppActions();
    loadStorageInfo();
    document.getElementById('db-size-refresh')?.addEventListener('click', loadStorageInfo);

    // Check embedded initial data first for instantaneous render
    const initialConfigEl = document.getElementById('initial-config-data');
    const initialYamlEl = document.getElementById('initial-yaml-data');

    if (initialYamlEl && initialYamlEl.textContent.trim()) {
      try {
        currentYAML = JSON.parse(initialYamlEl.textContent);
        savedYAML = currentYAML;
      } catch (err) {
        console.warn('Failed to parse initial YAML payload:', err);
      }
    }

    if (initialConfigEl && initialConfigEl.textContent.trim()) {
      try {
        currentConfig = JSON.parse(initialConfigEl.textContent);
        savedConfig = cloneConfig(currentConfig);
        populateForm(currentConfig);
      } catch (err) {
        console.warn('Failed to parse initial config JSON:', err);
      }
    }

    // Also fetch fresh from API
    fetchConfig();
  }

  function cloneConfig(cfg) {
    return cfg ? JSON.parse(JSON.stringify(cfg)) : null;
  }

  // Fetch live config from server
  async function fetchConfig(force = false): Promise<boolean> {
    try {
      const res = await fetch('/api/config');
      if (!res.ok) {
        const errData = await res.json().catch(() => ({}));
        throw new Error(errData.error || `HTTP ${res.status}`);
      }
      const data = await res.json();
      // Never clobber edits made while the initial fetch was in flight.
      if (!force && isDirty) return true;
      if (data.yaml) {
        currentYAML = data.yaml;
        savedYAML = currentYAML;
      }
      if (data.config) {
        currentConfig = data.config;
        savedConfig = cloneConfig(currentConfig);
        populateForm(currentConfig);
      }
      markDirty(false);
      return true;
    } catch (err) {
      showToast('error', `Failed to load settings: ${err.message}`);
      return false;
    }
  }

  // Tab Navigation
  function setupTabs() {
    tabBtns.forEach(btn => {
      btn.addEventListener('click', () => {
        const targetPanelId = btn.getAttribute('aria-controls');
        switchTab(btn.id, targetPanelId);
      });
    });
  }

  function switchTab(btnId, panelId) {
    activePanelId = panelId;
    tabBtns.forEach(b => {
      const isActive = b.id === btnId;
      b.classList.toggle('active', isActive);
      b.setAttribute('aria-selected', isActive ? 'true' : 'false');
    });

    tabPanels.forEach(p => {
      const isTarget = p.id === panelId;
      p.classList.toggle('active', isTarget);
    });

    if (panelId === 'panel-devices') {
      loadSettingsDevices();
    }
  }

  let deferredInstallPrompt: any = null;

  function setupAppActions() {
    const installBtn = document.getElementById('settings-install-btn') as HTMLButtonElement | null;
    const standalone = window.matchMedia('(display-mode: standalone)').matches ||
      (window.navigator as any).standalone === true;

    // The settings page is a separate document; register the same origin-scoped
    // service worker so the browser can offer the PWA install prompt here too.
    if ('serviceWorker' in navigator) {
      navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch((err) => {
        console.warn('[PWA] service worker registration failed:', err);
      });
    }

    const markInstalled = () => {
      if (!installBtn) return;
      installBtn.textContent = '✓ Installed';
      installBtn.disabled = true;
      installBtn.title = 'Scalper is installed on this device';
    };

    if (standalone) {
      markInstalled();
      return;
    }

    window.addEventListener('beforeinstallprompt', (event: any) => {
      event.preventDefault();
      deferredInstallPrompt = event;
      if (installBtn) installBtn.disabled = false;
    });

    window.addEventListener('appinstalled', () => {
      deferredInstallPrompt = null;
      markInstalled();
      showToast('success', 'Scalper installed successfully.');
    });

    installBtn?.addEventListener('click', async () => {
      if (!deferredInstallPrompt) {
        showToast('info', 'Use your browser menu and choose “Install app” or “Add to Home Screen”.', 7000);
        return;
      }
      const prompt = deferredInstallPrompt;
      deferredInstallPrompt = null;
      prompt.prompt();
      try {
        const choice = await prompt.userChoice;
        if (choice?.outcome === 'accepted') {
          showToast('success', 'Installing Scalper…');
        }
      } catch (err) {
        console.warn('[PWA] install prompt failed:', err);
      }
    });
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
    setVal('f-default-lots', q.default_lots || 1);
    setVal('f-default-unit', q.default_unit || 'lots');
    setChecked('f-allow-shares', !!q.allow_shares);
    setChecked('f-confirm-live-orders', !!t.confirm_live_orders);
    setChecked('f-confirm-chart-levels', t.confirm_chart_level_changes !== false);

    setVal('f-trading-mode', t.mode || 'live');
    const paper = t.paper || {};
    setChecked('f-paper-enabled', !!paper.enabled);
    setVal('f-paper-balance', paper.starting_balance ?? 500000);
    setVal('f-paper-slippage', paper.slippage_ticks ?? 0);
    setVal('f-paper-short-margin', paper.short_margin_pct ?? 20);
    setChecked('f-paper-confirm', !!paper.confirm_orders);
    setVal('f-paper-exchange-stop', normalizeExchangeStop(paper.exchange_stop));

    const es = (t.bracket && t.bracket.exchange_stop) || {};
    setVal('f-exchange-stop-mode', es.mode || 'off');
    setVal('f-exchange-stop-distance-mode', es.distance_mode || 'multiplier');
    setVal('f-exchange-stop-distance-value', es.distance_value ?? 2.5);
    setVal('f-exchange-stop-sll-offset', es.sl_l_offset_ticks ?? 2);
    setVal('f-exchange-stop-cancel-retries', es.cancel_retries ?? 3);
    setVal('f-exchange-stop-cancel-delay', es.cancel_retry_delay_ms ?? 100);
    setVal('f-exchange-stop-on-cancel-failure', es.on_cancel_failure || 'alert');
    setChecked('f-exchange-stop-reconcile', es.reconcile_on_startup !== false);
    setChecked('f-exchange-stop-partial-fill', es.place_on_partial_fill !== false);

    setVal('f-max-daily-loss', r.max_daily_loss_inr !== undefined ? r.max_daily_loss_inr : 5000);
    setVal('f-max-orders-min', r.max_orders_per_minute !== undefined ? r.max_orders_per_minute : 6);
    setVal('f-max-exit-sec', r.max_exit_orders_per_second !== undefined ? r.max_exit_orders_per_second : 4);
    setVal('f-stale-timeout', r.block_on_data_stale_ms !== undefined ? r.block_on_data_stale_ms : 5000);
    setVal('f-sl-margin-pct', r.stop_loss_margin_pct !== undefined ? r.stop_loss_margin_pct : 1.0);
    setVal('f-max-lots-order', r.max_lots_per_order !== undefined ? r.max_lots_per_order : 0);
    setVal('f-max-open-lots', r.max_open_lots_per_index !== undefined ? r.max_open_lots_per_index : 0);
    setVal('f-max-order-val', r.max_order_value_inr !== undefined ? r.max_order_value_inr : 0);

    setVal('f-session-open', s.open || '09:15');
    setVal('f-session-close', s.close || '15:40');
    setChecked('f-allow-outside-hours', !!s.allow_outside_hours);
    setChecked('f-margin-check', !!m.check_before_order);
    setChecked('f-margin-fail-closed', !!m.fail_closed);

    const rv = t.risk_view || {};
    setVal('f-payoff-range', rv.payoff_range_pct !== undefined ? rv.payoff_range_pct : 10);
    setVal('f-payoff-points', rv.payoff_points !== undefined ? rv.payoff_points : 121);

    // Market
    const mkt = cfg.market || {};
    const d = mkt.depth || {};
    const c = mkt.chain || {};
    const vix = mkt.india_vix || {};

    setVal('f-data-mode', mkt.data_mode || 'websocket');
    setVal('f-hsm-url', mkt.hsm_url || '');
    setVal('f-depth-mode', d.mode || 'poll');
    setChecked('f-depth-enabled', !!d.enabled);
    setVal('f-chain-batch-size', c.batch_size || 40);
    setVal('f-chain-premium-cutoff', c.premium_cutoff !== undefined ? c.premium_cutoff : 30);
    setVal('f-holidays', (mkt.holidays || []).join('\n'));

    const bg = mkt.background_collection || {};
    setChecked('f-bg-enabled', !!bg.enabled);
    setVal('f-bg-interval', bg.interval_seconds !== undefined ? bg.interval_seconds : 1);
    setVal('f-bg-strike-window', bg.strike_window !== undefined ? bg.strike_window : 1);
    setChecked('f-bg-include-positions', bg.include_open_positions !== undefined ? !!bg.include_open_positions : true);
    setChecked('f-bg-persist', bg.persist_last_selection !== undefined ? !!bg.persist_last_selection : true);

    setChecked('f-vix-enabled', !!vix.enabled);
    setVal('f-vix-segment', vix.exchange_segment || 'nse_cm');
    setVal('f-vix-symbol', vix.symbol || 'INDIA VIX');
    setVal('f-vix-scrip', vix.scrip_name || 'INDIA VIX');

    renderIndicesTable(mkt.indices || []);
    renderCommoditiesTable(mkt.commodities || []);
    renderBgUnderlyings(cfg, bg.underlyings || []);

    // Charts
    const ch = cfg.charts || {};
    setVal('f-default-timeframe', ch.default_timeframe || '5m');
    setVal('f-chart-retention', ch.retention_days !== undefined ? ch.retention_days : 30);
    renderTimeframes(ch.timeframes || ['1m', '3m', '5m', '15m', '1h', '4h']);

    // Scrip master
    const scrip = cfg.scrip || {};
    renderScripSegments(scrip.segments || []);

    // Application
    const app = cfg.app || {};
    setVal('f-app-listen', app.listen || '127.0.0.1:8080');
    setVal('f-app-db-path', app.db_path || './data/scalper.db');
    setVal('f-app-timezone', app.timezone || 'Asia/Kolkata');

    // Broker identity
    const kotak = cfg.kotak || {};
    setVal('f-kotak-mobile', kotak.mobile_number || '');
    setVal('f-kotak-ucc', kotak.ucc || '');
    setVal('f-kotak-fin-key', kotak.neo_fin_key || 'neotradeapi');

    // Web access
    const web = cfg.web || {};
    const ba = web.basic_auth || {};
    setVal('f-basic-auth-user', ba.username || '');
    setVal('f-basic-auth-pass', ba.password || '');
    setChecked('f-insecure-no-auth', !!web.insecure_no_auth);
    setVal('f-allowed-origins', (web.allowed_origins || []).join('\n'));

    // Logging
    const lg = cfg.logging || {};
    setVal('f-log-level', lg.level || 'info');
    setVal('f-log-format', lg.format || 'text');
    setVal('f-log-file', lg.file || './logs/scalper.log');
    setVal('f-max-size-mb', lg.max_size_mb || 100);
    setVal('f-max-backups', lg.max_backups || 10);
    setVal('f-retention-days', lg.retention_days || 14);
    setVal('f-tick-sample', lg.tick_sample_seconds !== undefined ? lg.tick_sample_seconds : 1);

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

    setSelectValue('f-agent-provider', ag.provider || 'openrouter');
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

  function setSelectValue(id, value) {
    const el = document.getElementById(id) as HTMLSelectElement | null;
    if (!el) return;
    const text = value == null ? '' : String(value);
    if (text && ![...el.options].some((option) => option.value === text)) {
      const option = document.createElement('option');
      option.value = text;
      option.textContent = text;
      el.appendChild(option);
    }
    el.value = text;
  }

  function normalizeExchangeStop(value) {
    switch (String(value || '').toLowerCase()) {
      case 'sl':
      case 'sl-m':
      case 'sl_m':
        return 'sl_m';
      case 'sl-l':
      case 'sl_l':
        return 'sl_l';
      default:
        return 'off';
    }
  }

  function setChecked(id, checked) {
    const el = document.getElementById(id);
    if (el) el.checked = checked;
  }

  // Setup form inputs to sync changes into currentYAML and state
  function setupFormInputs() {
    const map: Array<{ id: string; path: string[]; type: YamlScalarType }> = [
      // Trading
      { id: 'f-default-product', path: ['trading', 'default_product'], type: 'string' },
      { id: 'f-default-lots', path: ['trading', 'quantity', 'default_lots'], type: 'int' },
      { id: 'f-default-unit', path: ['trading', 'quantity', 'default_unit'], type: 'string' },
      { id: 'f-allow-shares', path: ['trading', 'quantity', 'allow_shares'], type: 'bool' },
      { id: 'f-confirm-live-orders', path: ['trading', 'confirm_live_orders'], type: 'bool' },
      { id: 'f-confirm-chart-levels', path: ['trading', 'confirm_chart_level_changes'], type: 'bool' },
      { id: 'f-exchange-stop-mode', path: ['trading', 'bracket', 'exchange_stop', 'mode'], type: 'string' },
      { id: 'f-exchange-stop-distance-mode', path: ['trading', 'bracket', 'exchange_stop', 'distance_mode'], type: 'string' },
      { id: 'f-exchange-stop-distance-value', path: ['trading', 'bracket', 'exchange_stop', 'distance_value'], type: 'float' },
      { id: 'f-exchange-stop-sll-offset', path: ['trading', 'bracket', 'exchange_stop', 'sl_l_offset_ticks'], type: 'int' },
      { id: 'f-exchange-stop-cancel-retries', path: ['trading', 'bracket', 'exchange_stop', 'cancel_retries'], type: 'int' },
      { id: 'f-exchange-stop-cancel-delay', path: ['trading', 'bracket', 'exchange_stop', 'cancel_retry_delay_ms'], type: 'int' },
      { id: 'f-exchange-stop-on-cancel-failure', path: ['trading', 'bracket', 'exchange_stop', 'on_cancel_failure'], type: 'string' },
      { id: 'f-exchange-stop-reconcile', path: ['trading', 'bracket', 'exchange_stop', 'reconcile_on_startup'], type: 'bool' },
      { id: 'f-exchange-stop-partial-fill', path: ['trading', 'bracket', 'exchange_stop', 'place_on_partial_fill'], type: 'bool' },
      { id: 'f-max-daily-loss', path: ['trading', 'risk', 'max_daily_loss_inr'], type: 'int' },
      { id: 'f-max-orders-min', path: ['trading', 'risk', 'max_orders_per_minute'], type: 'int' },
      { id: 'f-max-exit-sec', path: ['trading', 'risk', 'max_exit_orders_per_second'], type: 'int' },
      { id: 'f-stale-timeout', path: ['trading', 'risk', 'block_on_data_stale_ms'], type: 'int' },
      { id: 'f-sl-margin-pct', path: ['trading', 'risk', 'stop_loss_margin_pct'], type: 'float' },
      { id: 'f-max-lots-order', path: ['trading', 'risk', 'max_lots_per_order'], type: 'int' },
      { id: 'f-max-open-lots', path: ['trading', 'risk', 'max_open_lots_per_index'], type: 'int' },
      { id: 'f-max-order-val', path: ['trading', 'risk', 'max_order_value_inr'], type: 'int' },
      { id: 'f-session-open', path: ['trading', 'session', 'open'], type: 'string' },
      { id: 'f-session-close', path: ['trading', 'session', 'close'], type: 'string' },
      { id: 'f-allow-outside-hours', path: ['trading', 'session', 'allow_outside_hours'], type: 'bool' },
      { id: 'f-margin-check', path: ['trading', 'margin', 'check_before_order'], type: 'bool' },
      { id: 'f-margin-fail-closed', path: ['trading', 'margin', 'fail_closed'], type: 'bool' },
      { id: 'f-payoff-range', path: ['trading', 'risk_view', 'payoff_range_pct'], type: 'int' },
      { id: 'f-payoff-points', path: ['trading', 'risk_view', 'payoff_points'], type: 'int' },
      { id: 'f-trading-mode', path: ['trading', 'mode'], type: 'string' },
      { id: 'f-paper-enabled', path: ['trading', 'paper', 'enabled'], type: 'bool' },
      { id: 'f-paper-balance', path: ['trading', 'paper', 'starting_balance'], type: 'float' },
      { id: 'f-paper-slippage', path: ['trading', 'paper', 'slippage_ticks'], type: 'float' },
      { id: 'f-paper-short-margin', path: ['trading', 'paper', 'short_margin_pct'], type: 'float' },
      { id: 'f-paper-confirm', path: ['trading', 'paper', 'confirm_orders'], type: 'bool' },
      { id: 'f-paper-exchange-stop', path: ['trading', 'paper', 'exchange_stop'], type: 'string' },

      // Market
      { id: 'f-data-mode', path: ['market', 'data_mode'], type: 'string' },
      { id: 'f-hsm-url', path: ['market', 'hsm_url'], type: 'string' },
      { id: 'f-depth-mode', path: ['market', 'depth', 'mode'], type: 'string' },
      { id: 'f-depth-enabled', path: ['market', 'depth', 'enabled'], type: 'bool' },
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

      // Charts & scrip master
      { id: 'f-default-timeframe', path: ['charts', 'default_timeframe'], type: 'string' },
      { id: 'f-chart-retention', path: ['charts', 'retention_days'], type: 'int' },

      // Application & access
      { id: 'f-app-listen', path: ['app', 'listen'], type: 'string' },
      { id: 'f-app-db-path', path: ['app', 'db_path'], type: 'string' },
      { id: 'f-app-timezone', path: ['app', 'timezone'], type: 'string' },
      { id: 'f-kotak-mobile', path: ['kotak', 'mobile_number'], type: 'string' },
      { id: 'f-kotak-ucc', path: ['kotak', 'ucc'], type: 'string' },
      { id: 'f-kotak-fin-key', path: ['kotak', 'neo_fin_key'], type: 'string' },
      { id: 'f-basic-auth-user', path: ['web', 'basic_auth', 'username'], type: 'string' },
      { id: 'f-basic-auth-pass', path: ['web', 'basic_auth', 'password'], type: 'string' },
      { id: 'f-insecure-no-auth', path: ['web', 'insecure_no_auth'], type: 'bool' },

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

        if (item.id === 'f-insecure-no-auth' && val === true) {
          const ok = confirm('Allow binding to a public interface without credentials? Only enable this behind a trusted reverse proxy.');
          if (!ok) {
            el.checked = false;
            return;
          }
        }

        // Update YAML string
        currentYAML = setYamlScalar(currentYAML, item.path, val, item.type);
        markDirty(true);
      });
    });

    const holidaysInput = document.getElementById('f-holidays') as HTMLTextAreaElement | null;
    if (holidaysInput) {
      holidaysInput.addEventListener('input', () => {
        const values = holidaysInput.value.split('\n').map((value) => value.trim()).filter(Boolean);
        currentYAML = setYamlList(currentYAML, ['market', 'holidays'], values);
        clearFieldError('market.holidays');
        markDirty(true);
      });
    }

    const originsInput = document.getElementById('f-allowed-origins') as HTMLTextAreaElement | null;
    if (originsInput) {
      originsInput.addEventListener('input', () => {
        const values = originsInput.value.split('\n').map((value) => value.trim()).filter(Boolean);
        currentYAML = setYamlList(currentYAML, ['web', 'allowed_origins'], values);
        clearFieldError('web.allowed_origins');
        markDirty(true);
      });
    }

    bindCheckboxGroup('chart-timeframes-list', ['charts', 'timeframes']);
    bindCheckboxGroup('scrip-segments-list', ['scrip', 'segments']);
    bindCheckboxGroup('bg-underlyings-list', ['market', 'background_collection', 'underlyings']);
  }

  function bindCheckboxGroup(containerId, path) {
    const container = document.getElementById(containerId);
    if (!container) return;
    container.addEventListener('change', (event) => {
      const target = event.target as HTMLInputElement;
      if (!target || target.type !== 'checkbox') return;
      const values = [...container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
        .filter((cb) => cb.checked && cb.value)
        .map((cb) => cb.value);
      currentYAML = setYamlList(currentYAML, path, values);
      clearFieldError(path.join('.'));
      markDirty(true);
    });
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
    clearFieldError('market.indices');
    if (origKey && origKey !== newIdx.key) {
      renameUnderlyingInDependentLists(origKey, newIdx.key);
    }
    renderScannedUnderlyings(currentConfig);
    renderBgUnderlyings(currentConfig, currentConfig.market?.background_collection?.underlyings);
    currentYAML = setYamlIndices(currentYAML, indices);
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
    removeUnderlyingFromDependentLists(key);
    renderIndicesTable(currentConfig.market.indices);
    clearFieldError('market.indices');
    renderScannedUnderlyings(currentConfig);
    renderBgUnderlyings(currentConfig, currentConfig.market?.background_collection?.underlyings);
    currentYAML = setYamlIndices(currentYAML, currentConfig.market.indices);
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
    clearFieldError('market.commodities');
    if (origKey && origKey !== newCmd.key) {
      renameUnderlyingInDependentLists(origKey, newCmd.key);
    }
    renderScannedUnderlyings(currentConfig);
    renderBgUnderlyings(currentConfig, currentConfig.market?.background_collection?.underlyings);
    currentYAML = setYamlCommodities(currentYAML, commodities);
    markDirty(true);
    closeCommodityModal();
  }

  function deleteCommodity(key) {
    if (!currentConfig || !currentConfig.market || !currentConfig.market.commodities) return;
    if (!confirm(`Are you sure you want to remove commodity "${key}"?`)) return;

    currentConfig.market.commodities = currentConfig.market.commodities.filter(c => c.key !== key);
    removeUnderlyingFromDependentLists(key);
    renderCommoditiesTable(currentConfig.market.commodities);
    clearFieldError('market.commodities');
    renderScannedUnderlyings(currentConfig);
    renderBgUnderlyings(currentConfig, currentConfig.market?.background_collection?.underlyings);
    currentYAML = setYamlCommodities(currentYAML, currentConfig.market.commodities);
    markDirty(true);
  }

  // Scanned Underlyings Checkboxes Rendering
  function renderScannedUnderlyings(cfg: any) {
    const container = document.getElementById('ai-underlyings-list');
    if (!container) return;
    container.innerHTML = '';

    const indices = (cfg.market && cfg.market.indices) || [];
    const commodities = (cfg.market && cfg.market.commodities) || [];
    const configured = cfg.ai && cfg.ai.scanned_underlyings;
    const scanned: string[] = Array.isArray(configured) ? configured : ['NIFTY', 'SENSEX'];

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
        markDirty(true);
      });

      container.appendChild(label);
    });
  }

  function removeUnderlyingFromDependentLists(key) {
    if (!currentConfig || !key) return;
    const ai = currentConfig.ai;
    if (ai && Array.isArray(ai.scanned_underlyings) && ai.scanned_underlyings.includes(key)) {
      ai.scanned_underlyings = ai.scanned_underlyings.filter((item) => item !== key);
      currentYAML = setYamlScannedUnderlyings(currentYAML, ai.scanned_underlyings);
    }
    const bg = currentConfig.market && currentConfig.market.background_collection;
    if (bg && Array.isArray(bg.underlyings) && bg.underlyings.includes(key)) {
      bg.underlyings = bg.underlyings.filter((item) => item !== key);
      currentYAML = setYamlList(currentYAML, ['market', 'background_collection', 'underlyings'], bg.underlyings);
    }
  }

  function renameUnderlyingInDependentLists(oldKey, newKey) {
    if (!currentConfig || !oldKey || !newKey || oldKey === newKey) return;
    const rename = (list) => [...new Set(list.map((item) => (item === oldKey ? newKey : item)))];
    const ai = currentConfig.ai;
    if (ai && Array.isArray(ai.scanned_underlyings) && ai.scanned_underlyings.includes(oldKey)) {
      ai.scanned_underlyings = rename(ai.scanned_underlyings);
      currentYAML = setYamlScannedUnderlyings(currentYAML, ai.scanned_underlyings);
    }
    const bg = currentConfig.market && currentConfig.market.background_collection;
    if (bg && Array.isArray(bg.underlyings) && bg.underlyings.includes(oldKey)) {
      bg.underlyings = rename(bg.underlyings);
      currentYAML = setYamlList(currentYAML, ['market', 'background_collection', 'underlyings'], bg.underlyings);
    }
  }

  // Background collection underlyings (built from the configured indices and commodities)
  function renderBgUnderlyings(cfg, selected?: string[]) {
    const container = document.getElementById('bg-underlyings-list');
    if (!container) return;

    let chosen = selected;
    if (chosen === undefined) {
      chosen = [...container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
        .filter((cb) => cb.checked && cb.value)
        .map((cb) => cb.value);
    }
    const chosenSet = new Set(chosen || []);

    const indices = (cfg && cfg.market && cfg.market.indices) || [];
    const commodities = (cfg && cfg.market && cfg.market.commodities) || [];

    const items: Array<{ key: string; name: string; isMCX: boolean }> = [];
    indices.forEach((idx: any) => {
      items.push({ key: idx.key, name: idx.display_name || idx.key, isMCX: false });
    });
    commodities.forEach((cmd: any) => {
      items.push({ key: cmd.key, name: cmd.display_name || cmd.key, isMCX: true });
    });
    // Keep explicitly configured underlyings that are not in the current index/commodity list.
    chosenSet.forEach((key) => {
      if (!items.some((item) => item.key === key)) {
        items.push({ key, name: key, isMCX: false });
      }
    });

    container.innerHTML = '';
    if (items.length === 0) {
      container.innerHTML = '<span style="color:var(--text-dim);font-size:12px;">No indices or commodities configured.</span>';
      return;
    }

    items.forEach((item) => {
      const label = document.createElement('label');
      label.className = 'checkbox-label scanned-underlying-item';
      label.innerHTML = `
        <input type="checkbox" value="${escapeHtml(item.key)}" ${chosenSet.has(item.key) ? 'checked' : ''}>
        <span class="checkbox-custom"></span>
        <span class="checkbox-text">
          <strong>${escapeHtml(item.key)}</strong>
          <span class="form-help">${escapeHtml(item.name)} <code style="font-size:10px;margin-left:4px;opacity:0.8;">${item.isMCX ? 'MCX' : 'NSE/BSE'}</code></span>
        </span>
      `;
      container.appendChild(label);
    });
  }

  function renderTimeframes(values) {
    const container = document.getElementById('chart-timeframes-list');
    if (!container) return;
    const selected = new Set(values || []);
    container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]').forEach((cb) => {
      cb.checked = selected.has(cb.value);
    });
  }

  function renderScripSegments(values) {
    const container = document.getElementById('scrip-segments-list');
    if (!container) return;
    const selected = new Set(values || []);
    container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]').forEach((cb) => {
      cb.checked = selected.has(cb.value);
    });
  }

  async function revertChanges() {
    if (!confirm('Discard all unsaved changes and reload the last saved configuration?')) return;
    clearErrors();
    const loaded = await fetchConfig(true);
    if (!loaded) {
      // Fall back to the snapshot from the last successful load/save.
      currentYAML = savedYAML;
      currentConfig = cloneConfig(savedConfig);
      if (currentConfig) populateForm(currentConfig);
      markDirty(false);
    }
    showToast('success', 'Reverted to the last saved configuration');
  }

  // Header Actions (Validate & Save)
  function setupActions() {
    validateBtn.addEventListener('click', () => validateConfig(false));
    saveBtn.addEventListener('click', () => saveConfig());
    revertBtn?.addEventListener('click', revertChanges);
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
    const yamlToSend = currentYAML;

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

      if (!silentSuccess) {
        showToast('success', 'Configuration syntax and rules are completely valid!');
      }
      return true;
    } catch (err) {
      showToast('error', `Validation network error: ${err.message}`);
      return false;
    }
  }

  // Save configuration to disk
  async function saveConfig() {
    clearErrors();
    const yamlToSend = currentYAML;

    saveBtn.disabled = true;
    validateBtn.disabled = true;
    if (revertBtn) revertBtn.disabled = true;
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
      // replaced by the mask instead of remaining visible in the form.
      savedYAML = data.yaml || yamlToSend;
      const unchanged = currentYAML === yamlToSend;
      if (data.config) {
        savedConfig = cloneConfig(data.config);
      }

      if (unchanged) {
        currentYAML = savedYAML;
        markDirty(false);
        if (data.config) {
          currentConfig = data.config;
          populateForm(currentConfig);
        }
      } else {
        // The user kept editing while the save was in flight; keep their input
        // and leave the form marked dirty against the newly saved baseline.
        markDirty(true);
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
      validateBtn.disabled = false;
      if (revertBtn) revertBtn.disabled = false;
      saveSpinner.hidden = true;
      saveLabel.textContent = 'Save Configuration';
    }
  }

  // Error Handling
  function handleValidationError(data) {
    const errorMsg = data.message || data.error || 'Validation failed';
    const field = data.field || '';

    showAlert(field ? `Error in ${field}` : 'Validation Error', errorMsg);

    if (field) {
      highlightField(field, errorMsg);
    }
    showToast('error', `Validation Error: ${errorMsg}`);
  }

  function resolveFieldGroup(fieldPath) {
    const normalized = String(fieldPath || '').replace(/\[\d+\]/g, '');
    const candidates: string[] = [];
    let current = normalized;
    while (current) {
      candidates.push(current);
      const dot = current.lastIndexOf('.');
      if (dot < 0) break;
      current = current.slice(0, dot);
    }

    for (const candidate of candidates) {
      const group = document.querySelector(`[data-field="${candidate}"]`)
        || document.querySelector(`[data-field^="${candidate}."]`);
      if (group) return group;
    }
    return null;
  }

  function highlightField(fieldPath, msg) {
    const group = resolveFieldGroup(fieldPath);
    if (!group) return;

    group.classList.add('has-error');
    const errEl = group.querySelector('.field-error');
    if (errEl) errEl.textContent = msg;

    // Find which tab this field is in and switch to it if needed
    const panel = group.closest('.tab-panel');
    if (panel && panel.id !== activePanelId) {
      const tabBtn = document.querySelector(`[aria-controls="${panel.id}"]`);
      if (tabBtn) {
        switchTab(tabBtn.id, panel.id);
      }
    }

    group.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }

  function clearFieldError(fieldPath) {
    const group = resolveFieldGroup(fieldPath);
    if (!group) return;
    group.classList.remove('has-error');
    const errEl = group.querySelector('.field-error');
    if (errEl) errEl.textContent = '';
  }

  function clearErrors() {
    hideAlert();
    document.querySelectorAll('.form-group.has-error, .card.has-error').forEach(g => {
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
    }, duration);
  }

  function markDirty(dirty) {
    isDirty = dirty;
    unsavedBadge.hidden = !dirty;
  }

  // --- YAML Manipulation Helpers ---

  function mappingListIndents(yaml, path) {
    const lines = yaml.split('\n');
    const idx = findFieldLine(lines, path);
    if (idx === -1) {
      const keyIndent = 2 * (path.length - 1);
      return { item: keyIndent + 2, child: keyIndent + 4 };
    }
    const keyIndent = lines[idx].match(/^ */)[0].length;
    const item = blockListIndent(lines, idx);
    let child = item + 2;
    for (let i = idx + 1; i < lines.length; i++) {
      const line = lines[i];
      if (!line.trim() || line.trim().startsWith('#')) continue;
      const indent = line.match(/^ */)[0].length;
      if (indent <= keyIndent) break;
      if (line.trim().startsWith('- ')) continue;
      child = indent;
      break;
    }
    return { item, child };
  }

  function collectBlockComments(yaml, path) {
    const lines = yaml.split('\n');
    const keyIdx = findFieldLine(lines, path);
    const comments: Record<string, Record<string, string>> = {};
    if (keyIdx === -1) return comments;

    const keyIndent = lines[keyIdx].match(/^ */)[0].length;
    let current = '';
    for (let i = keyIdx + 1; i < lines.length; i++) {
      const line = lines[i];
      if (!line.trim() || line.trim().startsWith('#')) continue;
      if (line.match(/^ */)[0].length <= keyIndent) break;

      const item = line.match(/^\s*-\s+([A-Za-z0-9_]+):\s*(.*)$/);
      if (item) {
        const field = item[1];
        if (field === 'key') {
          current = yamlScalarValue(item[2]);
          comments[current] = comments[current] || {};
        }
        const comment = yamlTrailingComment(item[2]);
        if (current && comment) comments[current][field] = comment;
        continue;
      }

      const child = line.match(/^\s*([A-Za-z0-9_]+):\s*(.*)$/);
      if (child && current) {
        const comment = yamlTrailingComment(child[2]);
        if (comment) comments[current][child[1]] = comment;
      }
    }
    return comments;
  }

  function setYamlIndices(yaml, indices) {
    const { item, child } = mappingListIndents(yaml, ['market', 'indices']);
    const comments = collectBlockComments(yaml, ['market', 'indices']);
    const note = (key, field) => comments[key] && comments[key][field] ? ` ${comments[key][field]}` : '';
    const block: string[] = [];
    for (const idx of indices) {
      const pad = ' '.repeat(child);
      block.push(`${' '.repeat(item)}- key: ${yamlQuote(idx.key)}${note(idx.key, 'key')}`);
      block.push(`${pad}display_name: ${yamlQuote(idx.display_name || idx.key)}${note(idx.key, 'display_name')}`);
      block.push(`${pad}exchange_segment: ${yamlQuote(idx.exchange_segment || 'nse_fo')}${note(idx.key, 'exchange_segment')}`);
      block.push(`${pad}index_segment: ${yamlQuote(idx.index_segment || 'nse_cm')}${note(idx.key, 'index_segment')}`);
      block.push(`${pad}index_symbol: ${yamlQuote(idx.index_symbol || idx.key)}${note(idx.key, 'index_symbol')}`);
      if (idx.scrip_name) block.push(`${pad}scrip_name: ${yamlQuote(idx.scrip_name)}${note(idx.key, 'scrip_name')}`);
      block.push(`${pad}expiry_mode: ${yamlQuote(idx.expiry_mode || 'both')}${note(idx.key, 'expiry_mode')}`);
      block.push(`${pad}default_expiry: ${yamlQuote(idx.default_expiry || 'nearest_weekly')}${note(idx.key, 'default_expiry')}`);
    }
    return replaceYamlBlock(yaml, ['market', 'indices'], block);
  }

  function setYamlCommodities(yaml, commodities) {
    const { item, child } = mappingListIndents(yaml, ['market', 'commodities']);
    const comments = collectBlockComments(yaml, ['market', 'commodities']);
    const note = (key, field) => comments[key] && comments[key][field] ? ` ${comments[key][field]}` : '';
    const block: string[] = [];
    for (const cmd of commodities) {
      const pad = ' '.repeat(child);
      block.push(`${' '.repeat(item)}- key: ${yamlQuote(cmd.key)}${note(cmd.key, 'key')}`);
      block.push(`${pad}display_name: ${yamlQuote(cmd.display_name || cmd.key)}${note(cmd.key, 'display_name')}`);
      block.push(`${pad}exchange_segment: ${yamlQuote(cmd.exchange_segment || 'mcx_fo')}${note(cmd.key, 'exchange_segment')}`);
      block.push(`${pad}scrip_name: ${yamlQuote(cmd.scrip_name || cmd.key)}${note(cmd.key, 'scrip_name')}`);
    }
    return replaceYamlBlock(yaml, ['market', 'commodities'], block);
  }

  function setYamlScannedUnderlyings(yaml: string, underlyings: string[]): string {
    return setYamlList(yaml, ['ai', 'scanned_underlyings'], underlyings);
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
