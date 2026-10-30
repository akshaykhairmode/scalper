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
  return "live";
}

const S = {
  boot: {},
  activeIndex: "NIFTY",
  expiry: "",
  timeframe: "5m",
  expiries: [],
  chain: [],
  chainRequestID: 0,
  chainMore: { above: false, below: false },
  chainLoading: false,
  spots: new Map(),
  selected: { type: "CE", CE: null, PE: null },
  strikeSearchTarget: "CE",
  qty: { CE: 1, PE: 1 },
  unit: { CE: "LOTS", PE: "LOTS" },
  product: "MIS",
  positions: [],
  orders: [],
  trades: [],
  limits: { available: 0, used: 0, total: 0, loaded: false, error: "" },
  risk: [],
  tab: "positions",
  feed: "connecting",
  orderFeed: "disconnected",
  mode: "live",
  bookMode: "live",
  session: null,
  chainScrolled: false,
  polling: false,
  ws: null,
  wsTimer: null,
  wsFailCount: 0,
  feedSubscriptions: [],
  charts: {},
  candles: {},
  historicalCache: new Map(),
  chartLoadID: 0,
  quickOrder: null,
  autoTargets: [],
  marketOpen: null,
  focusedChart: null,
  soundEnabled: true,
};
window.S = S;

let marketRetryTimer = null;
let chartRetryTimer = null;

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
    error.retryAfter = response.headers.get("Retry-After") || "";
    throw error;
  }
  return data;
}

const getJSON = (url) => requestJSON(url);
const postJSON = (url, body) => requestJSON(url, { method: "POST", body: JSON.stringify(body) });

function postOrder(payload) {
	return postJSON("/api/broker/orders", {
		exchange_segment: payload.exchange_segment,
		trading_symbol: payload.trading_symbol || payload.symbol,
		transaction_type: payload.side,
		product: payload.product,
		order_type: payload.order_type,
		quantity: payload.quantity,
		price: payload.price,
		trigger_price: payload.trigger_price || 0,
		validity: "DAY",
		confirm: true,
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
  const bids = Array.isArray(raw.bids) ? raw.bids : [];
  const asks = Array.isArray(raw.asks) ? raw.asks : [];
  const instrumentKey = raw.instrument_key ?? raw.instrumentKey ?? raw.key ?? "";
  if (instrumentKey && (bids.length || asks.length)) {
    if (!S.depths) S.depths = {};
    S.depths[instrumentKey] = { bids, asks };
  }
  return {
    instrumentKey,
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
    bids,
    asks,
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

function chainURL({ center = 0, count = 19, direction = "around" } = {}) {
  const query = new URLSearchParams({
    index: S.activeIndex,
    expiry: S.expiry,
    center: String(center),
    count: String(count),
    direction,
  });
  return `/api/chain?${query}`;
}

function acceptChainPage(page, append = false) {
  const rows = normalizeChain(page);
  if (append) {
    const byStrike = new Map(S.chain.map((row) => [row.strike, row]));
    for (const row of rows) byStrike.set(row.strike, row);
    S.chain = [...byStrike.values()].sort((a, b) => a.strike - b.strike);
  } else {
    S.chain = rows;
  }
  S.chainMore = {
    above: S.chain.length > 0 && Boolean(page?.has_more_above ?? page?.hasMoreAbove),
    below: S.chain.length > 0 && Boolean(page?.has_more_below ?? page?.hasMoreBelow),
  };
  for (const type of ["CE", "PE"]) {
    const current = S.selected[type];
    const refreshed = current && S.chain.find((row) => row.strike === current.strike)?.[type === "CE" ? "call" : "put"];
    if (refreshed) S.selected[type] = refreshed;
  }
}

function renderChainMoreControls() {
  const above = $("load-more-above");
  const below = $("load-more-below");
  if (!above || !below) return;
  above.hidden = !S.chainMore.above;
  below.hidden = !S.chainMore.below;
  above.disabled = S.chainLoading;
  below.disabled = S.chainLoading;
  if (S.chainLoading) {
    (S.chainLoading === "above" ? above : below).textContent = "Loading…";
  } else {
    above.textContent = "Load more above";
    below.textContent = "Load more below";
  }
}

async function loadMoreChain(direction) {
  if (S.chainLoading || !S.chain.length || !S.chainMore[direction]) return;
  const requestID = S.chainRequestID;
  const index = S.activeIndex;
  const expiry = S.expiry;
  const anchor = direction === "above" ? S.chain[0].strike : S.chain[S.chain.length - 1].strike;
  const oldHeight = $("chain-body").scrollHeight;
  const oldTop = $("chain-body").scrollTop;
  S.chainLoading = direction;
  renderChainMoreControls();
  try {
    const apiDirection = direction === "above" ? "below" : "above";
    const page = await getJSON(chainURL({ center: anchor, count: 10, direction: apiDirection }));
    if (requestID !== S.chainRequestID || index !== S.activeIndex || expiry !== S.expiry) return;
    acceptChainPage(page, true);
    renderChain(true);
    if (direction === "above") {
      const body = $("chain-body");
      body.scrollTop = oldTop + (body.scrollHeight - oldHeight);
    }
  } catch (error) {
    if (requestID === S.chainRequestID) toast(`Could not load more strikes: ${error.message}`, "error");
  } finally {
    if (requestID === S.chainRequestID) {
      S.chainLoading = false;
      renderChainMoreControls();
    }
  }
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

function matchesSameContract(a, b) {
  if (!a || !b) return false;
  const symA = a.symbol || a.trdSym || a.trading_symbol || "";
  const symB = b.symbol || b.trdSym || b.trading_symbol || "";
  if (symA && symB && symA === symB) return true;
  const keyA = a.instrument_key || a.instrumentKey || "";
  const keyB = b.instrument_key || b.instrumentKey || "";
  if (keyA && keyB && keyA === keyB) return true;
  return false;
}

function computeOpenAverageFromTrades(pos, fallbackAvg) {
  const netQty = num(pos?.quantity);
  if (!netQty || !Array.isArray(S.trades) || !S.trades.length) return fallbackAvg;
  const contractTrades = S.trades.filter((t) => matchesSameContract(t, pos));
  if (!contractTrades.length) return fallbackAvg;

  const sorted = [...contractTrades].sort((a, b) => (num(a.timestamp) || 0) - (num(b.timestamp) || 0));

  if (netQty > 0) {
    let buyQueue = [];
    for (const t of sorted) {
      const side = String(t.side || "").toUpperCase();
      const qty = num(t.quantity);
      const price = num(t.price);
      if (qty <= 0 || price <= 0) continue;

      if (side === "B" || side === "BUY") {
        buyQueue.push({ qty, price });
      } else if (side === "S" || side === "SELL") {
        let remainingSell = qty;
        while (remainingSell > 0 && buyQueue.length > 0) {
          if (buyQueue[0].qty <= remainingSell) {
            remainingSell -= buyQueue[0].qty;
            buyQueue.shift();
          } else {
            buyQueue[0].qty -= remainingSell;
            remainingSell = 0;
          }
        }
      }
    }
    const totalOpenQty = buyQueue.reduce((s, b) => s + b.qty, 0);
    const totalCost = buyQueue.reduce((s, b) => s + b.qty * b.price, 0);
    if (totalOpenQty > 0 && totalCost > 0) {
      return Math.round((totalCost / totalOpenQty) * 100) / 100;
    }
  } else if (netQty < 0) {
    let sellQueue = [];
    for (const t of sorted) {
      const side = String(t.side || "").toUpperCase();
      const qty = num(t.quantity);
      const price = num(t.price);
      if (qty <= 0 || price <= 0) continue;

      if (side === "S" || side === "SELL") {
        sellQueue.push({ qty, price });
      } else if (side === "B" || side === "BUY") {
        let remainingBuy = qty;
        while (remainingBuy > 0 && sellQueue.length > 0) {
          if (sellQueue[0].qty <= remainingBuy) {
            remainingBuy -= sellQueue[0].qty;
            sellQueue.shift();
          } else {
            sellQueue[0].qty -= remainingBuy;
            remainingBuy = 0;
          }
        }
      }
    }
    const totalOpenQty = sellQueue.reduce((s, b) => s + b.qty, 0);
    const totalCost = sellQueue.reduce((s, b) => s + b.qty * b.price, 0);
    if (totalOpenQty > 0 && totalCost > 0) {
      return Math.round((totalCost / totalOpenQty) * 100) / 100;
    }
  }
  return fallbackAvg;
}

function computeRealisedPnLFromTrades(pos, fallbackPnL = 0) {
  if (!Array.isArray(S.trades) || !S.trades.length) return fallbackPnL;
  const contractTrades = S.trades.filter((t) => matchesSameContract(t, pos));
  if (!contractTrades.length) return fallbackPnL;

  const sorted = [...contractTrades].sort((a, b) => (num(a.timestamp) || 0) - (num(b.timestamp) || 0));
  let buyQueue = [];
  let realised = 0;

  for (const t of sorted) {
    const side = String(t.side || "").toUpperCase();
    const qty = num(t.quantity);
    const price = num(t.price);
    if (qty <= 0 || price <= 0) continue;

    if (side === "B" || side === "BUY") {
      buyQueue.push({ qty, price });
    } else if (side === "S" || side === "SELL") {
      let remainingSell = qty;
      while (remainingSell > 0 && buyQueue.length > 0) {
        const matchQty = Math.min(buyQueue[0].qty, remainingSell);
        realised += matchQty * (price - buyQueue[0].price);
        remainingSell -= matchQty;
        buyQueue[0].qty -= matchQty;
        if (buyQueue[0].qty <= 0) {
          buyQueue.shift();
        }
      }
    }
  }
  return Math.round(realised * 100) / 100;
}

function normalizePosition(raw) {
  const bought = num(raw.flBuyQty ?? raw.fl_buy_qty);
  const sold = num(raw.flSellQty ?? raw.fl_sell_qty);
  const carriedBought = num(raw.cfBuyQty ?? raw.cf_buy_qty);
  const carriedSold = num(raw.cfSellQty ?? raw.cf_sell_qty);
  const totalBought = bought + carriedBought;
  const totalSold = sold + carriedSold;
  const quantity = num(raw.quantity ?? raw.net_qty ?? raw.netQty ?? raw.qty ?? raw.netqty, totalBought - totalSold);

  const buyAmt = num(raw.buyAmt ?? raw.buy_amt) + num(raw.cfBuyAmt ?? raw.cf_buy_amt);
  const sellAmt = num(raw.sellAmt ?? raw.sell_amt) + num(raw.cfSellAmt ?? raw.cf_sell_amt);

  let average = num(raw.average_price ?? raw.averagePrice ?? raw.avg_price ?? raw.avgPrice ?? raw.avgPrc ?? raw.avg);
  if (!average && quantity > 0 && totalBought > 0) {
    average = buyAmt / totalBought;
  } else if (!average && quantity < 0 && totalSold > 0) {
    average = sellAmt / totalSold;
  }
  const ltp = num(raw.ltp ?? raw.last_price ?? raw.lastPrice, 0);

  // Realised P&L from closed trades
  const closedQty = Math.min(totalBought, totalSold);
  let realisedPnL = 0;
  if (raw.rPnl != null || raw.realised_pnl != null || raw.realizedPnl != null) {
    realisedPnL = num(raw.rPnl ?? raw.realised_pnl ?? raw.realizedPnl);
  } else if (closedQty > 0) {
    const buyAvg = totalBought > 0 ? buyAmt / totalBought : 0;
    const sellAvg = totalSold > 0 ? sellAmt / totalSold : 0;
    realisedPnL = (sellAvg - buyAvg) * closedQty;
  } else if (quantity === 0 && (buyAmt > 0 || sellAmt > 0)) {
    realisedPnL = sellAmt - buyAmt;
  }

  // Live open position Unrealised P&L: strictly (LTP - Avg) * quantity
  let unrealisedPnL = 0;
  if (quantity !== 0) {
    if (raw.unRlzPnL != null || raw.unrealised_pnl != null || raw.unrealizedPnl != null) {
      unrealisedPnL = num(raw.unRlzPnL ?? raw.unrealised_pnl ?? raw.unrealizedPnl);
    } else if (ltp > 0 && average > 0) {
      unrealisedPnL = (ltp - average) * quantity;
    }
  }

  const positionPnL = unrealisedPnL + realisedPnL;

  return {
    instrument_key: raw.instrument_key ?? raw.instrumentKey ?? raw.tok ?? "",
    exchange_segment: raw.exchange_segment ?? raw.exchangeSegment ?? raw.exSeg ?? "nse_fo",
    symbol: raw.symbol ?? raw.trading_symbol ?? raw.trdSymbol ?? raw.trdSym ?? "",
    underlying: raw.underlying ?? "",
    expiry: raw.expiry ?? "",
    strike: normalizeStrike(raw.strike ?? raw.strike_paise ?? 0),
    option_type: String(raw.option_type ?? raw.optionType ?? raw.type ?? "").toUpperCase(),
    lot_size: num(raw.lot_size ?? raw.lotSize, 1),
    quantity,
    average_price: average,
    ltp,
    pnl: positionPnL,
    realised_pnl: realisedPnL,
    unrealised_pnl: unrealisedPnL,
    product: raw.product ?? raw.prod ?? "MIS",
  };
}

function comparePositions(a, b) {
  // 1. Open positions first (quantity != 0), closed positions last (quantity == 0)
  const aOpen = Math.abs(num(a.quantity)) > 0 ? 0 : 1;
  const bOpen = Math.abs(num(b.quantity)) > 0 ? 0 : 1;
  if (aOpen !== bOpen) return aOpen - bOpen;

  // 2. Underlying: active index first, then alphabetical
  const active = S.activeIndex || "NIFTY";
  const aUnder = a.underlying || "";
  const bUnder = b.underlying || "";
  const aIsActive = (aUnder === active) ? 0 : 1;
  const bIsActive = (bUnder === active) ? 0 : 1;
  if (aIsActive !== bIsActive) return aIsActive - bIsActive;
  const underCmp = aUnder.localeCompare(bUnder);
  if (underCmp !== 0) return underCmp;

  // 3. Expiry date: earlier expiry first
  const expCmp = String(a.expiry || "").localeCompare(String(b.expiry || ""));
  if (expCmp !== 0) return expCmp;

  // 4. Strike: ascending
  const strikeDiff = num(a.strike) - num(b.strike);
  if (strikeDiff !== 0) return strikeDiff;

  // 5. Option type: CE before PE
  const typeCmp = String(a.option_type || "").localeCompare(String(b.option_type || ""));
  if (typeCmp !== 0) return typeCmp;

  // 6. Tie-breaker: symbol, then instrument_key
  const symCmp = String(a.symbol || "").localeCompare(String(b.symbol || ""));
  if (symCmp !== 0) return symCmp;
  return String(a.instrument_key || "").localeCompare(String(b.instrument_key || ""));
}

function normalizeOrder(raw) {
  return {
    id: raw.id ?? raw.client_order_id ?? raw.order_id ?? raw.broker_order_id ?? raw.nOrdNo ?? "—",
    symbol: raw.symbol ?? raw.trading_symbol ?? raw.trdSym ?? raw.instrument_key ?? "",
    instrument_key: raw.instrument_key ?? raw.instrumentKey ?? "",
    side: raw.side ?? raw.buy_sell ?? raw.buySell ?? raw.trnsTp ?? "",
    quantity: num(raw.quantity ?? raw.qty),
    price: num(raw.price ?? raw.prc ?? raw.avgPrc),
    status: raw.status ?? raw.ordSt ?? "",
    reason: raw.reason ?? raw.reject_reason ?? "",
    timestamp: raw.timestamp ?? raw.created_at ?? raw.updated_at ?? raw.ordDtTm ?? 0,
    product: raw.product ?? raw.prod ?? "MIS",
    mode: raw.mode ?? "",
  };
}

function normalizeTrade(raw) {
  return {
    id: raw.id ?? raw.trade_id ?? raw.order_id ?? raw.nOrdNo ?? raw.exOrdId ?? "—",
    symbol: raw.symbol ?? raw.trdSym ?? raw.instrument_key ?? "",
    side: raw.side ?? raw.buy_sell ?? raw.buySell ?? raw.trnsTp ?? "",
    quantity: num(raw.quantity ?? raw.qty),
    price: num(raw.price ?? raw.prc ?? raw.avgPrc),
    timestamp: raw.timestamp ?? raw.executed_at ?? raw.executedAt ?? raw.ordDtTm ?? 0,
  };
}

/* ── Initialisation ────────────────────────────────────── */

function init() {
  S.boot = parseBootstrap();
  S.mode = "live";
  S.bookMode = S.mode;
  initTheme();
  initSound();
  S.autoTargets = loadAutoTargets();
  S.timeframe = S.boot.defaultTimeframe || "5m";
  S.product = S.boot.defaultProduct || "MIS";
  const defaultLots = Math.max(1, num(S.boot.defaultLots, 1));
  const defaultUnit = (S.boot.defaultUnit === "shares") ? "SHARES" : "LOTS";
  S.unit.CE = defaultUnit;
  S.unit.PE = defaultUnit;
  S.qty.CE = defaultLots;
  S.qty.PE = defaultLots;
  const savedIndex = localStorage.getItem("scalper-last-index");
  const availableIndices = (S.boot.indices || []).concat(S.boot.commodities || []);
  if (savedIndex && availableIndices.some((i) => i.key === savedIndex)) {
    S.activeIndex = savedIndex;
  } else {
    S.activeIndex = S.boot.indices?.[0]?.key || S.boot.commodities?.[0]?.key || "NIFTY";
  }

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
  checkVersionUpdate();
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
  for (const chart of Object.values(S.charts)) chart.applyTheme();
  if (S.tab === "risk") renderTab();
  if (!persist) return;
  try {
    localStorage.setItem("scalper-theme", theme);
  } catch (error) {
    console.warn("Unable to save theme preference", error);
  }
}

/* ── Trade sound notifications ─────────────────────────── */

let audioCtx = null;
let lastUiOrderTime = 0;

function getAudioContext() {
  if (!audioCtx) {
    const AudioContextClass = window.AudioContext || window.webkitAudioContext;
    if (AudioContextClass) {
      audioCtx = new AudioContextClass();
    }
  }
  if (audioCtx && audioCtx.state === "suspended") {
    audioCtx.resume().catch(() => {});
  }
  return audioCtx;
}

function initAudioOnFirstGesture() {
  const unlock = () => {
    const ctx = getAudioContext();
    if (ctx && ctx.state === "suspended") {
      ctx.resume().catch(() => {});
    }
    window.removeEventListener("pointerdown", unlock);
    window.removeEventListener("keydown", unlock);
  };
  window.addEventListener("pointerdown", unlock, { once: true });
  window.addEventListener("keydown", unlock, { once: true });
}

function playTone(ctx, freq, startTime, duration, maxGain = 0.15, type = "sine") {
  try {
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();

    osc.type = type;
    osc.frequency.setValueAtTime(freq, startTime);

    gain.gain.setValueAtTime(0.0001, startTime);
    gain.gain.exponentialRampToValueAtTime(maxGain, startTime + 0.005);
    gain.gain.exponentialRampToValueAtTime(0.0001, startTime + duration);

    osc.connect(gain);
    gain.connect(ctx.destination);

    osc.start(startTime);
    osc.stop(startTime + duration + 0.02);
  } catch (_) {}
}

function playTradeSound(side) {
  if (!S.soundEnabled) return;
  const ctx = getAudioContext();
  if (!ctx) return;
  if (ctx.state === "suspended") {
    ctx.resume().catch(() => {});
  }

  const now = ctx.currentTime;
  const action = String(side || "").toLowerCase();

  if (action === "buy" || action === "b") {
    // Upbeat ascending chime: D5 (587.33 Hz) -> A5 (880.00 Hz)
    playTone(ctx, 587.33, now, 0.08, 0.16, "sine");
    playTone(ctx, 880.0, now + 0.07, 0.18, 0.18, "sine");
  } else if (action === "sell" || action === "s") {
    // Crisp descending chime: A5 (880.00 Hz) -> C5 (523.25 Hz)
    playTone(ctx, 880.0, now, 0.08, 0.16, "sine");
    playTone(ctx, 523.25, now + 0.07, 0.18, 0.18, "sine");
  } else if (action === "target" || action === "tp") {
    // Celebratory high chime: E5 (659.25 Hz) -> B5 (987.77 Hz) -> E6 (1318.5 Hz)
    playTone(ctx, 659.25, now, 0.07, 0.14, "sine");
    playTone(ctx, 987.77, now + 0.06, 0.09, 0.16, "sine");
    playTone(ctx, 1318.5, now + 0.14, 0.16, 0.20, "sine");
  } else if (action === "reject" || action === "error") {
    // Low warning pulse: 220 Hz -> 175 Hz
    playTone(ctx, 220.0, now, 0.08, 0.14, "triangle");
    playTone(ctx, 175.0, now + 0.09, 0.12, 0.14, "triangle");
  }
}

function updateSoundButton() {
  const btn = $("sound-toggle");
  if (!btn) return;
  const icon = $("sound-icon");
  const label = $("sound-label");
  if (S.soundEnabled) {
    if (icon) icon.textContent = "🔊";
    if (label) label.textContent = "Sound";
    btn.title = "Sound notifications enabled (Click to mute)";
    btn.setAttribute("aria-pressed", "true");
    btn.classList.remove("muted");
  } else {
    if (icon) icon.textContent = "🔇";
    if (label) label.textContent = "Muted";
    btn.title = "Sound notifications muted (Click to enable)";
    btn.setAttribute("aria-pressed", "false");
    btn.classList.add("muted");
  }
}

function toggleSound() {
  S.soundEnabled = !S.soundEnabled;
  try {
    localStorage.setItem("scalper-sound-enabled", S.soundEnabled ? "true" : "false");
  } catch (_) {}
  updateSoundButton();
  if (S.soundEnabled) {
    playTradeSound("buy");
    toast("Sound notifications enabled", "ok", 1500);
  } else {
    toast("Sound notifications muted", "warn", 1500);
  }
}

function initSound() {
  let enabled = true;
  try {
    const saved = localStorage.getItem("scalper-sound-enabled");
    if (saved === "false") enabled = false;
  } catch (_) {}
  S.soundEnabled = enabled;
  updateSoundButton();
  initAudioOnFirstGesture();
}

function handleBrokerOrderSound(raw) {
  if (!S.soundEnabled || Date.now() - lastUiOrderTime < 1500) return;
  const status = String(raw?.status ?? raw?.ordSt ?? "").toUpperCase();
  if (["TRAD", "COMPLETE", "EXECUTED", "FILLED"].includes(status)) {
    const side = String(raw?.side ?? raw?.buy_sell ?? raw?.buySell ?? raw?.trnsTp ?? "").toUpperCase();
    if (side === "B" || side === "BUY") {
      playTradeSound("buy");
    } else if (side === "S" || side === "SELL") {
      playTradeSound("sell");
    }
  } else if (["REJ", "REJECTED", "CANCELLED", "CAN"].includes(status)) {
    playTradeSound("reject");
  }
}

function buildTimeframes() {
  const group = $("tf-group");
  const list = S.boot.timeframes?.length ? S.boot.timeframes : ["1m", "3m", "5m", "15m", "1h"];
  group.innerHTML = list.map((tf) => `<button type="button" data-tf="${esc(tf)}" class="${tf === S.timeframe ? "active" : ""}">${esc(tf)}</button>`).join("");
}

function buildCharts() {
  const savedSR = localStorage.getItem("scalper-show-sr") === "true";
  const savedAutoTL = localStorage.getItem("scalper-show-auto-tl") === "true";
  const savedTrades = localStorage.getItem("scalper-show-trades") !== "false";
  S.showTrades = savedTrades;
  S.charts.ce = new CandleChart($("chart-ce"), { showVolume: true, showSR: savedSR, showTrades: savedTrades, showAutoTrendlines: savedAutoTL });
  S.charts.spot = new CandleChart($("chart-spot"), { showVolume: true, showSR: savedSR, showTrades: savedTrades, showAutoTrendlines: savedAutoTL });
  S.charts.pe = new CandleChart($("chart-pe"), { showVolume: true, showSR: savedSR, showTrades: savedTrades, showAutoTrendlines: savedAutoTL });

  let syncingRange = false;
  const syncRange = (sourceChart) => {
    if (syncingRange || !sourceChart || !sourceChart.chart) return;
    const dist = sourceChart.chart.getOffsetRightDistance();
    const space = sourceChart.chart.getBarSpace();
    syncingRange = true;
    for (const other of [S.charts.ce, S.charts.spot, S.charts.pe]) {
      if (other && other !== sourceChart && other.chart) {
        try {
          if (space && Math.abs(other.chart.getBarSpace() - space) > 0.05) {
            other.chart.setBarSpace(space);
          }
          if (Number.isFinite(dist) && Math.abs(other.chart.getOffsetRightDistance() - dist) > 1) {
            other.chart.setOffsetRightDistance(dist);
          }
        } catch (_) {}
      }
    }
    syncingRange = false;
  };

  for (const c of [S.charts.ce, S.charts.spot, S.charts.pe]) {
    if (c && c.chart) {
      c.chart.subscribeAction("onScroll", () => syncRange(c));
      c.chart.subscribeAction("onZoom", () => syncRange(c));
    }
  }

  for (const button of $$(".chart-tools button[data-chart-action='sr']")) {
    button.classList.toggle("active", savedSR);
    button.setAttribute("aria-pressed", savedSR ? "true" : "false");
  }

  for (const button of $$(".chart-tools button[data-chart-action='auto-tl']")) {
    button.classList.toggle("active", savedAutoTL);
    button.setAttribute("aria-pressed", savedAutoTL ? "true" : "false");
  }

  for (const button of $$(".chart-tools button[data-chart-action='trades']")) {
    button.classList.toggle("active", savedTrades);
    button.setAttribute("aria-pressed", savedTrades ? "true" : "false");
  }
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
  try {
    S.session = await getJSON("/api/session");
  } catch {
    S.session = null;
  }
  if (!S.session?.authenticated) clearBooksForMode("live");
  updateAuthButton();
}

function clearBooksForMode(mode) {
  S.bookMode = mode;
  S.positions = [];
  S.positionsLoaded = false;
  S.orders = [];
  S.trades = [];
  S.limits = { available: 0, used: 0, total: 0, loaded: false, error: "" };
  S.risk = [];
  updateFeedSubscriptions();
  renderHeader();
  renderTab();
  updateChartTrades();
}

async function checkSession() {
  await refreshSession();
  const wantsLogin = location.hash === "#login";
  if (wantsLogin || !S.session?.authenticated) {
    openLogin();
  } else {
    closeLogin();
    connectWebSocket();
    refreshBooksFromAPI(true).catch((error) => console.warn("account refresh failed", error));
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

  const requestID = ++S.chainRequestID;
  const chainIndex = S.activeIndex;
  const chainExpiry = S.expiry;
  const center = num(S.spots.get(chainIndex)?.ltp);
  const chainRaw = await getJSON(chainURL({ center }));
  if (requestID !== S.chainRequestID || chainIndex !== S.activeIndex || chainExpiry !== S.expiry) return;
  acceptChainPage(chainRaw);
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
  for (const idx of [...(S.boot.indices || []), ...(S.boot.commodities || [])]) {
    if (!map.has(idx.key)) {
      map.set(idx.key, {
        key: idx.key,
        displayName: idx.displayName || idx.key,
        exchangeSegment: idx.exchangeSegment,
        indexSegment: idx.indexSegment || "",
        symbol: idx.indexSymbol || idx.key,
        ltp: 0,
        prevClose: 0,
        close: 0,
        change: 0,
        changePct: 0,
        volume: 0,
        spotKey: idx.indexSegment ? `${idx.indexSegment}|${idx.key}` : "",
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
  const savedExpiry = localStorage.getItem(`scalper-last-expiry-${S.activeIndex}`);
  if (savedExpiry && list.some((e) => e.date === savedExpiry && !e.expired)) {
    return savedExpiry;
  }
  const first = list.find((e) => !e.expired) || list[0];
  return first?.date || "";
}

async function refreshBooksFromAPI(includePositions = true) {
  if (!S.session?.authenticated) return;
  if (booksInFlight) return booksInFlight;
  booksInFlight = loadBooksFromAPI(includePositions);
  try {
    await booksInFlight;
  } finally {
    booksInFlight = null;
  }
}

let booksInFlight = null;

async function loadBooksFromAPI(includePositions = true) {
  const [positions, orders, trades, limits] = await Promise.allSettled([
    includePositions ? getJSON("/api/broker/positions") : Promise.resolve(null),
    getJSON("/api/broker/orders"),
    getJSON("/api/broker/trades"),
    getJSON("/api/broker/limits"),
  ]);
  if (S.bookMode !== "live" || tradingMode() !== S.bookMode) return;
  if (orders.status === "fulfilled") {
    S.orders = toArray(orders.value).map(normalizeOrder);
  }
  if (trades.status === "fulfilled") {
    S.trades = toArray(trades.value).map(normalizeTrade);
  }
  // Keep the last known book on a transient failure (e.g. Kotak rate limiting).
  // Blanking it makes open positions vanish and disables the exit buttons.
  if (includePositions && positions.status === "fulfilled") {
    S.positions = toArray(positions.value).map(normalizePosition);
    for (const pos of S.positions) {
      if (Math.abs(num(pos.quantity)) > 0) {
        const fifoAvg = computeOpenAverageFromTrades(pos, pos.average_price);
        if (fifoAvg > 0) {
          pos.average_price = fifoAvg;
        }
        const fifoRealised = computeRealisedPnLFromTrades(pos, pos.realised_pnl);
        pos.realised_pnl = fifoRealised;
        if (pos.ltp > 0) {
          pos.unrealised_pnl = (pos.ltp - pos.average_price) * pos.quantity;
        }
        pos.pnl = pos.unrealised_pnl + (pos.realised_pnl || 0);
      } else {
        const fifoRealised = computeRealisedPnLFromTrades(pos, pos.realised_pnl);
        pos.realised_pnl = fifoRealised;
        pos.pnl = fifoRealised;
        pos.unrealised_pnl = 0;
      }
    }
    S.positions.sort(comparePositions);
    S.positionsLoaded = true;
    updateFeedSubscriptions();
  }
  if (limits.status === "fulfilled") {
    applyLimitsData(limits.value);
  } else {
    // Keep the last known limits; zeroing them disables every trade button.
    S.limits = { ...(S.limits || {}), error: limits.reason?.message || "Account limits are unavailable." };
  }
  S.risk = [];
  renderHeader();
  renderTab();
  renderOrderPad();
  updateChartTrades();
  checkAutoTargets();
}

function applyLimitsData(value) {
  const obj = value?.limits || value?.data || value || {};
  const available = obj.available_margin ?? obj.availableMargin ?? obj.available ?? obj.Net ?? obj.net ??
    value?.available_margin ?? value?.availableMargin ?? value?.available ?? value?.Net ?? value?.net ??
    value?.data?.availableMargin ?? value?.data?.Net ?? value?.limits?.availableMargin ?? value?.limits?.Net;
  const used = obj.used_margin ?? obj.usedMargin ?? obj.used ?? obj.marginUsed ?? obj.MarginUsed ??
    value?.used_margin ?? value?.usedMargin ?? value?.used ?? value?.marginUsed ?? value?.MarginUsed ?? value?.data?.MarginUsed;
  const total = obj.total_margin ?? obj.totalMargin ?? obj.total ?? obj.Net ?? obj.net ??
    value?.total_margin ?? value?.totalMargin ?? value?.total ?? value?.Net ?? value?.net ?? value?.data?.Net;
  S.limits = {
    available: num(available),
    used: num(used),
    total: num(total),
    loaded: available != null,
    error: available == null ? "Account response did not include available margin." : "",
  };
}

async function refreshMargin() {
  if (!S.session?.authenticated) {
    openLogin();
    return;
  }
  const metric = $("margin-metric");
  if (metric) metric.classList.add("refreshing");
  try {
    const res = await getJSON("/api/broker/limits?fresh=1");
    applyLimitsData(res);
    renderHeader();
    renderOrderPad();
    if (S.limits?.loaded) {
      toast(`Margin updated: ₹${fmt(num(S.limits.available), 0)}`, "info", 2000);
    } else {
      toast("Margin refreshed.", "info", 2000);
    }
  } catch (err) {
    toast(`Failed to update margin: ${err?.message || "network error"}`, "error", 4000);
  } finally {
    if (metric) metric.classList.remove("refreshing");
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
let underlyingTimer = null;
let booksRefreshTimer = null;
function startLivePolling() {
  if (!liveTimer) {
    // Poll account books regularly when logged in and visible.
    liveTimer = window.setInterval(() => {
      if (document.hidden || !S.session?.authenticated) return;
      refreshBooksFromAPI(true).catch((error) => console.warn("account refresh failed", error));
    }, 15000);
  }
  // The Scrip Master refresh can finish after the first page quote request.
  // Poll underlying prices independently so new spot/future tokens are picked
  // up and subscribed without requiring a manual Refresh click.
  if (!underlyingTimer) {
    underlyingTimer = window.setInterval(async () => {
      if (document.hidden || !S.marketOpen) return;
      try {
        applyIndexQuotes(await getJSON("/api/indices"));
        updateFeedSubscriptions();
        renderHeader();
      } catch (error) {
        console.warn("underlying quote refresh failed", error);
      }
    }, 30000);
  }
}

// Coalesce order-feed bursts into one cache read instead of one refresh per
// event. Kotak emits several order/position events per fill.
function scheduleBooksRefresh(delay = 1000) {
  if (booksRefreshTimer) window.clearTimeout(booksRefreshTimer);
  booksRefreshTimer = window.setTimeout(() => {
    booksRefreshTimer = null;
    refreshBooksFromAPI(true).catch((error) => console.warn("account refresh failed", error));
  }, delay);
}

async function pollLive({ refreshChain = false } = {}) {
  if (S.polling) return;
  S.polling = true;
  try {
    applyIndexQuotes(await getJSON("/api/indices"));
    await refreshBooksFromAPI(true);
    if (refreshChain && S.activeIndex && S.expiry) {
      const requestID = ++S.chainRequestID;
      const chainIndex = S.activeIndex;
      const chainExpiry = S.expiry;
      const center = num(S.spots.get(chainIndex)?.ltp);
      const chainRaw = await getJSON(chainURL({ center }));
      if (requestID !== S.chainRequestID || chainIndex !== S.activeIndex || chainExpiry !== S.expiry) return;
      acceptChainPage(chainRaw);
      renderChain(true);
      renderPaneHeaders();
    }
    renderAll(true);
  } catch (error) {
    console.warn("live poll failed", error);
    // REST polling failures do not imply that the authenticated market socket
    // disconnected. Its feed_status/close events own the feed badge state.
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
      S.wsFailCount = 0;
      hideServerDownModal();
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
        S.wsFailCount = (S.wsFailCount || 0) + 1;
        if (S.wsFailCount >= 2) showServerDownModal(S.wsFailCount);
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

function showServerDownModal(attempt) {
  const modal = $("server-down-modal");
  const status = $("server-down-status");
  if (modal) modal.hidden = false;
  if (status) status.textContent = `Reconnecting… (attempt ${attempt})`;
}

function hideServerDownModal() {
  const modal = $("server-down-modal");
  if (modal) modal.hidden = true;
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
  if (event.type === "order_feed_status") {
    S.orderFeed = data?.status || "disconnected";
    renderFeed();
    return;
  }
  if (event.type === "broker_order" || event.type === "broker_position") {
    scheduleBooksRefresh(1000);
    if (event.type === "broker_order" && data) {
      handleBrokerOrderSound(data);
      checkAutoTargets(data);
    }
    return;
  }
  if (event.type === "tick") {
    updateLiveCandle(data.key, data.ltp, data.timestamp, data.volume);
    if (data.bids || data.asks) {
      if (!S.depths) S.depths = {};
      S.depths[data.key] = { bids: data.bids || [], asks: data.asks || [] };
      if (S.tab === "depth") renderTab();
    }
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
      position.unrealised_pnl = position.quantity !== 0 ? (position.ltp - position.average_price) * position.quantity : 0;
      position.pnl = position.unrealised_pnl + (position.realised_pnl || 0);
      updatedPosition = true;
    }
    if (S.autoTargets && S.autoTargets.length) {
      for (const tgt of S.autoTargets) {
        if (tgt.instrumentKey === data.key) {
          tgt.ltp = num(data.ltp, tgt.ltp);
          updatedPosition = true;
        }
      }
    }
    if (updatedPosition) {
      renderHeader();
      if (S.tab === "positions" || S.tab === "brackets") renderTab();
      updateChartTrades();
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
      if (data.bids) opt.bids = data.bids;
      if (data.asks) opt.asks = data.asks;
      opt.quoteAvailable = opt.ltp > 0;
      opt.change = opt.ltp - opt.prevClose;
      opt.changePct = opt.prevClose ? ((opt.ltp - opt.prevClose) / opt.prevClose) * 100 : 0;
      const type = row.call?.instrumentKey === data.key ? "CE" : "PE";
      const cell = [...$("chain-body").querySelectorAll(".chain-row")]
        .find((item) => item.dataset.strike === String(row.strike))
        ?.querySelector(`[data-select="${type}"]`);
      if (cell) {
        cell.innerHTML = `<span class="chain-ltp">${fmt(opt.ltp)}</span><span class="chain-volume">VOL ${fmtCompact(opt.volume)}</span>`;
        cell.title = `${fmt(opt.ltp)} · ${fmtSigned(opt.changePct)}% · Volume ${fmtCompact(opt.volume)}`;
        cell.classList.toggle("up", opt.changePct > 0);
        cell.classList.toggle("down", opt.changePct < 0);
      }
      if (selectedOption(type)?.instrumentKey === data.key) {
        renderPaneHeaders();
        renderOrderPad();
      }
    } else {
      let updatedSelected = false;
      for (const type of ["CE", "PE"]) {
        const option = selectedOption(type);
        if (option?.instrumentKey !== data.key) continue;
        updateOptionFromTick(option, data);
        updatedSelected = true;
      }
      if (updatedSelected) {
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
    if (S.bookMode !== "live") return;
    S.positions = toArray(data).map(normalizePosition).sort(comparePositions);
    updateFeedSubscriptions();
    renderTab();
    renderOrderPad();
    return;
  }
  if (event.type === "margin") {
    if (S.bookMode !== "live") return;
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
    ...S.positions.map((position) => position.instrument_key),
    ...[...S.spots.values()].map((spot) => spot.spotKey)]
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
  if (!spot?.ltp || !S.chain.length) return S.chain.length ? S.chain[Math.floor(S.chain.length / 2)].strike : null;
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
    S.selected = { type: "CE", CE: null, PE: null };
    return;
  }
  const atm = computeATM() ?? S.chain[Math.floor(S.chain.length / 2)].strike;
  const savedCE = num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-${S.expiry}-CE`)) ||
    num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-CE`));
  const savedPE = num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-${S.expiry}-PE`)) ||
    num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-PE`));
  const ceStrike = (savedCE && S.chain.some((r) => r.strike === savedCE)) ? savedCE : atm;
  const peStrike = (savedPE && S.chain.some((r) => r.strike === savedPE)) ? savedPE : atm;
  selectStrike(ceStrike, "CE", false, false);
  selectStrike(peStrike, "PE", false, false);
  S.selected.type = "CE";
  S.chainScrolled = false;
}

function selectStrike(strike, type, rerender = true, reloadCharts = true) {
  const row = S.chain.find((r) => r.strike === strike);
  const option = row?.[type === "CE" ? "call" : "put"];
  if (!option) return;
  S.selected[type] = option;
  S.selected.type = type;
  if (S.activeIndex && S.expiry) {
    try {
      localStorage.setItem(`scalper-last-strike-${S.activeIndex}-${S.expiry}-${type}`, String(strike));
      localStorage.setItem(`scalper-last-strike-${S.activeIndex}-${type}`, String(strike));
    } catch (_) {}
  }
  S.chainScrolled = true;
  updateFeedSubscriptions();
  if (reloadCharts) loadCharts();
  renderPaneHeaders();
  if (rerender) {
    renderChain(true);
    renderOrderPad();
    if (S.tab === "depth") renderTab();
  }
  updateChartTrades();
}

function selectedOption(type) {
  return S.selected[type];
}

function updateOptionFromTick(option, data) {
  option.ltp = num(data.ltp, option.ltp);
  option.prevClose = num(data.prev_close, option.prevClose);
  option.bid = num(data.bid, option.bid);
  option.ask = num(data.ask, option.ask);
  option.volume = num(data.volume, option.volume);
  option.oi = num(data.oi, option.oi);
  if (data.bids) option.bids = data.bids;
  if (data.asks) option.asks = data.asks;
  option.quoteAvailable = option.ltp > 0;
  option.change = option.ltp - option.prevClose;
  option.changePct = option.prevClose ? (option.change / option.prevClose) * 100 : 0;
}

function lotSize(type = S.selected.type) {
  const option = selectedOption(type) || selectedOption(type === "CE" ? "PE" : "CE");
  return option?.lotSize || 0;
}

/* ── Charts ────────────────────────────────────────────── */

function matchesContract(item, option) {
  if (!item || !option) return false;
  const sym = item.symbol || item.trdSym || item.pTrdSymbol || "";
  const optSym = option.symbol || option.pTrdSymbol || "";
  if (sym && optSym && sym === optSym) return true;
  if (item.instrument_key && option.instrumentKey && item.instrument_key === option.instrumentKey) return true;
  if (item.instrumentKey && option.instrumentKey && item.instrumentKey === option.instrumentKey) return true;
  if (sym && option.instrumentKey && sym === option.instrumentKey) return true;
  return false;
}

function updateChartTrades() {
  const trades = Array.isArray(S.trades) ? S.trades : [];
  const positions = Array.isArray(S.positions) ? S.positions : [];

  // 1. CE Chart
  const ce = selectedOption("CE");
  if (S.charts.ce) {
    const ceTrades = ce ? trades.filter((t) => matchesContract(t, ce)) : [];
    let cePos = ce ? positions.find((p) => matchesContract(p, ce) && Math.abs(num(p.quantity)) > 0) : null;
    const activeCEBracket = ce ? (S.autoTargets || []).find((b) => b.status === "IN_POSITION" && matchesContract(b, ce)) : null;
    if (activeCEBracket) {
      const ltp = getInstrumentLTP(activeCEBracket);
      const isolatedPnL = (ltp - activeCEBracket.entryPrice) * activeCEBracket.shares * (activeCEBracket.action === "buy" ? 1 : -1);
      cePos = {
        symbol: activeCEBracket.symbol,
        quantity: activeCEBracket.shares * (activeCEBracket.action === "buy" ? 1 : -1),
        average_price: activeCEBracket.entryPrice,
        unrealised_pnl: isolatedPnL,
        pnl: isolatedPnL,
      };
      if (activeCEBracket.targetPrice > 0) {
        S.charts.ce.showTargetPreview?.(activeCEBracket.targetPrice, "TARGET");
      }
    } else if (!S.quickOrder || S.quickOrder.pane !== "ce") {
      S.charts.ce.clearTargetPreview?.();
    }
    const ceOpen = ce ? (S.orders || []).filter((o) => matchesContract(o, ce) && isOpenOrder(o)) : [];
    S.charts.ce.setTrades(ceTrades, { isSpot: false });
    S.charts.ce.setPosition(cePos);
    S.charts.ce.setOpenOrders(ceOpen);
  }

  // 2. PE Chart
  const pe = selectedOption("PE");
  if (S.charts.pe) {
    const peTrades = pe ? trades.filter((t) => matchesContract(t, pe)) : [];
    let pePos = pe ? positions.find((p) => matchesContract(p, pe) && Math.abs(num(p.quantity)) > 0) : null;
    const activePEBracket = pe ? (S.autoTargets || []).find((b) => b.status === "IN_POSITION" && matchesContract(b, pe)) : null;
    if (activePEBracket) {
      const ltp = getInstrumentLTP(activePEBracket);
      const isolatedPnL = (ltp - activePEBracket.entryPrice) * activePEBracket.shares * (activePEBracket.action === "buy" ? 1 : -1);
      pePos = {
        symbol: activePEBracket.symbol,
        quantity: activePEBracket.shares * (activePEBracket.action === "buy" ? 1 : -1),
        average_price: activePEBracket.entryPrice,
        unrealised_pnl: isolatedPnL,
        pnl: isolatedPnL,
      };
      if (activePEBracket.targetPrice > 0) {
        S.charts.pe.showTargetPreview?.(activePEBracket.targetPrice, "TARGET");
      }
    } else if (!S.quickOrder || S.quickOrder.pane !== "pe") {
      S.charts.pe.clearTargetPreview?.();
    }
    const peOpen = pe ? (S.orders || []).filter((o) => matchesContract(o, pe) && isOpenOrder(o)) : [];
    S.charts.pe.setTrades(peTrades, { isSpot: false });
    S.charts.pe.setPosition(pePos);
    S.charts.pe.setOpenOrders(peOpen);
  }

  // 3. Spot Chart
  if (S.charts.spot) {
    const activeIdx = S.activeIndex || "NIFTY";
    const spotTrades = trades.filter((t) => {
      const sym = t.symbol || "";
      return sym.startsWith(activeIdx) || t.underlying === activeIdx;
    }).map((t) => {
      const sym = t.symbol || "";
      const isCE = sym.endsWith("CE") || t.option_type === "CE";
      const isPE = sym.endsWith("PE") || t.option_type === "PE";
      const strikeMatch = sym.match(/(\d{4,5})(?:CE|PE)$/i);
      const strike = strikeMatch ? strikeMatch[1] : (t.strike ? String(t.strike) : "");
      const optType = isCE ? "CE" : (isPE ? "PE" : "");
      const isBull = (t.side === "B" && isCE) || (t.side === "S" && isPE);
      return {
        ...t,
        option_type: optType,
        strike,
        isBull,
      };
    });
    S.charts.spot.setTrades(spotTrades, { isSpot: true });
    S.charts.spot.setPosition(null);
    S.charts.spot.setOpenOrders([]);
  }
}

function toggleAllChartTrades() {
  S.showTrades = !S.showTrades;
  try {
    localStorage.setItem("scalper-show-trades", String(S.showTrades));
  } catch (_) {}
  for (const chart of Object.values(S.charts)) {
    chart?.toggleTrades(S.showTrades);
  }
  for (const button of $$(".chart-tools button[data-chart-action='trades']")) {
    button.classList.toggle("active", S.showTrades);
    button.setAttribute("aria-pressed", S.showTrades ? "true" : "false");
  }
  updateChartTrades();
  toast(S.showTrades ? "Execution markers enabled" : "Execution markers hidden", "ok", 2000);
  return S.showTrades;
}

function chartInstrument(type) {
  return selectedOption(type)?.instrumentKey || "";
}

function activeCommodity() {
  return (S.boot.commodities || []).find((item) => item.key === S.activeIndex) || null;
}

function activeSpotInstrument() {
  const spot = S.spots.get(S.activeIndex);
  if (spot?.spotKey) return spot.spotKey;
  const idx = [...(S.boot.indices || []), ...(S.boot.commodities || [])].find((item) => item.key === S.activeIndex);
  if (idx?.indexSegment) return `${idx.indexSegment}|${idx.key}`;
  return `nse_cm|${S.activeIndex}`;
}

async function loadCharts(forceRefresh = false) {
  const loadID = ++S.chartLoadID;
  if (forceRefresh) {
    S.historicalCache.clear();
  }
  const dateParts = new Intl.DateTimeFormat("en-GB", {
    timeZone: "Asia/Kolkata", year: "numeric", month: "2-digit", day: "2-digit",
  }).formatToParts(new Date());
  const dateValues = Object.fromEntries(dateParts.map((part) => [part.type, part.value]));
  const sessionDate = `${dateValues.year}-${dateValues.month}-${dateValues.day}`;
  const paneTargets = [
    ["ce", chartInstrument("CE"), "Choose a strike to show chart data."],
    ["spot", activeSpotInstrument(), "Loading spot index data…"],
    ["pe", chartInstrument("PE"), "Choose a strike to show chart data."],
  ];

  const batchQuery = new URLSearchParams({ tf: S.timeframe });
  if (forceRefresh) batchQuery.set("refresh", "1");
  let needsBatch = false;

  for (const [pane, key, defaultHint] of paneTargets) {
    const chart = S.charts[pane];
    if (!chart) continue;
    if (!key) {
      chart.setData([], { symbol: "" });
      setChartHint(pane, defaultHint);
      continue;
    }
    if (key.startsWith("mcx_fo|")) {
      const liveKey = `${key}|${S.timeframe}`;
      const live = S.candles[liveKey] || [];
      chart.setData(live, { symbol: key });
      setChartHint(pane, S.marketOpen
        ? (live.length ? "" : "Waiting for live ticks…")
        : "Kotak does not provide historical MCX candles. Live candles appear during MCX trading hours.");
      continue;
    }
    const cacheKey = `${key}|${S.timeframe}|${sessionDate}`;
    let cached = S.historicalCache.get(cacheKey);
    if (cached?.retryAt && Date.now() < cached.retryAt && !forceRefresh) {
      continue;
    }
    if (!cached || forceRefresh || cached.retryAt) {
      batchQuery.set(pane, key);
      needsBatch = true;
      setChartHint(pane, "Loading historical candles…");
    }
  }

  if (needsBatch) {
    try {
      const batchResp = await getJSON(`/api/candles/batch?${batchQuery.toString()}`);
      if (loadID !== S.chartLoadID) return;
      for (const [pane, key] of paneTargets) {
        if (!key || key.startsWith("mcx_fo|")) continue;
        const result = batchResp[pane];
        const cacheKey = `${key}|${S.timeframe}|${sessionDate}`;
        if (!result) continue;
        if (result.error) {
          const retryHeader = Number(result.retryAfter);
          const retryDelay = Number.isFinite(retryHeader) && retryHeader > 0
            ? Math.min(30_000, retryHeader * 1000)
            : 5_000;
          S.historicalCache.set(cacheKey, { retryAt: Date.now() + retryDelay, error: new Error(result.error) });
        } else {
          const parsed = {
            candles: (result.candles || []).map((c) => ({
              time: num(c.time ?? c.ts),
              open: num(c.open),
              high: num(c.high),
              low: num(c.low),
              close: num(c.close),
              volume: num(c.volume),
            })),
            warning: result.warning || "",
          };
          S.historicalCache.set(cacheKey, parsed);
        }
      }
    } catch (error) {
      if (loadID !== S.chartLoadID) return;
      console.warn("candles batch request failed", error);
      const retryHeader = Number(error.retryAfter);
      const retryDelay = Number.isFinite(retryHeader) && retryHeader > 0
        ? Math.min(30_000, retryHeader * 1000)
        : 5_000;
      if (chartRetryTimer) window.clearTimeout(chartRetryTimer);
      chartRetryTimer = window.setTimeout(() => {
        chartRetryTimer = null;
        loadCharts();
      }, retryDelay + 100);
    }
  }

  if (loadID !== S.chartLoadID) return;

  for (const [pane, key, defaultHint] of paneTargets) {
    const chart = S.charts[pane];
    if (!chart || !key || key.startsWith("mcx_fo|")) continue;
    const currentExpectedKey = pane === "spot" ? activeSpotInstrument() : (pane === "ce" ? chartInstrument("CE") : chartInstrument("PE"));
    if (currentExpectedKey !== key) continue;

    const cacheKey = `${key}|${S.timeframe}|${sessionDate}`;
    const entry = S.historicalCache.get(cacheKey);
    const liveKey = `${key}|${S.timeframe}`;

    if (!entry) {
      chart.setData([], { symbol: key });
      setChartHint(pane, defaultHint);
    } else if (entry.retryAt) {
      console.warn("historical candles unavailable", { pane, error: entry.error });
      chart.setData([], { symbol: key });
      setChartHint(pane, `Historical data unavailable: ${entry.error.message}`);
    } else {
      if (S.marketOpen) {
        if (!S.candles[liveKey]?.length) {
          S.candles[liveKey] = entry.candles.slice(-500).map((candle) => ({ ...candle }));
        }
        chart.setData(mergeChartCandles(entry.candles, S.candles[liveKey] || []), { symbol: key });
        setChartHint(pane, entry.warning || (entry.candles.length ? "" : "Waiting for live ticks…"));
      } else {
        chart.setData(entry.candles, { symbol: key });
        setChartHint(pane, entry.warning || (entry.candles.length ? "" : "No historical candles returned for this contract."));
      }
    }
  }
  renderPaneHeaders();
  updateChartTrades();
}

function mergeChartCandles(historical, live) {
  const byTime = new Map(historical.map((candle) => [candle.time, candle]));
  for (const candle of live) byTime.set(candle.time, candle);
  return [...byTime.values()].sort((a, b) => a.time - b.time);
}

function setChartHint(pane, message) {
  const hint = $("hint-" + pane);
  hint.textContent = message;
  hint.hidden = !message;
}

function updateLiveCandle(key, price, timestamp, volume) {
  if (!S.marketOpen || S.feed !== "live" || !Number.isFinite(Number(price)) || Number(price) <= 0) return;
  const timeMs = num(timestamp) > 1e12 ? num(timestamp) : num(timestamp) * 1000;
  const time = Math.floor((timeMs > 0 ? timeMs : Date.now()) / 1000);
  const interval = ({ "1m": 60, "3m": 180, "5m": 300, "15m": 900, "1h": 3600 })[S.timeframe];
  if (!interval) return;
  const sessionOpen = activeCommodity() ? "09:00" : (S.boot.marketSession?.open || "09:15");
  const openMatch = /^(\d{2}):(\d{2})$/.exec(sessionOpen);
  const openSeconds = openMatch ? (Number(openMatch[1]) * 60 + Number(openMatch[2])) * 60 : 9 * 3600 + 15 * 60;
  const localDayStart = Math.floor((time + 19800) / 86400) * 86400 - 19800;
  const sessionStart = localDayStart + openSeconds;
  const bucket = sessionStart + Math.floor((time - sessionStart) / interval) * interval;
  const value = Number(price);
  const cumVolume = Number.isFinite(Number(volume)) && Number(volume) > 0 ? Number(volume) : 0;
  const cacheKey = `${key}|${S.timeframe}`;
  const candles = S.candles[cacheKey] || (S.candles[cacheKey] = []);
  let candle = candles[candles.length - 1];
  if (!candle || candle.time !== bucket) {
    candle = {
      time: bucket,
      open: value,
      high: value,
      low: value,
      close: value,
      volume: 0,
      startVolume: cumVolume || 0,
      dayVolume: cumVolume || 0,
    };
    candles.push(candle);
    if (candles.length > 500) candles.shift();
  } else {
    candle.high = Math.max(candle.high, value);
    candle.low = Math.min(candle.low, value);
    candle.close = value;
    if (cumVolume > 0) {
      if (!candle.startVolume || candle.startVolume <= 0) {
        candle.startVolume = cumVolume;
      }
      candle.volume = Math.max(candle.volume || 0, cumVolume - candle.startVolume);
      candle.dayVolume = cumVolume;
    }
  }
  for (const type of ["CE", "PE"]) {
    if (chartInstrument(type) !== key) continue;
    const pane = type === "CE" ? "ce" : "pe";
    S.charts[pane]?.update(candle);
    setChartHint(pane, "");
  }
  if (activeSpotInstrument() === key && S.charts.spot) {
    S.charts.spot.update(candle);
    setChartHint("spot", "");
  }
}

function renderPaneHeaders() {
  const expiryLabel = expiryText(S.expiry);
  const ce = selectedOption("CE");
  const pe = selectedOption("PE");
  const spot = S.spots.get(S.activeIndex);

  $("ce-title").textContent = ce ? `${S.activeIndex} ${fmtStrike(ce.strike)} CE` : "CALL";
  $("ce-sub").textContent = ce ? `${expiryLabel} · ${S.timeframe}` : "select a strike";
  $("ce-ltp").textContent = ce ? fmt(ce.ltp) : "—";
  $("ce-change").textContent = ce ? `${fmtSigned(ce.change)} (${fmtSigned(ce.changePct)}%)` : "—";
  $("ce-change").className = "pane-change " + directionClass(ce?.changePct);
  renderOptionStrikeSelect("CE", ce);

  const spotTitle = $("spot-title");
  if (spotTitle) spotTitle.textContent = spot?.displayName || S.activeIndex || "UNDERLYING";
  const spotSub = $("spot-sub");
  if (spotSub) spotSub.textContent = `Spot · ${S.timeframe}`;
  const spotLtp = $("spot-ltp");
  if (spotLtp) spotLtp.textContent = spot?.ltp ? fmt(spot.ltp) : "—";
  const spotChange = $("spot-change");
  if (spotChange) {
    spotChange.textContent = spot?.ltp ? `${fmtSigned(spot.change)} (${fmtSigned(spot.changePct)}%)` : "—";
    spotChange.className = "pane-change " + directionClass(spot?.changePct);
  }

  $("pe-title").textContent = pe ? `${S.activeIndex} ${fmtStrike(pe.strike)} PE` : "PUT";
  $("pe-sub").textContent = pe ? `${expiryLabel} · ${S.timeframe}` : "select a strike";
  $("pe-ltp").textContent = pe ? fmt(pe.ltp) : "—";
  $("pe-change").textContent = pe ? `${fmtSigned(pe.change)} (${fmtSigned(pe.changePct)}%)` : "—";
  $("pe-change").className = "pane-change " + directionClass(pe?.changePct);
  renderOptionStrikeSelect("PE", pe);

  renderOrderPad();
}

function renderOptionStrikeSelect(type, selected) {
  const select = $(type === "CE" ? "ce-strike-select" : "pe-strike-select");
  if (!select) return;
  const field = type === "CE" ? "call" : "put";
  const strikes = S.chain
    .filter((row) => row[field])
    .map((row) => row.strike);
  const options = strikes.map((strike) => `<option value="${strike}">${fmtStrike(strike)}</option>`);
  if (selected && !S.chain.some((row) => row.strike === selected.strike && row[field])) {
    options.push(`<option value="${selected.strike}">${fmtStrike(selected.strike)}</option>`);
    strikes.push(selected.strike);
  }
  const optionsKey = strikes.join(",");
  if (select.dataset.optionsKey !== optionsKey) {
    select.innerHTML = options.length ? options.join("") : '<option value="">—</option>';
    select.dataset.optionsKey = optionsKey;
  }
  select.value = selected ? String(selected.strike) : "";
  select.disabled = options.length === 0;
  select.setAttribute("aria-label", `${type === "CE" ? "Call" : "Put"} strike`);
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
  const order = [...(S.boot.indices || []), ...(S.boot.commodities || [])].map((i) => i.key);
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

  const available = num(S.limits?.available);
  $("margin-chip").textContent = S.limits?.loaded ? `₹${fmt(available, 0)}` : (S.limits?.error ? "N/A" : "—");
  $("margin-chip").title = S.limits?.error || (S.limits?.loaded ? "Available account margin" : "Account margin not loaded");
  const totalPnl = (S.positions || []).reduce((sum, p) => sum + num(p.pnl), 0);
  const pnlChip = $("pnl-chip");
  if (S.session?.authenticated && (S.positionsLoaded || (S.positions && S.positions.length > 0))) {
    pnlChip.textContent = `₹${fmtSigned(totalPnl, 0)}`;
  } else {
    pnlChip.textContent = totalPnl ? `₹${fmtSigned(totalPnl, 0)}` : "—";
  }
  pnlChip.className = "metric-value " + directionClass(totalPnl);

  $("mode-badge").textContent = "LIVE";
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
  const orderStatus = S.orderFeed || "disconnected";
  $("order-feed-badge").className = `feed-badge ${orderStatus === "live" ? "live" : orderStatus === "connecting" ? "connecting" : "degraded"}`;
  $("order-feed-label").textContent = `orders ${orderStatus}`;
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
    renderChainMoreControls();
    return;
  }
  const scrollTop = preserveScroll ? body.scrollTop : 0;
  const atm = computeATM();
  body.innerHTML = S.chain.map((row) => {
    const call = row.call;
    const put = row.put;
    const callSelected = S.selected.CE?.strike === row.strike;
    const putSelected = S.selected.PE?.strike === row.strike;
    const active = callSelected || putSelected;
    return `<div class="chain-row${row.strike === atm ? " atm" : ""}${active ? " active" : ""}" data-strike="${row.strike}" role="row">
      <button type="button" class="cell-ce${callSelected ? " selected" : ""}${call ? ` ${directionClass(call.changePct)}` : ""}" data-select="CE" data-strike="${row.strike}" title="${call ? `${fmt(call.ltp)} · ${fmtSigned(call.changePct)}% · Volume ${fmtCompact(call.volume)}` : "Call quote unavailable"}">${call ? `<span class="chain-ltp">${fmt(call.ltp)}</span><span class="chain-volume">VOL ${fmtCompact(call.volume)}</span>` : "—"}</button>
      <button type="button" class="cell-strike" data-select="${esc(S.selected.type)}" data-strike="${row.strike}">${row.strike === atm ? '<span class="atm-dot">◆</span>' : ""}${fmtStrike(row.strike)}</button>
      <button type="button" class="cell-pe${putSelected ? " selected" : ""}${put ? ` ${directionClass(put.changePct)}` : ""}" data-select="PE" data-strike="${row.strike}" title="${put ? `${fmt(put.ltp)} · ${fmtSigned(put.changePct)}% · Volume ${fmtCompact(put.volume)}` : "Put quote unavailable"}">${put ? `<span class="chain-ltp">${fmt(put.ltp)}</span><span class="chain-volume">VOL ${fmtCompact(put.volume)}</span>` : "—"}</button>
    </div>`;
  }).join("");
  $("strike-meta").textContent = `${S.chain.length} strikes · ${expiryText(S.expiry)}`;
  renderChainMoreControls();

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

function openStrikeModal(targetType = S.selected.type) {
  S.strikeSearchTarget = targetType === "PE" ? "PE" : "CE";
  $("strike-target-select").value = S.strikeSearchTarget;
  $("strike-search-input").value = "";
  applyStrikeFilter();
  renderChainMoreControls();
  $("strike-modal").hidden = false;
  $("strike-search-input").focus();
  requestAnimationFrame(scrollATMIntoView);
}

function closeStrikeModal() {
  $("strike-modal").hidden = true;
}

async function selectSearchedStrike(value, targetType = S.strikeSearchTarget) {
  const target = Number(String(value).trim().replace(/,/g, ""));
  if (!Number.isFinite(target) || target <= 0) {
    toast("Enter a valid strike price.", "warn");
    return;
  }
  let row = S.chain.find((item) => item.strike === target);
  if (!row) {
    const requestID = ++S.chainRequestID;
    const index = S.activeIndex;
    const expiry = S.expiry;
    try {
      const page = await getJSON(chainURL({ center: target, count: 19 }));
      if (requestID !== S.chainRequestID || index !== S.activeIndex || expiry !== S.expiry) return;
      acceptChainPage(page);
      row = S.chain.find((item) => item.strike === target);
      if (!row) {
        renderChain();
        toast(`Strike ${fmtStrike(target)} is not available for ${index} ${expiry}.`, "warn", 5000);
        return;
      }
    } catch (error) {
      if (requestID === S.chainRequestID) toast(`Could not search strikes: ${error.message}`, "error");
      return;
    }
  }
  S.chainScrolled = false;
  selectStrike(row.strike, targetType === "PE" ? "PE" : "CE", false, true);
  renderAll();
  closeStrikeModal();
}

function scrollATMIntoView() {
  const body = $("chain-body");
  const atm = computeATM();
  if (!atm) return;
  const selected = selectedOption(S.selected.type)?.strike || atm;
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
  const hasLimits = S.limits?.loaded;
  const available = hasLimits ? num(S.limits?.available) : null;
  for (const [type, option] of [["CE", ce], ["PE", pe]]) {
    const lot = lotSize(type);
    const isShares = S.unit[type] === "SHARES";
    const shares = resolvedShares(lot, type);
    const prefix = type === "CE" ? "call" : "put";
    const input = $(`${prefix}-lots`);
    if (input) {
      input.value = String(S.qty[type]);
      input.disabled = !option;
      input.step = isShares ? String(Math.max(1, lot)) : "1";
      input.min = isShares ? String(Math.max(1, lot)) : "1";
    }
    const toggleBtn = document.querySelector(`.unit-toggle[data-unit-toggle="${type}"]`);
    if (toggleBtn) {
      toggleBtn.textContent = isShares ? "SHARES" : "LOTS";
      toggleBtn.title = isShares
        ? "Quantity shown in Shares. Click to switch to Lots."
        : "Quantity shown in Lots. Click to switch to Shares.";
    }

    const buyEl = $(`leg-${prefix}-margin-buy`);
    const sellEl = $(`leg-${prefix}-margin-sell`);
    const ltp = option ? num(option.ltp) : 0;
    if (!option || ltp <= 0 || !shares) {
      if (buyEl) { buyEl.textContent = "—"; buyEl.className = "margin-buy"; buyEl.title = "Buy margin"; }
      if (sellEl) { sellEl.textContent = "—"; sellEl.className = "margin-sell"; sellEl.title = "Sell margin"; }
    } else {
      const reqBuy = ltp * shares;
      const reqSell = requiredMargin(option, shares, "S");
      const sellFinite = Number.isFinite(reqSell);
      if (buyEl) {
        buyEl.textContent = `B ₹${fmt(reqBuy, 0)}`;
        if (hasLimits && available !== null) {
          buyEl.className = `margin-buy ${available >= reqBuy ? "up" : "down"}`;
          buyEl.title = `Buy: ₹${fmt(reqBuy, 2)} · Available: ₹${fmt(available, 0)} (${available >= reqBuy ? "Sufficient" : "Insufficient"})`;
        } else {
          buyEl.className = "margin-buy";
          buyEl.title = `Buy margin: ₹${fmt(reqBuy, 2)}`;
        }
      }
      if (sellEl) {
        sellEl.textContent = sellFinite ? `S ₹${fmt(reqSell, 0)}` : "S —";
        if (sellFinite && hasLimits && available !== null) {
          sellEl.className = `margin-sell ${available >= reqSell ? "up" : "down"}`;
          sellEl.title = `Sell: ₹${fmt(reqSell, 2)} · Available: ₹${fmt(available, 0)} (${available >= reqSell ? "Sufficient" : "Insufficient"})`;
        } else {
          sellEl.className = "margin-sell";
          sellEl.title = sellFinite ? `Sell margin: ₹${fmt(reqSell, 2)}` : "Sell margin unavailable (no spot price)";
        }
      }
    }

    $$(".leg-orderpad .trade").filter((button) => button.dataset.type === type).forEach((button) => {
      button.disabled = false;
    });
  }
}

function resolvedShares(lot, type = S.selected.type) {
  const qty = Math.max(1, Math.round(num(S.qty[type], 1)));
  if (S.unit[type] === "SHARES") {
    return qty;
  }
  return qty * lot;
}

function requiredMargin(option, shares, side) {
  if (!option) return 0;
  if (side === "B") return option.ltp * shares;
  // Selling against an existing long is a close, not a fresh short: it needs
  // no margin. Only the excess over the held quantity opens a short position.
  const closing = Math.min(shares, heldQuantity(option.instrumentKey));
  const opening = Math.max(0, shares - closing);
  if (opening === 0) return 0;
  const spot = S.spots.get(option.underlying)?.ltp || 0;
  if (spot <= 0) return Infinity;
  return spot * opening * (num(S.boot.shortMarginPct, 15) / 100) + option.ltp * opening;
}

function heldQuantity(instrumentKey) {
  const key = String(instrumentKey || "");
  if (!key) return 0;
  let quantity = 0;
  for (const position of S.positions || []) {
    if (String(position.instrument_key) === key && num(position.quantity) > 0) {
      quantity += num(position.quantity);
    }
  }
  return quantity;
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

  const openCount = (S.orders || []).filter(isOpenOrder).length;
  const badge = $("open-orders-badge");
  if (badge) {
    badge.textContent = String(openCount);
    badge.hidden = openCount === 0;
  }

  const activeBracketsCount = (S.autoTargets || []).filter((b) => b.status === "PENDING_ENTRY" || b.status === "IN_POSITION").length;
  const bracketBadge = $("brackets-badge");
  if (bracketBadge) {
    bracketBadge.textContent = String(activeBracketsCount);
    bracketBadge.hidden = activeBracketsCount === 0;
  }

  const hasOpen = S.positions.some((p) => Math.abs(num(p.quantity)) > 0);
  const sqBtn = $("square-off-button");
  if (sqBtn) sqBtn.disabled = !hasOpen;

  if (S.tab === "positions") content.innerHTML = positionsHTML();
  else if (S.tab === "brackets") content.innerHTML = bracketsHTML();
  else if (S.tab === "open") content.innerHTML = openOrdersHTML();
  else if (S.tab === "risk") {
    content.innerHTML = riskHTML();
    requestAnimationFrame(drawRiskCharts);
  } else if (S.tab === "orders") content.innerHTML = ordersHTML();
  else if (S.tab === "trades") content.innerHTML = tradesHTML();
  else if (S.tab === "depth") content.innerHTML = depthHTML();
}

function positionsHTML() {
  if (!S.positions.length) return `<div class="empty-state">No open positions. Sign in with Kotak to view your account or place an order.</div>`;
  const sorted = [...S.positions].sort(comparePositions);
  const groups = new Map();
  for (const p of sorted) {
    const key = `${p.underlying || S.activeIndex}|${p.expiry || S.expiry}`;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(p);
  }
  let html = `<table class="book"><thead><tr>
    <th>Contract</th><th>Net</th><th>Avg</th><th>LTP</th><th>P&amp;L</th>
    <th>Max P</th><th>Max L</th><th></th>
  </tr></thead><tbody>`;
  for (const [key, list] of groups) {
    const hasOpenInGroup = list.some((p) => Math.abs(num(p.quantity)) > 0);
    const payoff = hasOpenInGroup ? computePayoff(list.map((p) => ({
      type: p.option_type,
      strike: p.strike,
      qty: num(p.quantity),
      avg: num(p.average_price),
    }))) : null;

    const groupMeta = payoff
      ? ` — group Max P ${payoff.maxProfit === Infinity ? "∞" : `₹${fmt(payoff.maxProfit, 0)}`} · Max L ${payoff.maxLoss === -Infinity ? "∞" : `₹${fmt(payoff.maxLoss, 0)}`}`
      : " — closed";

    html += `<tr class="group-row${hasOpenInGroup ? "" : " closed"}"><td colspan="8">${esc(key.replace("|", " · "))}${groupMeta}</td></tr>`;
    for (const p of list) {
      const risk = legRisk(p);
      const isOpen = Math.abs(num(p.quantity)) > 0;
      const openPnL = p.unrealised_pnl != null ? p.unrealised_pnl : p.pnl;
      html += `<tr class="${isOpen ? "" : "closed"}">
        <td class="sym">${esc(p.symbol || `${p.underlying} ${fmtStrike(p.strike)} ${p.option_type}`)}</td>
        <td>${p.quantity > 0 ? "+" : ""}${p.quantity}</td>
        <td>${fmt(p.average_price)}</td>
        <td>${fmt(p.ltp)}</td>
        <td class="${directionClass(isOpen ? openPnL : p.pnl)}">
          <span class="font-bold">${fmtSigned(isOpen ? openPnL : p.pnl, 0)}</span>
          ${isOpen && p.realised_pnl ? `<span class="muted" style="display:block;font-size:9.5px;" title="Realised earlier today">Day: ${fmtSigned(p.pnl, 0)}</span>` : ""}
        </td>
        <td>${risk.maxProfit === Infinity ? "∞" : `₹${fmt(risk.maxProfit, 0)}`}</td>
        <td>${risk.maxLoss === -Infinity ? "∞" : `₹${fmt(risk.maxLoss, 0)}`}</td>
        <td>${isOpen ? `<button type="button" class="link-button" data-exit="${esc(p.instrument_key)}">EXIT</button>` : `<span class="muted">CLOSED</span>`}</td>
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

  const isLight = document.body.dataset.theme === "light";
  ctx.strokeStyle = isLight ? "#cbd5e1" : "#1a232c";
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
  ctx.strokeStyle = isLight ? "#b45309" : "#f0b429";
  ctx.lineWidth = 1.5;
  ctx.stroke();

  ctx.lineTo(xOf(points[points.length - 1].spot), yOf(0));
  ctx.lineTo(xOf(points[0].spot), yOf(0));
  ctx.closePath();
  ctx.fillStyle = isLight ? "rgba(180, 83, 9, 0.1)" : "#f0b42912";
  ctx.fill();

  if (spot) {
    const x = xOf(spot);
    ctx.strokeStyle = isLight ? "#0284c7" : "#58a6ff";
    ctx.setLineDash([2, 3]);
    ctx.beginPath();
    ctx.moveTo(x, 2);
    ctx.lineTo(x, height - 2);
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = isLight ? "#0284c7" : "#58a6ff";
    ctx.font = "8px ui-monospace, Menlo, monospace";
    ctx.fillText("spot", Math.min(width - 24, x + 3), 9);
  }
}

function isOpenOrder(o) {
  if (!o) return false;
  const status = String(o.status || "").toUpperCase();
  const terminal = ["TRAD", "COMPLETE", "EXECUTED", "FILLED", "CAN", "CANCELLED", "REJ", "REJECTED", "EXPIRED"];
  if (terminal.includes(status)) return false;
  const qty = num(o.quantity);
  const filled = num(o.filled_quantity);
  if (qty > 0 && filled >= qty) return false;
  return true;
}

function openOrdersHTML() {
  const open = (S.orders || []).filter(isOpenOrder);
  if (!open.length) return `<div class="empty-state">No pending limit orders. Click on a chart to place a limit order.</div>`;
  return `<div class="open-orders-bar">
    <span class="open-orders-title">${open.length} Pending Limit Order${open.length === 1 ? "" : "s"}</span>
    <button type="button" class="ghost danger small" id="btn-cancel-all-open">Cancel All Open</button>
  </div>
  <table class="book"><thead><tr>
    <th>Contract</th><th>Side</th><th>Pending / Total</th><th>Limit Price</th><th>Status</th><th>Order ID</th><th>Time</th><th>Action</th>
  </tr></thead><tbody>${open.map((o) => `<tr>
    <td class="sym font-bold">${esc(o.symbol || "—")}</td>
    <td class="${o.side === "B" ? "up" : "down"} font-bold">${o.side === "B" ? "BUY" : o.side === "S" ? "SELL" : esc(o.side)}</td>
    <td>${Math.max(0, o.quantity - (num(o.filled_quantity) || 0))} / ${o.quantity}</td>
    <td class="font-bold">₹${o.price ? fmt(o.price) : "MKT"}</td>
    <td><span class="status-badge pending">${esc(o.status)}</span></td>
    <td class="sym">${esc(o.id)}</td>
    <td>${fmtTime(o.timestamp || o.placed_at)}</td>
    <td><button type="button" class="btn-cancel-order danger small ghost" data-order-id="${esc(o.id)}" title="Cancel order ${esc(o.id)}">Cancel</button></td>
  </tr>`).join("")}</tbody></table>`;
}

function ordersHTML() {
  if (!S.orders.length) return `<div class="empty-state">No orders yet.</div>`;
  return `<table class="book"><thead><tr>
    <th>Order</th><th>Contract</th><th>Side</th><th>Qty</th><th>Price</th><th>Status</th><th>Time</th><th>Action</th>
  </tr></thead><tbody>${S.orders.map((o) => `<tr>
    <td class="sym">${esc(o.id)}</td>
    <td class="sym">${esc(o.symbol)}</td>
    <td class="${o.side === "B" ? "up" : "down"}">${o.side === "B" ? "BUY" : o.side === "S" ? "SELL" : esc(o.side)}</td>
    <td>${o.quantity}</td>
    <td>${o.price ? fmt(o.price) : "MKT"}</td>
    <td>${o.status}${o.reason ? `<span class="muted"> · ${esc(o.reason)}</span>` : ""}</td>
    <td>${fmtTime(o.timestamp || o.placed_at)}</td>
    <td>${isOpenOrder(o) ? `<button type="button" class="btn-cancel-order danger small ghost" data-order-id="${esc(o.id)}" title="Cancel order ${esc(o.id)}">Cancel</button>` : "—"}</td>
  </tr>`).join("")}</tbody></table>`;
}

function cancelBrokerOrder(orderId) {
  return requestJSON(`/api/broker/orders/${encodeURIComponent(orderId)}`, {
    method: "DELETE",
    body: JSON.stringify({ confirm: true }),
  });
}

async function onCancelOrder(orderId) {
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before managing orders.", "warn");
    return;
  }
  if (!orderId || orderId === "—") {
    toast("Invalid order ID.", "error");
    return;
  }

  const target = (S.autoTargets || []).find((t) => t.orderId === orderId && t.status === "PENDING_ENTRY");
  if (target) {
    target.status = "CANCELLED";
    saveAutoTargets();
  }

  try {
    toast(`Cancelling order ${orderId}…`, "info", 2000);
    await cancelBrokerOrder(orderId);
    playTradeSound("reject");
    toast(`Order ${orderId} cancelled.`, "ok", 3000);
    scheduleBooksRefresh(300);
  } catch (err) {
    toast(`Failed to cancel order ${orderId}: ${err.message}`, "error", 5000);
  }
}

async function onCancelAllOpenOrders() {
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before managing orders.", "warn");
    return;
  }
  const openOrders = (S.orders || []).filter(isOpenOrder);
  if (!openOrders.length) {
    toast("No open orders to cancel.", "info", 2000);
    return;
  }
  const ok = await confirmAction({
    title: "Cancel All Open Orders",
    body: `Are you sure you want to cancel all <b>${openOrders.length}</b> open limit order(s)?`,
    okLabel: "Cancel All",
    danger: true,
  });
  if (!ok) return;

  toast(`Cancelling ${openOrders.length} open order(s)…`, "info", 3000);
  let cancelled = 0;
  for (const o of openOrders) {
    if (!o.id || o.id === "—") continue;
    try {
      await cancelBrokerOrder(o.id);
      cancelled++;
      const tgt = (S.autoTargets || []).find((t) => t.orderId === o.id && t.status === "PENDING_ENTRY");
      if (tgt) tgt.status = "CANCELLED";
    } catch (e) {
      console.warn("failed to cancel order", o.id, e);
    }
  }
  saveAutoTargets();
  playTradeSound("reject");
  toast(`Cancelled ${cancelled} of ${openOrders.length} order(s).`, "ok", 4000);
  scheduleBooksRefresh(300);
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

function renderOptionDepthPanel(type) {
  const option = selectedOption(type);
  if (!option) {
    return `<div class="depth-column">
      <div class="depth-column-header"><h3>${type} — No strike selected</h3></div>
      <div class="empty-state">Select a ${type} strike from the chain</div>
    </div>`;
  }
  const cached = S.depths?.[option.instrumentKey] || {};
  let bids = (cached.bids && cached.bids.length) ? cached.bids : (option.bids || []);
  let asks = (cached.asks && cached.asks.length) ? cached.asks : (option.asks || []);

  if (!bids.length && option.bid > 0) {
    bids = [{ price: option.bid, quantity: 0, orders: 0 }];
  }
  if (!asks.length && option.ask > 0) {
    asks = [{ price: option.ask, quantity: 0, orders: 0 }];
  }

  const rows = [];
  const count = Math.max(5, bids.length, asks.length);
  let totalBidQty = 0;
  let totalAskQty = 0;

  for (let i = 0; i < count; i++) {
    const b = bids[i] || null;
    const a = asks[i] || null;
    if (b && b.quantity) totalBidQty += num(b.quantity);
    if (a && a.quantity) totalAskQty += num(a.quantity);
    rows.push(`<tr>
      <td class="col-orders">${b && b.orders ? b.orders : (b ? "—" : "")}</td>
      <td class="col-qty">${b && b.quantity ? b.quantity.toLocaleString("en-IN") : (b ? "—" : "")}</td>
      <td class="col-bid-px">${b && b.price ? fmt(b.price) : ""}</td>
      <td class="col-ask-px">${a && a.price ? fmt(a.price) : ""}</td>
      <td class="col-qty">${a && a.quantity ? a.quantity.toLocaleString("en-IN") : (a ? "—" : "")}</td>
      <td class="col-orders">${a && a.orders ? a.orders : (a ? "—" : "")}</td>
    </tr>`);
  }

  return `<div class="depth-column">
    <div class="depth-column-header">
      <h3>${esc(option.symbol || `${option.underlying} ${fmtStrike(option.strike)} ${type}`)}</h3>
      <span class="depth-ltp ${directionClass(option.changePct)}">${fmt(option.ltp)} (${fmtSigned(option.changePct)}%)</span>
    </div>
    <div class="depth-table-wrapper">
      <table class="depth-table">
        <thead>
          <tr class="depth-title-row">
            <th colspan="3" class="title-buyers">BUYERS</th>
            <th colspan="3" class="title-sellers">SELLERS</th>
          </tr>
          <tr class="depth-header-row">
            <th class="col-orders">ORDERS</th>
            <th class="col-qty">QTY</th>
            <th class="col-bid-px">PRICE</th>
            <th class="col-ask-px">PRICE</th>
            <th class="col-qty">QTY</th>
            <th class="col-orders">ORDERS</th>
          </tr>
        </thead>
        <tbody>
          ${rows.join("")}
        </tbody>
        <tfoot>
          <tr class="depth-total-row">
            <td colspan="2" class="total-bid-qty">${totalBidQty ? totalBidQty.toLocaleString("en-IN") : "—"}</td>
            <td colspan="2" class="total-label">Total Quantity</td>
            <td colspan="2" class="total-ask-qty">${totalAskQty ? totalAskQty.toLocaleString("en-IN") : "—"}</td>
          </tr>
        </tfoot>
      </table>
    </div>
  </div>`;
}

function depthHTML() {
  const cePanel = renderOptionDepthPanel("CE");
  const pePanel = renderOptionDepthPanel("PE");
  return `<div class="depth-container">
    <div class="depth-pane left">${cePanel}</div>
    <div class="depth-pane center-spacer"></div>
    <div class="depth-pane right">${pePanel}</div>
  </div>`;
}

function renderFooter() {
  $("footer-version").textContent = `v${S.boot.version || "dev"}`;
  $("footer-feed").textContent = `${S.feed} feed`;
}

/* ── Trading actions ──────────────────────────────────── */

function validateQuantity(type) {
  const qty = num(S.qty[type]);
  if (!Number.isInteger(qty) || qty <= 0) {
    return `Enter a positive ${type} quantity.`;
  }
  const lot = lotSize(type);
  if (S.unit[type] === "SHARES" && lot > 1 && qty % lot !== 0) {
    return `Quantity in shares must be a multiple of lot size (${lot}).`;
  }
  return "";
}

async function onTrade(action, type) {
  const orderMode = tradingMode();
  if (!S.session?.authenticated) { openLogin(); toast("Sign in with Kotak before placing an order.", "warn"); return; }
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
  if (orderMode !== "live" && !Number.isFinite(required) && (S.boot.indices || []).some((index) => index.key === S.activeIndex)) {
    toast("A live underlying quote is required to calculate short-order margin.", "error", 6000);
    return;
  }
  if (orderMode !== "live" && required > available) {
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
    mode: orderMode,
    lot_size: lot,
  };

  try {
    const result = await postOrder(payload);
    lastUiOrderTime = Date.now();
    playTradeSound(action);
    toast(`Live order ${result?.status?.toLowerCase() || "submitted"}.`, "ok");
    scheduleBooksRefresh(400);
  } catch (err) {
    playTradeSound("reject");
    toast(`Order rejected: ${err.message}`, "error", 6000);
  }
}

async function onLimitTrade(action, type, targetPrice, shares, autoTargetPrice = 0) {
  const orderMode = tradingMode();
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before placing an order.", "warn");
    return;
  }
  const option = selectedOption(type);
  if (!option) {
    toast(`Select a ${type} strike first.`, "warn");
    return;
  }
  const price = Math.round(num(targetPrice) * 20) / 20;
  if (!Number.isFinite(price) || price <= 0) {
    toast("Invalid limit price.", "error");
    return;
  }
  const lot = lotSize(type);
  const qty = num(shares);
  if (!Number.isInteger(qty) || qty <= 0) {
    toast(`Invalid ${type} quantity.`, "error");
    return;
  }
  if (lot > 1 && qty % lot !== 0) {
    toast(`Quantity must be a multiple of lot size (${lot}).`, "error");
    return;
  }

  const side = action === "buy" ? "B" : "S";
  const required = requiredMargin(option, qty, side);
  const available = num(S.limits?.available);
  if (orderMode !== "live" && required > available) {
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
    quantity: qty,
    qty,
    price,
    order_type: "LIMIT",
    product: S.product,
    intent: "ENTRY",
    mode: orderMode,
    lot_size: lot,
  };

  try {
    const result = await postOrder(payload);
    lastUiOrderTime = Date.now();
    playTradeSound(action);
    const orderId = String(result?.nOrdNo ?? result?.order_id ?? result?.id ?? "");
    toast(`Live ${action === "buy" ? "Buy" : "Sell"} Limit order placed: ${qty} shares @ ₹${fmt(price)}.`, "ok", 4000);

    if (autoTargetPrice > 0) {
      registerAutoTarget({
        id: "tgt-" + Date.now() + "-" + Math.random().toString(36).slice(2, 7),
        orderId,
        symbol: option.symbol,
        instrumentKey: option.instrumentKey,
        exchangeSegment: option.exchangeSegment,
        underlying: S.activeIndex,
        strike: option.strike,
        type,
        action,
        entryPrice: price,
        targetPrice: autoTargetPrice,
        shares: qty,
        filledQty: 0,
        exitOrderId: "",
        exitPrice: 0,
        ltp: price,
        status: "PENDING_ENTRY",
        createdAt: Date.now(),
      });
      toast(`🎯 Auto-Target armed: Exit at ₹${autoTargetPrice.toFixed(2)} (+${Math.abs(autoTargetPrice - price).toFixed(2)} pts) once filled.`, "info", 4500);
      renderTab();
    }

    scheduleBooksRefresh(400);
    return result;
  } catch (err) {
    playTradeSound("reject");
    toast(`Order rejected: ${err.message}`, "error", 6000);
  }
}

/* ── Auto-Target (Take-Profit Bracket) ─────────────────── */

function loadAutoTargets() {
  try {
    const raw = localStorage.getItem("scalper-auto-targets");
    if (!raw) return [];
    const list = JSON.parse(raw);
    const now = Date.now();
    return Array.isArray(list) ? list.filter((t) => t && (now - (t.createdAt || 0)) < 86400000) : [];
  } catch (_) {
    return [];
  }
}

function saveAutoTargets() {
  try {
    localStorage.setItem("scalper-auto-targets", JSON.stringify(S.autoTargets || []));
  } catch (_) {}
}

function registerAutoTarget(target) {
  if (!S.autoTargets) S.autoTargets = [];
  S.autoTargets.push(target);
  saveAutoTargets();
}

function getInstrumentLTP(b) {
  if (b.ltp && Number.isFinite(b.ltp) && b.ltp > 0) return b.ltp;
  const pos = (S.positions || []).find((p) => matchesSameContract(p, b));
  if (pos && num(pos.ltp) > 0) return num(pos.ltp);
  const row = (S.chain || []).find((r) => r.call?.instrumentKey === b.instrumentKey || r.put?.instrumentKey === b.instrumentKey);
  if (row) {
    if (row.call?.instrumentKey === b.instrumentKey && num(row.call.ltp) > 0) return num(row.call.ltp);
    if (row.put?.instrumentKey === b.instrumentKey && num(row.put.ltp) > 0) return num(row.put.ltp);
  }
  const sel = selectedOption(b.type);
  if (sel && sel.instrumentKey === b.instrumentKey && num(sel.ltp) > 0) return num(sel.ltp);
  return b.entryPrice || 0;
}

function bracketsHTML() {
  const brackets = S.autoTargets || [];
  if (!brackets.length) {
    return `<div class="empty-state">No custom bracket orders yet. Click on the chart with Auto-Target enabled to place an isolated bracket scalp trade.</div>`;
  }

  const activeBrackets = brackets.filter((b) => b.status === "PENDING_ENTRY" || b.status === "IN_POSITION");
  const hasFinished = brackets.some((b) => b.status !== "PENDING_ENTRY" && b.status !== "IN_POSITION");

  let html = `<div class="open-orders-bar">
    <span class="open-orders-title">${activeBrackets.length} Active Bracket${activeBrackets.length === 1 ? "" : "s"} (${brackets.length} Total Today)</span>
    <div style="display:flex;gap:8px;align-items:center;">
      ${hasFinished ? `<button type="button" class="ghost small" id="btn-clear-brackets" title="Remove completed and cancelled brackets from the list">Clear Finished</button>` : ""}
    </div>
  </div>
  <table class="book"><thead><tr>
    <th>Contract</th><th>Side</th><th>Qty</th><th>Entry Price</th><th>Target</th><th>Live LTP</th><th>Isolated P&amp;L</th><th>Status</th><th>Action</th>
  </tr></thead><tbody>`;

  for (const b of [...brackets].reverse()) {
    const ltp = getInstrumentLTP(b);
    let pnl = 0;
    let pnlClass = "";
    let pnlText = "—";

    if (b.status === "IN_POSITION") {
      pnl = (ltp - b.entryPrice) * b.shares * (b.action === "buy" ? 1 : -1);
      pnlClass = directionClass(pnl);
      pnlText = fmtSigned(pnl, 0);
    } else if (b.status === "TARGET_HIT" || b.status === "EXITED_MANUAL" || b.status === "COMPLETED") {
      const exitP = b.exitPrice || b.targetPrice;
      pnl = (exitP - b.entryPrice) * b.shares * (b.action === "buy" ? 1 : -1);
      pnlClass = directionClass(pnl);
      pnlText = fmtSigned(pnl, 0) + (b.status === "TARGET_HIT" ? " 🎯" : "");
    }

    let badgeClass = "pending";
    let badgeLabel = "PENDING ENTRY";
    if (b.status === "IN_POSITION") {
      badgeClass = "in-position";
      badgeLabel = "IN POSITION";
    } else if (b.status === "TARGET_HIT") {
      badgeClass = "target-hit";
      badgeLabel = "TARGET HIT";
    } else if (b.status === "EXITED_MANUAL") {
      badgeClass = "exited";
      badgeLabel = "MANUAL EXIT";
    } else if (b.status === "CANCELLED") {
      badgeClass = "muted";
      badgeLabel = "CANCELLED";
    } else if (b.status === "COMPLETED") {
      badgeClass = "target-hit";
      badgeLabel = "COMPLETED";
    }

    let actionBtn = "—";
    if (b.status === "IN_POSITION") {
      actionBtn = `<button type="button" class="btn-exit-bracket danger small" data-exit-bracket="${esc(b.id)}" title="Cancel resting target and exit immediately at market">EXIT (MKT)</button>`;
    } else if (b.status === "PENDING_ENTRY") {
      actionBtn = `<button type="button" class="btn-cancel-order danger small ghost" data-cancel-bracket="${esc(b.id)}" title="Cancel pending entry limit order">Cancel</button>`;
    }

    const sideLabel = b.action === "buy" ? "BUY" : "SELL";
    const sideClass = b.action === "buy" ? "up" : "down";
    const targetDiff = (b.targetPrice - b.entryPrice) * (b.action === "buy" ? 1 : -1);

    html += `<tr>
      <td class="sym font-bold">${esc(b.symbol || `${b.underlying || S.activeIndex} ${b.strike} ${b.type}`)}</td>
      <td class="${sideClass} font-bold">${sideLabel}</td>
      <td>${b.shares}</td>
      <td class="font-bold">₹${fmt(b.entryPrice)}</td>
      <td class="up font-bold">₹${fmt(b.targetPrice)} <span class="muted" style="font-size:9.5px;">(+${targetDiff.toFixed(2)})</span></td>
      <td class="font-bold">₹${fmt(ltp)}</td>
      <td class="${pnlClass} font-bold">${pnlText}</td>
      <td><span class="status-badge ${badgeClass}">${badgeLabel}</span></td>
      <td>${actionBtn}</td>
    </tr>`;
  }

  return html + `</tbody></table>`;
}

async function onExitBracket(bracketId) {
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before managing orders.", "warn");
    return;
  }
  const bracket = (S.autoTargets || []).find((t) => t.id === bracketId);
  if (!bracket) {
    toast("Bracket order not found.", "error");
    return;
  }
  if (bracket.status !== "IN_POSITION") {
    toast(`Bracket is not in active position (status: ${bracket.status}).`, "warn");
    return;
  }

  const exitSide = bracket.action === "buy" ? "S" : "B";
  const ok = await confirmAction({
    title: "Exit Bracket Position",
    body: `Exit <b>${bracket.shares}</b> shares of <b>${esc(bracket.symbol)}</b> at Market now?<br><span class="muted">Any resting target limit order on Kotak Neo will be automatically cancelled.</span>`,
    okLabel: "Exit at Market",
    danger: true,
  });
  if (!ok) return;

  // 1. Cancel resting target limit order if it exists
  if (bracket.exitOrderId && bracket.exitOrderId !== "—") {
    try {
      toast(`Cancelling resting target order ${bracket.exitOrderId}…`, "info", 2000);
      await cancelBrokerOrder(bracket.exitOrderId);
    } catch (e) {
      console.warn("Target cancel failed (may already be filled/cancelled):", e);
    }
  }

  // 2. Place immediate market exit order
  try {
    toast(`Exiting ${bracket.shares} shares @ Market…`, "info", 2000);
    const lot = lotSize(bracket.type);
    await postOrder({
      instrument_key: bracket.instrumentKey,
      exchange_segment: bracket.exchangeSegment,
      symbol: bracket.symbol,
      underlying: bracket.underlying || S.activeIndex,
      strike: bracket.strike,
      option_type: bracket.type,
      side: exitSide,
      buy_sell: exitSide,
      quantity: bracket.shares,
      qty: bracket.shares,
      price: 0,
      order_type: "MARKET",
      product: S.product || "NRML",
      intent: "EXIT",
      mode: tradingMode(),
      lot_size: lot,
    });
    lastUiOrderTime = Date.now();
    playTradeSound(exitSide === "S" ? "sell" : "buy");
    bracket.status = "EXITED_MANUAL";
    bracket.exitPrice = getInstrumentLTP(bracket);
    saveAutoTargets();
    toast(`Bracket exited at market.`, "ok", 4000);
    scheduleBooksRefresh(400);
    renderTab();
    updateChartTrades();
  } catch (err) {
    playTradeSound("reject");
    toast(`Market exit failed: ${err.message}`, "error", 6000);
  }
}

async function onCancelBracket(bracketId) {
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before managing orders.", "warn");
    return;
  }
  const bracket = (S.autoTargets || []).find((t) => t.id === bracketId);
  if (!bracket) {
    toast("Bracket order not found.", "error");
    return;
  }
  if (bracket.status !== "PENDING_ENTRY") {
    toast(`Bracket entry is already ${bracket.status.toLowerCase()}.`, "warn");
    return;
  }

  // Cancel entry limit order if present
  if (bracket.orderId && bracket.orderId !== "—") {
    try {
      toast(`Cancelling entry limit order ${bracket.orderId}…`, "info", 2000);
      await cancelBrokerOrder(bracket.orderId);
      playTradeSound("reject");
    } catch (e) {
      console.warn("Entry cancel failed:", e);
    }
  }

  bracket.status = "CANCELLED";
  saveAutoTargets();
  toast("Bracket entry order cancelled.", "ok", 3000);
  scheduleBooksRefresh(300);
  renderTab();
  updateChartTrades();
}

function onClearFinishedBrackets() {
  if (!S.autoTargets) return;
  S.autoTargets = S.autoTargets.filter((b) => b.status === "PENDING_ENTRY" || b.status === "IN_POSITION");
  saveAutoTargets();
  renderTab();
}

function checkAutoTargets(eventOrder) {
  if (!S.autoTargets || !S.autoTargets.length) return;
  const orders = S.orders || [];

  for (const target of S.autoTargets) {
    if (target.status === "PENDING_ENTRY") {
      let matchedOrder = null;

      if (eventOrder) {
        const evId = String(eventOrder.nOrdNo ?? eventOrder.order_id ?? eventOrder.id ?? "");
        if (target.orderId && evId && target.orderId === evId) {
          matchedOrder = normalizeOrder(eventOrder);
        }
      }

      if (!matchedOrder && target.orderId) {
        matchedOrder = orders.find((o) => String(o.id) === target.orderId);
      }

      if (!matchedOrder) {
        const targetSide = target.action === "buy" ? "B" : "S";
        matchedOrder = orders.find((o) => {
          if (o.symbol !== target.symbol) return false;
          if (o.side !== targetSide) return false;
          if (Math.abs(num(o.price) - target.entryPrice) > 0.06) return false;
          return true;
        });
        if (matchedOrder && matchedOrder.id && matchedOrder.id !== "—") {
          target.orderId = String(matchedOrder.id);
          saveAutoTargets();
        }
      }

      if (!matchedOrder) continue;

      const status = String(matchedOrder.status || "").toUpperCase();
      const isCancelled = ["CAN", "CANCELLED", "REJ", "REJECTED"].includes(status);
      const isFilledStatus = ["TRAD", "COMPLETE", "EXECUTED", "FILLED"].includes(status);
      const filledQty = num(matchedOrder.filled_quantity);

      if (isCancelled && filledQty === 0) {
        target.status = "CANCELLED";
        saveAutoTargets();
        toast(`Auto-Target cancelled: entry order ${matchedOrder.id} was ${status.toLowerCase()}.`, "warn", 4000);
        renderTab();
        continue;
      }

      const currentFilled = Math.max(filledQty, isFilledStatus ? target.shares : 0);
      const previousFilled = target.filledQty || 0;

      if (currentFilled > previousFilled) {
        const qtyToExit = currentFilled - previousFilled;
        target.filledQty = currentFilled;

        // Try to capture actual filled entry price from matching trades
        const matchingTrade = (S.trades || []).find((t) => matchesSameContract(t, target) && (t.id === matchedOrder.id || Math.abs((num(t.timestamp) || 0) - (target.createdAt || 0)) < 300000));
        if (matchingTrade && num(matchingTrade.price) > 0) {
          target.entryPrice = num(matchingTrade.price);
        } else if (num(matchedOrder.price) > 0) {
          target.entryPrice = num(matchedOrder.price);
        }

        target.status = "IN_POSITION";
        saveAutoTargets();
        renderTab();
        updateChartTrades();

        placeAutoTargetExit(target, qtyToExit);
      }
    } else if (target.status === "IN_POSITION") {
      // Check if exit limit order has filled
      let matchedExit = null;
      if (eventOrder && target.exitOrderId) {
        const evId = String(eventOrder.nOrdNo ?? eventOrder.order_id ?? eventOrder.id ?? "");
        if (target.exitOrderId === evId) {
          matchedExit = normalizeOrder(eventOrder);
        }
      }
      if (!matchedExit && target.exitOrderId) {
        matchedExit = orders.find((o) => String(o.id) === target.exitOrderId);
      }

      if (matchedExit) {
        const exitStatus = String(matchedExit.status || "").toUpperCase();
        const isExitFilled = ["TRAD", "COMPLETE", "EXECUTED", "FILLED"].includes(exitStatus) || (num(matchedExit.filled_quantity) >= target.shares);
        if (isExitFilled) {
          target.status = "TARGET_HIT";
          target.exitPrice = num(matchedExit.price) || target.targetPrice;
          saveAutoTargets();
          playTradeSound("target");
          const pnl = (target.exitPrice - target.entryPrice) * target.shares * (target.action === "buy" ? 1 : -1);
          toast(`🎯 Bracket Target Hit! Sold ${target.shares} @ ₹${fmt(target.exitPrice)} (Profit: +₹${fmt(pnl, 0)})`, "ok", 8000);
          renderTab();
          updateChartTrades();
          continue;
        }

        const isExitCancelled = ["CAN", "CANCELLED", "REJ", "REJECTED"].includes(exitStatus);
        if (isExitCancelled && num(matchedExit.filled_quantity) === 0) {
          // If exit order cancelled on broker, check if user closed position manually
          const pos = S.positions.find((p) => matchesSameContract(p, target));
          if (!pos || num(pos.quantity) === 0) {
            target.status = "EXITED_MANUAL";
            target.exitPrice = target.ltp || target.targetPrice;
            saveAutoTargets();
            renderTab();
            updateChartTrades();
          }
        }
      }
    }
  }
}

async function placeAutoTargetExit(target, qtyToExit) {
  const exitSide = target.action === "buy" ? "S" : "B";
  const lot = lotSize(target.type);
  const payload = {
    instrument_key: target.instrumentKey,
    exchange_segment: target.exchangeSegment,
    symbol: target.symbol,
    underlying: target.underlying || S.activeIndex,
    strike: target.strike,
    option_type: target.type,
    side: exitSide,
    buy_sell: exitSide,
    quantity: qtyToExit,
    qty: qtyToExit,
    price: target.targetPrice,
    order_type: "LIMIT",
    product: S.product || "NRML",
    intent: "EXIT",
    mode: tradingMode(),
    lot_size: lot,
  };

  try {
    toast(`🎯 Bracket In Position! Placing ${exitSide === "S" ? "Sell" : "Buy"} Limit for ${qtyToExit} shares @ ₹${fmt(target.targetPrice)}…`, "info", 3000);
    const result = await postOrder(payload);
    lastUiOrderTime = Date.now();
    playTradeSound("target");
    const exitId = String(result?.nOrdNo ?? result?.order_id ?? result?.id ?? "");
    if (exitId) {
      target.exitOrderId = exitId;
    }
    target.status = "IN_POSITION";
    saveAutoTargets();
    toast(`🎯 Target Limit placed! Resting @ ₹${fmt(target.targetPrice)}.`, "ok", 6000);
    scheduleBooksRefresh(400);
    renderTab();
    updateChartTrades();
    return result;
  } catch (err) {
    playTradeSound("reject");
    toast(`Auto-Target exit placement failed: ${err.message}`, "error", 8000);
  }
}

/* ── Chart Quick Limit Order ───────────────────────────── */

function initChartQuickOrder() {
  $("cqo-close")?.addEventListener("click", closeChartQuickOrder);

  $("cqo-price-dec")?.addEventListener("click", () => {
    if (S.quickOrder) updateQuickOrderPrice(S.quickOrder.price - 0.05);
  });
  $("cqo-price-inc")?.addEventListener("click", () => {
    if (S.quickOrder) updateQuickOrderPrice(S.quickOrder.price + 0.05);
  });

  $("cqo-price")?.addEventListener("input", (e) => {
    const val = num(e.target.value);
    if (Number.isFinite(val) && val > 0 && S.quickOrder) {
      S.quickOrder.price = val;
      $("cqo-buy-price").textContent = `@ ₹${val.toFixed(2)}`;
      $("cqo-sell-price").textContent = `@ ₹${val.toFixed(2)}`;
      S.charts[S.quickOrder.pane]?.showOrderPreview(val);
      if (S.quickOrder.targetEnabled) {
        S.quickOrder.targetPrice = Math.round((val + S.quickOrder.targetPts) * 20) / 20;
        renderQuickOrderTarget();
        S.charts[S.quickOrder.pane]?.showTargetPreview(S.quickOrder.targetPrice, "TARGET");
      }
    }
  });
  $("cqo-price")?.addEventListener("change", (e) => {
    const val = num(e.target.value);
    if (Number.isFinite(val) && val > 0) {
      updateQuickOrderPrice(val);
    }
  });

  $("cqo-qty-dec")?.addEventListener("click", () => updateQuickOrderLots(-1));
  $("cqo-qty-inc")?.addEventListener("click", () => updateQuickOrderLots(1));

  // Auto-Target controls
  $("cqo-target-enable")?.addEventListener("change", (e) => {
    if (!S.quickOrder) return;
    S.quickOrder.targetEnabled = e.target.checked;
    try {
      localStorage.setItem("scalper-target-enabled", e.target.checked ? "true" : "false");
    } catch (_) {}
    renderQuickOrderTarget();
    if (S.quickOrder.targetEnabled) {
      S.charts[S.quickOrder.pane]?.showTargetPreview(S.quickOrder.targetPrice, "TARGET");
    } else {
      S.charts[S.quickOrder.pane]?.clearTargetPreview();
    }
  });

  $("cqo-target-dec")?.addEventListener("click", () => {
    if (!S.quickOrder) return;
    const newPts = Math.max(0.5, S.quickOrder.targetPts - 1);
    updateQuickOrderTargetPts(newPts);
  });

  $("cqo-target-inc")?.addEventListener("click", () => {
    if (!S.quickOrder) return;
    updateQuickOrderTargetPts(S.quickOrder.targetPts + 1);
  });

  $("cqo-target-price")?.addEventListener("input", (e) => {
    if (!S.quickOrder) return;
    const val = num(e.target.value);
    if (Number.isFinite(val) && val > 0) {
      S.quickOrder.targetPrice = val;
      const pts = Math.max(0.05, Math.round((val - S.quickOrder.price) * 20) / 20);
      S.quickOrder.targetPts = pts;
      try { localStorage.setItem("scalper-target-pts", String(pts)); } catch (_) {}
      renderQuickOrderTarget();
      if (S.quickOrder.targetEnabled) {
        S.charts[S.quickOrder.pane]?.showTargetPreview(val, "TARGET");
      }
    }
  });

  $("cqo-target-price")?.addEventListener("change", (e) => {
    if (!S.quickOrder) return;
    const val = num(e.target.value);
    if (Number.isFinite(val) && val > 0) {
      updateQuickOrderTargetPrice(val);
    }
  });

  $("cqo-buy-btn")?.addEventListener("click", () => submitQuickOrder("buy"));
  $("cqo-sell-btn")?.addEventListener("click", () => submitQuickOrder("sell"));

  const widget = $("chart-quick-order");
  if (widget) {
    widget.addEventListener("pointerdown", (e) => e.stopPropagation());
    widget.addEventListener("click", (e) => e.stopPropagation());
  }

  document.addEventListener("pointerdown", (event) => {
    if (!S.quickOrder) return;
    const w = $("chart-quick-order");
    if (w && (w === event.target || w.contains(event.target))) {
      return;
    }
    const isChartCanvas = event.target.closest(".chart-canvas");
    if (!isChartCanvas) {
      closeChartQuickOrder();
    }
  });
}

function bindChartClickEvents() {
  for (const pane of ["ce", "pe", "spot"]) {
    const container = $("chart-" + pane);
    if (!container) continue;
    let downX = 0;
    let downY = 0;
    let downTime = 0;

    container.addEventListener("pointerdown", (event) => {
      if (event.button !== 0) return;
      downX = event.clientX;
      downY = event.clientY;
      downTime = Date.now();
    });

    container.addEventListener("pointerup", (event) => {
      if (!downTime) return;
      const dx = Math.abs(event.clientX - downX);
      const dy = Math.abs(event.clientY - downY);
      const dt = Date.now() - downTime;
      downTime = 0;
      if (dx > 6 || dy > 6 || dt > 500) return;

      const chart = S.charts[pane];
      if (!chart || chart.drawingTrendlineId) return;

      if (pane === "spot") {
        toast("Click on the CALL (left) or PUT (right) chart to place a Limit order at that strike premium.", "info", 4000);
        return;
      }

      const rect = container.getBoundingClientRect();
      const x = event.clientX - rect.left;
      const y = event.clientY - rect.top;
      const price = chart.getPriceFromPixel(y, x);
      if (!price || price <= 0) return;

      openChartQuickOrder(pane, price, x, y);
    });
  }
}

function openChartQuickOrder(pane, price, x, y) {
  const chart = S.charts[pane];
  if (!chart) return;
  const type = pane === "ce" ? "CE" : "PE";
  const option = selectedOption(type);
  if (!option) {
    toast(`Select a ${type} strike from the option chain first.`, "warn", 3500);
    return;
  }

  for (const [p, c] of Object.entries(S.charts)) {
    if (p !== pane) {
      c?.clearOrderPreview?.();
      c?.clearTargetPreview?.();
    }
  }

  const lot = lotSize(type);
  let lotsCount = Math.max(1, Math.round(num(S.qty[type], 1)));
  if (S.unit[type] === "SHARES" && lot > 1) {
    lotsCount = Math.max(1, Math.round(lotsCount / lot));
  }

  let defaultPts = 2.0;
  try {
    const saved = localStorage.getItem("scalper-target-pts");
    if (saved) defaultPts = num(saved, 2.0);
  } catch (_) {}
  if (defaultPts <= 0) defaultPts = 2.0;

  let targetEnabled = true;
  try {
    if (localStorage.getItem("scalper-target-enabled") === "false") targetEnabled = false;
  } catch (_) {}

  const targetPrice = Math.round((price + defaultPts) * 20) / 20;

  S.quickOrder = {
    pane,
    type,
    option,
    price,
    lotsCount,
    lot,
    targetPts: defaultPts,
    targetPrice,
    targetEnabled,
  };

  const widget = $("chart-quick-order");
  if (!widget) return;

  $("cqo-title").textContent = `${S.activeIndex} ${fmtStrike(option.strike)} ${type}`;
  $("cqo-price").value = price.toFixed(2);
  $("cqo-buy-price").textContent = `@ ₹${price.toFixed(2)}`;
  $("cqo-sell-price").textContent = `@ ₹${price.toFixed(2)}`;
  renderQuickOrderQty();
  renderQuickOrderTarget();

  const frame = chart.container.closest(".chart-frame") || chart.container;
  frame.appendChild(widget);

  const frameRect = frame.getBoundingClientRect();
  const widgetWidth = 250;
  const widgetHeight = 160;

  let left = x - widgetWidth / 2;
  left = Math.max(10, Math.min(left, frameRect.width - widgetWidth - 10));

  let top = y - widgetHeight - 12;
  if (top < 10) {
    top = y + 14;
  }
  top = Math.max(10, Math.min(top, frameRect.height - widgetHeight - 10));

  widget.style.left = `${Math.round(left)}px`;
  widget.style.top = `${Math.round(top)}px`;
  widget.hidden = false;

  chart.showOrderPreview(price);
  if (targetEnabled) {
    chart.showTargetPreview(targetPrice, "TARGET");
  }
}

function renderQuickOrderQty() {
  if (!S.quickOrder) return;
  const { lotsCount, lot } = S.quickOrder;
  const shares = lotsCount * lot;
  $("cqo-qty-val").textContent = String(shares);
  $("cqo-lots-info").textContent = `${lotsCount} ${lotsCount === 1 ? "Lot" : "Lots"}`;
}

function renderQuickOrderTarget() {
  if (!S.quickOrder) return;
  const { targetPts, targetPrice, targetEnabled, lotsCount, lot } = S.quickOrder;
  const shares = lotsCount * lot;
  const pnl = Math.round(targetPts * shares);

  const enableEl = $("cqo-target-enable");
  if (enableEl) enableEl.checked = targetEnabled;

  const targetPriceEl = $("cqo-target-price");
  if (targetPriceEl) {
    targetPriceEl.value = targetPrice.toFixed(2);
    targetPriceEl.disabled = !targetEnabled;
  }

  const decBtn = $("cqo-target-dec");
  const incBtn = $("cqo-target-inc");
  if (decBtn) decBtn.disabled = !targetEnabled;
  if (incBtn) incBtn.disabled = !targetEnabled;

  const summaryEl = $("cqo-target-summary");
  if (summaryEl) summaryEl.style.opacity = targetEnabled ? "1" : "0.35";

  const ptsEl = $("cqo-target-pts");
  if (ptsEl) ptsEl.textContent = `+${targetPts.toFixed(2)} pts`;

  const pnlEl = $("cqo-target-pnl");
  if (pnlEl) pnlEl.textContent = `(Est. +₹${pnl.toLocaleString("en-IN")})`;
}

function updateQuickOrderTargetPts(newPts) {
  if (!S.quickOrder) return;
  const pts = Math.max(0.05, Math.round(num(newPts) * 20) / 20);
  S.quickOrder.targetPts = pts;
  S.quickOrder.targetPrice = Math.round((S.quickOrder.price + pts) * 20) / 20;
  try {
    localStorage.setItem("scalper-target-pts", String(pts));
  } catch (_) {}
  renderQuickOrderTarget();
  if (S.quickOrder.targetEnabled) {
    S.charts[S.quickOrder.pane]?.showTargetPreview(S.quickOrder.targetPrice, "TARGET");
  }
}

function updateQuickOrderTargetPrice(newTargetPrice) {
  if (!S.quickOrder) return;
  const tp = Math.max(0.05, Math.round(num(newTargetPrice) * 20) / 20);
  S.quickOrder.targetPrice = tp;
  const pts = Math.max(0.05, Math.round((tp - S.quickOrder.price) * 20) / 20);
  S.quickOrder.targetPts = pts;
  try {
    localStorage.setItem("scalper-target-pts", String(pts));
  } catch (_) {}
  renderQuickOrderTarget();
  if (S.quickOrder.targetEnabled) {
    S.charts[S.quickOrder.pane]?.showTargetPreview(tp, "TARGET");
  }
}

function updateQuickOrderPrice(newPrice) {
  if (!S.quickOrder) return;
  const price = Math.max(0.05, Math.round(num(newPrice) * 20) / 20);
  S.quickOrder.price = price;
  $("cqo-price").value = price.toFixed(2);
  $("cqo-buy-price").textContent = `@ ₹${price.toFixed(2)}`;
  $("cqo-sell-price").textContent = `@ ₹${price.toFixed(2)}`;
  S.charts[S.quickOrder.pane]?.showOrderPreview(price);

  if (S.quickOrder.targetEnabled) {
    S.quickOrder.targetPrice = Math.round((price + S.quickOrder.targetPts) * 20) / 20;
    renderQuickOrderTarget();
    S.charts[S.quickOrder.pane]?.showTargetPreview(S.quickOrder.targetPrice, "TARGET");
  }
}

function updateQuickOrderLots(delta) {
  if (!S.quickOrder) return;
  const newLots = Math.max(1, S.quickOrder.lotsCount + delta);
  S.quickOrder.lotsCount = newLots;
  S.qty[S.quickOrder.type] = S.unit[S.quickOrder.type] === "SHARES" ? newLots * S.quickOrder.lot : newLots;
  renderQuickOrderQty();
  renderQuickOrderTarget();
  renderOrderPad();
}

function closeChartQuickOrder() {
  if (S.quickOrder) {
    S.charts[S.quickOrder.pane]?.clearOrderPreview();
    S.charts[S.quickOrder.pane]?.clearTargetPreview();
    S.quickOrder = null;
  }
  const widget = $("chart-quick-order");
  if (widget) widget.hidden = true;
}

async function submitQuickOrder(action) {
  if (!S.quickOrder) return;
  const { type, lotsCount, lot, targetEnabled, targetPrice } = S.quickOrder;
  const inputPrice = num($("cqo-price")?.value);
  const price = Number.isFinite(inputPrice) && inputPrice > 0 ? inputPrice : S.quickOrder.price;
  const shares = lotsCount * lot;
  const autoTarget = (targetEnabled && action === "buy" && targetPrice > price) ? targetPrice : 0;

  closeChartQuickOrder();
  await onLimitTrade(action, type, price, shares, autoTarget);
  if (autoTarget > 0) {
    S.tab = "brackets";
    renderTab();
  }
}

async function onExit(instrumentKey) {
  const orderMode = tradingMode();
  if (!S.session?.authenticated) { openLogin(); toast("Sign in with Kotak before exiting a position.", "warn"); return; }
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
  if (tradingMode() !== orderMode) {
    toast("Trading mode changed; exit was not sent. Review the selected account and try again.", "warn", 5000);
    return;
  }

  try {
    await postOrder({
      instrument_key: instrumentKey,
      exchange_segment: position.exchange_segment,
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
      mode: orderMode,
    });
    lastUiOrderTime = Date.now();
    playTradeSound(side === "B" ? "buy" : "sell");
    toast("Live exit order submitted.", "ok");
    scheduleBooksRefresh(400);
  } catch (err) {
    playTradeSound("reject");
    toast(`Exit failed: ${err.message}`, "error", 6000);
  }
}

async function onSquareOffAll() {
  const orderMode = tradingMode();
  if (!S.session?.authenticated) { openLogin(); toast("Sign in with Kotak before squaring off positions.", "warn"); return; }
  const openPositions = S.positions.filter((p) => Math.abs(num(p.quantity)) > 0);
  if (!openPositions.length) {
    toast("No open positions to square off.", "warn");
    return;
  }
  const ok = await confirmAction({
    title: "Square off all",
    body: `Close <b>${openPositions.length}</b> open position${openPositions.length === 1 ? "" : "s"} at market? ` +
      `<span class="warn">This cannot be undone.</span>`,
    okLabel: "Square Off All",
    danger: true,
  });
  if (!ok) return;
  if (tradingMode() !== orderMode) {
    toast("Trading mode changed; square-off was not sent. Review the selected account and try again.", "warn", 5000);
    return;
  }

  try {
    for (const position of openPositions) {
      const quantity = Math.abs(num(position.quantity));
      if (!quantity || !position.instrument_key || !position.exchange_segment) continue;
      await postOrder({ mode: "live", instrument_key: position.instrument_key, exchange_segment: position.exchange_segment,
        symbol: position.symbol, side: position.quantity > 0 ? "S" : "B", quantity, product: position.product || S.product,
        order_type: "MARKET", price: 0 });
    }
    lastUiOrderTime = Date.now();
    playTradeSound("sell");
    toast("Live square-off orders submitted.", "ok");
    scheduleBooksRefresh(500);
  } catch (err) {
    playTradeSound("reject");
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

/* ── Login modal & version update ──────────────────────── */

let totpAccepted = false;
let loginBusy = false;
let versionCheckDone = false;

async function checkVersionUpdate() {
  if (versionCheckDone && S.latestRelease) {
    if (S.latestRelease.has_update) {
      showUpdateBanner(S.latestRelease);
    }
    return;
  }
  versionCheckDone = true;
  try {
    let data = null;
    try {
      data = await getJSON("/api/version/check");
    } catch {
      const res = await fetch("https://api.github.com/repos/akshaykhairmode/scalper/releases/latest", {
        headers: { Accept: "application/vnd.github.v3+json" },
      });
      if (res.ok) {
        const gh = await res.json();
        const latest = gh.tag_name || "";
        const current = S.boot?.version || "dev";
        data = {
          current_version: current,
          latest_version: latest,
          has_update: isNewerVersion(latest, current),
          release_url: gh.html_url || "https://github.com/akshaykhairmode/scalper/releases/latest",
          release_name: gh.name || latest,
          published_at: gh.published_at || "",
        };
      }
    }

    if (data && data.has_update && data.latest_version) {
      S.latestRelease = data;
      showUpdateBanner(data);
      updateFooterVersion(data);
    }
  } catch (err) {
    console.debug("version check skipped:", err);
  }
}

function showUpdateBanner(data) {
  const banner = $("login-update-banner");
  if (!banner) return;
  const ver = $("update-banner-version");
  if (ver) ver.textContent = data.latest_version;
  const link = $("update-banner-link");
  if (link && data.release_url) link.href = data.release_url;
  const copy = $("update-banner-copy");
  if (copy) {
    if (data.current_version && data.current_version !== "dev") {
      copy.innerHTML = `A new release <strong>${esc(data.latest_version)}</strong> is available (running <strong>${esc(data.current_version)}</strong>). <a href="${esc(data.release_url)}" target="_blank" rel="noreferrer">View release</a>`;
    } else {
      copy.innerHTML = `A new release <strong>${esc(data.latest_version)}</strong> is available on GitHub. <a href="${esc(data.release_url)}" target="_blank" rel="noreferrer">View release</a>`;
    }
  }
  banner.hidden = false;
}

function updateFooterVersion(data) {
  const footerVer = $("footer-version");
  if (!footerVer) return;
  const existing = footerVer.querySelector(".version-update-tag");
  if (existing) return;
  const tag = document.createElement("a");
  tag.className = "version-update-tag";
  tag.href = data.release_url;
  tag.target = "_blank";
  tag.rel = "noreferrer";
  tag.title = `New release ${data.latest_version} available! Click to view.`;
  tag.textContent = `update ${data.latest_version}`;
  footerVer.appendChild(tag);
}

function isNewerVersion(latest, current) {
  if (!latest) return false;
  if (!current || current === "dev" || current === "unknown") return false;
  const parse = (v) => {
    const clean = String(v).trim().replace(/^v/i, "").split(/[-+]/)[0];
    const parts = clean.split(".").map((n) => parseInt(n, 10));
    return parts.some((n) => isNaN(n)) ? null : parts;
  };
  const l = parse(latest);
  const c = parse(current);
  if (!l || !c) return false;
  for (let i = 0; i < Math.max(l.length, c.length); i++) {
    const lPart = l[i] ?? 0;
    const cPart = c[i] ?? 0;
    if (lPart !== cPart) return lPart > cPart;
  }
  return false;
}

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
  checkVersionUpdate();
  if (S.latestRelease?.has_update) {
    showUpdateBanner(S.latestRelease);
  }
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
  S.session = result.session_data || { authenticated: true };
  S.session.authenticated = true;
  updateAuthButton();
  renderHeader();
  closeLogin();
  toast("Session active. Feeds are connecting.", "ok");
  connectWebSocket();
  if (!S.expiry) loadMarket();
  refreshBooksFromAPI(true).catch((error) => console.warn("account refresh failed", error));
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
  clearBooksForMode("live");
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
  toast("Signed out. Sign in with Kotak to view account data or place orders.", "warn");
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

  const marginMetric = $("margin-metric");
  if (marginMetric) {
    marginMetric.addEventListener("click", refreshMargin);
    marginMetric.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        refreshMargin();
      }
    });
  }

  $("spot-strip").addEventListener("click", (event) => {
    const chip = event.target.closest(".spot-chip");
    if (!chip) return;
    const key = chip.dataset.index;
    if (!key || key === "INDIA VIX" || key === S.activeIndex) return;
    switchIndex(key);
  });

  $("index-select").addEventListener("change", (event) => switchIndex(event.target.value));
  $("expiry-select").addEventListener("change", (event) => switchExpiry(event.target.value));
  $("ce-strike-select").addEventListener("change", (event) => selectStrike(num(event.target.value), "CE"));
  $("pe-strike-select").addEventListener("change", (event) => selectStrike(num(event.target.value), "PE"));
  $("load-more-above").addEventListener("click", () => loadMoreChain("above"));
  $("load-more-below").addEventListener("click", () => loadMoreChain("below"));
  $("tf-group").addEventListener("click", (event) => {
    const button = event.target.closest("button[data-tf]");
    if (!button) return;
    S.timeframe = button.dataset.tf;
    renderToolbar();
    loadCharts();
  });
  $("refresh-button").addEventListener("click", refreshMarket);
  const soundToggle = $("sound-toggle");
  if (soundToggle) soundToggle.addEventListener("click", toggleSound);
  $("fullscreen-button").addEventListener("click", toggleFullscreen);
  $("strike-search-button").addEventListener("click", openStrikeModal);
  $("strike-target-select").addEventListener("change", (event) => {
    S.strikeSearchTarget = event.target.value === "PE" ? "PE" : "CE";
  });
  $("strike-close").addEventListener("click", closeStrikeModal);
  $("strike-modal").addEventListener("click", (event) => {
    if (event.target === $("strike-modal")) closeStrikeModal();
  });
  $("strike-search-input").addEventListener("input", applyStrikeFilter);
  $("strike-search-input").addEventListener("keydown", async (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    await selectSearchedStrike(event.currentTarget.value);
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
      if (action === "trades") {
        toggleAllChartTrades();
        return;
      }
      if (action === "sr") {
        const nextState = chart.toggleSR();
        button.classList.toggle("active", nextState);
        button.setAttribute("aria-pressed", nextState ? "true" : "false");
        try {
          localStorage.setItem("scalper-show-sr", String(nextState));
        } catch {
          // ignore
        }
        return;
      }
      if (action === "auto-tl") {
        const nextState = chart.toggleAutoTrendlines();
        button.classList.toggle("active", nextState);
        button.setAttribute("aria-pressed", nextState ? "true" : "false");
        try {
          localStorage.setItem("scalper-show-auto-tl", String(nextState));
        } catch {
          // ignore
        }
        toast(nextState ? "Auto trendlines enabled" : "Auto trendlines hidden", "ok", 1500);
        return;
      }
      if (action === "trendline") {
        for (const [otherPane, otherChart] of Object.entries(S.charts)) {
          if (otherChart && otherChart !== chart) {
            otherChart.cancelTrendline?.();
          }
        }
        chart.toggleTrendline(button, {
          onStart: () => toast("Click chart to set trendline start point (Esc to cancel)", "ok", 4000),
          onComplete: () => toast("Trendline placed (drag ends to adjust, right-click to delete)", "ok", 3500),
          onCancel: () => toast("Trendline cancelled", "warn", 2000),
        });
        return;
      }
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
    const current = rows.findIndex((row) => num(row.dataset.strike) === selectedOption(S.selected.type)?.strike);
    const next = Math.max(0, Math.min(rows.length - 1, current + (event.key === "ArrowUp" ? -1 : 1)));
    selectStrike(num(rows[next].dataset.strike), S.selected.type);
    event.preventDefault();
  });

  for (const button of $$("[data-qty-step]")) {
    button.addEventListener("click", () => stepQuantity(button.dataset.qtyStep, num(button.dataset.step)));
  }
  for (const button of $$(".unit-toggle")) {
    button.addEventListener("click", () => toggleQuantityUnit(button.dataset.unitToggle));
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
    if (exit) { onExit(exit.dataset.exit); return; }
    const exitBracket = event.target.closest("[data-exit-bracket]");
    if (exitBracket) { onExitBracket(exitBracket.dataset.exitBracket); return; }
    const cancelBracket = event.target.closest("[data-cancel-bracket]");
    if (cancelBracket) { onCancelBracket(cancelBracket.dataset.cancelBracket); return; }
    const clearBrackets = event.target.closest("#btn-clear-brackets");
    if (clearBrackets) { onClearFinishedBrackets(); return; }
    const cancelBtn = event.target.closest(".btn-cancel-order");
    if (cancelBtn) { onCancelOrder(cancelBtn.dataset.orderId); return; }
    const cancelAllBtn = event.target.closest("#btn-cancel-all-open");
    if (cancelAllBtn) { onCancelAllOpenOrders(); return; }
  });

  $("square-off-button").addEventListener("click", onSquareOffAll);
  $("auth-button").addEventListener("click", () => {
    if (S.session?.authenticated) logout();
    else openLogin();
  });
  $("login-close").addEventListener("click", closeLogin);
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
    if (S.quickOrder) {
      closeChartQuickOrder();
      return;
    }
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

  initChartQuickOrder();
  bindChartClickEvents();
}

function toggleQuantityUnit(type) {
  const lot = Math.max(1, lotSize(type) || 1);
  if (S.unit[type] === "LOTS") {
    S.unit[type] = "SHARES";
    S.qty[type] = Math.max(lot, Math.round(num(S.qty[type], 1)) * lot);
  } else {
    S.unit[type] = "LOTS";
    S.qty[type] = Math.max(1, Math.round(num(S.qty[type], lot) / lot));
  }
  renderOrderPad();
}

function stepQuantity(type, direction) {
  const lot = Math.max(1, lotSize(type) || 1);
  const step = S.unit[type] === "SHARES" ? lot : 1;
  const current = num(S.qty[type], step);
  S.qty[type] = Math.max(step, Math.round(current + direction * step));
  renderOrderPad();
}

function updateLotInput(type, value) {
  const lot = Math.max(1, lotSize(type) || 1);
  const minVal = S.unit[type] === "SHARES" ? lot : 1;
  S.qty[type] = Math.max(minVal, Math.round(num(value, minVal)));
  renderOrderPad();
}

async function switchIndex(key) {
  if (!key) return;
  try { localStorage.setItem("scalper-last-index", key); } catch (_) {}
  const requestID = ++S.chainRequestID;
  S.activeIndex = key;
  S.expiry = "";
  S.expiries = [];
  S.chain = [];
  S.chainMore = { above: false, below: false };
  S.chainLoading = false;
  S.selected = { type: "CE", CE: null, PE: null };
  S.chainScrolled = false;
  updateFeedSubscriptions();
  renderAll();
  try {
    const raw = await getJSON(`/api/expiries?index=${encodeURIComponent(key)}`);
    if (requestID !== S.chainRequestID || key !== S.activeIndex) return;
    S.expiries = normalizeExpiryList(raw);
    S.expiry = pickExpiry(S.expiries);
    renderToolbar();
    if (!S.expiry) {
      renderAll();
      toast(`No active expiries are available for ${key}.`, "warn", 6000);
      return;
    }
    const chainIndex = S.activeIndex;
    const chainExpiry = S.expiry;
    const center = num(S.spots.get(chainIndex)?.ltp);
    const chainRaw = await getJSON(chainURL({ center }));
    if (requestID !== S.chainRequestID || chainIndex !== S.activeIndex || chainExpiry !== S.expiry) return;
    acceptChainPage(chainRaw);
  } catch (error) {
    if (requestID !== S.chainRequestID) return;
    renderAll();
    toast(`Could not load ${key}: ${error.message}`, "error");
    return;
  }
  selectDefaultStrike();
  renderAll();
  loadCharts();
}

async function switchExpiry(date) {
  if (!date) return;
  try { localStorage.setItem(`scalper-last-expiry-${S.activeIndex}`, date); } catch (_) {}
  const requestID = ++S.chainRequestID;
  S.expiry = date;
  S.chain = [];
  S.chainMore = { above: false, below: false };
  S.chainLoading = false;
  S.selected = { type: "CE", CE: null, PE: null };
  S.chainScrolled = false;
  updateFeedSubscriptions();
  renderAll();
  try {
    const chainIndex = S.activeIndex;
    const chainExpiry = date;
    const center = num(S.spots.get(chainIndex)?.ltp);
    const chainRaw = await getJSON(chainURL({ center }));
    if (requestID !== S.chainRequestID || chainIndex !== S.activeIndex || chainExpiry !== S.expiry) return;
    acceptChainPage(chainRaw);
  } catch (error) {
    if (requestID !== S.chainRequestID) return;
    toast(`Could not load expiry: ${error.message}`, "error");
    return;
  }
  selectDefaultStrike();
  renderAll();
  loadCharts();
}

async function refreshMarket() {
  S.historicalCache.clear();
  pollLive({ refreshChain: true });
  loadCharts(true);
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
  let open;
  let close;
  if (activeCommodity()) {
    open = 9 * 60;
    const year = Number(values.year);
    const firstSunday = (month) => {
      const weekday = new Date(Date.UTC(year, month - 1, 1)).getUTCDay();
      return 1 + ((7 - weekday) % 7);
    };
    const secondSundayInMarch = firstSunday(3) + 7;
    const firstSundayInNovember = firstSunday(11);
    const dstStart = `${year}-03-${String(secondSundayInMarch).padStart(2, "0")}`;
    const dstEnd = `${year}-11-${String(firstSundayInNovember).padStart(2, "0")}`;
    // MCX non-agri contracts close at 23:30 during US daylight time and
    // 23:55 outside it. The exchange is closed on the transition Sundays.
    close = date >= dstStart && date < dstEnd ? 23 * 60 + 30 : 23 * 60 + 55;
  } else {
    open = toMinutes(S.boot.marketSession?.open, 9 * 60 + 15);
    close = toMinutes(S.boot.marketSession?.close, 15 * 60 + 30);
  }
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
    S.historicalCache.clear();
    loadCharts();
  } else {
    // The earlier chart fetch may have occurred before today's session ended.
    // Re-request once closed so the backend can fill and save the final bars.
    S.historicalCache.clear();
    loadCharts();
  }
}

/* ── Boot ──────────────────────────────────────────────── */

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
