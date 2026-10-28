import { CandleChart } from "./charts.js";

const $ = (id) => document.getElementById(id);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (c) => ({
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
  "'": "&#39;",
}[c]));

const num = (value, fallback = 0) => {
  const n = Number(value);
  return Number.isFinite(n) ? n : fallback;
};

function fmt(value, digits = 2) {
  if (!Number.isFinite(value)) return "—";
  return value.toLocaleString("en-IN", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

function fmtSigned(value, digits = 2) {
  if (!Number.isFinite(value)) return "—";
  const sign = value > 0 ? "+" : value < 0 ? "−" : "";
  return sign + fmt(Math.abs(value), digits);
}

function fmtCompact(value) {
  if (!Number.isFinite(value)) return "—";
  if (Math.abs(value) >= 1e7) return `${fmt(value / 1e7, 2)}Cr`;
  if (Math.abs(value) >= 1e5) return `${fmt(value / 1e5, 2)}L`;
  if (Math.abs(value) >= 1e3) return `${fmt(value / 1e3, 1)}K`;
  return fmt(value, 0);
}

function fmtStrike(value) {
  if (!Number.isFinite(value)) return "—";
  return Number.isInteger(value) ? String(value) : fmt(value, 2);
}

function directionClass(value) {
  return value > 0 ? "up" : value < 0 ? "down" : "";
}

function fmtTime(ts) {
  if (!ts) return "—";
  const date = typeof ts === "number" ? new Date(ts < 1e12 ? ts * 1000 : ts) : new Date(ts);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit", timeZone: "Asia/Kolkata" });
}

function parseBootstrap() {
  const node = $("bootstrap");
  if (!node) return {};
  try {
    return JSON.parse(node.textContent || "{}");
  } catch (error) {
    console.error("bootstrap parse failed", error);
    return {};
  }
}

function tradingMode() {
  return "paper";
}

const S = {
  boot: {},
  activeIndex: "NIFTY",
  expiry: "",
  timeframe: "5m",
  expiries: [],
  chain: [],
  spots: new Map(),
  selected: { strike: null, type: "CE", call: null, put: null },
  qty: { CE: 1, PE: 1 },
  product: "MIS",
  positions: [],
  orders: [],
  trades: [],
  limits: { available: 0 },
  risk: [],
  tab: "positions",
  feed: "connecting",
  session: null,
  chainScrolled: false,
  polling: false,
  ws: null,
  wsTimer: null,
  feedSubscriptions: [],
  charts: {},
  candles: {},
  historicalCache: new Map(),
  chartLoadID: 0,
  marketOpen: null,
  focusedChart: null,
};

let marketRetryTimer = null;

/* ── API helpers ───────────────────────────────────────── */

async function requestJSON(url, options = {}) {
  const response = await fetch(url, {
    headers: { Accept: "application/json", ...(options.body ? { "Content-Type": "application/json" } : {}) },
    ...options,
  });
  const text = await response.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!response.ok) {
    const message = data?.error || text || `${response.status} ${response.statusText}`;
    const error = new Error(message);
    error.status = response.status;
    throw error;
  }
  return data;
}

const getJSON = (url) => requestJSON(url);
const postJSON = (url, body) => requestJSON(url, { method: "POST", body: JSON.stringify(body) });

function postOrder(payload) {
  return postJSON("/api/orders", payload).catch((error) => {
    if (error.status !== 404) throw error;
    // Compatibility with the broker server's current route.
    return postJSON("/api/broker/order", payload);
  });
}

/* ── Normalisers for the documented API shapes ─────────── */

function normalizeStrike(value) {
  let strike = num(value);
  if (!strike) return 0;
  if (strike > 100000) strike /= 100;
  return strike;
}

function normalizeOption(raw, type) {
  if (!raw) return null;
  const strike = normalizeStrike(raw.strike ?? raw.strike_price ?? raw.strike_paise ?? raw.strikePaise);
  const ltp = num(raw.ltp ?? raw.last_price ?? raw.lastPrice ?? raw.price);
  const prevClose = num(raw.previous_close ?? raw.prev_close ?? raw.prevClose ?? raw.close ?? ltp);
  const oi = num(raw.oi ?? raw.open_interest ?? raw.openInterest);
  return {
    instrumentKey: raw.instrument_key ?? raw.instrumentKey ?? raw.key ?? "",
    exchangeSegment: raw.exchange_segment ?? raw.exchangeSegment ?? "nse_fo",
    underlying: raw.underlying ?? S.activeIndex,
    expiry: raw.expiry ?? S.expiry,
    strike,
    type: (raw.option_type ?? raw.optionType ?? raw.type ?? type ?? "").toUpperCase(),
    lotSize: num(raw.lot_size ?? raw.lotSize, 75),
    symbol: raw.symbol ?? raw.p_trd_symbol ?? raw.pTrdSymbol ?? "",
    pSymbol: raw.p_symbol ?? raw.pSymbol ?? "",
    pTrdSymbol: raw.p_trd_symbol ?? raw.pTrdSymbol ?? "",
    quoteAvailable: raw.quote_available === true,
    ltp,
    prevClose,
    close: prevClose,
    change: num(raw.change, ltp - prevClose),
    changePct: prevClose ? ((ltp - prevClose) / prevClose) * 100 : 0,
    oi,
    volume: num(raw.volume),
    bid: num(raw.bid ?? raw.best_bid ?? raw.bestBid),
    ask: num(raw.ask ?? raw.best_ask ?? raw.bestAsk),
  };
}

function normalizeChain(raw) {
  const rowsRaw = Array.isArray(raw) ? raw : (raw?.rows || raw?.entries || raw?.chain || []);
  const byStrike = new Map();
  for (const item of rowsRaw) {
    const strike = normalizeStrike(item.strike ?? item.strike_price ?? item.strike_paise ?? item.strikePaise);
    if (!strike) continue;
    let row = byStrike.get(strike);
    if (!row) {
      row = { strike, atm: Boolean(item.is_atm ?? item.isATM ?? item.atm), call: null, put: null };
      byStrike.set(strike, row);
    }
    const callRaw = item.call ?? item.ce ?? item.CE;
    const putRaw = item.put ?? item.pe ?? item.PE;
    if (callRaw) row.call = normalizeOption(callRaw, "CE");
    if (putRaw) row.put = normalizeOption(putRaw, "PE");
    if (!callRaw && !putRaw && (item.option_type || item.optionType || item.type)) {
      const type = String(item.option_type ?? item.optionType ?? item.type).toUpperCase();
      if (type === "CE") row.call = normalizeOption(item, "CE");
      if (type === "PE") row.put = normalizeOption(item, "PE");
    }
  }
  return [...byStrike.values()].sort((a, b) => a.strike - b.strike);
}

function normalizeExpiry(value) {
  if (typeof value === "string") {
    const date = new Date(value + "T00:00:00");
    return {
      date: value,
      label: Number.isNaN(date.getTime())
        ? value
        : date.toLocaleDateString("en-GB", { day: "2-digit", month: "short" }).toUpperCase(),
      weekly: true,
      monthly: false,
      expired: false,
    };
  }
  const date = value.date ?? value.expiry ?? value.value ?? "";
  const parsed = new Date(date + "T00:00:00");
  return {
    date,
    label: value.label ?? (Number.isNaN(parsed.getTime())
      ? date
      : parsed.toLocaleDateString("en-GB", { day: "2-digit", month: "short" }).toUpperCase()),
    weekly: value.weekly !== false,
    monthly: Boolean(value.monthly),
    expired: Boolean(value.expired),
  };
}

function normalizePosition(raw) {
  return {
    instrument_key: raw.instrument_key ?? raw.instrumentKey ?? "",
    exchange_segment: raw.exchange_segment ?? raw.exchangeSegment ?? "nse_fo",
    symbol: raw.symbol ?? raw.trading_symbol ?? raw.trdSymbol ?? "",
    underlying: raw.underlying ?? "",
    expiry: raw.expiry ?? "",
    strike: normalizeStrike(raw.strike ?? raw.strike_paise ?? 0),
    option_type: String(raw.option_type ?? raw.optionType ?? raw.type ?? "").toUpperCase(),
    lot_size: num(raw.lot_size ?? raw.lotSize, 1),
    quantity: num(raw.quantity ?? raw.net_qty ?? raw.netQty ?? raw.qty),
    average_price: num(raw.average_price ?? raw.averagePrice ?? raw.avg_price ?? raw.avgPrice ?? raw.avg),
    ltp: num(raw.ltp ?? raw.last_price ?? raw.lastPrice, 0),
    pnl: num(raw.pnl ?? raw.unrealised_pnl ?? raw.unrealizedPnl ?? raw.today_pnl ?? raw.todayPnl, 0),
    product: raw.product ?? "MIS",
  };
}

function normalizeOrder(raw) {
  return {
    id: raw.id ?? raw.client_order_id ?? raw.order_id ?? raw.broker_order_id ?? "—",
    symbol: raw.symbol ?? raw.trading_symbol ?? raw.instrument_key ?? "",
    instrument_key: raw.instrument_key ?? raw.instrumentKey ?? "",
    side: raw.side ?? raw.buy_sell ?? raw.buySell ?? "",
    quantity: num(raw.quantity ?? raw.qty),
    price: num(raw.price),
    status: raw.status ?? "",
    reason: raw.reason ?? raw.reject_reason ?? "",
    timestamp: raw.timestamp ?? raw.created_at ?? raw.updated_at ?? 0,
    product: raw.product ?? "MIS",
    mode: raw.mode ?? "",
  };
}

function normalizeTrade(raw) {
  return {
    id: raw.id ?? raw.trade_id ?? raw.order_id ?? "—",
    symbol: raw.symbol ?? raw.instrument_key ?? "",
    side: raw.side ?? raw.buy_sell ?? raw.buySell ?? "",
    quantity: num(raw.quantity ?? raw.qty),
    price: num(raw.price),
    timestamp: raw.timestamp ?? raw.executed_at ?? raw.executedAt ?? 0,
  };
}

/* ── Initialisation ────────────────────────────────────── */

function init() {
  S.boot = parseBootstrap();
  initTheme();
  S.timeframe = S.boot.defaultTimeframe || "5m";
  S.product = S.boot.defaultProduct || "MIS";
  const defaultLots = Math.max(1, num(S.boot.defaultLots, 1));
  S.qty.CE = defaultLots;
  S.qty.PE = defaultLots;
  S.activeIndex = S.boot.indices?.[0]?.key || "NIFTY";

  updateMarketPhase(false);
  buildTimeframes();
  buildCharts();
  bindEvents();
  updateClock();
  window.setInterval(() => {
    updateClock();
    updateMarketPhase();
  }, 1000);
  checkSession();
  window.setInterval(refreshSession, 30000);
  loadMarket();
}

function initTheme() {
  let theme = "dark";
  try {
    const savedTheme = localStorage.getItem("scalper-theme");
    if (savedTheme === "light" || savedTheme === "dark") theme = savedTheme;
  } catch (error) {
    console.warn("Unable to read saved theme preference", error);
  }
  setTheme(theme, false);
}

function setTheme(theme, persist = true) {
  document.body.dataset.theme = theme;
  const toggle = $("theme-toggle");
  const nextTheme = theme === "dark" ? "light" : "dark";
  toggle.setAttribute("aria-label", `Switch to ${nextTheme} theme`);
  toggle.title = `Switch to ${nextTheme} theme`;
  document.querySelector('meta[name="color-scheme"]').content = theme;
  for (const chart of Object.values(S.charts)) chart.schedule();
  if (!persist) return;
  try {
    localStorage.setItem("scalper-theme", theme);
  } catch (error) {
    console.warn("Unable to save theme preference", error);
  }
}

function buildTimeframes() {
  const group = $("tf-group");
  const list = S.boot.timeframes?.length ? S.boot.timeframes : ["1m", "3m", "5m", "15m", "1h"];
  group.innerHTML = list.map((tf) => `<button type="button" data-tf="${esc(tf)}" class="${tf === S.timeframe ? "active" : ""}">${esc(tf)}</button>`).join("");
}

function buildCharts() {
  S.charts.ce = new CandleChart($("chart-ce"), { showVolume: true });
  S.charts.pe = new CandleChart($("chart-pe"), { showVolume: true });
}

function setFocusedChart(pane) {
  if (S.focusedChart === pane) pane = null;
  S.focusedChart = pane || null;
  const terminal = $("charts-row").closest(".terminal");
  terminal.classList.toggle("chart-focused", Boolean(S.focusedChart));
  $("charts-row").classList.toggle("expanded", Boolean(S.focusedChart));
  for (const article of $$(".charts-row .pane")) {
    article.classList.toggle("expanded", article.dataset.pane === S.focusedChart);
  }
  for (const button of $$(".chart-tools button[data-chart-action='expand']")) {
    const active = button.dataset.chart === S.focusedChart;
    button.classList.toggle("active", active);
    button.title = active ? "Restore chart layout (Esc)" : "Expand chart";
    button.setAttribute("aria-pressed", active ? "true" : "false");
  }
  requestAnimationFrame(() => {
    for (const chart of Object.values(S.charts)) chart.schedule();
  });
}

async function refreshSession() {
  const previousMode = tradingMode();
  try {
    S.session = await getJSON("/api/session");
  } catch {
    S.session = null;
  }
  updateAuthButton();
  if (tradingMode() !== previousMode) renderHeader();
}

async function checkSession() {
  await refreshSession();
  const wantsLogin = location.hash === "#login";
  if (wantsLogin || !S.session?.authenticated) {
    openLogin();
  } else {
    closeLogin();
    connectWebSocket();
  }
}

function updateAuthButton() {
  const button = $("auth-button");
  if (S.session?.authenticated) {
    button.textContent = "Logout";
    button.classList.add("logged-in");
  } else {
    button.textContent = "Login";
    button.classList.remove("logged-in");
  }
}

/* ── Market loading ────────────────────────────────────── */

async function loadMarket() {
  try {
    await loadFromAPI();
    if (S.feed !== "live") S.feed = S.session?.authenticated ? "connecting" : "degraded";
    S.apiAvailable = true;
    startLivePolling();
    if (S.session?.authenticated) connectWebSocket();
  } catch (error) {
    console.error("live market data unavailable", error);
    S.feed = "degraded";
    renderAll();
    toast(`Live market data unavailable: ${error.message}`, "error", 8000);
    if (!marketRetryTimer) {
      marketRetryTimer = window.setTimeout(() => {
        marketRetryTimer = null;
        loadMarket();
      }, 30000);
    }
  }
}

async function loadFromAPI() {
  const indexRaw = await getJSON("/api/indices");
  applyIndexQuotes(indexRaw);
  const expiryRaw = await getJSON(`/api/expiries?index=${encodeURIComponent(S.activeIndex)}`);
  S.expiries = normalizeExpiryList(expiryRaw);
  S.expiry = pickExpiry(S.expiries);

  const chainRaw = await getJSON(`/api/chain?index=${encodeURIComponent(S.activeIndex)}&expiry=${encodeURIComponent(S.expiry)}`);
  S.chain = normalizeChain(chainRaw);
  await refreshBooksFromAPI();
  selectDefaultStrike();
  S.chainScrolled = false;
  renderAll();
  loadCharts();
}

function applyIndexQuotes(indexRaw) {
  const list = Array.isArray(indexRaw) ? indexRaw : (indexRaw?.indices || []);
  const map = new Map();
  for (const item of list) {
    const key = item.key ?? item.underlying ?? item.display_name ?? item.displayName;
    if (!key) continue;
    const spot = item.spot || item;
    map.set(key, {
      key,
      displayName: item.display_name ?? item.displayName ?? key,
      exchangeSegment: item.exchange_segment ?? item.exchangeSegment ?? "",
      indexSegment: item.index_segment ?? item.indexSegment ?? "",
      symbol: item.index_symbol ?? item.indexSymbol ?? item.symbol ?? key,
      ltp: num(spot.ltp ?? spot.last_price ?? spot.price),
      prevClose: num(spot.prev_close ?? spot.previous_close ?? spot.close ?? spot.prevClose),
      close: num(spot.close ?? spot.prevClose),
      change: num(spot.change),
      changePct: num(spot.change_pct ?? spot.changePct),
      volume: num(spot.volume),
      spotKey: item.instrument_key ?? item.instrumentKey ?? `${item.index_segment ?? "nse_cm"}|${key}`,
    });
  }
  for (const idx of S.boot.indices || []) {
    if (!map.has(idx.key)) {
      map.set(idx.key, {
        key: idx.key,
        displayName: idx.displayName || idx.key,
        exchangeSegment: idx.exchangeSegment,
        indexSegment: idx.indexSegment,
        symbol: idx.indexSymbol,
        ltp: 0,
        prevClose: 0,
        close: 0,
        change: 0,
        changePct: 0,
        volume: 0,
        spotKey: `${idx.indexSegment}|${idx.key}`,
      });
    }
  }
  S.spots = map;
}

function normalizeExpiryList(raw) {
  const list = Array.isArray(raw) ? raw : (raw?.expiries || raw?.expiry || []);
  return list.map(normalizeExpiry).filter((e) => e.date && !e.expired);
}

function pickExpiry(list) {
  const first = list.find((e) => !e.expired) || list[0];
  return first?.date || "";
}

async function refreshBooksFromAPI(includePositions = true) {
  const [positions, orders, trades, limits, risk] = await Promise.allSettled([
    includePositions ? getJSON("/api/positions") : Promise.resolve(null),
    getJSON("/api/orders"),
    getJSON("/api/trades"),
    getJSON("/api/limits"),
    getJSON("/api/risk"),
  ]);
  if (includePositions && positions.status === "fulfilled") {
    S.positions = toArray(positions.value).map(normalizePosition);
    updateFeedSubscriptions();
  }
  if (orders.status === "fulfilled") {
    S.orders = toArray(orders.value).map(normalizeOrder);
  }
  if (trades.status === "fulfilled") {
    S.trades = toArray(trades.value).map(normalizeTrade);
  }
  if (limits.status === "fulfilled") {
    const value = limits.value || {};
    S.limits = {
      available: num(value.available_margin ?? value.availableMargin ?? value.available ?? value.Net ?? value.net),
      used: num(value.used_margin ?? value.usedMargin ?? value.used ?? value.MarginUsed),
      total: num(value.total_margin ?? value.totalMargin ?? value.total ?? value.Net ?? value.net),
    };
  }
  if (risk.status === "fulfilled") {
    const value = risk.value;
    S.risk = Array.isArray(value) ? value : (value?.groups || []);
  }
}

function toArray(value) {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.data)) return value.data;
  if (Array.isArray(value?.positions)) return value.positions;
  if (Array.isArray(value?.orders)) return value.orders;
  if (Array.isArray(value?.trades)) return value.trades;
  return [];
}

let liveTimer = null;
function startLivePolling() {
  if (liveTimer) return;
  liveTimer = window.setInterval(() => {
    refreshBooksFromAPI(false).catch((error) => console.warn("account refresh failed", error));
  }, 15000);
}

async function pollLive() {
  if (S.polling) return;
  S.polling = true;
  try {
    applyIndexQuotes(await getJSON("/api/indices"));
    await refreshBooksFromAPI(true);
    if (S.activeIndex && S.expiry) {
      const chainRaw = await getJSON(`/api/chain?index=${encodeURIComponent(S.activeIndex)}&expiry=${encodeURIComponent(S.expiry)}`);
      S.chain = normalizeChain(chainRaw);
      selectStrike(S.selected.strike, S.selected.type, false, false);
    }
    renderChain(true);
    renderAll(true);
    loadCharts();
  } catch (error) {
    console.warn("live poll failed", error);
    S.feed = "degraded";
    renderFeed();
  } finally {
    S.polling = false;
  }
}

function connectWebSocket() {
  if (S.ws) return;
  const proto = location.protocol === "https:" ? "wss" : "ws";
  try {
    const ws = new WebSocket(`${proto}://${location.host}/ws`);
    S.ws = ws;
    ws.addEventListener("open", () => {
      updateFeedSubscriptions();
    });
    ws.addEventListener("message", (event) => {
      try {
        handleSocketEvent(JSON.parse(event.data));
      } catch (error) {
        console.warn("bad ws event", error);
      }
    });
    ws.addEventListener("close", () => {
      S.ws = null;
      S.feedSubscriptions = [];
      S.feed = "degraded";
      renderFeed();
      if (S.session?.authenticated && !S.wsTimer) {
        S.wsTimer = window.setTimeout(() => {
          S.wsTimer = null;
          connectWebSocket();
        }, 5000);
      }
    });
    ws.addEventListener("error", () => {
      S.feed = "degraded";
      renderFeed();
    });
  } catch (error) {
    console.warn("ws connect failed", error);
    S.feed = "degraded";
  }
}

function handleSocketEvent(event) {
  if (!event || !event.type) return;
  const data = event.data;
  if (event.type === "feed_status") {
    S.feed = data?.status || "polling";
    renderFeed();
    if (S.marketOpen) {
      for (const type of ["CE", "PE"]) {
        const option = selectedOption(type);
        if (!option) continue;
        const key = `${option.instrumentKey}|${S.timeframe}`;
        const hasCandles = Boolean(S.candles[key]?.length);
        setChartHint(type === "CE" ? "ce" : "pe", hasCandles ? ""
          : S.feed === "live" ? "Waiting for live ticks…" : "Waiting for authenticated live feed…");
      }
    }
    return;
  }
  if (event.type === "tick") {
    updateLiveCandle(data.key, data.ltp, data.timestamp);
    for (const spot of S.spots.values()) {
      if (spot.spotKey !== data.key) continue;
      spot.ltp = num(data.ltp, spot.ltp);
      spot.prevClose = num(data.prev_close, spot.prevClose);
      spot.change = num(data.change, spot.ltp - spot.prevClose);
      spot.changePct = num(data.change_pct,
        spot.prevClose ? ((spot.ltp - spot.prevClose) / spot.prevClose) * 100 : 0);
      renderHeader();
      renderPaneHeaders();
      renderOrderPad();
      return;
    }
    let updatedPosition = false;
    for (const position of S.positions) {
      if (position.instrument_key !== data.key) continue;
      position.ltp = num(data.ltp, position.ltp);
      position.pnl = (position.ltp - position.average_price) * position.quantity;
      updatedPosition = true;
    }
    if (updatedPosition) {
      renderHeader();
      if (S.tab === "positions") renderTab();
    }
    const row = S.chain.find((r) => r.call?.instrumentKey === data.key || r.put?.instrumentKey === data.key);
    if (row) {
      const opt = row.call?.instrumentKey === data.key ? row.call : row.put;
      opt.ltp = num(data.ltp, opt.ltp);
      opt.prevClose = num(data.prev_close, opt.prevClose);
      opt.bid = num(data.bid, opt.bid);
      opt.ask = num(data.ask, opt.ask);
      opt.volume = num(data.volume, opt.volume);
      opt.oi = num(data.oi, opt.oi);
      opt.quoteAvailable = opt.ltp > 0;
      opt.change = opt.ltp - opt.prevClose;
      opt.changePct = opt.prevClose ? ((opt.ltp - opt.prevClose) / opt.prevClose) * 100 : 0;
      const type = row.call?.instrumentKey === data.key ? "CE" : "PE";
      const cell = [...$("chain-body").querySelectorAll(".chain-row")]
        .find((item) => item.dataset.strike === String(row.strike))
        ?.querySelector(`[data-select="${type}"]`);
      if (cell) {
        cell.textContent = fmt(opt.ltp);
        cell.title = `${fmt(opt.ltp)} · ${fmtSigned(opt.changePct)}%`;
        cell.classList.toggle("up", opt.changePct > 0);
        cell.classList.toggle("down", opt.changePct < 0);
      }
      if (selectedOption(type)?.instrumentKey === data.key) {
        renderPaneHeaders();
        renderOrderPad();
      }
    }
    return;
  }
  if (event.type === "chain") {
    S.chain = normalizeChain(data);
    renderChain(true);
    return;
  }
  if (event.type === "position") {
    S.positions = toArray(data).map(normalizePosition);
    updateFeedSubscriptions();
    renderTab();
    return;
  }
  if (event.type === "margin") {
    S.limits.available = num(data?.available ?? data?.available_margin ?? data?.availableMargin);
    renderHeader();
    renderOrderPad();
    return;
  }
  if (event.type === "session") {
    checkSession();
  }
}

function updateFeedSubscriptions() {
  const keys = [selectedOption("CE")?.instrumentKey, selectedOption("PE")?.instrumentKey,
    ...S.positions.map((position) => position.instrument_key)]
    .filter(Boolean);
  const desired = [...new Set(keys)];
  if (S.ws?.readyState !== WebSocket.OPEN) return;
  const remove = S.feedSubscriptions.filter((key) => !desired.includes(key));
  const add = desired.filter((key) => !S.feedSubscriptions.includes(key));
  if (remove.length) S.ws.send(JSON.stringify({ type: "unsubscribe", keys: remove }));
  if (add.length) S.ws.send(JSON.stringify({ type: "subscribe", keys: add }));
  S.feedSubscriptions = desired;
}

/* ── Chain selection ───────────────────────────────────── */

function computeATM() {
  const spot = S.spots.get(S.activeIndex);
  if (!spot?.ltp || !S.chain.length) return null;
  let best = S.chain[0];
  let bestDiff = Infinity;
  for (const row of S.chain) {
    const diff = Math.abs(row.strike - spot.ltp);
    if (diff < bestDiff) {
      bestDiff = diff;
      best = row;
    }
  }
  return best.strike;
}

function selectDefaultStrike() {
  if (!S.chain.length) {
    S.selected = { strike: null, type: "CE", call: null, put: null };
    return;
  }
  const atm = computeATM() ?? S.chain[Math.floor(S.chain.length / 2)].strike;
  selectStrike(atm, "CE", false, false);
  S.chainScrolled = false;
}

function selectStrike(strike, type, rerender = true, reloadCharts = true) {
  const row = S.chain.find((r) => r.strike === strike);
  if (!row) return;
  S.selected = { strike, type, call: row.call, put: row.put };
  S.chainScrolled = true;
  updateFeedSubscriptions();
  if (reloadCharts) loadCharts();
  renderPaneHeaders();
  if (rerender) {
    renderChain(true);
    renderOrderPad();
    if (S.tab === "depth") renderTab();
  }
}

function selectedOption(type) {
  return type === "CE" ? S.selected.call : S.selected.put;
}

function lotSize(type = S.selected.type) {
  const option = selectedOption(type) || selectedOption(type === "CE" ? "PE" : "CE");
  return option?.lotSize || 0;
}

/* ── Charts ────────────────────────────────────────────── */

function chartInstrument(type) {
  return selectedOption(type)?.instrumentKey || "";
}

async function loadCharts() {
  const loadID = ++S.chartLoadID;
  for (const [pane, type] of [["ce", "CE"], ["pe", "PE"]]) {
    const key = chartInstrument(type);
    const chart = S.charts[pane];
    chart.resetView();
    if (!key) {
      chart.setData([]);
      setChartHint(pane, "Choose a strike to show chart data.");
      continue;
    }
    chart.setData([]);
    if (S.marketOpen) {
      const live = S.candles[`${key}|${S.timeframe}`] || [];
      chart.setData(live);
      setChartHint(pane, live.length ? "" : S.feed === "live"
        ? "Waiting for live ticks…"
        : "Waiting for authenticated live feed…");
      continue;
    }
    try {
      const cacheKey = `${key}|${S.timeframe}`;
      let candles = S.historicalCache.get(cacheKey);
      if (!candles) {
        setChartHint(pane, "Loading historical candles…");
        const request = getJSON(`/api/candles?instrument=${encodeURIComponent(key)}&tf=${encodeURIComponent(S.timeframe)}`)
          .then((raw) => (Array.isArray(raw) ? raw : (raw?.candles || [])).map((c) => ({
            time: num(c.time ?? c.ts),
            open: num(c.open),
            high: num(c.high),
            low: num(c.low),
            close: num(c.close),
            volume: num(c.volume),
          })));
        S.historicalCache.set(cacheKey, request);
        try {
          candles = await request;
          S.historicalCache.set(cacheKey, candles);
        } catch (error) {
          S.historicalCache.delete(cacheKey);
          throw error;
        }
      }
      if (loadID !== S.chartLoadID || chartInstrument(type) !== key || S.marketOpen) return;
      chart.setData(candles);
      setChartHint(pane, candles.length ? "" : "No historical candles returned for this contract.");
    } catch (error) {
      if (loadID !== S.chartLoadID || chartInstrument(type) !== key) return;
      console.warn("historical candles unavailable", { pane, error });
      chart.setData([]);
      setChartHint(pane, `Historical data unavailable: ${error.message}`);
    }
  }
  renderPaneHeaders();
}

function setChartHint(pane, message) {
  const hint = $("hint-" + pane);
  hint.textContent = message;
  hint.hidden = !message;
}

function updateLiveCandle(key, price, timestamp) {
  if (!S.marketOpen || S.feed !== "live" || !Number.isFinite(Number(price)) || Number(price) <= 0) return;
  const timeMs = num(timestamp) > 1e12 ? num(timestamp) : num(timestamp) * 1000;
  const time = Math.floor((timeMs > 0 ? timeMs : Date.now()) / 1000);
  const interval = ({ "1m": 60, "3m": 180, "5m": 300, "15m": 900, "1h": 3600 })[S.timeframe];
  if (!interval) return;
  const openMatch = /^(\d{2}):(\d{2})$/.exec(S.boot.marketSession?.open || "09:15");
  const openSeconds = openMatch ? (Number(openMatch[1]) * 60 + Number(openMatch[2])) * 60 : 9 * 3600 + 15 * 60;
  const localDayStart = Math.floor((time + 19800) / 86400) * 86400 - 19800;
  const sessionStart = localDayStart + openSeconds;
  const bucket = sessionStart + Math.floor((time - sessionStart) / interval) * interval;
  const value = Number(price);
  const cacheKey = `${key}|${S.timeframe}`;
  const candles = S.candles[cacheKey] || (S.candles[cacheKey] = []);
  let candle = candles[candles.length - 1];
  if (!candle || candle.time !== bucket) {
    candle = { time: bucket, open: value, high: value, low: value, close: value, volume: 0 };
    candles.push(candle);
    if (candles.length > 500) candles.shift();
  } else {
    candle.high = Math.max(candle.high, value);
    candle.low = Math.min(candle.low, value);
    candle.close = value;
  }
  for (const type of ["CE", "PE"]) {
    if (chartInstrument(type) !== key) continue;
    const pane = type === "CE" ? "ce" : "pe";
    S.charts[pane].setData(candles);
    setChartHint(pane, "");
  }
}

function renderPaneHeaders() {
  const expiryLabel = expiryText(S.expiry);
  const ce = selectedOption("CE");
  const pe = selectedOption("PE");

  $("ce-title").textContent = ce ? `${S.activeIndex} ${fmtStrike(ce.strike)} CE` : "CALL";
  $("ce-sub").textContent = ce ? `${expiryLabel} · ${S.timeframe}` : "select a strike";
  $("ce-ltp").textContent = ce ? fmt(ce.ltp) : "—";
  $("ce-change").textContent = ce ? `${fmtSigned(ce.change)} (${fmtSigned(ce.changePct)}%)` : "—";
  $("ce-change").className = "pane-change " + directionClass(ce?.changePct);

  $("pe-title").textContent = pe ? `${S.activeIndex} ${fmtStrike(pe.strike)} PE` : "PUT";
  $("pe-sub").textContent = pe ? `${expiryLabel} · ${S.timeframe}` : "select a strike";
  $("pe-ltp").textContent = pe ? fmt(pe.ltp) : "—";
  $("pe-change").textContent = pe ? `${fmtSigned(pe.change)} (${fmtSigned(pe.changePct)}%)` : "—";
  $("pe-change").className = "pane-change " + directionClass(pe?.changePct);

  $("leg-call-name").textContent = ce ? `${S.activeIndex} ${fmtStrike(ce.strike)} CE` : "CALL";
  $("leg-call-size").textContent = ce ? `${ce.lotSize} shares / lot · LTP ${fmt(ce.ltp)}` : "Choose a strike";
  $("leg-put-name").textContent = pe ? `${S.activeIndex} ${fmtStrike(pe.strike)} PE` : "PUT";
  $("leg-put-size").textContent = pe ? `${pe.lotSize} shares / lot · LTP ${fmt(pe.ltp)}` : "Choose a strike";
}

function expiryText(date) {
  if (!date) return "—";
  const expiry = S.expiries.find((e) => e.date === date);
  return expiry?.label || date;
}

/* ── Renderers ─────────────────────────────────────────── */

function renderAll(preserveChainScroll = false) {
  renderHeader();
  renderToolbar();
  renderChain(preserveChainScroll);
  renderOrderPad();
  renderTab();
  renderFooter();
}

function renderHeader() {
  const order = (S.boot.indices || []).map((i) => i.key);
  if (S.spots.has("INDIA VIX")) order.push("INDIA VIX");
  $("spot-strip").innerHTML = order.map((key) => {
    const spot = S.spots.get(key);
    if (!spot) {
      return `<span class="spot-chip${key === S.activeIndex ? " active" : ""}" data-index="${esc(key)}">
        <span class="spot-name">${esc(key)}</span><span class="spot-price">—</span><span class="spot-change">—</span></span>`;
    }
    const cls = directionClass(spot.changePct ?? spot.change);
    return `<span class="spot-chip${key === S.activeIndex ? " active" : ""}" data-index="${esc(key)}">
      <span class="spot-name">${esc(spot.displayName || key)}</span>
      <span class="spot-price">${fmt(spot.ltp)}</span>
      <span class="spot-change ${cls}">${fmtSigned(spot.change)} · ${fmtSigned(spot.changePct)}%</span>
    </span>`;
  }).join("");

  const available = S.limits?.available || 0;
  $("margin-chip").textContent = available ? `₹${fmt(available, 0)}` : "—";
  const totalPnl = (S.positions || []).reduce((sum, p) => sum + num(p.pnl), 0);
  const pnlChip = $("pnl-chip");
  pnlChip.textContent = totalPnl ? `₹${fmtSigned(totalPnl, 0)}` : "—";
  pnlChip.className = "metric-value " + directionClass(totalPnl);

  const mode = tradingMode().toUpperCase();
  $("mode-badge").textContent = mode;
  $("mode-badge").className = "badge " + (mode === "LIVE" ? "live" : "paper");
  renderFeed();
}

function renderFeed() {
  const badge = $("feed-badge");
  const label = $("feed-label");
  const feed = S.feed;
  const cls = feed === "live" ? "live" : feed === "polling" || feed === "degraded" ? "degraded" : "connecting";
  badge.className = `feed-badge ${cls}`;
  label.textContent = feed;
  $("footer-feed").textContent = `${feed} feed`;
}

function renderToolbar() {
  $("index-select").value = S.activeIndex;
  $("expiry-select").innerHTML = expiryOptionsHTML();
  $("expiry-select").value = S.expiry;
  for (const button of $$("#tf-group button")) {
    button.classList.toggle("active", button.dataset.tf === S.timeframe);
  }
  $("atm-value").textContent = fmtStrike(computeATM() ?? 0);
}

function expiryOptionsHTML() {
  if (!S.expiries.length) return `<option value="">no expiries</option>`;
  const weekly = S.expiries.filter((e) => !e.monthly && !e.expired);
  const monthly = S.expiries.filter((e) => e.monthly && !e.expired);
  let html = "";
  if (weekly.length) {
    html += `<optgroup label="Weekly">${weekly.map((e) => `<option value="${esc(e.date)}">${esc(e.label)} · W</option>`).join("")}</optgroup>`;
  }
  if (monthly.length) {
    html += `<optgroup label="Monthly">${monthly.map((e) => `<option value="${esc(e.date)}">${esc(e.label)} · M</option>`).join("")}</optgroup>`;
  }
  if (!html) {
    html = S.expiries.map((e) => `<option value="${esc(e.date)}">${esc(e.label)}</option>`).join("");
  }
  return html;
}

function renderChain(preserveScroll = false) {
  const body = $("chain-body");
  if (!S.chain.length) {
    body.innerHTML = `<div class="empty-state">Waiting for the option chain…</div>`;
    $("strike-meta").textContent = "—";
    return;
  }
  const scrollTop = preserveScroll ? body.scrollTop : 0;
  const atm = computeATM();
  body.innerHTML = S.chain.map((row) => {
    const call = row.call;
    const put = row.put;
    const active = row.strike === S.selected.strike;
    return `<div class="chain-row${row.strike === atm ? " atm" : ""}${active ? " active" : ""}" data-strike="${row.strike}" role="row">
      <button type="button" class="cell-ce${active && S.selected.type === "CE" ? " selected" : ""}${call ? ` ${directionClass(call.changePct)}` : ""}" data-select="CE" data-strike="${row.strike}" title="${call ? `${fmt(call.ltp)} · ${fmtSigned(call.changePct)}%` : "Call quote unavailable"}">${call ? fmt(call.ltp) : "—"}</button>
      <button type="button" class="cell-strike" data-select="${esc(S.selected.type)}" data-strike="${row.strike}">${row.strike === atm ? '<span class="atm-dot">◆</span>' : ""}${fmtStrike(row.strike)}</button>
      <button type="button" class="cell-pe${active && S.selected.type === "PE" ? " selected" : ""}${put ? ` ${directionClass(put.changePct)}` : ""}" data-select="PE" data-strike="${row.strike}" title="${put ? `${fmt(put.ltp)} · ${fmtSigned(put.changePct)}%` : "Put quote unavailable"}">${put ? fmt(put.ltp) : "—"}</button>
    </div>`;
  }).join("");
  $("strike-meta").textContent = `${S.chain.length} strikes · ${expiryText(S.expiry)}`;

  body.scrollTop = scrollTop;
  applyStrikeFilter();
  if (!S.chainScrolled) {
    scrollATMIntoView();
    S.chainScrolled = true;
  }
}

function applyStrikeFilter() {
  const query = $("strike-search-input").value.trim().replace(/,/g, "");
  const rows = $$("#chain-body .chain-row");
  let visible = 0;
  for (const row of rows) {
    const matches = !query || row.dataset.strike.includes(query);
    row.hidden = !matches;
    if (matches) visible++;
  }
  $("strike-meta").textContent = query
    ? `${visible} matching strike${visible === 1 ? "" : "s"} · ${S.chain.length} available`
    : `${S.chain.length} strikes · ${expiryText(S.expiry)}`;
}

function openStrikeModal() {
  $("strike-search-input").value = "";
  applyStrikeFilter();
  $("strike-modal").hidden = false;
  $("strike-search-input").focus();
  requestAnimationFrame(scrollATMIntoView);
}

function closeStrikeModal() {
  $("strike-modal").hidden = true;
}

function scrollATMIntoView() {
  const body = $("chain-body");
  const atm = computeATM();
  if (!atm) return;
  const selected = S.selected.strike || atm;
  const target = [...body.querySelectorAll(".chain-row")].find((row) => num(row.dataset.strike) === selected);
  if (target) {
    const bodyRect = body.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    body.scrollTop += targetRect.top - bodyRect.top - (body.clientHeight - targetRect.height) / 2;
  }
}

function renderOrderPad() {
  const ce = selectedOption("CE");
  const pe = selectedOption("PE");
  const available = num(S.limits?.available);
  for (const [type, option] of [["CE", ce], ["PE", pe]]) {
    const lot = lotSize(type);
    const shares = Math.max(0, Math.round(num(S.qty[type])) * lot);
    const prefix = type === "CE" ? "call" : "put";
    const input = $(`${prefix}-lots`);
    input.value = String(S.qty[type]);
    input.disabled = !option;
    $$(".leg-orderpad .trade").filter((button) => button.dataset.type === type).forEach((button) => {
      const required = requiredMargin(option, shares, button.dataset.action === "buy" ? "B" : "S");
      button.disabled = !option || !option.quoteAvailable || option.ltp <= 0 ||
        !shares || !Number.isFinite(required) || required > available;
    });
  }
}

function resolvedShares(lot, type = S.selected.type) {
  return Math.max(0, Math.round(num(S.qty[type])) * lot);
}

function requiredMargin(option, shares, side) {
  if (!option) return 0;
  if (side === "B") return option.ltp * shares;
  const spot = S.spots.get(option.underlying)?.ltp || 0;
  if (spot <= 0) return Infinity;
  return spot * shares * (num(S.boot.shortMarginPct, 15) / 100) + option.ltp * shares;
}

/* ── Payoff math ───────────────────────────────────────── */

function intrinsic(type, strike, spot) {
  return type === "CE" ? Math.max(0, spot - strike) : Math.max(0, strike - spot);
}

function computePayoff(legs) {
  const clean = legs.filter((leg) => leg.qty !== 0 && Number.isFinite(leg.strike) && Number.isFinite(leg.avg));
  if (!clean.length) {
    return { points: [], maxProfit: 0, maxLoss: 0, breakevens: [], unlimitedProfit: false, unlimitedLoss: false };
  }
  const strikes = [...new Set(clean.map((leg) => leg.strike))].sort((a, b) => a - b);
  const maxStrike = strikes[strikes.length - 1];
  const map = [0, ...strikes, maxStrike * 2];
  const points = map.map((spot) => ({
    spot,
    pnl: clean.reduce((sum, leg) => sum + leg.qty * (intrinsic(leg.type, leg.strike, spot) - leg.avg), 0),
  }));

  const netCalls = clean.filter((leg) => leg.type === "CE").reduce((sum, leg) => sum + leg.qty, 0);
  const unlimitedProfit = netCalls > 0;
  const unlimitedLoss = netCalls < 0;

  let maxProfit = -Infinity;
  let maxLoss = Infinity;
  for (const point of points) {
    maxProfit = Math.max(maxProfit, point.pnl);
    maxLoss = Math.min(maxLoss, point.pnl);
  }
  if (unlimitedProfit) maxProfit = Infinity;
  if (unlimitedLoss) maxLoss = -Infinity;

  const breakevens = [];
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1];
    const b = points[i];
    if (a.pnl === 0) {
      if (!breakevens.includes(a.spot)) breakevens.push(a.spot);
    }
    if ((a.pnl < 0 && b.pnl > 0) || (a.pnl > 0 && b.pnl < 0)) {
      const ratio = -a.pnl / (b.pnl - a.pnl);
      const be = a.spot + ratio * (b.spot - a.spot);
      if (!breakevens.some((v) => Math.abs(v - be) < 0.001)) breakevens.push(be);
    }
  }
  return { points, maxProfit, maxLoss, breakevens, unlimitedProfit, unlimitedLoss };
}

function buildRiskGroups(positions) {
  const groups = new Map();
  for (const position of positions) {
    if (!position.underlying || !position.expiry) continue;
    const key = `${position.underlying}|${position.expiry}`;
    if (!groups.has(key)) {
      groups.set(key, { underlying: position.underlying, expiry: position.expiry, legs: [] });
    }
    groups.get(key).legs.push({
      type: position.option_type,
      strike: position.strike,
      qty: num(position.quantity),
      avg: num(position.average_price),
    });
  }
  return [...groups.values()].map((group) => {
    const payoff = computePayoff(group.legs);
    return { ...group, ...payoff, netPremium: group.legs.reduce((sum, leg) => sum - leg.qty * leg.avg, 0) };
  });
}

/* ── Bottom tabs ──────────────────────────────────────── */

function renderTab() {
  const content = $("tab-content");
  for (const button of $$("#tabs button")) {
    button.classList.toggle("active", button.dataset.tab === S.tab);
  }
  $("paper-note").textContent = "P&L is gross of charges";

  if (S.tab === "positions") content.innerHTML = positionsHTML();
  else if (S.tab === "risk") {
    content.innerHTML = riskHTML();
    requestAnimationFrame(drawRiskCharts);
  } else if (S.tab === "orders") content.innerHTML = ordersHTML();
  else if (S.tab === "trades") content.innerHTML = tradesHTML();
  else if (S.tab === "depth") content.innerHTML = depthHTML();
}

function positionsHTML() {
  if (!S.positions.length) return `<div class="empty-state">No open positions. Use the order pad to place a paper trade.</div>`;
  const groups = new Map();
  for (const p of S.positions) {
    const key = `${p.underlying || S.activeIndex}|${p.expiry || S.expiry}`;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(p);
  }
  let html = `<table class="book"><thead><tr>
    <th>Contract</th><th>Net</th><th>Avg</th><th>LTP</th><th>P&amp;L</th>
    <th>Max P</th><th>Max L</th><th></th>
  </tr></thead><tbody>`;
  for (const [key, list] of groups) {
    const payoff = computePayoff(list.map((p) => ({
      type: p.option_type,
      strike: p.strike,
      qty: num(p.quantity),
      avg: num(p.average_price),
    })));
    html += `<tr class="group-row"><td colspan="8">${esc(key.replace("|", " · "))} — group Max P ${payoff.maxProfit === Infinity ? "∞" : `₹${fmt(payoff.maxProfit, 0)}`} · Max L ${payoff.maxLoss === -Infinity ? "∞" : `₹${fmt(payoff.maxLoss, 0)}`}</td></tr>`;
    for (const p of list) {
      const risk = legRisk(p);
      html += `<tr>
        <td class="sym">${esc(p.symbol || `${p.underlying} ${fmtStrike(p.strike)} ${p.option_type}`)}</td>
        <td>${p.quantity > 0 ? "+" : ""}${p.quantity}</td>
        <td>${fmt(p.average_price)}</td>
        <td>${fmt(p.ltp)}</td>
        <td class="${directionClass(p.pnl)}">${fmtSigned(p.pnl, 0)}</td>
        <td>${risk.maxProfit === Infinity ? "∞" : `₹${fmt(risk.maxProfit, 0)}`}</td>
        <td>${risk.maxLoss === -Infinity ? "∞" : `₹${fmt(risk.maxLoss, 0)}`}</td>
        <td><button type="button" class="link-button" data-exit="${esc(p.instrument_key)}">EXIT</button></td>
      </tr>`;
    }
  }
  return html + `</tbody></table>`;
}

function legRisk(position) {
  const qty = Math.abs(num(position.quantity));
  const avg = num(position.average_price);
  const strike = num(position.strike);
  const type = position.option_type;
  const long = num(position.quantity) > 0;
  if (type === "CE") {
    return long
      ? { maxProfit: Infinity, maxLoss: avg * qty }
      : { maxProfit: avg * qty, maxLoss: -Infinity };
  }
  return long
    ? { maxProfit: (strike - avg) * qty, maxLoss: avg * qty }
    : { maxProfit: avg * qty, maxLoss: (strike - avg) * qty };
}

function riskHTML() {
  const groups = S.risk?.length ? S.risk : buildRiskGroups(S.positions);
  if (!groups.length) {
    return `<div class="empty-state">No open legs. The at-expiry payoff appears here once a position is open.</div>`;
  }
  return `<div class="risk-cards">${groups.map((group, index) => {
    const maxP = group.maxProfit === Infinity ? "∞" : `₹${fmt(num(group.maxProfit), 0)}`;
    const maxL = group.maxLoss === -Infinity ? "∞" : `₹${fmt(num(group.maxLoss), 0)}`;
    const be = (group.breakevens || []).map((b) => fmt(num(b), 2)).join(", ") || "—";
    const flag = group.unlimitedLoss
      ? `<span class="risk-flag bad">unlimited loss</span>`
      : group.unlimitedProfit
        ? `<span class="risk-flag good">unlimited profit</span>`
        : "";
    return `<article class="risk-card">
      <div class="risk-card-head">
        <div>
          <div class="risk-card-title">${esc(group.underlying || "—")} · ${esc(expiryText(group.expiry) || "—")}</div>
          <div class="risk-card-sub">at expiry · theoretical · gross of charges</div>
        </div>
        ${flag}
      </div>
      <div class="risk-metrics">
        <div class="risk-metric"><span>Net premium</span><b>₹${fmt(num(group.netPremium), 0)}</b></div>
        <div class="risk-metric"><span>Max P</span><b class="up">${maxP}</b></div>
        <div class="risk-metric"><span>Max L</span><b class="down">${maxL}</b></div>
        <div class="risk-metric"><span>Break-even</span><b>${be}</b></div>
      </div>
      <canvas id="risk-canvas-${index}" data-risk-index="${index}"></canvas>
    </article>`;
  }).join("")}</div>`;
}

function drawRiskCharts() {
  const groups = S.risk?.length ? S.risk : buildRiskGroups(S.positions);
  groups.forEach((group, index) => {
    const canvas = $(`risk-canvas-${index}`);
    if (!canvas) return;
    drawPayoff(canvas, group.points || [], S.spots.get(group.underlying)?.ltp || 0);
  });
}

function drawPayoff(canvas, points, spot) {
  const dpr = window.devicePixelRatio || 1;
  const width = canvas.clientWidth || 300;
  const height = canvas.clientHeight || 112;
  canvas.width = Math.round(width * dpr);
  canvas.height = Math.round(height * dpr);
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, width, height);

  if (!points.length) return;
  const xs = points.map((p) => p.spot);
  const ys = points.map((p) => p.pnl);
  const minX = Math.min(...xs);
  const maxX = Math.max(...xs);
  const minY = Math.min(0, ...ys);
  const maxY = Math.max(0, ...ys);
  const pad = 6;
  const xOf = (x) => pad + ((x - minX) / Math.max(1, maxX - minX)) * (width - pad * 2);
  const yOf = (y) => pad + ((maxY - y) / Math.max(1, maxY - minY)) * (height - pad * 2);

  ctx.strokeStyle = "#1a232c";
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(pad, yOf(0));
  ctx.lineTo(width - pad, yOf(0));
  ctx.stroke();

  ctx.beginPath();
  points.forEach((point, index) => {
    const x = xOf(point.spot);
    const y = yOf(point.pnl);
    if (index === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  });
  ctx.strokeStyle = "#f0b429";
  ctx.lineWidth = 1.5;
  ctx.stroke();

  ctx.lineTo(xOf(points[points.length - 1].spot), yOf(0));
  ctx.lineTo(xOf(points[0].spot), yOf(0));
  ctx.closePath();
  ctx.fillStyle = "#f0b42912";
  ctx.fill();

  if (spot) {
    const x = xOf(spot);
    ctx.strokeStyle = "#58a6ff";
    ctx.setLineDash([2, 3]);
    ctx.beginPath();
    ctx.moveTo(x, 2);
    ctx.lineTo(x, height - 2);
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = "#58a6ff";
    ctx.font = "8px ui-monospace, Menlo, monospace";
    ctx.fillText("spot", Math.min(width - 24, x + 3), 9);
  }
}

function ordersHTML() {
  if (!S.orders.length) return `<div class="empty-state">No orders yet.</div>`;
  return `<table class="book"><thead><tr>
    <th>Order</th><th>Contract</th><th>Side</th><th>Qty</th><th>Price</th><th>Status</th><th>Time</th>
  </tr></thead><tbody>${S.orders.map((o) => `<tr>
    <td class="sym">${esc(o.id)}</td>
    <td class="sym">${esc(o.symbol)}</td>
    <td class="${o.side === "B" ? "up" : "down"}">${o.side === "B" ? "BUY" : o.side === "S" ? "SELL" : esc(o.side)}</td>
    <td>${o.quantity}</td>
    <td>${o.price ? fmt(o.price) : "MKT"}</td>
    <td>${o.status}${o.reason ? `<span class="muted"> · ${esc(o.reason)}</span>` : ""}</td>
    <td>${fmtTime(o.timestamp)}</td>
  </tr>`).join("")}</tbody></table>`;
}

function tradesHTML() {
  if (!S.trades.length) return `<div class="empty-state">No trades yet.</div>`;
  return `<table class="book"><thead><tr>
    <th>Trade</th><th>Contract</th><th>Side</th><th>Qty</th><th>Price</th><th>Value</th><th>Time</th>
  </tr></thead><tbody>${S.trades.map((t) => `<tr>
    <td class="sym">${esc(t.id)}</td>
    <td class="sym">${esc(t.symbol)}</td>
    <td class="${t.side === "B" ? "up" : "down"}">${t.side === "B" ? "BUY" : t.side === "S" ? "SELL" : esc(t.side)}</td>
    <td>${t.quantity}</td>
    <td>${fmt(t.price)}</td>
    <td>₹${fmt(t.price * t.quantity, 0)}</td>
    <td>${fmtTime(t.timestamp)}</td>
  </tr>`).join("")}</tbody></table>`;
}

function depthHTML() {
  const option = selectedOption(S.selected.type);
  if (!option) return `<div class="empty-state">Select a CE or PE strike to see depth.</div>`;
  const levels = S.depth || [];
  const maxQty = Math.max(1, ...levels.flatMap((l) => [l.bidQty || 0, l.askQty || 0]));
  const barWidth = (qty) => Math.max(2, Math.min(100, Math.round(((qty || 0) / maxQty) * 100)));
  const bidRows = levels.map((level, index) => `<tr>
    <td>${index + 1}</td>
    <td>${fmt(level.bid)}</td>
    <td>${fmtCompact(level.bidQty)}</td>
    <td class="depth-bar"><svg viewBox="0 0 100 8" preserveAspectRatio="none" aria-hidden="true"><rect x="0" y="0" width="${barWidth(level.bidQty)}" height="8" rx="3"></rect></svg></td>
  </tr>`).join("");
  const askRows = levels.map((level, index) => `<tr>
    <td>${index + 1}</td>
    <td>${fmt(level.ask)}</td>
    <td>${fmtCompact(level.askQty)}</td>
    <td class="depth-bar"><svg viewBox="0 0 100 8" preserveAspectRatio="none" aria-hidden="true"><rect x="0" y="0" width="${barWidth(level.askQty)}" height="8" rx="3"></rect></svg></td>
  </tr>`).join("");
  return `<div class="depth-grid">
    <div class="depth-side bid">
      <h3>Bids · ${esc(option.symbol || "selected contract")}</h3>
      <table><tbody>${bidRows || `<tr><td colspan="4">no bids</td></tr>`}</tbody></table>
    </div>
    <div class="depth-side ask">
      <h3>Asks · ${esc(option.symbol || "selected contract")}</h3>
      <table><tbody>${askRows || `<tr><td colspan="4">no asks</td></tr>`}</tbody></table>
    </div>
  </div>`;
}

function renderFooter() {
  $("footer-version").textContent = `v${S.boot.version || "dev"}`;
  $("footer-feed").textContent = `${S.feed} feed`;
}

/* ── Trading actions ──────────────────────────────────── */

function validateQuantity(type) {
  if (!Number.isInteger(num(S.qty[type])) || num(S.qty[type]) <= 0) {
    return `Enter a positive ${type} lot quantity.`;
  }
  return "";
}

async function onTrade(action, type) {
  const option = selectedOption(type);
  if (!option) {
    toast(`Select a ${type} strike first.`, "warn");
    return;
  }
  if (!option.quoteAvailable || option.ltp <= 0) {
    toast("No live quote is available for this contract; order was not submitted.", "error", 6000);
    return;
  }
  const lot = lotSize(type);
  const error = validateQuantity(type);
  if (error) {
    toast(error, "error");
    return;
  }
  const shares = resolvedShares(lot, type);
  const side = action === "buy" ? "B" : "S";
  const required = requiredMargin(option, shares, side);
  const available = num(S.limits?.available);
  if (!Number.isFinite(required)) {
    toast("A live underlying quote is required to calculate short-order margin.", "error", 6000);
    return;
  }
  if (required > available) {
    toast(`Insufficient margin: required ₹${fmt(required, 0)}, available ₹${fmt(available, 0)}.`, "error", 5000);
    return;
  }

  const payload = {
    instrument_key: option.instrumentKey,
    exchange_segment: option.exchangeSegment,
    symbol: option.symbol,
    underlying: S.activeIndex,
    expiry: S.expiry,
    strike: option.strike,
    option_type: option.type,
    side,
    buy_sell: side,
    quantity: shares,
    qty: shares,
    price: 0,
    order_type: "MARKET",
    product: S.product,
    intent: "ENTRY",
    mode: tradingMode(),
    lot_size: lot,
  };

  try {
    const result = await postOrder(payload);
    toast(`Paper order ${result?.status?.toLowerCase() || "submitted"} at live quote ₹${fmt(result?.price ?? option.ltp)}.`, "ok");
    window.setTimeout(() => refreshBooksFromAPI(true), 400);
  } catch (err) {
    toast(`Order rejected: ${err.message}`, "error", 6000);
  }
}

async function onExit(instrumentKey) {
  const position = S.positions.find((p) => p.instrument_key === instrumentKey);
  if (!position) return;
  const side = position.quantity > 0 ? "S" : "B";
  const qty = Math.abs(position.quantity);
  const ok = await confirmAction({
    title: "Exit position",
    body: `${side === "B" ? "Buy" : "Sell"} <b>${qty}</b> shares of <b>${esc(position.symbol)}</b> at market to close the position?`,
    okLabel: "Exit",
    danger: true,
  });
  if (!ok) return;

  try {
    await postOrder({
      instrument_key: instrumentKey,
      symbol: position.symbol,
      underlying: position.underlying,
      expiry: position.expiry,
      strike: position.strike,
      option_type: position.option_type,
      side,
      buy_sell: side,
      quantity: qty,
      qty,
      price: 0,
      order_type: "MARKET",
      product: position.product || S.product,
      intent: "EXIT",
      mode: tradingMode(),
    });
    toast("Paper exit filled at live quote.", "ok");
    window.setTimeout(() => refreshBooksFromAPI(true), 400);
  } catch (err) {
    toast(`Exit failed: ${err.message}`, "error", 6000);
  }
}

async function onSquareOffAll() {
  if (!S.positions.length) {
    toast("No open positions to square off.", "warn");
    return;
  }
  const ok = await confirmAction({
    title: "Square off all",
    body: `Close <b>${S.positions.length}</b> open position${S.positions.length === 1 ? "" : "s"} at market? ` +
      `<span class="warn">This cannot be undone.</span>`,
    okLabel: "Square Off All",
    danger: true,
  });
  if (!ok) return;

  try {
    await postJSON("/api/orders/square-off-all", { confirm: true, mode: tradingMode() }).catch((error) => {
      if (error.status !== 404) throw error;
      return postJSON("/api/broker/square-off-all", { confirm: true, mode: tradingMode() });
    });
    toast("Square-off orders sent", "ok");
    window.setTimeout(() => refreshBooksFromAPI(true), 500);
  } catch (err) {
    toast(`Square off failed: ${err.message}`, "error", 6000);
  }
}

/* ── Confirmation modal ───────────────────────────────── */

let confirmResolve = null;

function confirmAction({ title, body, okLabel = "Confirm", danger = false }) {
  $("confirm-title").textContent = title;
  $("confirm-body").innerHTML = body;
  const ok = $("confirm-ok");
  ok.textContent = okLabel;
  ok.className = danger ? "danger" : "primary";
  $("confirm-modal").hidden = false;
  ok.focus();
  return new Promise((resolve) => {
    confirmResolve = resolve;
  });
}

function closeConfirm(value) {
  $("confirm-modal").hidden = true;
  if (confirmResolve) {
    const resolve = confirmResolve;
    confirmResolve = null;
    resolve(value);
  }
}

/* ── Login modal ──────────────────────────────────────── */

let totpAccepted = false;
let loginBusy = false;

function openLogin() {
  totpAccepted = false;
  $("totp-input").value = "";
  $("totp-input").disabled = false;
  $("totp-hint").textContent = "Invalid code? Sync your device time and try again.";
  if ($("mpin-input")) {
    $("mpin-input").value = "";
    $("mpin-input").disabled = true;
  }
  loginError("");
  $("login-submit").textContent = "Continue";
  $("login-modal").hidden = false;
  window.setTimeout(() => $("totp-input").focus(), 30);
}

function closeLogin() {
  $("login-modal").hidden = true;
  totpAccepted = false;
  $("totp-input").value = "";
  $("totp-input").disabled = false;
  if ($("mpin-input")) {
    $("mpin-input").value = "";
    $("mpin-input").disabled = true;
  }
  if (location.hash === "#login") history.replaceState(null, "", location.pathname);
}

function loginError(message) {
  const box = $("login-error");
  box.textContent = message;
  box.hidden = !message;
}

async function submitLogin(event) {
  event.preventDefault();
  if (loginBusy) return;
  const submit = $("login-submit");
  loginError("");

  if (!totpAccepted) {
    const totp = $("totp-input").value.trim();
    if (!/^\d{6}$/.test(totp)) {
      loginError("Enter the 6-digit TOTP from your authenticator.");
      return;
    }
    loginBusy = true;
    submit.disabled = true;
    submit.textContent = "Checking…";
    try {
      const result = await postJSON("/api/auth/totp", { totp });
      $("totp-input").value = "";
      if (result.session_data) {
        completeLogin(result);
      } else if (S.boot.mpinConfigured) {
        throw new Error("Login did not return a session. Please try again.");
      } else {
        totpAccepted = true;
        $("totp-input").disabled = true;
        $("totp-hint").textContent = "TOTP accepted.";
        $("mpin-input").disabled = false;
        $("login-submit").textContent = "Sign in";
        $("mpin-input").focus();
      }
    } catch (error) {
      loginError(error.message || "TOTP validation failed.");
    } finally {
      loginBusy = false;
      submit.disabled = false;
      submit.textContent = totpAccepted ? "Sign in" : "Continue";
    }
    return;
  }

  const mpin = $("mpin-input").value.trim();
  if (!/^\d{6}$/.test(mpin)) {
    loginError("Enter your 6-digit MPIN.");
    return;
  }
  loginBusy = true;
  submit.disabled = true;
  submit.textContent = "Signing in…";
  try {
    const result = await postJSON("/api/auth/mpin", { mpin });
    $("mpin-input").value = "";
    completeLogin(result);
  } catch (error) {
    loginError(error.message || "MPIN validation failed.");
  } finally {
    loginBusy = false;
    submit.disabled = false;
    submit.textContent = totpAccepted ? "Sign in" : "Continue";
  }
}

function completeLogin(result) {
  S.session = result.session_data || { authenticated: true, paper_mode: false };
  S.session.authenticated = true;
  updateAuthButton();
  renderHeader();
  closeLogin();
  toast("Session active. Feeds are connecting.", "ok");
  connectWebSocket();
  if (!S.expiry) loadMarket();
}

async function logout() {
  try {
    await postJSON("/api/auth/logout", {});
  } catch (error) {
    console.warn("logout failed", error);
  }
  $("totp-input").value = "";
  if ($("mpin-input")) $("mpin-input").value = "";
  S.session = { authenticated: false };
  if (S.wsTimer) {
    window.clearTimeout(S.wsTimer);
    S.wsTimer = null;
  }
  if (S.ws) {
    const ws = S.ws;
    S.ws = null;
    ws.close();
  }
  updateAuthButton();
  renderHeader();
  closeLogin();
  toast("Signed out. Paper mode remains available.", "warn");
}

/* ── Toasts ───────────────────────────────────────────── */

function toast(message, kind = "ok", ms = 3500) {
  const node = document.createElement("div");
  node.className = `toast ${kind}`;
  node.textContent = message;
  $("toast-stack").appendChild(node);
  window.setTimeout(() => {
    node.classList.add("leaving");
    window.setTimeout(() => node.remove(), 200);
  }, ms);
}

/* ── Events ───────────────────────────────────────────── */

function bindEvents() {
  $("bottom-toggle").addEventListener("click", () => {
    const panel = $("bottom-panel");
    const collapsed = panel.classList.toggle("bottom-collapsed");
    panel.closest(".terminal").classList.toggle("bottom-collapsed", collapsed);
    panel.closest(".terminal").classList.toggle("bottom-open", !collapsed);
    $("bottom-toggle").textContent = collapsed ? "Expand" : "Collapse";
    $("bottom-toggle").setAttribute("aria-expanded", collapsed ? "false" : "true");
  });
  $("theme-toggle").addEventListener("click", () => {
    setTheme(document.body.dataset.theme === "dark" ? "light" : "dark");
  });

  $("spot-strip").addEventListener("click", (event) => {
    const chip = event.target.closest(".spot-chip");
    if (!chip) return;
    const key = chip.dataset.index;
    if (!key || key === "INDIA VIX" || key === S.activeIndex) return;
    switchIndex(key);
  });

  $("index-select").addEventListener("change", (event) => switchIndex(event.target.value));
  $("expiry-select").addEventListener("change", (event) => switchExpiry(event.target.value));
  $("tf-group").addEventListener("click", (event) => {
    const button = event.target.closest("button[data-tf]");
    if (!button) return;
    S.timeframe = button.dataset.tf;
    renderToolbar();
    loadCharts();
  });
  $("refresh-button").addEventListener("click", refreshMarket);
  $("fullscreen-button").addEventListener("click", toggleFullscreen);
  $("strike-search-button").addEventListener("click", openStrikeModal);
  $("strike-close").addEventListener("click", closeStrikeModal);
  $("strike-modal").addEventListener("click", (event) => {
    if (event.target === $("strike-modal")) closeStrikeModal();
  });
  $("strike-search-input").addEventListener("input", applyStrikeFilter);
  $("strike-search-input").addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    const query = event.currentTarget.value.trim().replace(/,/g, "");
    const targetStrike = Number(query);
    const row = S.chain.find((item) => Number.isFinite(targetStrike) && item.strike === targetStrike);
    if (!row) {
      toast("No exact matching strike. Choose a visible CALL or PUT.", "warn");
      return;
    }
    selectStrike(row.strike, S.selected.type);
    closeStrikeModal();
    event.preventDefault();
  });

  for (const button of $$(".chart-tools button")) {
    button.addEventListener("click", () => {
      const action = button.dataset.chartAction;
      if (action === "expand") {
        setFocusedChart(button.dataset.chart);
        return;
      }
      const chart = S.charts[button.dataset.chart];
      if (!chart) return;
      if (action === "in") chart.zoomIn();
      else if (action === "out") chart.zoomOut();
      else chart.resetView();
    });
  }

  $("chain-body").addEventListener("click", (event) => {
    const cell = event.target.closest("[data-select]");
    if (cell) {
      selectStrike(num(cell.dataset.strike), cell.dataset.select);
      closeStrikeModal();
    }
  });

  $("chain-body").addEventListener("keydown", (event) => {
    if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
    const rows = [...$("chain-body").querySelectorAll(".chain-row")];
    if (!rows.length) return;
    const current = rows.findIndex((row) => num(row.dataset.strike) === S.selected.strike);
    const next = Math.max(0, Math.min(rows.length - 1, current + (event.key === "ArrowUp" ? -1 : 1)));
    selectStrike(num(rows[next].dataset.strike), S.selected.type);
    event.preventDefault();
  });

  for (const button of $$("[data-qty-step]")) {
    button.addEventListener("click", () => stepQuantity(button.dataset.qtyStep, num(button.dataset.step)));
  }
  for (const input of $$("[data-lots]")) {
    input.addEventListener("change", () => updateLotInput(input.dataset.lots, input.value));
    input.addEventListener("keydown", (event) => {
      if (event.key === "ArrowUp" || event.key === "ArrowDown") {
        stepQuantity(input.dataset.lots, event.key === "ArrowUp" ? 1 : -1);
        event.preventDefault();
      }
    });
  }
  $("product-select").addEventListener("change", (event) => {
    S.product = event.target.value;
    renderOrderPad();
  });

  for (const button of $$(".leg-orderpad .trade")) {
    button.addEventListener("click", () => onTrade(button.dataset.action, button.dataset.type));
  }

  $("tabs").addEventListener("click", (event) => {
    const button = event.target.closest("button[data-tab]");
    if (!button) return;
    S.tab = button.dataset.tab;
    renderTab();
  });

  $("tab-content").addEventListener("click", (event) => {
    const exit = event.target.closest("[data-exit]");
    if (exit) onExit(exit.dataset.exit);
  });

  $("square-off-button").addEventListener("click", onSquareOffAll);
  $("auth-button").addEventListener("click", () => {
    if (S.session?.authenticated) logout();
    else openLogin();
  });
  $("login-close").addEventListener("click", closeLogin);
  $("login-paper").addEventListener("click", () => {
    closeLogin();
    toast("Continuing without a broker session — paper mode.", "warn");
  });
  $("login-form").addEventListener("submit", submitLogin);
  $("confirm-cancel").addEventListener("click", () => closeConfirm(false));
  $("confirm-ok").addEventListener("click", () => closeConfirm(true));
  $("confirm-modal").addEventListener("click", (event) => {
    if (event.target === $("confirm-modal")) closeConfirm(false);
  });
  document.addEventListener("keydown", (event) => {
    const target = event.target;
    const typing = target instanceof HTMLElement &&
      (target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName));
    if (!typing && (event.key === "/" || (event.key.toLowerCase() === "k" && (event.metaKey || event.ctrlKey)))) {
      event.preventDefault();
      openStrikeModal();
      return;
    }
    if (event.key !== "Escape") return;
    if (!$("confirm-modal").hidden) {
      closeConfirm(false);
      return;
    }
    if (!$("strike-modal").hidden) {
      closeStrikeModal();
      return;
    }
    if (S.focusedChart) setFocusedChart(null);
  });
}

function stepQuantity(type, direction) {
  S.qty[type] = Math.max(1, Math.round(num(S.qty[type], 1)) + direction);
  renderOrderPad();
}

function updateLotInput(type, value) {
  S.qty[type] = Math.max(1, Math.round(num(value, 1)));
  renderOrderPad();
}

async function switchIndex(key) {
  if (!key) return;
  S.activeIndex = key;
  S.expiry = "";
  S.chainScrolled = false;
  try {
    const raw = await getJSON(`/api/expiries?index=${encodeURIComponent(key)}`);
    S.expiries = normalizeExpiryList(raw);
    S.expiry = pickExpiry(S.expiries);
    const chainRaw = await getJSON(`/api/chain?index=${encodeURIComponent(key)}&expiry=${encodeURIComponent(S.expiry)}`);
    S.chain = normalizeChain(chainRaw);
  } catch (error) {
    toast(`Could not load ${key}: ${error.message}`, "error");
    return;
  }
  selectDefaultStrike();
  renderAll();
  loadCharts();
}

async function switchExpiry(date) {
  if (!date) return;
  S.expiry = date;
  S.chainScrolled = false;
  try {
    const chainRaw = await getJSON(`/api/chain?index=${encodeURIComponent(S.activeIndex)}&expiry=${encodeURIComponent(date)}`);
    S.chain = normalizeChain(chainRaw);
  } catch (error) {
    toast(`Could not load expiry: ${error.message}`, "error");
    return;
  }
  selectDefaultStrike();
  renderAll();
  loadCharts();
}

function refreshMarket() {
  pollLive();
  toast("Refreshed", "ok", 1200);
}

function toggleFullscreen() {
  if (document.fullscreenElement) document.exitFullscreen?.();
  else document.documentElement.requestFullscreen?.();
}

function updateClock() {
  const clock = $("clock");
  if (!clock) return;
  const text = new Date().toLocaleTimeString("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
    timeZone: "Asia/Kolkata",
  });
  clock.textContent = `${text} IST`;
}

function currentMarketState() {
  const parts = new Intl.DateTimeFormat("en-GB", {
    timeZone: "Asia/Kolkata",
    weekday: "short",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(new Date());
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  const date = `${values.year}-${values.month}-${values.day}`;
  if (values.weekday === "Sat" || values.weekday === "Sun" ||
      (S.boot.marketSession?.holidays || []).includes(date)) return false;
  const toMinutes = (value, fallback) => {
    const match = /^(\d{2}):(\d{2})$/.exec(value || "");
    return match ? Number(match[1]) * 60 + Number(match[2]) : fallback;
  };
  const open = toMinutes(S.boot.marketSession?.open, 9 * 60 + 15);
  const close = toMinutes(S.boot.marketSession?.close, 15 * 60 + 30);
  const now = Number(values.hour) * 60 + Number(values.minute);
  return now >= open && now < close;
}

function updateMarketPhase(reloadOnChange = true) {
  const open = currentMarketState();
  if (S.marketOpen === open) return;
  const wasKnown = S.marketOpen !== null;
  S.marketOpen = open;
  if (!wasKnown || !reloadOnChange || !S.expiry) return;
  if (open) {
    S.candles = {};
    loadCharts();
  } else {
    loadCharts();
  }
}

/* ── Boot ──────────────────────────────────────────────── */

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
