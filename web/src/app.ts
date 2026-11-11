import { CandleChart } from "./charts";
import { generateQRCodeSVG } from "./utils/qrcode";
import { computePayoff, isOptionLeg, legRisk, payoffCurve } from "./utils/payoff";

const $: any = (id: string) => document.getElementById(id);
const $$: any = (selector: string, root: any = document) => [...root.querySelectorAll(selector)];

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

const numberFormatters = new Map<number, Intl.NumberFormat>();
const timeFormatter = new Intl.DateTimeFormat("en-GB", {
  hour: "2-digit", minute: "2-digit", second: "2-digit", timeZone: "Asia/Kolkata",
});
const clockFormatter = new Intl.DateTimeFormat("en-GB", {
  hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false, timeZone: "Asia/Kolkata",
});
const marketStateFormatter = new Intl.DateTimeFormat("en-GB", {
  timeZone: "Asia/Kolkata", weekday: "short", year: "numeric", month: "2-digit",
  day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23",
});

function istParts(ms) {
  const parts = marketStateFormatter.formatToParts(new Date(ms));
  const pick = (type) => parts.find((part) => part.type === type)?.value || "";
  return {
    day: `${pick("year")}-${pick("month")}-${pick("day")}`,
    weekday: pick("weekday"),
    minute: Number(pick("hour")) * 60 + Number(pick("minute")),
  };
}

// Equity derivatives trade until 15:40 IST; the cash closing auction keeps the
// index printing until ~15:35. Anything outside this window is a stale broker
// snapshot, not a real bar.
const NSE_CANDLE_CLOSE_MINUTE = 15 * 60 + 40;

function candleSessionAllows(timeSec, key = "") {
  if (!Number.isFinite(timeSec) || timeSec <= 0) return false;
  const parts = istParts(timeSec * 1000);
  if (parts.weekday === "Sat" || parts.weekday === "Sun") return false;
  const holidays = S.boot?.marketSession?.holidays || [];
  if (holidays.includes(parts.day)) return false;
  const commodity = String(key).toLowerCase().startsWith("mcx_");
  const open = commodity ? 9 * 60 : 9 * 60 + 15;
  const close = commodity ? 23 * 60 + 55 : NSE_CANDLE_CLOSE_MINUTE;
  return parts.minute >= open && parts.minute <= close;
}

// A broker replaying the previous close on a holiday produces a full day of
// flat bars. Those are not candles and should not stretch the chart.
function dropFlatNonTradingDays(candles) {
  if (!Array.isArray(candles) || candles.length < 2) return candles || [];
  const out = [];
  let prevClose = 0;
  let day = "";
  let group = [];
  const flush = () => {
    if (!group.length) return;
    const flat = group.every((c) => c.open === c.high && c.high === c.low && c.low === c.close);
    if (flat && prevClose > 0 && Math.abs(group[0].close - prevClose) < 1e-6) {
      group = [];
      return;
    }
    out.push(...group);
    prevClose = group[group.length - 1].close;
    group = [];
  };
  for (const candle of candles) {
    const candleDay = istParts(candle.time * 1000).day;
    if (day && candleDay !== day) flush();
    day = candleDay;
    group.push(candle);
  }
  flush();
  return out;
}

function fmt(value, digits = 2) {
  if (!Number.isFinite(value)) return "—";
  let formatter = numberFormatters.get(digits);
  if (!formatter) {
    formatter = new Intl.NumberFormat("en-IN", { minimumFractionDigits: digits, maximumFractionDigits: digits });
    numberFormatters.set(digits, formatter);
  }
  return formatter.format(value);
}

function fmtSigned(value, digits = 2) {
  if (!Number.isFinite(value)) return "—";
  const sign = value > 0 ? "+" : value < 0 ? "−" : "";
  return sign + fmt(Math.abs(value), digits);
}

function fmtMoney(value, digits = 0) {
  if (!Number.isFinite(value)) return "—";
  return `${value < 0 ? "−" : ""}₹${fmt(Math.abs(value), digits)}`;
}

function fmtMaxProfit(value) {
  if (value == null) return "—";
  if (Number(value) === Infinity) return "∞";
  return fmtMoney(num(value));
}

function fmtMaxLoss(value) {
  if (value == null) return "—";
  if (Number(value) === -Infinity) return "−∞";
  return fmtMoney(num(value));
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
  return timeFormatter.format(date);
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

const S: any = {
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
  aiScanner: null,
  aiScanRefreshTriggered: false,
  aiScannerIntervalStarted: false,
  selected: { type: "CE", CE: null, PE: null },
  strikeSearchTarget: "CE",
  qty: { CE: 1, PE: 1 },
  unit: { CE: "LOTS", PE: "LOTS" },
  product: "MIS",
  positions: [],
  orders: [],
  trades: [],
  holdings: [],
  limits: { available: 0, used: 0, total: 0, loaded: false, error: "" },
  orderValidity: { CE: "DAY", PE: "DAY" },
  risk: [],
  tab: "positions",
  mobileView: "ce",
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
  showTradesPanes: { ce: true, underlying: true, pe: true },
  candles: {},
  historicalCache: new Map(),
  chartLoadID: 0,
  quickOrder: null,
  autoTargets: [],
  marketOpen: null,
  focusedChart: null,
  soundEnabled: true,
  wsConnectedAt: null,
  lastWsEventTime: null,
};
(window as any).S = S;

let marketRetryTimer = null;
let chartRetryTimer = null;
let depthRenderTimer = null;

// scheduleDepthRender debounces re-renders of the depth tab so that a burst of
// tick events (one per instrument) only triggers a single DOM update.
function scheduleDepthRender() {
  if (depthRenderTimer !== null) return; // already scheduled
  depthRenderTimer = window.setTimeout(() => {
    depthRenderTimer = null;
    if (S.tab === "depth") renderTab();
  }, 200);
}

// mergeDepthData updates the cached depth ladder.
// If incoming bids/asks contain full depth (> 1 level), it replaces the ladder.
// If incoming bids/asks contain only 1 level (top-of-book / touch packet), it
// updates level 0 while preserving levels 1..4 so the depth ladder does not collapse
// and flicker between 1 and 5 rows.
function mergeDepthData(existing, incomingBids, incomingAsks) {
  const current = existing || { bids: [], asks: [] };
  let newBids = Array.isArray(current.bids) ? [...current.bids] : [];
  let newAsks = Array.isArray(current.asks) ? [...current.asks] : [];

  if (Array.isArray(incomingBids) && incomingBids.length > 0) {
    if (incomingBids.length > 1 || newBids.length <= 1) {
      newBids = incomingBids;
    } else {
      newBids[0] = incomingBids[0];
    }
  }

  if (Array.isArray(incomingAsks) && incomingAsks.length > 0) {
    if (incomingAsks.length > 1 || newAsks.length <= 1) {
      newAsks = incomingAsks;
    } else {
      newAsks[0] = incomingAsks[0];
    }
  }

  return { bids: newBids, asks: newAsks };
}

/* ── API helpers ───────────────────────────────────────── */

async function requestJSON(url: string, options: any = {}) {
  const headers: Record<string, string> = { Accept: "application/json", ...(options.body ? { "Content-Type": "application/json" } : {}) };
  try {
    const devId = localStorage.getItem("scalper_device_id");
    if (devId) headers["X-Device-ID"] = devId;
    const sessToken = localStorage.getItem("scalper_serve_session");
    if (sessToken) headers["X-Serve-Session"] = sessToken;
  } catch (_) {}
  if (options.headers) Object.assign(headers, options.headers);

  const response = await fetch(url, {
    credentials: "include",
    ...options,
    headers,
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
    const error: any = new Error(message);
    error.status = response.status;
    error.retryAfter = response.headers.get("Retry-After") || "";
    throw error;
  }
  return data;
}

const getJSON = (url) => requestJSON(url);
const postJSON = (url, body) => requestJSON(url, { method: "POST", body: JSON.stringify(body) });
const patchJSON = (url, body) => requestJSON(url, { method: "PATCH", body: JSON.stringify(body) });

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
		validity: payload.validity || "DAY",
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
  const oi = num(raw.oi ?? raw.open_int ?? raw.open_interest ?? raw.openInterest);
  const bids = Array.isArray(raw.bids) ? raw.bids : [];
  const asks = Array.isArray(raw.asks) ? raw.asks : [];
  const instrumentKey = raw.instrument_key ?? raw.instrumentKey ?? raw.key ?? "";
  if (instrumentKey && (bids.length || asks.length)) {
    if (!S.depths) S.depths = {};
    S.depths[instrumentKey] = mergeDepthData(S.depths[instrumentKey], bids, asks);
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

function acceptChainPage(page, append = false, direction = "") {
  const rows = normalizeChain(page);
  if (append) {
    const byStrike = new Map(S.chain.map((row) => [row.strike, row]));
    for (const row of rows) byStrike.set(row.strike, row);
    S.chain = [...byStrike.values()].sort((a: any, b: any) => a.strike - b.strike);
  } else {
    S.chain = rows;
  }
  const pageAbove = S.chain.length > 0 && Boolean(page?.has_more_above ?? page?.hasMoreAbove);
  const pageBelow = S.chain.length > 0 && Boolean(page?.has_more_below ?? page?.hasMoreBelow);
  if (append && direction === "above") {
    S.chainMore = { above: pageAbove, below: S.chainMore.below };
  } else if (append && direction === "below") {
    S.chainMore = { above: S.chainMore.above, below: pageBelow };
  } else {
    S.chainMore = { above: pageAbove, below: pageBelow };
  }
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
  // The list is ascending, so the top button extends it with lower strikes
  // and the bottom button with higher strikes.
  above.hidden = !S.chainMore.below;
  below.hidden = !S.chainMore.above;
  above.disabled = S.chainLoading || chainExpanding;
  below.disabled = S.chainLoading || chainExpanding;
  if (S.chainLoading) {
    (S.chainLoading === "above" ? above : below).textContent = "Loading…";
  } else {
    above.textContent = "Load more above";
    below.textContent = "Load more below";
  }
}

async function loadMoreChain(direction) {
  if (chainExpanding || S.chainLoading || !S.chain.length || !S.chainMore[direction === "above" ? "below" : "above"]) return;
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
    acceptChainPage(page, true, apiDirection);
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

let chainExpanding = false;
const CHAIN_PREMIUM_PAGE_SIZE = 10;
const CHAIN_PREMIUM_MAX_PAGES = 5;

function chainPremiumCutoff() {
  return num(S.boot?.chainPremiumCutoff, 30);
}

// Expand one side while the OTM premium at the edge is still above the cutoff.
function chainNeedsPremiumPage(direction) {
  if (!S.chain.length) return false;
  if (direction === "above" ? !S.chainMore.above : !S.chainMore.below) return false;
  const row = direction === "above" ? S.chain[S.chain.length - 1] : S.chain[0];
  const option = direction === "above" ? row?.call : row?.put;
  if (!option) return true;
  return num(option.ltp) > chainPremiumCutoff();
}

// The boot page is a fixed +/-9 strikes. Extend it outward until the call
// premium above (or put premium below) reaches the configured cutoff; the
// Load more buttons still cover everything beyond that.
async function expandChainToPremium() {
  const cutoff = chainPremiumCutoff();
  if (cutoff <= 0 || chainExpanding || !S.chain.length) return;
  const index = S.activeIndex;
  const expiry = S.expiry;
  if (!index || !expiry) return;
  const requestID = S.chainRequestID;
  chainExpanding = true;
  try {
    for (let page = 0; page < CHAIN_PREMIUM_MAX_PAGES; page++) {
      if (S.chainLoading) break;
      if (requestID !== S.chainRequestID || index !== S.activeIndex || expiry !== S.expiry) return;
      const directions = ["above", "below"].filter(chainNeedsPremiumPage);
      if (!directions.length) break;
      const pages = await Promise.all(directions.map(async (direction) => {
        const edge = direction === "above" ? S.chain[S.chain.length - 1] : S.chain[0];
        const raw = await getJSON(chainURL({
          center: num(edge?.strike),
          count: CHAIN_PREMIUM_PAGE_SIZE,
          direction,
        }));
        return { direction, raw };
      }));
      if (requestID !== S.chainRequestID || index !== S.activeIndex || expiry !== S.expiry) return;
      let grew = false;
      for (const { direction, raw } of pages) {
        const before = S.chain.length;
        acceptChainPage(raw, true, direction);
        if (S.chain.length > before) grew = true;
      }
      if (grew) renderChain(true);
      if (!grew) break;
    }
  } catch (error) {
    console.warn("chain premium expansion failed", error);
  } finally {
    chainExpanding = false;
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
  const symA = String(a.symbol || a.trading_symbol || a.tradingSymbol || a.trdSym || a.sym || "").trim().toUpperCase();
  const symB = String(b.symbol || b.trading_symbol || b.tradingSymbol || b.trdSym || b.sym || "").trim().toUpperCase();
  if (symA && symB && symA === symB) return true;
  const keyA = String(a.instrument_key || a.instrumentKey || "").trim();
  const keyB = String(b.instrument_key || b.instrumentKey || "").trim();
  if (keyA && keyB && keyA === keyB) return true;
  const tokA = String(a.tok || a.token || a.p_symbol || "").trim();
  const tokB = String(b.tok || b.token || b.p_symbol || "").trim();
  if (tokA && tokB && tokA === tokB) return true;
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
  let sellQueue = [];
  let realised = 0;
  let matchedQty = 0;

  for (const t of sorted) {
    const side = String(t.side || "").toUpperCase();
    const qty = num(t.quantity);
    const price = num(t.price);
    if (qty <= 0 || price <= 0) continue;

    if (side === "B" || side === "BUY") {
      let remainingBuy = qty;
      while (remainingBuy > 0 && sellQueue.length > 0) {
        const matchQty = Math.min(sellQueue[0].qty, remainingBuy);
        realised += matchQty * (sellQueue[0].price - price);
        matchedQty += matchQty;
        remainingBuy -= matchQty;
        sellQueue[0].qty -= matchQty;
        if (sellQueue[0].qty <= 0) {
          sellQueue.shift();
        }
      }
      if (remainingBuy > 0) {
        buyQueue.push({ qty: remainingBuy, price });
      }
    } else if (side === "S" || side === "SELL") {
      let remainingSell = qty;
      while (remainingSell > 0 && buyQueue.length > 0) {
        const matchQty = Math.min(buyQueue[0].qty, remainingSell);
        realised += matchQty * (price - buyQueue[0].price);
        matchedQty += matchQty;
        remainingSell -= matchQty;
        buyQueue[0].qty -= matchQty;
        if (buyQueue[0].qty <= 0) {
          buyQueue.shift();
        }
      }
      if (remainingSell > 0) {
        sellQueue.push({ qty: remainingSell, price });
      }
    }
  }

  // If FIFO could not match any executed legs, keep position-level fallback P&L
  if (matchedQty === 0) {
    return fallbackPnL;
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
  const rawRPnl = raw.rPnl ?? raw.rpnl ?? raw.realised_pnl ?? raw.realizedPnl;
  if (rawRPnl != null && num(rawRPnl) !== 0) {
    realisedPnL = num(rawRPnl);
  } else if (closedQty > 0) {
    const buyAvg = totalBought > 0 ? buyAmt / totalBought : 0;
    const sellAvg = totalSold > 0 ? sellAmt / totalSold : 0;
    realisedPnL = (sellAvg - buyAvg) * closedQty;
  } else if (quantity === 0 && (buyAmt > 0 || sellAmt > 0)) {
    realisedPnL = sellAmt - buyAmt;
  } else if (rawRPnl != null) {
    realisedPnL = num(rawRPnl);
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
  const rawSide = String(raw.side ?? raw.tt ?? raw.trdType ?? raw.trnsTp ?? raw.transactionType ?? raw.buy_sell ?? raw.buySell ?? "").trim().toUpperCase();
  const side = (rawSide === "B" || rawSide === "BUY") ? "B" : (rawSide === "S" || rawSide === "SELL") ? "S" : rawSide;
  return {
    id: raw.id ?? raw.client_order_id ?? raw.order_id ?? raw.broker_order_id ?? raw.nOrdNo ?? "—",
    symbol: raw.symbol ?? raw.trading_symbol ?? raw.tradingSymbol ?? raw.trdSym ?? raw.sym ?? raw.instrument_key ?? "",
    instrument_key: raw.instrument_key ?? raw.instrumentKey ?? "",
    side,
    quantity: num(raw.quantity ?? raw.qty ?? raw.fldQty ?? raw.fillSz),
    price: num(raw.price ?? raw.prc ?? raw.avgPrc ?? raw.fillPrc),
    status: raw.status ?? raw.ordSt ?? "",
    reason: raw.reason ?? raw.reject_reason ?? raw.rejRsn ?? "",
    timestamp: raw.timestamp ?? raw.created_at ?? raw.updated_at ?? raw.ordDtTm ?? raw.hsUpTm ?? raw.ordDt ?? 0,
    product: raw.product ?? raw.prod ?? "MIS",
    mode: raw.mode ?? "",
  };
}

function normalizeTrade(raw) {
  const rawSide = String(raw.side ?? raw.tt ?? raw.trdType ?? raw.trnsTp ?? raw.transactionType ?? raw.buy_sell ?? raw.buySell ?? "").trim().toUpperCase();
  const side = (rawSide === "B" || rawSide === "BUY") ? "B" : (rawSide === "S" || rawSide === "SELL") ? "S" : rawSide;
  return {
    id: raw.id ?? raw.trade_id ?? raw.trdId ?? raw.order_id ?? raw.nOrdNo ?? raw.exOrdId ?? raw.exchOrdId ?? raw.flId ?? "—",
    symbol: raw.symbol ?? raw.trading_symbol ?? raw.tradingSymbol ?? raw.trdSym ?? raw.sym ?? raw.instrument_key ?? "",
    side,
    quantity: num(raw.quantity ?? raw.qty ?? raw.fillSz ?? raw.fldQty ?? raw.flQty ?? raw.tradedQty),
    price: num(raw.price ?? raw.prc ?? raw.avgPrc ?? raw.fillPrc ?? raw.fldPrc ?? raw.flPrc ?? raw.tradePrice ?? raw.trdPrc),
    timestamp: raw.timestamp ?? raw.executed_at ?? raw.executedAt ?? raw.ordDtTm ?? raw.hsUpTm ?? raw.ordDt ?? raw.trdDt ?? 0,
  };
}

function normalizeHolding(raw) {
  if (!raw) return null;
  const symbol = raw.symbol || raw.trdSym || raw.tradingSymbol || raw.sym || raw.scripName || raw.trading_symbol || "";
  const quantity = num(raw.quantity ?? raw.hldQty ?? raw.holdingQuantity ?? raw.netQty ?? raw.qty);
  const average_price = num(raw.average_price ?? raw.upldPrc ?? raw.avgPrc ?? raw.buyAvg ?? raw.price);
  const ltp = num(raw.ltp ?? raw.mktPrc ?? raw.lastPrice ?? raw.close ?? average_price);
  const invested = quantity * average_price;
  const current_value = quantity * ltp;
  const pnl = raw.pnl != null ? num(raw.pnl) : (current_value - invested);
  const pnl_pct = invested > 0 ? ((current_value - invested) / invested) * 100 : 0;
  return {
    symbol,
    segment: raw.exchangeSegment || raw.exSeg || raw.segment || "nse_cm",
    quantity,
    average_price,
    ltp,
    invested,
    current_value,
    pnl,
    pnl_pct,
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
  initMobileNavigation();
  registerServiceWorker();
  initPWAInstallPrompt();
  updateClock();
  ensureAIScannerTicker();
  updateAIWinRateChip();
  window.setInterval(() => {
    updateClock();
    updateMarketPhase();
  }, 1000);
  window.setInterval(updateAIWinRateChip, 30000);

  const serveAuthModal = $("serve-auth-modal");
  const isTerminalLocked = serveAuthModal && !serveAuthModal.hidden && serveAuthModal.style.display !== "none";
  updateBiometricButtons();

  const urlParams = new URLSearchParams(window.location.search);
  const pairToken = urlParams.get("pair_token");
  if (pairToken) {
    setTimeout(() => {
      toast("Pairing link detected. Registering biometric passkey...", "ok", 4000);
      handleRegisterDevice("Mobile Device", pairToken);
    }, 300);
  } else if (isTerminalLocked) {
    tryAutoUnlockDevice();
  } else {
    startTerminalSession();
  }
}

let sessionRefreshTimer = null;

async function startTerminalSession() {
  checkVersionUpdate();
  await refreshSession();
  if (!S.session?.authenticated) {
    openLogin();
    return;
  }
  closeLogin();
  connectWebSocket();
  // loadMarket() -> loadFromAPI() already refreshes account books.
  loadMarket();
  if (!sessionRefreshTimer) {
    sessionRefreshTimer = window.setInterval(refreshSession, 30000);
  }
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
  const themeMeta = document.querySelector('meta[name="theme-color"]');
  if (themeMeta) {
    themeMeta.content = theme === "dark" ? "#0b0e14" : "#f8fafc";
  }
  for (const chart of (Object.values(S.charts) as any[])) chart.applyTheme();
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

function indicatorPrefs() {
  try {
    return {
      rsi: localStorage.getItem("scalper-show-rsi") === "true",
      divergence: localStorage.getItem("scalper-show-divergence") === "true",
      momentum: localStorage.getItem("scalper-show-momentum") === "true",
    };
  } catch (_) {
    return { rsi: false, divergence: false, momentum: false };
  }
}

function updateIndicatorButtonState() {
  const prefs = indicatorPrefs();
  const active = prefs.rsi || prefs.divergence || prefs.momentum;
  for (const button of $$('button[data-chart-action="ind"]')) {
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", active ? "true" : "false");
  }
}

function toggleIndicatorPopover(anchor) {
  const popover = $("indicator-popover");
  if (!popover) return;
  if (popover.style.display !== "none") {
    popover.style.display = "none";
    return;
  }
  const prefs = indicatorPrefs();
  const rsiBox = $("ind-rsi");
  const divBox = $("ind-divergence");
  const momBox = $("ind-momentum");
  if (rsiBox) rsiBox.checked = prefs.rsi;
  if (divBox) divBox.checked = prefs.divergence;
  if (momBox) momBox.checked = prefs.momentum;

  const rect = anchor.getBoundingClientRect();
  popover.style.display = "block";
  popover.style.position = "fixed";
  popover.style.top = `${Math.max(10, Math.min(window.innerHeight - 190, rect.bottom + 4))}px`;
  popover.style.left = `${Math.max(10, Math.min(window.innerWidth - 300, rect.right - 260))}px`;
}

function applyIndicatorPrefs() {
  const prefs = {
    rsi: Boolean($("ind-rsi")?.checked),
    divergence: Boolean($("ind-divergence")?.checked),
    momentum: Boolean($("ind-momentum")?.checked),
  };
  try {
    localStorage.setItem("scalper-show-rsi", String(prefs.rsi));
    localStorage.setItem("scalper-show-divergence", String(prefs.divergence));
    localStorage.setItem("scalper-show-momentum", String(prefs.momentum));
  } catch (_) {}
  for (const chart of [S.charts.ce, S.charts.spot, S.charts.pe]) {
    chart?.toggleRSI?.(prefs.rsi);
    chart?.toggleDivergence?.(prefs.divergence);
    chart?.toggleMomentumCandles?.(prefs.momentum);
  }
  updateIndicatorButtonState();
}

function buildCharts() {
  const savedSR = localStorage.getItem("scalper-show-sr") === "true";
  const savedAutoTL = localStorage.getItem("scalper-show-auto-tl") === "true";
  const savedPatterns = localStorage.getItem("scalper-show-patterns") === "true";
  // TX visibility is independent per chart. Fall back to the legacy global key
  // so existing users keep the visibility they had before the split.
  const legacyTrades = localStorage.getItem("scalper-show-trades") !== "false";
  const savedTradesFor = (pane) => {
    const value = localStorage.getItem(`scalper-show-trades-${pane}`);
    return value === null ? legacyTrades : value !== "false";
  };
  const savedTradesCE = savedTradesFor("ce");
  const savedTradesUnderlying = savedTradesFor("underlying");
  const savedTradesPE = savedTradesFor("pe");
  S.showTradesPanes = { ce: savedTradesCE, underlying: savedTradesUnderlying, pe: savedTradesPE };
  const savedMACE = localStorage.getItem("scalper-ma-ce") === "true";
  const savedMAUnderlying = (localStorage.getItem("scalper-ma-underlying") ?? localStorage.getItem("scalper-ma-spot")) === "true";
  const savedMAPE = localStorage.getItem("scalper-ma-pe") === "true";
  const savedDOM = localStorage.getItem("scalper-show-dom") !== "false";
  const savedRSI = localStorage.getItem("scalper-show-rsi") === "true";
  const savedDivergence = localStorage.getItem("scalper-show-divergence") === "true";
  const savedMomentum = localStorage.getItem("scalper-show-momentum") === "true";

  const indicatorOptions = { showRSI: savedRSI, showDivergence: savedDivergence, showMomentum: savedMomentum };
  S.charts.ce = new CandleChart($("chart-ce"), { showVolume: true, showSR: savedSR, showTrades: savedTradesCE, showAutoTrendlines: savedAutoTL, showPatterns: savedPatterns, showMA: savedMACE, showDepth: savedDOM, onLevelDrop: (info) => handleChartLevelDrop("ce", info), onLevelRemove: (info) => handleChartLevelRemove("ce", info), ...indicatorOptions });
  S.charts.spot = new CandleChart($("chart-spot"), { showVolume: true, showSR: savedSR, showTrades: savedTradesUnderlying, showAutoTrendlines: savedAutoTL, showPatterns: savedPatterns, showMA: savedMAUnderlying, showDepth: savedDOM, onLevelDrop: (info) => handleChartLevelDrop("spot", info), onLevelRemove: (info) => handleChartLevelRemove("spot", info), ...indicatorOptions });
  S.charts.pe = new CandleChart($("chart-pe"), { showVolume: true, showSR: savedSR, showTrades: savedTradesPE, showAutoTrendlines: savedAutoTL, showPatterns: savedPatterns, showMA: savedMAPE, showDepth: savedDOM, onLevelDrop: (info) => handleChartLevelDrop("pe", info), onLevelRemove: (info) => handleChartLevelRemove("pe", info), ...indicatorOptions });

  Object.defineProperty(S.charts, "underlying", {
    get() { return S.charts.spot; },
    set(val) { S.charts.spot = val; },
    enumerable: false,
    configurable: true,
  });

  $$('button[data-chart-action="sr"]').forEach((b) => {
    b.classList.toggle("active", savedSR);
    b.setAttribute("aria-pressed", savedSR ? "true" : "false");
  });
  $$('button[data-chart-action="auto-tl"]').forEach((b) => {
    b.classList.toggle("active", savedAutoTL);
    b.setAttribute("aria-pressed", savedAutoTL ? "true" : "false");
  });
  $$('button[data-chart-action="patterns"]').forEach((b) => {
    b.classList.toggle("active", savedPatterns);
    b.setAttribute("aria-pressed", savedPatterns ? "true" : "false");
  });
  $$('button[data-chart-action="dom"]').forEach((b) => {
    b.classList.toggle("active", savedDOM);
    b.setAttribute("aria-pressed", savedDOM ? "true" : "false");
    b.title = "Order Flow DOM · Fixed-interval Buyer & Seller volume ladder on chart";
  });
  $$('button[data-chart-action="ma"]').forEach((b) => {
    const pane = b.dataset.chart;
    const active = (pane === "ce" && savedMACE) ||
                   ((pane === "spot" || pane === "underlying") && savedMAUnderlying) ||
                   (pane === "pe" && savedMAPE);
    b.classList.toggle("active", active);
    b.setAttribute("aria-pressed", active ? "true" : "false");
  });
  updateIndicatorButtonState();

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
    const pane = tradesPaneKey(button.dataset.chart);
    const active = Boolean(S.showTradesPanes[pane]);
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", active ? "true" : "false");
  }

  for (const button of $$(".chart-tools button[data-chart-action='ma']")) {
    const pane = button.dataset.chart;
    const active = (pane === "ce" && savedMACE) ||
                   ((pane === "spot" || pane === "underlying") && savedMAUnderlying) ||
                   (pane === "pe" && savedMAPE);
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", active ? "true" : "false");
  }

  initChartVisibility();
}

function setFocusedChart(pane) {
  if (S.focusedChart === pane) pane = null;
  S.focusedChart = pane || null;
  const terminal = $("charts-row").closest(".terminal");
  terminal.classList.toggle("chart-focused", Boolean(S.focusedChart));
  $("charts-row").classList.toggle("expanded", Boolean(S.focusedChart));
  for (const article of $$(".charts-row .pane")) {
    const isTarget = article.dataset.pane === S.focusedChart ||
      (S.focusedChart === "underlying" && (article.dataset.pane === "spot" || article.dataset.paneAlias === "spot")) ||
      (S.focusedChart === "spot" && (article.dataset.pane === "underlying" || article.dataset.pane === "spot"));
    article.classList.toggle("expanded", isTarget);
  }
  for (const button of $$(".chart-tools button[data-chart-action='expand']")) {
    const btnChart = button.dataset.chart;
    const active = btnChart === S.focusedChart ||
      (S.focusedChart === "underlying" && btnChart === "spot") ||
      (S.focusedChart === "spot" && btnChart === "underlying");
    button.classList.toggle("active", active);
    button.title = active ? "Restore chart layout (Esc)" : "Expand chart";
    button.setAttribute("aria-pressed", active ? "true" : "false");
  }
  requestAnimationFrame(() => {
    for (const chart of (Object.values(S.charts) as any[])) chart?.schedule?.();
  });
}

function getVisiblePanes() {
  const visible = [];
  const panes = [
    { key: "ce", el: document.querySelector('.pane[data-pane="ce"]') },
    { key: "underlying", el: document.querySelector('.pane[data-pane="underlying"], .pane[data-pane-alias="underlying"], .pane[data-pane="spot"]') },
    { key: "pe", el: document.querySelector('.pane[data-pane="pe"]') },
  ];
  for (const { key, el } of panes) {
    if (el && !el.classList.contains("pane-hidden")) {
      visible.push(key);
    }
  }
  return visible;
}

function isPaneVisible(paneKey) {
  const normKey = (paneKey === "spot" ? "underlying" : paneKey);
  const el = document.querySelector(`.pane[data-pane="${normKey}"], .pane[data-pane-alias="${normKey}"]${normKey === "underlying" ? ', .pane[data-pane="spot"]' : ''}`);
  return el ? !el.classList.contains("pane-hidden") : false;
}

function setPaneVisible(paneKey, visible) {
  const normKey = (paneKey === "spot" ? "underlying" : paneKey);
  const currentVisible = getVisiblePanes();
  const isCurrentlyVis = currentVisible.includes(normKey);

  if (!visible && isCurrentlyVis && currentVisible.length <= 1) {
    toast("At least one chart must remain visible", "warn", 2000);
    return false;
  }

  const el = document.querySelector(`.pane[data-pane="${normKey}"], .pane[data-pane-alias="${normKey}"]${normKey === "underlying" ? ', .pane[data-pane="spot"]' : ''}`);
  if (el) {
    el.classList.toggle("pane-hidden", !visible);
  }

  if (!visible && (S.focusedChart === normKey || (normKey === "underlying" && S.focusedChart === "spot"))) {
    setFocusedChart(null);
  }

  updateChartVisibilityUI();
  saveChartVisibility();

  requestAnimationFrame(() => {
    for (const [key, chart] of Object.entries(S.charts) as [string, any][]) {
      if (chart) {
        if (typeof chart.resize === "function") chart.resize();
        else if (typeof chart.schedule === "function") chart.schedule();
        if (chart.chart && typeof chart.chart.resize === "function") chart.chart.resize();
      }
    }
  });

  return true;
}

function updateChartVisibilityUI() {
  const visible = getVisiblePanes();
  const count = Math.max(1, visible.length);
  const row = $("charts-row");
  if (row) {
    row.dataset.visiblePanes = String(count);
    row.style.setProperty("--chart-cols", String(count));
    row.style.gridTemplateColumns = `repeat(${count}, minmax(0, 1fr))`;
  }

  $$("#chart-toggle-group button, .chart-vis-btn").forEach((btn) => {
    const pane = btn.dataset.chartToggle || btn.dataset.chartVis;
    const norm = (pane === "spot" ? "underlying" : pane);
    const isVis = visible.includes(norm);
    btn.classList.toggle("active", isVis);
    btn.setAttribute("aria-pressed", isVis ? "true" : "false");
    const indicator = btn.querySelector(".vis-indicator");
    if (indicator) {
      indicator.textContent = isVis ? "✓" : "";
    }
  });
}

function saveChartVisibility() {
  const visible = getVisiblePanes();
  try {
    localStorage.setItem("scalper-chart-visibility", JSON.stringify(visible));
    localStorage.setItem("scalper-visible-ce", String(visible.includes("ce")));
    localStorage.setItem("scalper-visible-underlying", String(visible.includes("underlying")));
    localStorage.setItem("scalper-visible-spot", String(visible.includes("underlying")));
    localStorage.setItem("scalper-visible-pe", String(visible.includes("pe")));
  } catch (_) {}
}

function initChartVisibility() {
  let visiblePanes = ["ce", "underlying", "pe"];
  try {
    const raw = localStorage.getItem("scalper-chart-visibility");
    if (raw) {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed) && parsed.length > 0) {
        visiblePanes = parsed.map((p) => (p === "spot" ? "underlying" : p));
      }
    } else {
      const visCE = localStorage.getItem("scalper-visible-ce");
      const visUnder = localStorage.getItem("scalper-visible-underlying") ?? localStorage.getItem("scalper-visible-spot");
      const visPE = localStorage.getItem("scalper-visible-pe");
      if (visCE !== null || visUnder !== null || visPE !== null) {
        const list = [];
        if (visCE !== "false") list.push("ce");
        if (visUnder !== "false") list.push("underlying");
        if (visPE !== "false") list.push("pe");
        if (list.length > 0) visiblePanes = list;
      }
    }
  } catch (_) {}

  // Enforce at least 1 visible pane
  if (!visiblePanes || visiblePanes.length === 0) {
    visiblePanes = ["ce", "underlying", "pe"];
  }

  const allPanes = ["ce", "underlying", "pe"];
  for (const pane of allPanes) {
    const el = document.querySelector(`.pane[data-pane="${pane}"], .pane[data-pane-alias="${pane}"]${pane === "underlying" ? ', .pane[data-pane="spot"]' : ''}`);
    if (el) {
      el.classList.toggle("pane-hidden", !visiblePanes.includes(pane));
    }
  }
  updateChartVisibilityUI();
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
  const chip = $("session-chip");
  if (S.session?.authenticated) {
    if (button) {
      button.textContent = "Logout";
      button.classList.add("logged-in");
    }
    if (chip) chip.hidden = false;
    updateSessionCountdown();
  } else {
    if (button) {
      button.textContent = "Login";
      button.classList.remove("logged-in");
    }
    if (chip) chip.hidden = true;
  }
}

function updateSessionCountdown() {
  const chip = $("session-chip");
  const timer = $("session-countdown");
  if (!chip || !timer || !S.session?.authenticated) {
    if (chip) chip.hidden = true;
    return;
  }

  let rem = 0;
  if (S.session.expires_at) {
    const expMs = Date.parse(S.session.expires_at);
    if (!isNaN(expMs)) {
      rem = Math.max(0, Math.floor((expMs - Date.now()) / 1000));
    }
  } else if (typeof S.session.expires_in_seconds === "number") {
    rem = Math.max(0, S.session.expires_in_seconds);
  } else if (typeof S.session.expires_in === "number") {
    rem = Math.max(0, S.session.expires_in * 60);
  }

  if (rem <= 0) {
    timer.textContent = "EXPIRED";
    chip.classList.remove("expiring-soon");
    chip.classList.add("expired");
    if (S.session.authenticated) {
      S.session.authenticated = false;
      clearBooksForMode("live");
      updateAuthButton();
      openLogin();
      toast("Kotak session expired (3h TTL). Please re-authenticate.", "warn");
    }
    return;
  }

  const hours = Math.floor(rem / 3600);
  const minutes = Math.floor((rem % 3600) / 60);
  const seconds = rem % 60;
  const pad = (n) => String(n).padStart(2, "0");
  timer.textContent = `${pad(hours)}:${pad(minutes)}:${pad(seconds)}`;

  if (rem <= 900) {
    chip.classList.add("expiring-soon");
    chip.classList.remove("expired");
  } else {
    chip.classList.remove("expiring-soon");
    chip.classList.remove("expired");
  }
}

/* ── Market loading ────────────────────────────────────── */

async function loadMarket() {
  if (!S.session?.authenticated) return;
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
  await ensureSavedStrikesLoaded();
  await refreshBooksFromAPI();
  selectDefaultStrike();
  S.chainScrolled = false;
  renderAll();
  loadCharts();
  expandChainToPremium();
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
  S.lastBooksRefresh = Date.now();
  const [positions, orders, trades, limits, brackets, holdings] = await Promise.allSettled([
    includePositions ? getJSON("/api/broker/positions") : Promise.resolve(null),
    getJSON("/api/broker/orders"),
    getJSON("/api/broker/trades"),
    getJSON("/api/broker/limits"),
    getJSON("/api/brackets"),
    getJSON("/api/broker/holdings"),
  ]);
  if (S.bookMode !== "live" || tradingMode() !== S.bookMode) return;
  if (orders.status === "fulfilled") {
    S.orders = toArray(orders.value).map(normalizeOrder);
  }
  if (trades.status === "fulfilled") {
    S.trades = toArray(trades.value).map(normalizeTrade);
  }
  if (holdings.status === "fulfilled") {
    S.holdings = toArray(holdings.value).map(normalizeHolding).filter(Boolean);
    const badge = $("holdings-badge");
    if (badge) {
      badge.textContent = String(S.holdings.length);
      badge.hidden = S.holdings.length === 0;
    }
  }
  if (brackets.status === "fulfilled" && brackets.value) {
    applyServerBrackets(brackets.value);
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
  scheduleChartTrades();
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
  if (Array.isArray(value?.holdings)) return value.holdings;
  if (Array.isArray(value?.history)) return value.history;
  return [];
}

let liveTimer = null;
let underlyingTimer = null;
let booksRefreshTimer = null;
let chainPollCounter = 0;
function startLivePolling() {
  if (!S.session?.authenticated) return;
  if (!liveTimer) {
    // Safety-net poll only; order/position/margin events refresh immediately
    // via scheduleBooksRefresh(). This cuts broker REST traffic substantially.
    liveTimer = window.setInterval(() => {
      if (document.hidden || !S.session?.authenticated) return;
      refreshBooksFromAPI(true).catch((error) => console.warn("account refresh failed", error));
    }, 60000);
  }
  // The Scrip Master refresh can finish after the first page quote request.
  // Poll underlying prices independently so new spot/future tokens are picked
  // up and subscribed without requiring a manual Refresh click.
  if (!underlyingTimer) {
    underlyingTimer = window.setInterval(async () => {
      if (document.hidden || !S.session?.authenticated || !S.marketOpen) return;
      try {
        applyIndexQuotes(await getJSON("/api/indices"));
        updateFeedSubscriptions();
        renderHeader();
        // Periodically refresh option chain every 60s during open market hours
        chainPollCounter++;
        if (chainPollCounter >= 2 && S.activeIndex && S.expiry) {
          chainPollCounter = 0;
          const center = num(S.spots.get(S.activeIndex)?.ltp);
          const chainRaw = await getJSON(chainURL({ center }));
          acceptChainPage(chainRaw, true);
          renderChain(true);
          renderPaneHeaders();
          renderPCR();
        }
      } catch (error) {
        console.warn("underlying/chain quote refresh failed", error);
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
  if (!S.session?.authenticated) return;
  const proto = location.protocol === "https:" ? "wss" : "ws";
  try {
    const ws = new WebSocket(`${proto}://${location.host}/ws`);
    S.ws = ws;
    ws.addEventListener("open", () => {
      S.wsFailCount = 0;
      S.wsConnectedAt = Date.now();
      S.lastWsEventTime = Date.now();
      hideServerDownModal();
      updateFeedSubscriptions();
    });
    ws.addEventListener("message", (event) => {
      S.lastWsEventTime = Date.now();
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

function handleTickData(data, { batch = false } = {}) {
  if (!data || !data.key) return null;
  updateLiveCandle(data.key, data.ltp, data.timestamp, data.volume);
  if (data.bids || data.asks) {
    if (!S.depths) S.depths = {};
    S.depths[data.key] = mergeDepthData(S.depths[data.key], data.bids, data.asks);
    const depth = S.depths[data.key];
    if (chartInstrument("CE") === data.key) {
      S.charts.ce?.setDepth?.(depth);
    }
    if (chartInstrument("PE") === data.key) {
      S.charts.pe?.setDepth?.(depth);
    }
    if (activeSpotInstrument() === data.key) {
      S.charts.spot?.setDepth?.(depth);
    }
    const ceKey = selectedOption("CE")?.instrumentKey;
    const peKey = selectedOption("PE")?.instrumentKey;
    if (S.tab === "depth" && (data.key === ceKey || data.key === peKey)) {
      scheduleDepthRender();
    }
  }
  for (const spot of S.spots.values()) {
    if (spot.spotKey !== data.key) continue;
    spot.ltp = num(data.ltp, spot.ltp);
    spot.prevClose = num(data.prev_close, spot.prevClose);
    spot.change = num(data.change, spot.ltp - spot.prevClose);
    spot.changePct = num(data.change_pct,
      spot.prevClose ? ((spot.ltp - spot.prevClose) / spot.prevClose) * 100 : 0);
    if (!batch) {
      renderHeader();
      renderPaneHeaders();
      renderOrderPad();
    }
    return { spot: true };
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
  if (updatedPosition && !batch) {
    renderHeader();
    updateLiveTabQuotes(data.key);
    scheduleChartTrades();
  }
  let hadChain = false;
  let hadSelected = false;
  const row = S.chain.find((r) => r.call?.instrumentKey === data.key || r.put?.instrumentKey === data.key);
  if (row) {
    const opt = row.call?.instrumentKey === data.key ? row.call : row.put;
    opt.ltp = num(data.ltp, opt.ltp);
    opt.prevClose = num(data.prev_close, opt.prevClose);
    opt.bid = num(data.bid, opt.bid);
    opt.ask = num(data.ask, opt.ask);
    opt.volume = num(data.volume, opt.volume);
    if (data.oi != null && Number(data.oi) >= 0 && Number(data.oi) <= 100000000) {
      opt.oi = num(data.oi, opt.oi);
    }
    if (data.bids || data.asks) {
      const merged = mergeDepthData({ bids: opt.bids, asks: opt.asks }, data.bids, data.asks);
      opt.bids = merged.bids;
      opt.asks = merged.asks;
    }
    opt.quoteAvailable = opt.ltp > 0;
    opt.change = opt.ltp - opt.prevClose;
    opt.changePct = opt.prevClose ? ((opt.ltp - opt.prevClose) / opt.prevClose) * 100 : 0;
    const type = row.call?.instrumentKey === data.key ? "CE" : "PE";
    const cell = $("chain-body")?.querySelector(`button[data-select="${type}"][data-strike="${row.strike}"]`);
    if (cell) {
      cell.innerHTML = `<span class="chain-ltp">${fmt(opt.ltp)}</span><span class="chain-volume">VOL ${fmtCompact(opt.volume)}</span>`;
      cell.title = `${fmt(opt.ltp)} · ${fmtSigned(opt.changePct)}% · Vol ${fmtCompact(opt.volume)}${opt.oi ? ` · OI ${fmtCompact(opt.oi)}` : ""}`;
      cell.classList.toggle("up", opt.changePct > 0);
      cell.classList.toggle("down", opt.changePct < 0);
      hadChain = true;
    }
    if (!batch) renderPCR();
    if (selectedOption(type)?.instrumentKey === data.key) {
      if (!batch) {
        renderPaneHeaders();
        renderOrderPad();
      }
      hadSelected = true;
    }
  } else {
    for (const type of ["CE", "PE"]) {
      const option = selectedOption(type);
      if (option?.instrumentKey !== data.key) continue;
      updateOptionFromTick(option, data);
      hadSelected = true;
    }
    if (hadSelected && !batch) {
      renderPaneHeaders();
      renderOrderPad();
    }
  }
  return { position: updatedPosition, chain: hadChain, selected: hadSelected };
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
  if (event.type === "bracket_update") {
    handleBracketUpdate(data);
    return;
  }
  if (event.type === "ai_call_outcome" || event.type === "ai_call_new" || event.type === "ai_scanner_status") {
    handleAICallEvent(event.type, data);
    return;
  }
  if (event.type === "broker_order" || event.type === "broker_position") {
    if (event.type === "broker_order" && data) {
      const norm = normalizeOrder(data);
      if (norm.id && norm.id !== "—") {
        if (!S.orders) S.orders = [];
        const existingIdx = S.orders.findIndex((o) => String(o.id) === String(norm.id));
        if (existingIdx !== -1) {
          S.orders[existingIdx] = { ...S.orders[existingIdx], ...norm };
        } else {
          S.orders.unshift(norm);
        }
      }
      handleBrokerOrderSound(data);
      checkAutoTargets(data);
      scheduleChartTrades();
      renderTab();
    }
    scheduleBooksRefresh(400);
    return;
  }
  if (event.type === "tick") {
    handleTickData(data);
    return;
  }
  if (event.type === "ticks" && Array.isArray(data)) {
    let hadSpot = false;
    let hadPosition = false;
    let hadSelected = false;
    let hadChain = false;
    const posKeys = new Set();
    for (const tick of data) {
      const res = handleTickData(tick, { batch: true });
      if (res?.spot) hadSpot = true;
      if (res?.position) {
        hadPosition = true;
        if (tick.key) posKeys.add(tick.key);
      }
      if (res?.selected) hadSelected = true;
      if (res?.chain) hadChain = true;
    }
    if (hadSpot || hadPosition) {
      renderHeader();
    }
    if (hadSpot || hadSelected) {
      renderPaneHeaders();
      renderOrderPad();
    }
    if (hadPosition) {
      for (const k of posKeys) {
        updateLiveTabQuotes(k);
      }
      scheduleChartTrades();
    }
    if (hadChain) {
      renderPCR();
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
    renderHeader();
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
  const keys = [
    selectedOption("CE")?.instrumentKey,
    selectedOption("PE")?.instrumentKey,
    ...S.positions.map((position) => position.instrument_key),
    ...[...S.spots.values()].map((spot) => spot.spotKey),
  ].filter((k) => typeof k === "string" && k.includes("|") && !k.endsWith("|"));
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

function computePCR() {
  if (!Array.isArray(S.chain) || !S.chain.length) return null;
  let totalCallOI = 0;
  let totalPutOI = 0;
  let totalCallVol = 0;
  let totalPutVol = 0;

  for (const row of S.chain) {
    if (row.call) {
      totalCallOI += num(row.call.oi);
      totalCallVol += num(row.call.volume);
    }
    if (row.put) {
      totalPutOI += num(row.put.oi);
      totalPutVol += num(row.put.volume);
    }
  }

  let pcrOI = null;
  if (totalCallOI > 0 && totalPutOI > 0) {
    const ratio = totalPutOI / totalCallOI;
    if (Number.isFinite(ratio) && ratio >= 0.01 && ratio <= 50) {
      pcrOI = ratio;
    }
  }

  let pcrVol = null;
  if (totalCallVol > 0 && totalPutVol > 0) {
    const ratio = totalPutVol / totalCallVol;
    if (Number.isFinite(ratio) && ratio >= 0.01 && ratio <= 50) {
      pcrVol = ratio;
    }
  }

  return {
    pcrOI,
    pcrVol,
    totalCallOI,
    totalPutOI,
    totalCallVol,
    totalPutVol,
  };
}

function computeStrikePCR(strike) {
  if (!strike || !Array.isArray(S.chain) || !S.chain.length) return null;
  const numStrike = normalizeStrike(strike);
  if (!numStrike) return null;
  const row = S.chain.find((r) => {
    const s = normalizeStrike(r.strike);
    return s === numStrike || Math.abs(s - numStrike) < 0.01;
  });
  if (!row) return null;
  const callOI = num(row.call?.oi);
  const putOI = num(row.put?.oi);
  const callVol = num(row.call?.volume);
  const putVol = num(row.put?.volume);

  // A strike PCR is only meaningful when both legs have OI. Never invent a
  // sentinel ratio (that used to render a fake "PCR 50.00" in the header).
  let pcrOI = null;
  if (callOI > 0 && putOI > 0) {
    const ratio = putOI / callOI;
    if (Number.isFinite(ratio) && ratio >= 0.01 && ratio <= 50) {
      pcrOI = ratio;
    }
  }

  let pcrVol = null;
  if (callVol > 0 && putVol > 0) {
    const ratio = putVol / callVol;
    if (Number.isFinite(ratio) && ratio >= 0.01 && ratio <= 50) {
      pcrVol = ratio;
    }
  }

  return {
    strike: numStrike,
    pcrOI,
    pcrVol,
    callOI,
    putOI,
    callVol,
    putVol,
  };
}

function renderPanePCR(pane, option, chainPCR, expiryLabel) {
  const chip = $(`${pane}-pcr-chip`);
  const valEl = $(`${pane}-pcr-val`);
  const subEl = $(`${pane}-sub`);
  if (!chip || !valEl) return;

  if (!option) {
    valEl.textContent = "—";
    valEl.className = "";
    chip.title = "Select a strike to view PCR";
    if (subEl) subEl.textContent = "select a strike";
    return;
  }

  const strikePCR = computeStrikePCR(option.strike);
  const totalPCRVal = (chainPCR?.pcrOI != null && Number.isFinite(chainPCR.pcrOI) && chainPCR.pcrOI <= 50) ? chainPCR.pcrOI : (chainPCR?.pcrVol != null && Number.isFinite(chainPCR.pcrVol) && chainPCR.pcrVol <= 50 ? chainPCR.pcrVol : null);
  const strikePCRVal = (strikePCR?.pcrOI != null && Number.isFinite(strikePCR.pcrOI) && strikePCR.pcrOI <= 50) ? strikePCR.pcrOI : (strikePCR?.pcrVol != null && Number.isFinite(strikePCR.pcrVol) && strikePCR.pcrVol <= 50 ? strikePCR.pcrVol : null);

  const displayVal = strikePCRVal != null ? strikePCRVal : totalPCRVal;
  const isStrike = strikePCRVal != null;

  if (displayVal != null) {
    valEl.textContent = displayVal.toFixed(2);
    valEl.className = displayVal > 1.02 ? "up" : displayVal < 0.98 ? "down" : "";

    const sentiment = displayVal >= 1.3 ? "Strong Bullish / Put Support" :
      displayVal >= 1.05 ? "Mild Bullish" :
      displayVal >= 0.95 ? "Neutral" :
      displayVal >= 0.7 ? "Mild Bearish" : "Strong Bearish / Call Resistance";

    let tooltip = `${option.symbol || `${S.activeIndex} ${option.strike} ${option.type}`}\n`;
    if (isStrike) {
      tooltip += `Strike ${fmtStrike(option.strike)} PCR: ${displayVal.toFixed(2)} [${sentiment}]\n`;
      tooltip += `  Put OI: ${fmtCompact(strikePCR.putOI)} · Call OI: ${fmtCompact(strikePCR.callOI)}\n`;
      if (strikePCR.putVol || strikePCR.callVol) {
        tooltip += `  Put Vol: ${fmtCompact(strikePCR.putVol)} · Call Vol: ${fmtCompact(strikePCR.callVol)}${strikePCR.pcrVol != null ? ` (Vol PCR: ${strikePCR.pcrVol.toFixed(2)})` : ""}\n`;
      }
    }
    if (!isStrike && strikePCR) {
      tooltip += `Strike ${fmtStrike(option.strike)} PCR: unavailable (one side has no OI)\n`;
    }
    if (totalPCRVal != null) {
      tooltip += `Total Chain PCR: ${totalPCRVal.toFixed(2)}\n`;
      tooltip += `  Total Put OI: ${fmtCompact(chainPCR.totalPutOI)} · Total Call OI: ${fmtCompact(chainPCR.totalCallOI)}`;
    }
    chip.title = tooltip.trim();

    if (subEl) {
      const strikePart = isStrike ? ` · Strike PCR ${displayVal.toFixed(2)}` : "";
      const totalPart = totalPCRVal != null ? ` · Total PCR ${totalPCRVal.toFixed(2)}` : "";
      subEl.textContent = `${expiryLabel} · ${S.timeframe}${strikePart}${totalPart}`;
    }
  } else {
    valEl.textContent = "—";
    valEl.className = "";
    chip.title = "PCR quote data loading…";
    if (subEl) subEl.textContent = `${expiryLabel} · ${S.timeframe}`;
  }
}

function renderPCR() {
  const pcrEl = $("pcr-value");
  const pcrChip = $("pcr-chip");
  const pcr = computePCR();
  if (pcrEl) {
    if (!pcr || (pcr.pcrOI == null && pcr.pcrVol == null)) {
      pcrEl.textContent = "—";
      pcrEl.className = "";
      if (pcrChip) pcrChip.title = "Put/Call Ratio: loading chain data";
    } else {
      const primaryPCR = pcr.pcrOI != null ? pcr.pcrOI : pcr.pcrVol;
      const isOI = pcr.pcrOI != null;
      pcrEl.textContent = primaryPCR.toFixed(2);
      pcrEl.className = primaryPCR > 1.02 ? "up" : primaryPCR < 0.98 ? "down" : "";

      if (pcrChip) {
        const sentiment = primaryPCR >= 1.3 ? "Strong Bullish / Put Support" :
          primaryPCR >= 1.05 ? "Mild Bullish" :
          primaryPCR >= 0.95 ? "Neutral" :
          primaryPCR >= 0.7 ? "Mild Bearish" : "Strong Bearish / Call Resistance";
        const oiText = isOI ? `Put OI: ${fmtCompact(pcr.totalPutOI)} | Call OI: ${fmtCompact(pcr.totalCallOI)}` : "OI: unavailable";
        const volText = pcr.pcrVol != null ? `Vol PCR: ${pcr.pcrVol.toFixed(2)} (Put: ${fmtCompact(pcr.totalPutVol)} | Call: ${fmtCompact(pcr.totalCallVol)})` : "";
        pcrChip.title = `PCR (${isOI ? "Open Interest" : "Volume"}): ${primaryPCR.toFixed(2)} [${sentiment}]\n${oiText}\n${volText}`.trim();
      }
    }
  }

  // Update PCR on both left (CE) and right (PE) chart panes
  const expiryLabel = expiryText(S.expiry);
  renderPanePCR("ce", selectedOption("CE"), pcr, expiryLabel);
  renderPanePCR("pe", selectedOption("PE"), pcr, expiryLabel);
}

function updateLiveTabQuotes(key) {
  if (S.tab === "positions") {
    const pos = S.positions.find((p) => p.instrument_key === key);
    if (!pos) return;
    const ltpCells = $$(`[data-pos-ltp="${key}"]`);
    for (const cell of ltpCells) cell.textContent = fmt(pos.ltp);
    const pnlCells = $$(`[data-pos-pnl="${key}"]`);
    for (const cell of pnlCells) {
      const isOpen = Math.abs(num(pos.quantity)) > 0;
      const openPnL = pos.unrealised_pnl != null ? pos.unrealised_pnl : pos.pnl;
      const val = isOpen ? openPnL : pos.pnl;
      cell.className = directionClass(val);
      const span = cell.querySelector(".font-bold");
      if (span) span.textContent = fmtSigned(val, 0);
      else cell.textContent = fmtSigned(val, 0);
    }
  } else if (S.tab === "brackets") {
    const matchingTargets = (S.autoTargets || []).filter((t) => t.instrumentKey === key);
    for (const tgt of matchingTargets) {
      const ltpCell = document.querySelector(`[data-bracket-ltp="${tgt.id}"]`);
      if (ltpCell) ltpCell.textContent = `₹${fmt(tgt.ltp)}`;
      const pnlCell = document.querySelector(`[data-bracket-pnl="${tgt.id}"]`);
      if (pnlCell && tgt.status === "IN_POSITION") {
        const pnl = (tgt.ltp - tgt.entryPrice) * tgt.shares * (tgt.action === "buy" ? 1 : -1);
        pnlCell.className = `${directionClass(pnl)} font-bold`;
        pnlCell.textContent = fmtSigned(pnl, 0);
      }
    }
  }
}

function savedStrike(type) {
  if (!S.activeIndex) return 0;
  const expiryValue = S.expiry
    ? num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-${S.expiry}-${type}`))
    : 0;
  return expiryValue || num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-${type}`));
}

// The initial chain page only covers a window around spot. A saved strike can
// sit outside it, and selectDefaultStrike would then fall back to ATM and
// overwrite the preference. Fetch the missing window(s) before selecting.
async function ensureSavedStrikesLoaded() {
  const index = S.activeIndex;
  const expiry = S.expiry;
  if (!index || !expiry) return;
  const requestID = S.chainRequestID;
  for (const type of ["CE", "PE"]) {
    const strike = savedStrike(type);
    if (!strike) continue;
    const row = S.chain.find((entry) => entry.strike === strike);
    const option = row?.[type === "CE" ? "call" : "put"];
    if (option) continue;
    try {
      const raw = await getJSON(chainURL({ center: strike }));
      if (requestID !== S.chainRequestID || index !== S.activeIndex || expiry !== S.expiry) return;
      acceptChainPage(raw, true);
    } catch (error) {
      console.warn("failed to load the saved strike window", error);
    }
  }
}

function selectDefaultStrike() {
  if (!S.chain.length) {
    S.selected = { type: "CE", CE: null, PE: null };
    return;
  }
  const atm = computeATM() ?? S.chain[Math.floor(S.chain.length / 2)].strike;
  const savedCE = savedStrike("CE");
  const savedPE = savedStrike("PE");
  const ceRow = S.chain.find((row) => row.strike === savedCE);
  const peRow = S.chain.find((row) => row.strike === savedPE);
  const ceStrike = ceRow?.call ? savedCE : atm;
  const peStrike = peRow?.put ? savedPE : atm;
  selectStrike(ceStrike, "CE", false, false, false);
  selectStrike(peStrike, "PE", false, false, false);
  S.selected.type = "CE";
  S.chainScrolled = false;
}

function selectStrike(strike, type, rerender = true, reloadCharts = true, persist = true) {
  const row = S.chain.find((r) => r.strike === strike);
  const option = row?.[type === "CE" ? "call" : "put"];
  if (!option) return;
  S.selected[type] = option;
  S.selected.type = type;
  if (persist && S.activeIndex && S.expiry) {
    try {
      localStorage.setItem(`scalper-last-strike-${S.activeIndex}-${S.expiry}-${type}`, String(strike));
      localStorage.setItem(`scalper-last-strike-${S.activeIndex}-${type}`, String(strike));
    } catch (_) {}
  }
  S.chainScrolled = true;
  updateFeedSubscriptions();
  if (reloadCharts) loadCharts();
  renderPaneHeaders();
  if (window.innerWidth <= 768) {
    setMobileView(type === "CE" ? "ce" : "pe");
  }
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
  if (data.oi != null && Number(data.oi) >= 0 && Number(data.oi) <= 100000000) {
    option.oi = num(data.oi, option.oi);
  }
  if (data.bids || data.asks) {
    const merged = mergeDepthData({ bids: option.bids, asks: option.asks }, data.bids, data.asks);
    option.bids = merged.bids;
    option.asks = merged.asks;
  }
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

let chartTradesTimer: number | null = null;

function scheduleChartTrades() {
  if (chartTradesTimer !== null) return;
  chartTradesTimer = window.setTimeout(() => {
    chartTradesTimer = null;
    updateChartTrades();
  }, 500);
}

function updateChartTrades() {
  const trades = Array.isArray(S.trades) ? S.trades : [];
  const positions = Array.isArray(S.positions) ? S.positions : [];
  const finishedExitIds = new Set(
    (S.autoTargets || [])
      .filter((t) => t.status !== "IN_POSITION" && t.exitOrderId)
      .map((t) => String(t.exitOrderId))
  );

  // 1. CE Chart
  const ce = selectedOption("CE");
  if (S.charts.ce) {
    const ceTrades = ce ? trades.filter((t) => matchesContract(t, ce)) : [];
    let cePos = ce ? positions.find((p) => matchesContract(p, ce) && Math.abs(num(p.quantity)) > 0) : null;
    const ceBracket = ce ? findChartBracket(ce) : null;
    if (ceBracket && ceBracket.status === "IN_POSITION") {
      const ltp = getInstrumentLTP(ceBracket);
      const isolatedPnL = (ltp - ceBracket.entryPrice) * ceBracket.shares * (ceBracket.action === "buy" ? 1 : -1);
      cePos = {
        symbol: ceBracket.symbol,
        quantity: ceBracket.shares * (ceBracket.action === "buy" ? 1 : -1),
        average_price: ceBracket.entryPrice,
        unrealised_pnl: isolatedPnL,
        pnl: isolatedPnL,
      };
    }
    if (ceBracket) {
      S.charts.ce.showBracketLevels?.({
        bracketId: ceBracket.id,
        entryPrice: ceBracket.entryPrice,
        targetPrice: ceBracket.targetPrice,
        stopPrice: ceBracket.stopLossPrice,
        side: ceBracket.action,
        pending: ceBracket.status === "PENDING_ENTRY",
        exchangeStopPrice: ceBracket.exchangeStopPrice,
        exchangeStopMode: ceBracket.exchangeStopMode,
        exchangeStopStatus: ceBracket.exchangeStopStatus,
      });
    } else {
      S.charts.ce.clearBracketLevels?.();
    }
    if (!ceBracket && (!S.quickOrder || S.quickOrder.pane !== "ce")) {
      S.charts.ce.clearTargetPreview?.();
    }
    const ceOpen = ce
      ? (S.orders || [])
          .filter((o) => matchesContract(o, ce) && isOpenOrder(o) && !finishedExitIds.has(String(o.id)))
          .map((o) => (ceBracket && String(ceBracket.orderId) === String(o.id) ? { ...o, bracketId: ceBracket.id } : o))
      : [];
    S.charts.ce.setTrades(ceTrades, { isSpot: false });
    S.charts.ce.setPosition(cePos);
    S.charts.ce.setOpenOrders(ceOpen);
  }

  // 2. PE Chart
  const pe = selectedOption("PE");
  if (S.charts.pe) {
    const peTrades = pe ? trades.filter((t) => matchesContract(t, pe)) : [];
    let pePos = pe ? positions.find((p) => matchesContract(p, pe) && Math.abs(num(p.quantity)) > 0) : null;
    const peBracket = pe ? findChartBracket(pe) : null;
    if (peBracket && peBracket.status === "IN_POSITION") {
      const ltp = getInstrumentLTP(peBracket);
      const isolatedPnL = (ltp - peBracket.entryPrice) * peBracket.shares * (peBracket.action === "buy" ? 1 : -1);
      pePos = {
        symbol: peBracket.symbol,
        quantity: peBracket.shares * (peBracket.action === "buy" ? 1 : -1),
        average_price: peBracket.entryPrice,
        unrealised_pnl: isolatedPnL,
        pnl: isolatedPnL,
      };
    }
    if (peBracket) {
      S.charts.pe.showBracketLevels?.({
        bracketId: peBracket.id,
        entryPrice: peBracket.entryPrice,
        targetPrice: peBracket.targetPrice,
        stopPrice: peBracket.stopLossPrice,
        side: peBracket.action,
        pending: peBracket.status === "PENDING_ENTRY",
        exchangeStopPrice: peBracket.exchangeStopPrice,
        exchangeStopMode: peBracket.exchangeStopMode,
        exchangeStopStatus: peBracket.exchangeStopStatus,
      });
    } else {
      S.charts.pe.clearBracketLevels?.();
    }
    if (!peBracket && (!S.quickOrder || S.quickOrder.pane !== "pe")) {
      S.charts.pe.clearTargetPreview?.();
    }
    const peOpen = pe
      ? (S.orders || [])
          .filter((o) => matchesContract(o, pe) && isOpenOrder(o) && !finishedExitIds.has(String(o.id)))
          .map((o) => (peBracket && String(peBracket.orderId) === String(o.id) ? { ...o, bracketId: peBracket.id } : o))
      : [];
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

function tradesPaneKey(chartKey) {
  return chartKey === "spot" || chartKey === "underlying" ? "underlying" : chartKey;
}

function chartTradesLabel(pane) {
  if (pane === "ce") return "CALL";
  if (pane === "pe") return "PUT";
  return "UNDERLYING";
}

function toggleChartTrades(chartKey) {
  const pane = tradesPaneKey(chartKey);
  const chart = S.charts[chartKey] || (pane === "underlying" ? S.charts.spot : null);
  if (!chart) return false;
  const nextState = chart.toggleTrades();
  S.showTradesPanes[pane] = Boolean(nextState);
  try {
    localStorage.setItem(`scalper-show-trades-${pane}`, String(nextState));
  } catch (_) {}
  for (const button of $$(".chart-tools button[data-chart-action='trades']")) {
    if (tradesPaneKey(button.dataset.chart) !== pane) continue;
    button.classList.toggle("active", nextState);
    button.setAttribute("aria-pressed", nextState ? "true" : "false");
  }
  updateChartTrades();
  const label = chartTradesLabel(pane);
  toast(nextState ? `Orders & execution overlays enabled (${label})` : `Orders & execution overlays hidden (${label})`, "ok", 2000);
  return nextState;
}

function roundToTick(price) {
  return Math.round(Number(price) * 20) / 20;
}

function chartLevelConfirmationsEnabled() {
  return S.boot?.confirmChartLevelChanges !== false;
}

function validateBracketLevels(bracket, entry, target, stop) {
  if (!bracket) return "";
  const isBuy = bracket.action === "buy";
  if (isBuy) {
    if (entry > 0 && target > 0 && target <= entry) return "Target must be above the entry price for a BUY bracket.";
    if (entry > 0 && stop > 0 && stop >= entry) return "Stop-loss must be below the entry price for a BUY bracket.";
    if (target > 0 && stop > 0 && target <= stop) return "Target must be above the stop-loss for a BUY bracket.";
  } else {
    if (entry > 0 && target > 0 && target >= entry) return "Target must be below the entry price for a SELL bracket.";
    if (entry > 0 && stop > 0 && stop <= entry) return "Stop-loss must be above the entry price for a SELL bracket.";
    if (target > 0 && stop > 0 && target >= stop) return "Target must be below the stop-loss for a SELL bracket.";
  }
  return "";
}

async function handleChartLevelDrop(pane, info) {
  if (!info) return;
  const price = roundToTick(info.price);
  const previous = roundToTick(info.previousPrice);
  if (!Number.isFinite(price) || price <= 0 || Math.abs(price - previous) < 0.025) return;
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before modifying orders.", "warn");
    return;
  }

  const kind = String(info.kind || "ENTRY").toUpperCase();
  const levelLabel = kind === "TARGET" ? "TARGET" : kind === "STOP" ? "STOP-LOSS" : "ENTRY";
  const bracket = info.bracketId
    ? (S.autoTargets || []).find((b) => String(b.id) === String(info.bracketId))
    : null;
  const order = !bracket && info.orderId
    ? (S.orders || []).find((o) => String(o.id) === String(info.orderId))
    : null;

  if (bracket) {
    const entry = kind === "ENTRY" ? price : num(bracket.entryPrice);
    const target = kind === "TARGET" ? price : num(bracket.targetPrice);
    const stop = kind === "STOP" ? price : num(bracket.stopLossPrice);
    const error = validateBracketLevels(bracket, entry, target, stop);
    if (error) {
      toast(error, "error", 5000);
      return;
    }
  } else if (!order) {
    toast("Could not find the order for this chart level.", "error", 4000);
    return;
  }

  const symbol = bracket?.symbol || order?.symbol || chartTradesLabel(tradesPaneKey(pane));
  const detail = kind === "ENTRY"
    ? "The pending limit order will be modified at the broker."
    : "The server bracket level will update immediately and trigger the exit at the new price.";
  if (chartLevelConfirmationsEnabled()) {
    const ok = await confirmAction({
      title: `Move ${levelLabel}`,
      body: `Move <b>${esc(levelLabel)}</b> for <b>${esc(symbol)}</b> from <b>₹${fmt(previous)}</b> to <b>₹${fmt(price)}</b>?<br><span class="muted">${detail}</span>`,
      okLabel: `Move ${levelLabel}`,
      danger: kind === "ENTRY",
    });
    if (!ok) return;
  }

  try {
    if (bracket) {
      const payload: any = { confirm: true };
      if (kind === "ENTRY") payload.entry_price = price;
      else if (kind === "TARGET") payload.target_price = price;
      else payload.stop_loss_price = price;
      const response = await patchJSON(`/api/brackets/${encodeURIComponent(bracket.id)}`, payload);
      const updated = normalizeBracket(response?.bracket || response);
      if (updated && updated.id) {
        upsertBracket(updated);
      } else {
        if (kind === "ENTRY") bracket.entryPrice = price;
        else if (kind === "TARGET") bracket.targetPrice = price;
        else bracket.stopLossPrice = price;
      }
      saveAutoTargets();
      renderTab();
      updateChartTrades();
      scheduleBooksRefresh(500);
      toast(`${levelLabel} moved to ₹${fmt(price)}${bracket.symbol ? ` (${bracket.symbol})` : ""}.`, "ok", 3500);
    } else {
      await patchJSON(`/api/broker/orders/${encodeURIComponent(String(order.id))}`, {
        price,
        quantity: order.quantity,
        order_type: "LIMIT",
        validity: "DAY",
        confirm: true,
      });
      toast(`Order ${order.id} modified to ₹${fmt(price)}.`, "ok", 3500);
      scheduleBooksRefresh(300);
    }
  } catch (err) {
    toast(`Could not move ${levelLabel}: ${err.message}`, "error", 6000);
  }
}

async function handleChartLevelRemove(pane, info) {
  if (!info) return;
  const kind = String(info.kind || "").toUpperCase();
  if (kind !== "TARGET" && kind !== "STOP" && kind !== "ENTRY") return;
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before modifying orders.", "warn");
    return;
  }
  const bracket = info.bracketId
    ? (S.autoTargets || []).find((b) => String(b.id) === String(info.bracketId))
    : null;
  const order = !bracket && info.orderId
    ? (S.orders || []).find((o) => String(o.id) === String(info.orderId))
    : null;

  if (kind === "ENTRY") {
    if (bracket) {
      if (bracket.status !== "PENDING_ENTRY") {
        toast("Only a pending entry can be removed from the chart.", "warn", 3500);
        return;
      }
      if (chartLevelConfirmationsEnabled()) {
        const ok = await confirmAction({
          title: "Remove Pending Entry",
          body: `Cancel the pending entry for <b>${esc(bracket.symbol || chartTradesLabel(tradesPaneKey(pane)))}</b> at <b>₹${fmt(bracket.entryPrice)}</b>?<br><span class="muted">The entry order is cancelled at the broker and its target/stop levels are discarded.</span>`,
          okLabel: "Cancel Entry",
          danger: true,
        });
        if (!ok) return;
      }
      await onCancelBracket(bracket.id);
      return;
    }
    if (order) {
      if (chartLevelConfirmationsEnabled()) {
        const ok = await confirmAction({
          title: "Remove Pending Entry",
          body: `Cancel order <b>${esc(order.id)}</b> for <b>${esc(order.symbol || chartTradesLabel(tradesPaneKey(pane)))}</b> at <b>₹${fmt(order.price)}</b>?<br><span class="muted">The order is cancelled at the broker.</span>`,
          okLabel: "Cancel Order",
          danger: true,
        });
        if (!ok) return;
      }
      await onCancelOrder(order.id);
      return;
    }
    toast("Could not find the pending order for this chart level.", "error", 4000);
    return;
  }

  if (!bracket) {
    toast("Could not find the bracket for this chart level.", "error", 4000);
    return;
  }

  const levelLabel = kind === "TARGET" ? "TARGET" : "STOP-LOSS";
  const detail = kind === "TARGET"
    ? "The target exit will be removed. The position stays open until you exit manually or the stop-loss hits."
    : "The stop-loss will be removed. The position will have no stop-loss protection until you set one again.";
  if (chartLevelConfirmationsEnabled()) {
    const ok = await confirmAction({
      title: `Remove ${levelLabel}`,
      body: `Remove <b>${esc(levelLabel)}</b> for <b>${esc(bracket.symbol || chartTradesLabel(tradesPaneKey(pane)))}</b>?<br><span class="muted">${detail}</span>`,
      okLabel: `Remove ${levelLabel}`,
      danger: true,
    });
    if (!ok) return;
  }

  try {
    const payload: any = { confirm: true };
    if (kind === "TARGET") payload.target_price = 0;
    else payload.stop_loss_price = 0;
    const response = await patchJSON(`/api/brackets/${encodeURIComponent(bracket.id)}`, payload);
    const updated = normalizeBracket(response?.bracket || response);
    if (updated && updated.id) {
      upsertBracket(updated);
    } else if (kind === "TARGET") {
      bracket.targetPrice = 0;
    } else {
      bracket.stopLossPrice = 0;
    }
    saveAutoTargets();
    renderTab();
    updateChartTrades();
    scheduleBooksRefresh(500);
    toast(`${levelLabel} removed${bracket.symbol ? ` (${bracket.symbol})` : ""}.`, "warn", 3500);
  } catch (err) {
    toast(`Could not remove ${levelLabel}: ${err.message}`, "error", 6000);
  }
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
  if (!S.session?.authenticated) return;
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
        if (!key) continue;
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
          const rawCandles = (result.candles || []).map((c: any) => ({
            time: num(c.time ?? c.ts),
            open: num(c.open),
            high: num(c.high),
            low: num(c.low),
            close: num(c.close),
            volume: num(c.volume),
          }));
          const validCandles = rawCandles.map((c: any) => {
            if (c.low <= 0 && (c.open > 0 || c.close > 0 || c.high > 0)) {
              const pos = [c.open, c.close, c.high].filter((v: number) => v > 0);
              c.low = pos.length ? Math.min(...pos) : 0;
            }
            if (c.open <= 0) c.open = c.low > 0 ? c.low : c.close;
            if (c.close <= 0) c.close = c.open > 0 ? c.open : c.low;
            if (c.high <= 0 || c.high < Math.max(c.open, c.close, c.low)) {
              c.high = Math.max(c.open, c.close, c.low);
            }
            return c;
          }).filter((c: any) => c.time > 0 && c.open > 0 && c.high > 0 && c.low > 0 && c.close > 0 && c.high >= c.low);

          const parsed = {
            candles: validCandles,
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
    if (!chart || !key) continue;
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
      const live = S.candles[liveKey] || [];
      chart.setData(live, { symbol: key });
      setChartHint(pane, live.length
        ? "Live candles only; historical refresh is retrying."
        : `Historical data unavailable: ${entry.error.message}`);
    } else {
      const merged = mergeChartCandles(entry.candles, S.candles[liveKey], key);
      if (merged.length) {
        S.candles[liveKey] = merged.slice(-500).map((candle) => ({ ...candle }));
      }
      chart.setData(merged, { symbol: key });
      setChartHint(pane, entry.warning || (merged.length ? "" : "Waiting for live ticks…"));
    }
  }
  renderPaneHeaders();
  updateChartTrades();
}

function mergeChartCandles(historical, live, instrumentKey = "") {
  const byTime = new Map<any, any>(
    (historical || [])
      .filter((c: any) => c && c.low > 0 && c.open > 0 && c.high > 0 && c.close > 0 && c.high >= c.low)
      .filter((c: any) => candleSessionAllows(c.time, instrumentKey))
      .map((candle) => [candle.time, { ...candle }])
  );
  for (const candle of (live || [])) {
    if (!candle || candle.low <= 0 || candle.open <= 0 || candle.high <= 0 || candle.close <= 0 || candle.high < candle.low) {
      continue;
    }
    if (!candleSessionAllows(candle.time, instrumentKey)) continue;
    const existing = byTime.get(candle.time);
    if (!existing) {
      byTime.set(candle.time, { ...candle });
      continue;
    }
    // Same bucket: keep the earliest open, union the range, use the latest
    // close, and keep the larger volume. This preserves a partial bar stored
    // before a page refresh while live ticks continue to extend it.
    existing.open = existing.open > 0 ? existing.open : candle.open;
    existing.high = Math.max(existing.high, candle.high);
    existing.low = Math.min(existing.low, candle.low);
    existing.close = candle.close;
    existing.volume = Math.max(existing.volume || 0, candle.volume || 0);
  }
  const merged = [...byTime.values()].sort((a: any, b: any) => a.time - b.time);
  return dropFlatNonTradingDays(merged);
}

function setChartHint(pane, message) {
  const hint = $("hint-" + pane);
  hint.textContent = message;
  hint.hidden = !message;
}

function updateLiveCandle(key, price, timestamp, volume) {
  if (!Number.isFinite(Number(price)) || Number(price) <= 0) return;
  const timeMs = num(timestamp) > 1e12 ? num(timestamp) : num(timestamp) * 1000;
  const time = Math.floor((timeMs > 0 ? timeMs : Date.now()) / 1000);
  if (!candleSessionAllows(time, key)) return;
  const interval = ({ "1m": 60, "3m": 180, "5m": 300, "15m": 900, "1h": 3600 })[S.timeframe];
  if (!interval) return;
  const sessionOpen = activeCommodity() ? "09:00" : (S.boot.marketSession?.open || "09:15");
  const openMatch = /^(\d{2}):(\d{2})$/.exec(sessionOpen);
  const openSeconds = openMatch ? (Number(openMatch[1]) * 60 + Number(openMatch[2])) * 60 : 9 * 3600 + 15 * 60;
  const localDayStart = Math.floor((time + 19800) / 86400) * 86400 - 19800;
  const sessionStart = localDayStart + openSeconds;
  let bucket = sessionStart + Math.floor((time - sessionStart) / interval) * interval;
  const value = Number(price);
  const cumVolume = Number.isFinite(Number(volume)) && Number(volume) > 0 ? Number(volume) : 0;
  const cacheKey = `${key}|${S.timeframe}`;
  const candles = S.candles[cacheKey] || (S.candles[cacheKey] = []);
  let candle = candles[candles.length - 1];
  if (candle && bucket < candle.time) {
    bucket = candle.time;
  }
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
  const chainPCR = computePCR();

  $("ce-title").textContent = ce ? `${S.activeIndex} ${fmtStrike(ce.strike)} CE` : "CALL";
  $("ce-ltp").textContent = ce ? fmt(ce.ltp) : "—";
  $("ce-change").textContent = ce ? `${fmtSigned(ce.change)} (${fmtSigned(ce.changePct)}%)` : "—";
  $("ce-change").className = "pane-change " + directionClass(ce?.changePct);
  renderOptionStrikeSelect("CE", ce);
  renderPanePCR("ce", ce, chainPCR, expiryLabel);

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
  $("pe-ltp").textContent = pe ? fmt(pe.ltp) : "—";
  $("pe-change").textContent = pe ? `${fmtSigned(pe.change)} (${fmtSigned(pe.changePct)}%)` : "—";
  $("pe-change").className = "pane-change " + directionClass(pe?.changePct);
  renderOptionStrikeSelect("PE", pe);
  renderPanePCR("pe", pe, chainPCR, expiryLabel);
  renderPCR();

  const ceInst = chartInstrument("CE");
  if (ceInst) {
    const ceDepth = S.depths?.[ceInst] || (selectedOption("CE")?.bids?.length || selectedOption("CE")?.asks?.length ? { bids: selectedOption("CE").bids, asks: selectedOption("CE").asks } : null);
    if (ceDepth) S.charts.ce?.setDepth?.(ceDepth);
  }
  const peInst = chartInstrument("PE");
  if (peInst) {
    const peDepth = S.depths?.[peInst] || (selectedOption("PE")?.bids?.length || selectedOption("PE")?.asks?.length ? { bids: selectedOption("PE").bids, asks: selectedOption("PE").asks } : null);
    if (peDepth) S.charts.pe?.setDepth?.(peDepth);
  }
  const spotInst = activeSpotInstrument();
  if (spotInst && S.depths?.[spotInst]) {
    S.charts.spot?.setDepth?.(S.depths[spotInst]);
  }
  if (S.chain && S.chain.length) {
    S.charts.spot?.setChainData?.(S.chain);
  }

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
  renderSpotPicker();

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

function spotPickerKeys(): string[] {
  const order = [...(S.boot.indices || []), ...(S.boot.commodities || [])].map((i) => i.key);
  if (S.spots.has("INDIA VIX")) order.push("INDIA VIX");
  return order;
}

function spotStatus(key: string, spot: any): string {
  if (!marketOpenForKey(key)) return "CLOSED";
  if (!spot || num(spot.ltp) <= 0) return "WAIT";
  return "LIVE";
}

function spotMenuRowHTML(key: string): string {
  const spot = S.spots.get(key);
  const selected = key === S.activeIndex;
  const isStatic = key === "INDIA VIX";
  return `<button type="button" class="spot-menu-row${selected ? " active" : ""}${isStatic ? " static" : ""}" data-index="${esc(key)}" role="option" aria-selected="${selected}"${isStatic ? ' aria-disabled="true"' : ""}>
    <span class="spot-row-main">
      <span class="spot-row-name">${esc(spot?.displayName || key)}</span>
      <span class="spot-row-sub">${esc(key)}</span>
    </span>
    <span class="spot-row-quote">
      <span class="spot-row-price" data-spot-price>—</span>
      <span class="spot-row-change" data-spot-change>—</span>
    </span>
    <span class="spot-row-badges">
      <span class="spot-badge active" data-spot-active${selected ? "" : " hidden"}>ACTIVE</span>
      <span class="spot-badge wait" data-spot-status>WAIT</span>
    </span>
  </button>`;
}

function renderSpotPicker(force = false) {
  const order = spotPickerKeys();
  const active = S.spots.get(S.activeIndex);

  const dot = $("spot-current-dot");
  if (dot) dot.className = `spot-live-dot ${spotStatus(S.activeIndex, active).toLowerCase()}`;
  const nameEl = $("spot-current-name");
  if (nameEl) nameEl.textContent = active?.displayName || S.activeIndex || "—";
  const priceEl = $("spot-current-price");
  if (priceEl) priceEl.textContent = active ? fmt(active.ltp) : "—";
  const changeEl = $("spot-current-change");
  if (changeEl) {
    changeEl.textContent = active ? `${fmtSigned(active.change)} · ${fmtSigned(active.changePct)}%` : "—";
    changeEl.className = `spot-current-change ${active ? directionClass(active.changePct ?? active.change) : ""}`;
  }

  const menu = $("spot-menu");
  if (!menu) return;
  const signature = `${order.join(",")}|${S.activeIndex}`;
  if (force || menu.dataset.signature !== signature || !menu.childElementCount) {
    menu.dataset.signature = signature;
    menu.innerHTML = order.map(spotMenuRowHTML).join("");
  }

  menu.querySelectorAll(".spot-menu-row").forEach((row) => {
    const key = row.getAttribute("data-index") || "";
    const spot = S.spots.get(key);
    const selected = key === S.activeIndex;
    row.classList.toggle("active", selected);
    row.setAttribute("aria-selected", String(selected));
    const activeBadge = row.querySelector("[data-spot-active]");
    if (activeBadge) activeBadge.hidden = !selected;
    const rowName = row.querySelector(".spot-row-name");
    if (rowName) rowName.textContent = spot?.displayName || key;
    const rowPrice = row.querySelector("[data-spot-price]");
    if (rowPrice) rowPrice.textContent = spot ? fmt(spot.ltp) : "—";
    const rowChange = row.querySelector("[data-spot-change]");
    if (rowChange) {
      rowChange.textContent = spot ? `${fmtSigned(spot.change)} · ${fmtSigned(spot.changePct)}%` : "—";
      rowChange.className = `spot-row-change ${spot ? directionClass(spot.changePct ?? spot.change) : ""}`;
    }
    const statusEl = row.querySelector("[data-spot-status]");
    if (statusEl) {
      const status = spotStatus(key, spot);
      statusEl.textContent = status;
      statusEl.className = `spot-badge ${status.toLowerCase()}`;
    }
  });
}

function setSpotMenuOpen(open: boolean) {
  const menu = $("spot-menu");
  const trigger = $("spot-current");
  if (!menu || !trigger) return;
  if (open) renderSpotPicker(true);
  menu.hidden = !open;
  trigger.setAttribute("aria-expanded", open ? "true" : "false");
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
  renderPCR();
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
    renderPCR();
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
    const isATM = row.strike === atm;
    const callITM = atm ? row.strike < atm : false;
    const putITM = atm ? row.strike > atm : false;
    return `<div class="chain-row${isATM ? " atm" : ""}${active ? " active" : ""}" data-strike="${row.strike}" role="row">
      <button type="button" class="cell-ce${callITM ? " itm" : ""}${callSelected ? " selected" : ""}${call ? ` ${directionClass(call.changePct)}` : ""}" data-select="CE" data-strike="${row.strike}" title="${call ? `${fmt(call.ltp)} · ${fmtSigned(call.changePct)}% · Vol ${fmtCompact(call.volume)}${call.oi ? ` · OI ${fmtCompact(call.oi)}` : ""}` : "Call quote unavailable"}">${call ? `<span class="chain-ltp">${fmt(call.ltp)}</span><span class="chain-volume">VOL ${fmtCompact(call.volume)}</span>` : "—"}</button>
      <button type="button" class="cell-strike" data-select="${esc(S.selected.type)}" data-strike="${row.strike}">${isATM ? '<span class="atm-badge">ATM</span>' : ""}${fmtStrike(row.strike)}</button>
      <button type="button" class="cell-pe${putITM ? " itm" : ""}${putSelected ? " selected" : ""}${put ? ` ${directionClass(put.changePct)}` : ""}" data-select="PE" data-strike="${row.strike}" title="${put ? `${fmt(put.ltp)} · ${fmtSigned(put.changePct)}% · Vol ${fmtCompact(put.volume)}${put.oi ? ` · OI ${fmtCompact(put.oi)}` : ""}` : "Put quote unavailable"}">${put ? `<span class="chain-ltp">${fmt(put.ltp)}</span><span class="chain-volume">VOL ${fmtCompact(put.volume)}</span>` : "—"}</button>
    </div>`;
  }).join("");
  const pcr = computePCR();
  const pcrSummary = pcr && pcr.pcrOI != null ? ` · PCR ${pcr.pcrOI.toFixed(2)}` : (pcr && pcr.pcrVol != null ? ` · Vol PCR ${pcr.pcrVol.toFixed(2)}` : "");
  $("strike-meta").textContent = `${S.chain.length} strikes${pcrSummary} · ${expiryText(S.expiry)}`;
  renderChainMoreControls();
  renderPCR();
  if (S.chain && S.chain.length) {
    S.charts.spot?.setChainData?.(S.chain);
  }

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
  const pcr = computePCR();
  const pcrSummary = pcr && pcr.pcrOI != null ? ` · PCR ${pcr.pcrOI.toFixed(2)}` : (pcr && pcr.pcrVol != null ? ` · Vol PCR ${pcr.pcrVol.toFixed(2)}` : "");
  $("strike-meta").textContent = query
    ? `${visible} matching strike${visible === 1 ? "" : "s"} · ${S.chain.length} available`
    : `${S.chain.length} strikes${pcrSummary} · ${expiryText(S.expiry)}`;
}

function openStrikeModal(targetType = S.selected.type) {
  S.strikeSearchTarget = targetType === "PE" ? "PE" : "CE";
  $("strike-target-select").value = S.strikeSearchTarget;
  $("strike-search-input").value = "";
  applyStrikeFilter();
  renderChainMoreControls();
  $("strike-modal").hidden = false;
  $("strike-search-input").focus();
  requestAnimationFrame(() => scrollATMIntoView());
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

function scrollATMIntoView(forceATM = false) {
  const body = $("chain-body");
  const atm = computeATM();
  if (!atm) return;
  const targetStrike = forceATM ? atm : (selectedOption(S.selected.type)?.strike || atm);
  const target = body ? body.querySelector(`.chain-row[data-strike="${targetStrike}"]`) : null;
  if (target) {
    const bodyRect = body.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    body.scrollTop += targetRect.top - bodyRect.top - (body.clientHeight - targetRect.height) / 2;
    if (forceATM) {
      target.classList.remove("pulse-atm");
      void target.offsetWidth;
      target.classList.add("pulse-atm");
    }
  }
}

const marginDebounceTimers: Record<string, any> = {};
const lastMarginReqKey: Record<string, string> = {};

function requestBrokerMargin(type: "CE" | "PE", option: any, shares: number) {
  if (!option || !shares || !S.session?.authenticated) return;
  const prefix = type === "CE" ? "call" : "put";
  if (marginDebounceTimers[type]) {
    clearTimeout(marginDebounceTimers[type]);
  }
  marginDebounceTimers[type] = setTimeout(async () => {
    const token = option.pSymbol || (option.instrumentKey ? option.instrumentKey.split("|")[1] : "");
    const seg = option.exchangeSegment || "nse_fo";

    // Buy margin
    try {
      const res = await postJSON("/api/broker/margin/required", {
        exchange_segment: seg,
        instrument_token: token,
        trading_symbol: option.symbol,
        transaction_type: "B",
        order_type: "MKT",
        product: S.product,
        quantity: shares,
        price: option.ltp || 0,
      });
      const data = res?.data || res;
      const ordMrgn = num(data?.ordMrgn ?? data?.order_margin);
      const insuf = num(data?.insufFund ?? data?.insufficient_funds);
      const buyEl = $(`leg-${prefix}-margin-buy`);
      if (buyEl && ordMrgn > 0) {
        if (insuf > 0) {
          buyEl.textContent = `B ₹${fmt(ordMrgn, 0)} (⚠️ Short ₹${fmt(insuf, 0)})`;
          buyEl.className = "margin-buy down";
        } else {
          buyEl.textContent = `B ₹${fmt(ordMrgn, 0)}`;
          buyEl.className = "margin-buy up";
        }
      }
    } catch (_) {
      // Local heuristic already rendered
    }

    // Sell margin
    try {
      const res = await postJSON("/api/broker/margin/required", {
        exchange_segment: seg,
        instrument_token: token,
        trading_symbol: option.symbol,
        transaction_type: "S",
        order_type: "MKT",
        product: S.product,
        quantity: shares,
        price: option.ltp || 0,
      });
      const data = res?.data || res;
      const ordMrgn = num(data?.ordMrgn ?? data?.order_margin);
      const insuf = num(data?.insufFund ?? data?.insufficient_funds);
      const sellEl = $(`leg-${prefix}-margin-sell`);
      if (sellEl && ordMrgn > 0) {
        if (insuf > 0) {
          sellEl.textContent = `S ₹${fmt(ordMrgn, 0)} (⚠️ Short ₹${fmt(insuf, 0)})`;
          sellEl.className = "margin-sell down";
        } else {
          sellEl.textContent = `S ₹${fmt(ordMrgn, 0)}`;
          sellEl.className = "margin-sell up";
        }
      }
    } catch (_) {
      // Local heuristic already rendered
    }
  }, 300);
}

function stopLossMarginWarnPct(): number {
  const pct = num(S.boot?.stopLossMarginPct, 1.0);
  if (!Number.isFinite(pct) || pct <= 0) return 0;
  return pct;
}

function availableMarginValue(): number | null {
  if (!S.limits?.loaded) return null;
  const available = num(S.limits?.available);
  if (!Number.isFinite(available) || available <= 0) return null;
  return available;
}

/** Informational only: returns a warning when SL risk exceeds the configured
 *  percent of available margin, otherwise an empty string. */
function stopLossMarginWarning(risk: number): string {
  const pct = stopLossMarginWarnPct();
  const available = availableMarginValue();
  if (pct <= 0 || available === null || !Number.isFinite(risk) || risk <= 0) return "";
  const threshold = (available * pct) / 100;
  if (risk <= threshold) return "";
  return `SL risk ₹${fmt(risk, 0)} exceeds ${pct.toFixed(2)}% of available margin (₹${fmt(available, 0)}). Informational only.`;
}

function renderOrderPad() {
  const ce = selectedOption("CE");
  const pe = selectedOption("PE");
  const hasLimits = S.limits?.loaded;
  const available = hasLimits ? num(S.limits?.available) : null;
  let defaultSlPts = 2.0;
  try {
    const savedPts = localStorage.getItem("scalper-sl-pts");
    if (savedPts) defaultSlPts = num(savedPts, 2.0);
  } catch (_) {}
  if (!Number.isFinite(defaultSlPts) || defaultSlPts <= 0) defaultSlPts = 2.0;
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
    const valBtn = document.querySelector(`.validity-toggle[data-validity-toggle="${type}"]`);
    if (valBtn) {
      const v = S.orderValidity?.[type] || "DAY";
      valBtn.textContent = v;
      valBtn.classList.toggle("active", v === "IOC");
      valBtn.title = `Validity: ${v} (Click to toggle DAY/IOC)`;
    }

    // Informational stop-loss risk estimate for this order pad.
    const quick = S.quickOrder && S.quickOrder.pane === (type === "CE" ? "ce" : "pe") ? S.quickOrder : null;
    let assumedSlPts = defaultSlPts;
    if (quick && quick.slEnabled) {
      const quickPts = Math.abs(num(quick.price) - num(quick.slPrice));
      if (quickPts > 0) assumedSlPts = quickPts;
    }
    if (!Number.isFinite(assumedSlPts) || assumedSlPts <= 0) assumedSlPts = defaultSlPts;
    const riskEl = $(`leg-${prefix}-sl-risk`);
    if (riskEl) {
      const pct = stopLossMarginWarnPct();
      const margin = availableMarginValue();
      const risk = assumedSlPts * shares;
      if (pct > 0 && margin !== null && risk > (margin * pct) / 100) {
        const riskPct = (risk / margin) * 100;
        riskEl.hidden = false;
        riskEl.textContent = `⚠ SL ${riskPct.toFixed(2)}% > ${pct.toFixed(2)}%`;
        riskEl.title = `Est. SL risk ₹${fmt(risk, 0)} (${assumedSlPts.toFixed(2)} pts × ${shares} shares) exceeds ${pct.toFixed(2)}% of available margin (₹${fmt(margin, 0)}). Informational only; orders are not blocked.`;
      } else {
        riskEl.hidden = true;
        riskEl.textContent = "";
        riskEl.title = "";
      }
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
      const token = option.pSymbol || (option.instrumentKey ? option.instrumentKey.split("|")[1] : "");
      const currentMarginKey = `${type}:${token}:${shares}:${S.product}`;
      if (option && shares > 0 && lastMarginReqKey[type] !== currentMarginKey) {
        lastMarginReqKey[type] = currentMarginKey;
        requestBrokerMargin(type, option, shares);
      }
    }

    $$(".leg-orderpad .trade").filter((button) => button.dataset.type === type).forEach((button) => {
      button.disabled = false;
    });
  }
  renderQuickOrderSLRiskWarning();
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
// computePayoff, payoffCurve and legRisk live in ./utils/payoff so the maths
// is unit-tested without a DOM.

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

function switchTab(tabName: string) {
  S.tab = tabName;
  if (S.tab === "brackets") {
    fetchBrackets().catch(() => {});
  }
  renderTab();
}

function closeMobileDrawer() {
  const backdrop = $("mobile-drawer-backdrop");
  if (backdrop) backdrop.hidden = true;
}

let bottomDirty = false;

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

  const holdingsBadge = $("holdings-badge");
  if (holdingsBadge) {
    const hCount = (S.holdings || []).length;
    holdingsBadge.textContent = String(hCount);
    holdingsBadge.hidden = hCount === 0;
  }

  const activeBracketsCount = (S.autoTargets || []).filter((b) => b.status === "PENDING_ENTRY" || b.status === "IN_POSITION").length;
  const bracketBadge = $("brackets-badge");
  if (bracketBadge) {
    bracketBadge.textContent = String(activeBracketsCount);
    bracketBadge.hidden = activeBracketsCount === 0;
  }

  const openPositionsCount = (S.positions || []).filter((p) => Math.abs(num(p.quantity)) > 0).length;
  const posBadge = $("positions-badge");
  if (posBadge) {
    posBadge.textContent = String(openPositionsCount);
    posBadge.hidden = openPositionsCount === 0;
  }
  const mobilePosBadge = $("mobile-pos-badge");
  if (mobilePosBadge) {
    mobilePosBadge.textContent = String(openPositionsCount);
    mobilePosBadge.hidden = openPositionsCount === 0;
  }
  const mbBottomPosBadge = $("mobile-bottom-pos-badge");
  if (mbBottomPosBadge) {
    mbBottomPosBadge.textContent = String(openPositionsCount);
    mbBottomPosBadge.hidden = openPositionsCount === 0;
  }

  const mobilePanicPill = $("mobile-panic-pill");
  if (mobilePanicPill) {
    if (openPositionsCount > 0) {
      mobilePanicPill.hidden = false;
      const countEl = $("mobile-panic-count");
      if (countEl) countEl.textContent = `${openPositionsCount} ${openPositionsCount === 1 ? "Pos" : "Positions"}`;
      const pnlEl = $("mobile-panic-pnl");
      if (pnlEl) {
        let totalPnl = 0;
        for (const p of S.positions) {
          if (Math.abs(num(p.quantity)) > 0) totalPnl += num(p.pnl || p.unrealised || 0);
        }
        pnlEl.textContent = `${totalPnl >= 0 ? "+" : ""}₹${fmt(totalPnl, 0)}`;
        pnlEl.className = `panic-pill-pnl ${totalPnl >= 0 ? "up" : "down"}`;
      }
    } else {
      mobilePanicPill.hidden = true;
    }
  }

  const hasOpen = S.positions.some((p) => Math.abs(num(p.quantity)) > 0);
  const sqBtn = $("square-off-button");
  if (sqBtn) sqBtn.disabled = !hasOpen;

  // The bottom panel ships collapsed. Avoid rebuilding hidden tables; mark
  // dirty and render once when the user expands it.
  const bottomPanel = $("bottom-panel");
  if (bottomPanel?.classList.contains("bottom-collapsed")) {
    bottomDirty = true;
    return;
  }
  bottomDirty = false;

  if (S.tab === "positions") content.innerHTML = positionsHTML();
  else if (S.tab === "brackets") content.innerHTML = bracketsHTML();
  else if (S.tab === "open") content.innerHTML = openOrdersHTML();
  else if (S.tab === "risk") {
    content.innerHTML = riskHTML();
    requestAnimationFrame(drawRiskCharts);
  } else if (S.tab === "orders") content.innerHTML = ordersHTML();
  else if (S.tab === "trades") content.innerHTML = tradesHTML();
  else if (S.tab === "holdings") content.innerHTML = holdingsHTML();
  else if (S.tab === "depth") content.innerHTML = depthHTML();
}

function positionGroup(position) {
  if (position.underlying && position.expiry) {
    return { key: `${position.underlying}|${position.expiry}`, label: `${position.underlying} · ${position.expiry}` };
  }
  // Equity/ETF/futures legs carry no option expiry. Keep them out of the
  // option groups so their premium cannot leak into a Nifty/Bank Nifty card.
  const label = position.symbol || position.instrument_key || "Unresolved position";
  return { key: `spot|${label}`, label };
}

function productLabel(position) {
  const raw = String(position?.product ?? "").trim().toUpperCase();
  if (!raw) return "—";
  if (raw === "NORMAL") return "NRML";
  if (raw === "INTRADAY") return "MIS";
  if (raw === "DELIVERY") return "CNC";
  return raw;
}

function positionsHTML() {
  if (!S.positions.length) return `<div class="empty-state">No open positions. Sign in with Kotak to view your account or place an order.</div>`;
  const sorted = [...S.positions].sort(comparePositions);
  const groups = new Map();
  for (const p of sorted) {
    const { key, label } = positionGroup(p);
    if (!groups.has(key)) groups.set(key, { label, list: [] });
    groups.get(key).list.push(p);
  }
  let html = `<table class="book"><thead><tr>
    <th>Contract</th><th title="Product type (MIS / NRML / CNC)">Type</th><th>Net</th><th>Avg</th><th>LTP</th><th>P&amp;L</th>
    <th>Max P</th><th>Max L</th><th></th>
  </tr></thead><tbody>`;
  for (const { label, list } of groups.values()) {
    const hasOpenInGroup = list.some((p) => Math.abs(num(p.quantity)) > 0);
    const optionLegs = list.filter((p) => isOptionLeg({
      type: p.option_type, strike: p.strike, qty: p.quantity, avg: p.average_price,
    }));
    const payoff = hasOpenInGroup && optionLegs.length
      ? computePayoff(optionLegs.map((p) => ({
        type: p.option_type,
        strike: p.strike,
        qty: num(p.quantity),
        avg: num(p.average_price),
      })))
      : null;

    let groupMeta = "";
    if (!hasOpenInGroup) {
      groupMeta = " — closed";
    } else if (payoff) {
      if (payoff.points.length) {
        groupMeta = ` — group Max P ${fmtMaxProfit(payoff.maxProfit)} · Max L ${fmtMaxLoss(payoff.maxLoss)}`;
      }
      if (payoff.excluded) {
        groupMeta += `${groupMeta ? " · " : " — "}${payoff.excluded} unpriced`;
      }
    }

    html += `<tr class="group-row${hasOpenInGroup ? "" : " closed"}"><td colspan="9">${esc(label)}${groupMeta}</td></tr>`;
    for (const p of list) {
      const risk = legRisk(p);
      const isOpen = Math.abs(num(p.quantity)) > 0;
      const openPnL = p.unrealised_pnl != null ? p.unrealised_pnl : p.pnl;
      html += `<tr class="${isOpen ? "" : "closed"}" data-pos-row="${esc(p.instrument_key)}">
        <td class="sym">${esc(p.symbol || `${p.underlying} ${fmtStrike(p.strike)} ${p.option_type}`)}</td>
        <td class="muted">${esc(productLabel(p))}</td>
        <td>${p.quantity > 0 ? "+" : ""}${p.quantity}</td>
        <td>${fmt(p.average_price)}</td>
        <td data-pos-ltp="${esc(p.instrument_key)}">${fmt(p.ltp)}</td>
        <td class="${directionClass(isOpen ? openPnL : p.pnl)}" data-pos-pnl="${esc(p.instrument_key)}">
          <span class="font-bold">${fmtSigned(isOpen ? openPnL : p.pnl, 0)}</span>
          ${isOpen && p.realised_pnl ? `<span class="muted" style="display:block;font-size:9.5px;" title="Realised earlier today">Day: ${fmtSigned(p.pnl, 0)}</span>` : ""}
        </td>
        <td>${risk ? fmtMaxProfit(risk.maxProfit) : "—"}</td>
        <td>${risk ? fmtMaxLoss(risk.maxLoss) : "—"}</td>
        <td>${isOpen ? `<button type="button" class="link-button" data-exit="${esc(p.instrument_key)}">EXIT</button>` : `<span class="muted">CLOSED</span>`}</td>
      </tr>`;
    }
  }
  return html + `</tbody></table>`;
}

function riskHTML() {
  const groups = S.risk?.length ? S.risk : buildRiskGroups(S.positions);
  if (!groups.length) {
    return `<div class="empty-state">No open legs. The at-expiry payoff appears here once a position is open.</div>`;
  }
  const unpricedPositions = (S.positions || []).filter((position) => {
    const type = String(position?.option_type ?? "").toUpperCase();
    return Math.abs(num(position?.quantity)) > 0
      && (type === "CE" || type === "PE")
      && (num(position?.strike) <= 0 || num(position?.average_price) <= 0);
  }).length;
  const unpricedGroups = groups.reduce((sum, group) => sum + num(group.excluded), 0);
  const excluded = Math.max(unpricedPositions, unpricedGroups);
  const warning = excluded > 0
    ? `<div class="risk-warning">${excluded} open leg${excluded === 1 ? "" : "s"} excluded from the payoff: strike or average price unavailable.</div>`
    : "";
  return `${warning}<div class="risk-cards">${groups.map((group, index) => {
    const hasPayoff = (group.points || []).length > 0;
    const maxP = hasPayoff ? fmtMaxProfit(group.maxProfit) : "—";
    const maxL = hasPayoff ? fmtMaxLoss(group.maxLoss) : "—";
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
        <div class="risk-metric"><span>Net premium</span><b>${fmtMoney(num(group.netPremium))}</b></div>
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
  const rangePct = num(S.boot.payoffRangePct, 10);
  const count = num(S.boot.payoffPoints, 121);
  groups.forEach((group, index) => {
    const canvas = $(`risk-canvas-${index}`);
    if (!canvas) return;
    const spot = S.spots.get(group.underlying)?.ltp || 0;
    const curve = payoffCurve(group.legs || [], spot, rangePct, count);
    drawPayoff(canvas, curve.length ? curve : group.points || [], spot);
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
    <th>Order ID</th><th>Contract</th><th>Side</th><th>Qty</th><th>Price</th><th>Status</th><th>Time</th><th>Action</th>
  </tr></thead><tbody>${S.orders.map((o) => `<tr>
    <td class="sym"><button type="button" class="link-button" data-order-history="${esc(o.id)}" title="Click to view lifecycle audit history">${esc(o.id)}</button></td>
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

function holdingsHTML() {
  if (!S.holdings || !S.holdings.length) {
    return `<div class="empty-state">No demat holdings found in this account.</div>`;
  }
  let totalInvested = 0;
  let totalCurrent = 0;
  let totalPnL = 0;
  for (const h of S.holdings) {
    totalInvested += h.invested;
    totalCurrent += h.current_value;
    totalPnL += h.pnl;
  }
  const totalPnLPct = totalInvested > 0 ? (totalPnL / totalInvested) * 100 : 0;

  let html = `<div class="holdings-summary" style="display:flex; gap:20px; padding:10px 14px; margin-bottom:8px; background:rgba(255,255,255,0.03); border-radius:6px; font-size:12px;">
    <div><span class="muted">Total Invested:</span> <strong>₹${fmt(totalInvested, 2)}</strong></div>
    <div><span class="muted">Current Value:</span> <strong>₹${fmt(totalCurrent, 2)}</strong></div>
    <div><span class="muted">Overall P&amp;L:</span> <strong class="${directionClass(totalPnL)}">${fmtSigned(totalPnL, 2)} (${fmtSigned(totalPnLPct, 2)}%)</strong></div>
  </div>`;

  html += `<table class="book"><thead><tr>
    <th>Symbol</th><th>Qty</th><th>Avg Price</th><th>LTP</th><th>Invested</th><th>Current</th><th>P&amp;L</th><th>P&amp;L %</th>
  </tr></thead><tbody>`;

  for (const h of S.holdings) {
    html += `<tr>
      <td class="sym font-bold">${esc(h.symbol)} <span class="muted" style="font-size:10px;">${esc(h.segment)}</span></td>
      <td>${h.quantity}</td>
      <td>₹${fmt(h.average_price)}</td>
      <td>₹${fmt(h.ltp)}</td>
      <td>₹${fmt(h.invested, 2)}</td>
      <td>₹${fmt(h.current_value, 2)}</td>
      <td class="${directionClass(h.pnl)}">${fmtSigned(h.pnl, 2)}</td>
      <td class="${directionClass(h.pnl_pct)}">${fmtSigned(h.pnl_pct, 2)}%</td>
    </tr>`;
  }
  html += `</tbody></table>`;
  return html;
}

async function showOrderHistory(orderId) {
  const modal = $("order-history-modal");
  const meta = $("order-history-meta");
  const body = $("order-history-body");
  if (!modal || !body) return;

  modal.hidden = false;
  body.innerHTML = `<div class="empty-state">Loading history for order ${esc(orderId)}…</div>`;
  if (meta) meta.innerHTML = `<span>Order ID: <strong>${esc(orderId)}</strong></span>`;

  try {
    const raw = await getJSON(`/api/broker/orders/${encodeURIComponent(orderId)}/history`);
    const historyList = toArray(raw);
    if (!historyList || !historyList.length) {
      body.innerHTML = `<div class="empty-state">No lifecycle events recorded for order ${esc(orderId)}.</div>`;
      return;
    }

    const first = historyList[0] || {};
    if (meta) {
      meta.innerHTML = `
        <div>Order: <strong>${esc(orderId)}</strong></div>
        <div>Symbol: <strong>${esc(first.trdSym || first.tradingSymbol || first.sym || "—")}</strong></div>
        <div>Type: <strong>${esc(first.prcTp || first.order_type || "—")}</strong></div>
        <div>Prod: <strong>${esc(first.prod || first.product || "—")}</strong></div>
      `;
    }

    let html = `<table class="book" style="width:100%; font-size:12px;"><thead><tr>
      <th>Status</th><th>Filled / Total</th><th>Price</th><th>Avg Price</th><th>Reject Reason</th><th>Timestamp</th>
    </tr></thead><tbody>`;

    for (const h of historyList) {
      const st = (h.ordSt || h.status || "").toLowerCase();
      const statusClass = st === "complete" ? "up" : st === "rejected" || st === "cancelled" ? "down" : "warn";
      const rej = h.rejRsn && h.rejRsn !== "--" && h.rejRsn !== "" ? h.rejRsn : "—";
      const tm = h.exchTmstp || h.timestamp || h.orderDateTime || h.time || "—";
      const qty = h.qty || h.quantity || 0;
      const fldQty = h.fldQty ?? h.filledQuantity ?? 0;
      const prc = num(h.prc || h.price);
      const avgPrc = num(h.avgPrc || h.average_price);

      html += `<tr>
        <td class="${statusClass}"><strong>${esc(h.ordSt || h.status || "—")}</strong></td>
        <td>${fldQty} / ${qty}</td>
        <td>${prc ? fmt(prc) : "MKT"}</td>
        <td>${avgPrc ? fmt(avgPrc) : "—"}</td>
        <td class="${rej !== "—" ? "down font-bold" : "muted"}">${esc(rej)}</td>
        <td class="mono" style="font-size:11px;">${esc(tm)}</td>
      </tr>`;
    }
    html += `</tbody></table>`;
    body.innerHTML = html;
  } catch (err) {
    body.innerHTML = `<div class="empty-state" style="color:var(--danger,#f44336);">Failed to fetch order history: ${esc(err.message)}</div>`;
  }
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
  const ver = (S.boot.version || "dev").trim();
  let displayVer = ver;
  if (!displayVer || displayVer.toLowerCase() === "dev" || displayVer.toLowerCase().startsWith("dev")) {
    displayVer = displayVer || "dev";
  } else if (/^[0-9a-f]{7,40}(-dirty)?$/i.test(displayVer)) {
    displayVer = `dev (${displayVer})`;
  } else if (!displayVer.toLowerCase().startsWith("v")) {
    displayVer = `v${displayVer}`;
  }
  $("footer-version").textContent = displayVer;
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
  if (action === "ask") {
    onAskAI(type);
    return;
  }
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
    validity: S.orderValidity?.[type] || "DAY",
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

async function onLimitTrade(action, type, targetPrice, shares, autoTargetPrice = 0, autoStopPrice = 0, exchangeStop = null) {
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
    validity: S.orderValidity?.[type] || "DAY",
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

    const hasExchangeBackstop = exchangeStop && (exchangeStop.mode === "SL-M" || exchangeStop.mode === "SL");
    if (autoTargetPrice > 0 || autoStopPrice > 0 || hasExchangeBackstop) {
      const newTarget = {
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
        stopLossPrice: autoStopPrice,
        trailingStop: 0,
        shares: qty,
        filledQty: 0,
        exitOrderId: "",
        exitPrice: 0,
        exchangeStopMode: exchangeStop?.mode || "",
        exchangeStopOrderId: "",
        exchangeStopPrice: exchangeStop?.price || 0,
        exchangeStopStatus: "",
        ltp: price,
        status: "PENDING_ENTRY",
        createdAt: Date.now(),
      };
      registerAutoTarget(newTarget);
      postJSON("/api/brackets", {
        id: newTarget.id,
        exchange_segment: option.exchangeSegment,
        trading_symbol: option.symbol,
        instrument_key: option.instrumentKey,
        transaction_type: action.toUpperCase(),
        quantity: qty,
        product: S.product || "MIS",
        entry_price: price,
        entry_order_id: orderId,
        target_price: autoTargetPrice,
        stoploss_price: autoStopPrice,
        trailing_stop: 0,
        exchange_stop_mode: exchangeStop?.mode || "",
      }).catch((err) => {
        console.warn("failed to register server bracket:", err);
      });
      const exits = [];
      if (autoTargetPrice > 0) {
        exits.push(`Target ₹${autoTargetPrice.toFixed(2)} (+${Math.abs(autoTargetPrice - price).toFixed(2)} pts)`);
      }
      if (autoStopPrice > 0) {
        exits.push(`SL ₹${autoStopPrice.toFixed(2)} (−${Math.abs(price - autoStopPrice).toFixed(2)} pts)`);
      }
      if (hasExchangeBackstop) {
        exits.push(`${exchangeStop.mode === "SL" ? "SL-L" : "SL-M"} ₹${Number(exchangeStop.price).toFixed(2)} at EXCH`);
      }
      toast(`🎯 Auto exits armed: ${exits.join(" · ")} once filled.`, "info", 4500);
      renderTab();
      scheduleChartTrades();
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
    return Array.isArray(list)
      ? list.map(normalizeBracket).filter((t) => t && (now - (t.createdAt || 0)) < 86400000)
      : [];
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

function normalizeBracket(raw) {
  if (!raw) return null;
  const id = String(raw.id ?? raw.ID ?? "");
  if (!id) return null;

  let action = String(raw.action || raw.side || raw.transaction_type || raw.Action || "buy").toLowerCase();
  if (action === "b") action = "buy";
  if (action === "s") action = "sell";

  let status = String(raw.status || raw.Status || "PENDING_ENTRY").toUpperCase();
  if (status === "OPEN" || status === "ACTIVE") status = "IN_POSITION";
  if (status === "PENDING") status = "PENDING_ENTRY";
  if (status === "CANCELED") status = "CANCELLED";
  if (status === "TRIGGERED" || status === "HIT") status = "TARGET_HIT";
  if (status === "MANUAL_EXIT") status = "EXITED_MANUAL";

  const entryPrice = num(raw.entry_price ?? raw.entryPrice ?? raw.price ?? raw.EntryPrice);
  const targetPrice = num(raw.target_price ?? raw.targetPrice ?? raw.target ?? raw.TargetPrice);
  const stopLossPrice = num(raw.stoploss_price ?? raw.stopLossPrice ?? raw.stop_loss_price ?? raw.StopLossPrice ?? raw.sl_price ?? 0);
  const trailingStop = num(raw.trailing_stop ?? raw.trailingStop ?? raw.TrailingStop ?? 0);
  const exitPrice = num(raw.exit_price ?? raw.exitPrice ?? raw.ExitPrice);
  const shares = Math.abs(num(raw.shares ?? raw.quantity ?? raw.qty ?? raw.Shares));
  const filledQty = num(raw.filled_qty ?? raw.filledQty ?? raw.filled_quantity ?? raw.FilledQty);
  const ltp = num(raw.ltp ?? raw.last_price ?? raw.LTP ?? 0);

  const sym = String(raw.trading_symbol || raw.tradingSymbol || raw.symbol || raw.Symbol || "").trim();
  const key = String(raw.instrument_key || raw.instrumentKey || raw.InstrumentKey || "").trim();
  let underlying = String(raw.underlying || raw.Underlying || "").trim();
  let strike = num(raw.strike ?? raw.Strike);
  let type = String(raw.option_type || raw.type || raw.Type || "").toUpperCase();

  // 1. First priority: parse structured instrument_key (e.g. nse_fo|NIFTY|2026-11-09|24850|PE)
  if (key.includes("|")) {
    const parts = key.split("|");
    if (parts.length >= 5) {
      if (!underlying) underlying = parts[1];
      if (!strike) strike = num(parts[3]);
      if (!type) type = parts[4].toUpperCase();
    }
  }

  // 2. Second priority: parse trading_symbol (e.g. NIFTY26N0924850PE or SENSEX26DEC80000CE)
  if ((!strike || !type) && sym) {
    const match = /(?:CE|PE)?.*?(\d{4,6})\s*(CE|PE)$/i.exec(sym);
    if (match) {
      if (!strike) strike = Number(match[1]);
      if (!type) type = match[2].toUpperCase();
    }
  }

  if (!underlying) underlying = S.activeIndex || "";

  return {
    id,
    orderId: String(raw.order_id ?? raw.orderId ?? raw.OrderId ?? ""),
    symbol: sym,
    instrumentKey: key,
    exchangeSegment: raw.exchange_segment || raw.exchangeSegment || raw.ExchangeSegment || "NSE_FO",
    underlying,
    strike,
    type,
    action,
    entryPrice,
    targetPrice,
    stopLossPrice,
    trailingStop,
    shares,
    filledQty,
    exchangeStopMode: String(raw.exchange_stop_mode ?? raw.exchangeStopMode ?? ""),
    exchangeStopOrderId: String(raw.exchange_stop_order_id ?? raw.exchangeStopOrderId ?? ""),
    exchangeStopPrice: num(raw.exchange_stop_price ?? raw.exchangeStopPrice ?? 0),
    exchangeStopStatus: String(raw.exchange_stop_status ?? raw.exchangeStopStatus ?? ""),
    exitOrderId: String(raw.exit_order_id ?? raw.exitOrderId ?? raw.ExitOrderId ?? ""),
    exitPrice,
    ltp: ltp > 0 ? ltp : entryPrice,
    status,
    createdAt: num(raw.created_at ?? raw.createdAt ?? raw.CreatedAt) || Date.now(),
  };
}

function upsertBracket(bracket) {
  if (!bracket || !bracket.id) return;
  if (!S.autoTargets) S.autoTargets = [];
  const idx = S.autoTargets.findIndex((b) => b.id === bracket.id);
  if (idx !== -1) {
    const existing = S.autoTargets[idx];
    if (!bracket.ltp && existing.ltp) bracket.ltp = existing.ltp;
    // A pending bracket has no exchange trigger yet; keep the local preview
    // until the server computes one from the actual entry fill.
    if (!bracket.exchangeStopPrice && existing.exchangeStopPrice) {
      bracket.exchangeStopPrice = existing.exchangeStopPrice;
    }
    const merged = {
      ...existing,
      ...bracket,
      symbol: bracket.symbol || existing.symbol || "",
      underlying: bracket.underlying || existing.underlying || S.activeIndex || "",
      strike: bracket.strike || existing.strike || 0,
      type: bracket.type || existing.type || "",
    };
    if (merged.instrumentKey && (!merged.strike || !merged.type || !merged.underlying)) {
      const parts = String(merged.instrumentKey).split("|");
      if (parts.length >= 5) {
        if (!merged.underlying || merged.underlying === S.activeIndex) merged.underlying = parts[1];
        if (!merged.strike) merged.strike = num(parts[3]);
        if (!merged.type) merged.type = parts[4].toUpperCase();
      }
    }
    if ((!merged.strike || !merged.type) && merged.symbol) {
      const match = /(?:CE|PE)?.*?(\d{4,6})\s*(CE|PE)$/i.exec(merged.symbol);
      if (match) {
        if (!merged.strike) merged.strike = Number(match[1]);
        if (!merged.type) merged.type = match[2].toUpperCase();
      }
    }
    S.autoTargets[idx] = merged;
  } else {
    S.autoTargets.unshift(bracket);
  }
}

function applyServerBrackets(data) {
  if (!data) return;
  const list = Array.isArray(data) ? data : (data.brackets || data.data || []);
  if (!Array.isArray(list)) return;

  const normalized = list.map(normalizeBracket).filter(Boolean);
  if (!S.autoTargets) S.autoTargets = [];

  for (const b of normalized) {
    upsertBracket(b);
  }

  saveAutoTargets();
  renderTab();
  scheduleChartTrades();
}

async function fetchBrackets() {
  if (!S.session?.authenticated) return;
  try {
    const res = await getJSON("/api/brackets");
    if (res) applyServerBrackets(res);
  } catch (_) {}
}

function handleBracketUpdate(data) {
  if (!data) return;
  const items = Array.isArray(data)
    ? data
    : Array.isArray(data.brackets)
      ? data.brackets
      : [data.bracket || data];
  for (const item of items) {
    if (!item) continue;
    const norm = normalizeBracket(item);
    if (!norm || !norm.id) continue;
    const previous = (S.autoTargets || []).find((b) => b.id === norm.id);
    const previousStatus = previous?.status || "";
    upsertBracket(norm);
    // The engine emits several updates for one target hit (trigger + exit fill),
    // so only announce the status transition to avoid duplicate toasts/sounds.
    if (norm.status === "TARGET_HIT" && previousStatus !== "TARGET_HIT") {
      playTradeSound("target");
      const pnl = (norm.exitPrice - norm.entryPrice) * norm.shares * (norm.action === "buy" ? 1 : -1);
      toast(`🎯 Bracket Target Hit! Sold ${norm.shares} @ ₹${fmt(norm.exitPrice || norm.targetPrice)} (Profit: +₹${fmt(pnl, 0)})`, "ok", 8000);
      S.charts.ce?.clearTargetPreview?.();
      S.charts.pe?.clearTargetPreview?.();
    } else if (norm.status === "SL_HIT" && previousStatus !== "SL_HIT") {
      playTradeSound("reject");
      const pnl = (norm.exitPrice - norm.entryPrice) * norm.shares * (norm.action === "buy" ? 1 : -1);
      toast(`🛑 Stop-Loss Hit! Sold ${norm.shares} @ ₹${fmt(norm.exitPrice || norm.stopLossPrice)} (P&L: ${fmtSigned(pnl, 0)})`, "warn", 8000);
      S.charts.ce?.clearTargetPreview?.();
      S.charts.pe?.clearTargetPreview?.();
    } else if (norm.status === "CANCELLED" && previousStatus !== "CANCELLED") {
      toast(`Bracket ${norm.symbol || norm.id} cancelled.`, "warn", 4000);
    }
  }
  saveAutoTargets();
  renderTab();
  scheduleChartTrades();
}

function findChartBracket(option) {
  if (!option) return null;
  const brackets = S.autoTargets || [];
  return (
    brackets.find((b) => b.status === "IN_POSITION" && matchesContract(b, option)) ||
    brackets.find((b) => b.status === "PENDING_ENTRY" && matchesContract(b, option)) ||
    null
  );
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
      <button type="button" class="ghost small" id="btn-refresh-brackets" title="Refresh brackets from server">Refresh</button>
      ${hasFinished ? `<button type="button" class="ghost small" id="btn-clear-brackets" title="Remove completed and cancelled brackets from the list">Clear Finished</button>` : ""}
    </div>
  </div>
  <table class="book"><thead><tr>
    <th>Contract</th><th>Side</th><th>Qty</th><th>Entry Price</th><th>Target <span class="muted">SRV</span></th><th>SL <span class="muted">SRV</span></th><th>Backstop <span class="muted">EXCH</span></th><th>Live LTP</th><th>Isolated P&amp;L</th><th>Status</th><th>Action</th>
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
    } else if (b.status === "TARGET_HIT" || b.status === "SL_HIT" || b.status === "EXITED_MANUAL" || b.status === "COMPLETED") {
      const exitP = b.exitPrice || (b.status === "SL_HIT" ? b.stopLossPrice : b.targetPrice);
      pnl = (exitP - b.entryPrice) * b.shares * (b.action === "buy" ? 1 : -1);
      pnlClass = directionClass(pnl);
      pnlText = fmtSigned(pnl, 0) + (b.status === "TARGET_HIT" ? " 🎯" : b.status === "SL_HIT" ? " 🛑" : "");
    }

    let badgeClass = "pending";
    let badgeLabel = "PENDING ENTRY";
    if (b.status === "IN_POSITION") {
      badgeClass = "in-position";
      badgeLabel = "IN POSITION";
    } else if (b.status === "TARGET_HIT") {
      badgeClass = "target-hit";
      badgeLabel = "TARGET HIT";
    } else if (b.status === "SL_HIT") {
      badgeClass = "sl-hit";
      badgeLabel = "SL HIT";
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
      actionBtn = `<button type="button" class="btn-cancel-bracket danger small ghost" data-cancel-bracket="${esc(b.id)}" title="Cancel pending entry limit order">Cancel</button>`;
    }

    const sideLabel = b.action === "buy" ? "BUY" : "SELL";
    const sideClass = b.action === "buy" ? "up" : "down";
    const targetDiff = (b.targetPrice - b.entryPrice) * (b.action === "buy" ? 1 : -1);

    const contractDisplay = (b.strike && b.type && b.strike > 0)
      ? `${b.underlying || S.activeIndex} ${fmtStrike(b.strike)} ${b.type}`
      : (b.symbol || `${b.underlying || S.activeIndex}`);

    const hasBackstop = b.exchangeStopMode && b.exchangeStopMode !== "OFF" && b.exchangeStopPrice > 0;
    const backstopMode = b.exchangeStopMode === "SL" ? "SL-L" : b.exchangeStopMode;
    const backstopStatus = String(b.exchangeStopStatus || "").toUpperCase();
    const backstopText = hasBackstop
      ? `EXCH ${backstopMode} ₹${fmt(b.exchangeStopPrice)}${backstopStatus ? ` · ${backstopStatus}` : ""}`
      : "—";
    const backstopClass = !hasBackstop
      ? "muted"
      : backstopStatus === "FILLED"
        ? "sl-hit"
        : (backstopStatus === "REJECTED" || backstopStatus === "FAILED")
          ? "muted"
          : "pending";

    html += `<tr data-bracket-id="${esc(b.id)}">
      <td class="sym font-bold" title="${esc(b.symbol)}">${esc(contractDisplay)}</td>
      <td class="${sideClass} font-bold">${sideLabel}</td>
      <td>${b.shares}</td>
      <td class="font-bold">₹${fmt(b.entryPrice)}</td>
      <td class="up font-bold">${b.targetPrice > 0 ? `₹${fmt(b.targetPrice)} <span class="muted" style="font-size:9.5px;">(+${targetDiff.toFixed(2)})</span>` : "—"}</td>
      <td class="down font-bold">${b.stopLossPrice > 0 ? `₹${fmt(b.stopLossPrice)} <span class="muted" style="font-size:9.5px;">(−${Math.abs(b.entryPrice - b.stopLossPrice).toFixed(2)})</span>` : "—"}</td>
      <td><span class="status-badge ${backstopClass}">${esc(backstopText)}</span></td>
      <td class="font-bold" data-bracket-ltp="${esc(b.id)}">₹${fmt(ltp)}</td>
      <td class="${pnlClass} font-bold" data-bracket-pnl="${esc(b.id)}">${pnlText}</td>
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

  const ok = await confirmAction({
    title: "Exit Bracket Position",
    body: `Exit <b>${bracket.shares}</b> shares of <b>${esc(bracket.symbol)}</b> at Market now?<br><span class="muted">Any resting target limit order will be automatically cancelled.</span>`,
    okLabel: "Exit at Market",
    danger: true,
  });
  if (!ok) return;

  try {
    toast("Exiting bracket position @ Market…", "info", 2000);
    await postJSON(`/api/brackets/${encodeURIComponent(bracketId)}/exit`, { confirm: true });
    lastUiOrderTime = Date.now();
    playTradeSound(bracket.action === "buy" ? "sell" : "buy");
    bracket.status = "EXITED_MANUAL";
    bracket.exitPrice = getInstrumentLTP(bracket);
    saveAutoTargets();
    toast("Bracket exited at market.", "ok", 4000);
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

  try {
    toast("Cancelling bracket entry order…", "info", 2000);
    await postJSON(`/api/brackets/${encodeURIComponent(bracketId)}/cancel`, { confirm: true });
    bracket.status = "CANCELLED";
    saveAutoTargets();
    if (bracket.orderId && bracket.orderId !== "—") {
      const oInState = (S.orders || []).find((o) => String(o.id) === String(bracket.orderId));
      if (oInState) {
        oInState.status = "CANCELLED";
      }
    }
    playTradeSound("reject");
    toast("Bracket entry order cancelled.", "ok", 3000);
    scheduleBooksRefresh(400);
    renderTab();
    updateChartTrades();
  } catch (err) {
    playTradeSound("reject");
    toast(`Bracket cancel failed: ${err.message}`, "error", 6000);
  }
}

function onClearFinishedBrackets() {
  if (!S.autoTargets) return;
  S.autoTargets = S.autoTargets.filter((b) => b.status === "PENDING_ENTRY" || b.status === "IN_POSITION");
  saveAutoTargets();
  renderTab();
}

function checkAutoTargets(eventOrder?: any) {
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
          const exitFill = num(matchedExit.price) || target.targetPrice;
          const stopLevel = num(target.stopLossPrice);
          const stopHit = stopLevel > 0 && (target.action === "buy" ? exitFill <= stopLevel : exitFill >= stopLevel);
          target.status = stopHit ? "SL_HIT" : "TARGET_HIT";
          target.exitPrice = exitFill;
          saveAutoTargets();
          const pnl = (target.exitPrice - target.entryPrice) * target.shares * (target.action === "buy" ? 1 : -1);
          if (stopHit) {
            playTradeSound("reject");
            toast(`🛑 Stop-Loss Hit! Sold ${target.shares} @ ₹${fmt(target.exitPrice)} (P&L: ${fmtSigned(pnl, 0)})`, "warn", 8000);
          } else {
            playTradeSound("target");
            toast(`🎯 Bracket Target Hit! Sold ${target.shares} @ ₹${fmt(target.exitPrice)} (Profit: +₹${fmt(pnl, 0)})`, "ok", 8000);
          }

          if (target.exitOrderId) {
            const oInState = (S.orders || []).find((o) => String(o.id) === String(target.exitOrderId));
            if (oInState) {
              oInState.status = "TRAD";
              oInState.filled_quantity = target.shares;
            }
          }
          S.charts.ce?.clearTargetPreview?.();
          S.charts.pe?.clearTargetPreview?.();

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

const syncAutoTargetsWithOrders = checkAutoTargets;

/* ── Chart Quick Limit Order ───────────────────────────── */

function initChartQuickOrder() {
  $("cqo-close")?.addEventListener("click", closeChartQuickOrder);

  $("cqo-price-dec")?.addEventListener("click", () => {
    if (S.quickOrder) updateQuickOrderPrice(S.quickOrder.price - 0.05);
  });
  $("cqo-price-inc")?.addEventListener("click", () => {
    if (S.quickOrder) updateQuickOrderPrice(S.quickOrder.price + 0.05);
  });

  $("cqo-price")?.addEventListener("focus", (e: any) => {
    e.target?.select?.();
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
      if (S.quickOrder.slEnabled) {
        const pts = Math.min(S.quickOrder.slPts, Math.max(0.05, val - 0.05));
        S.quickOrder.slPts = pts;
        S.quickOrder.slPrice = Math.max(0.05, Math.round((val - pts) * 20) / 20);
        renderQuickOrderSL();
        S.charts[S.quickOrder.pane]?.showStopPreview(S.quickOrder.slPrice, `SL ₹${S.quickOrder.slPrice.toFixed(2)}`);
      }
      renderQuickOrderExchangeStop();
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
    const currentTarget = num($("cqo-target-price")?.value, S.quickOrder.targetPrice);
    const newTarget = Math.max(S.quickOrder.price + 0.05, Math.round((currentTarget - 0.50) * 20) / 20);
    updateQuickOrderTargetPrice(newTarget);
  });

  $("cqo-target-inc")?.addEventListener("click", () => {
    if (!S.quickOrder) return;
    const currentTarget = num($("cqo-target-price")?.value, S.quickOrder.targetPrice);
    const newTarget = Math.round((currentTarget + 0.50) * 20) / 20;
    updateQuickOrderTargetPrice(newTarget);
  });

  $("cqo-target-price")?.addEventListener("focus", (e: any) => {
    e.target?.select?.();
  });

  $("cqo-target-price")?.addEventListener("input", (e: any) => {
    if (!S.quickOrder) return;
    const rawVal = e.target.value;
    const val = parseFloat(rawVal);
    if (!isNaN(val) && val > 0) {
      S.quickOrder.targetPrice = val;
      const pts = Math.round((val - S.quickOrder.price) * 100) / 100;
      S.quickOrder.targetPts = Math.max(0.05, pts);
      if (val > S.quickOrder.price) {
        try { localStorage.setItem("scalper-target-pts", String(S.quickOrder.targetPts)); } catch (_) {}
        const shares = S.quickOrder.lotsCount * S.quickOrder.lot;
        const pnl = Math.round(pts * shares);
        const ptsEl = $("cqo-target-pts");
        if (ptsEl) ptsEl.textContent = `+${pts.toFixed(2)} pts`;
        const pnlEl = $("cqo-target-pnl");
        if (pnlEl) pnlEl.textContent = `(Est. +₹${pnl.toLocaleString("en-IN")})`;
        if (S.quickOrder.targetEnabled) {
          S.charts[S.quickOrder.pane]?.showTargetPreview(val, "TARGET");
        }
      } else {
        const ptsEl = $("cqo-target-pts");
        if (ptsEl) ptsEl.textContent = `${pts >= 0 ? "+" : ""}${pts.toFixed(2)} pts`;
        const pnlEl = $("cqo-target-pnl");
        if (pnlEl) pnlEl.textContent = "(Est. —)";
      }
    }
  });

  const commitTargetPrice = (e: any) => {
    if (!S.quickOrder) return;
    const val = parseFloat(e.target.value);
    if (!isNaN(val) && val > S.quickOrder.price) {
      updateQuickOrderTargetPrice(val);
    } else if (!isNaN(val) && val > 0) {
      updateQuickOrderTargetPrice(S.quickOrder.price + 0.05);
    } else {
      const fallbackPts = Math.max(0.5, S.quickOrder.targetPts || 0.5);
      updateQuickOrderTargetPts(fallbackPts);
    }
  };

  $("cqo-target-price")?.addEventListener("change", commitTargetPrice);
  $("cqo-target-price")?.addEventListener("blur", commitTargetPrice);

  // Auto Stop-Loss controls
  $("cqo-sl-enable")?.addEventListener("change", (e) => {
    if (!S.quickOrder) return;
    S.quickOrder.slEnabled = e.target.checked;
    try {
      localStorage.setItem("scalper-sl-enabled", e.target.checked ? "true" : "false");
    } catch (_) {}
    renderQuickOrderSL();
    if (S.quickOrder.slEnabled) {
      S.charts[S.quickOrder.pane]?.showStopPreview(S.quickOrder.slPrice, `SL ₹${S.quickOrder.slPrice.toFixed(2)}`);
    } else {
      S.charts[S.quickOrder.pane]?.clearStopPreview();
    }
  });

  $("cqo-sl-dec")?.addEventListener("click", () => {
    if (!S.quickOrder) return;
    updateQuickOrderSLPts(S.quickOrder.slPts + 0.50);
  });

  $("cqo-sl-inc")?.addEventListener("click", () => {
    if (!S.quickOrder) return;
    updateQuickOrderSLPts(S.quickOrder.slPts - 0.50);
  });

  $("cqo-sl-price")?.addEventListener("focus", (e: any) => {
    e.target?.select?.();
  });

  $("cqo-sl-price")?.addEventListener("input", (e: any) => {
    if (!S.quickOrder) return;
    const val = parseFloat(e.target.value);
    if (!isNaN(val) && val > 0 && val < S.quickOrder.price) {
      S.quickOrder.slPrice = val;
      S.quickOrder.slPts = Math.max(0.05, Math.round((S.quickOrder.price - val) * 100) / 100);
      const ptsEl = $("cqo-sl-pts");
      if (ptsEl) ptsEl.textContent = `−${S.quickOrder.slPts.toFixed(2)} pts`;
      const risk = Math.round(S.quickOrder.slPts * S.quickOrder.lotsCount * S.quickOrder.lot);
      const pnlEl = $("cqo-sl-pnl");
      if (pnlEl) pnlEl.textContent = `(Risk −₹${risk.toLocaleString("en-IN")})`;
      if (S.quickOrder.slEnabled) {
        S.charts[S.quickOrder.pane]?.showStopPreview(val, `SL ₹${val.toFixed(2)}`);
      }
      renderQuickOrderExchangeStop();
    }
  });

  const commitSlPrice = (e: any) => {
    if (!S.quickOrder) return;
    const val = parseFloat(e.target.value);
    if (!isNaN(val) && val > 0 && val < S.quickOrder.price) {
      updateQuickOrderSLPrice(val);
    } else {
      updateQuickOrderSLPts(Math.max(0.05, S.quickOrder.slPts || 2.0));
    }
  };

  $("cqo-sl-price")?.addEventListener("change", commitSlPrice);
  $("cqo-sl-price")?.addEventListener("blur", commitSlPrice);

  // Exchange backstop controls
  $("cqo-exchange-stop-enable")?.addEventListener("change", (e) => {
    if (!S.quickOrder) return;
    S.quickOrder.exchangeStopEnabled = e.target.checked;
    try {
      localStorage.setItem("scalper-exchange-stop-enabled", e.target.checked ? "true" : "false");
    } catch (_) {}
    renderQuickOrderExchangeStop();
  });

  $("cqo-exchange-stop-mode")?.addEventListener("change", (e) => {
    if (!S.quickOrder) return;
    S.quickOrder.exchangeStopMode = e.target.value === "SL" ? "SL" : "SL-M";
    try {
      localStorage.setItem("scalper-exchange-stop-mode", S.quickOrder.exchangeStopMode);
    } catch (_) {}
    renderQuickOrderExchangeStop();
  });

  $("cqo-buy-btn")?.addEventListener("click", () => submitQuickOrder("buy"));
  $("cqo-sell-btn")?.addEventListener("click", () => submitQuickOrder("sell"));

  const widget = $("chart-quick-order");
  if (widget) {
    let isDragging = false;
    let activePointerId = -1;
    let startX = 0;
    let startY = 0;
    let startLeft = 0;
    let startTop = 0;

    widget.addEventListener("pointerdown", (e: PointerEvent) => {
      e.stopPropagation();
      if (e.button !== 0 || isDragging) return;
      const target = e.target as HTMLElement | null;
      // Do not initiate drag if user clicked interactive controls
      if (target?.closest("button, input, select, textarea, label, a, .cqo-stepper, .cqo-actions")) {
        return;
      }

      isDragging = true;
      activePointerId = e.pointerId;
      startX = e.clientX;
      startY = e.clientY;
      startLeft = widget.offsetLeft;
      startTop = widget.offsetTop;
      widget.classList.add("is-dragging");
      try {
        widget.setPointerCapture(e.pointerId);
      } catch (_) {}
      e.preventDefault();
    });

    widget.addEventListener("pointermove", (e: PointerEvent) => {
      if (!isDragging || e.pointerId !== activePointerId) return;
      e.preventDefault();
      e.stopPropagation();

      const frame = widget.parentElement;
      const maxLeft = frame ? frame.clientWidth - widget.offsetWidth : window.innerWidth - widget.offsetWidth;
      const maxTop = frame ? frame.clientHeight - widget.offsetHeight : window.innerHeight - widget.offsetHeight;

      const dx = e.clientX - startX;
      const dy = e.clientY - startY;

      const newLeft = Math.max(0, Math.min(startLeft + dx, Math.max(0, maxLeft)));
      const newTop = Math.max(0, Math.min(startTop + dy, Math.max(0, maxTop)));

      widget.style.left = `${Math.round(newLeft)}px`;
      widget.style.top = `${Math.round(newTop)}px`;
    });

    const stopDrag = (e: PointerEvent) => {
      if (!isDragging || (e.pointerId !== undefined && e.pointerId !== activePointerId)) return;
      isDragging = false;
      activePointerId = -1;
      widget.classList.remove("is-dragging");
      try {
        if (widget.hasPointerCapture(e.pointerId)) {
          widget.releasePointerCapture(e.pointerId);
        }
      } catch (_) {}
    };

    widget.addEventListener("pointerup", stopDrag);
    widget.addEventListener("pointercancel", stopDrag);
    widget.addEventListener("lostpointercapture", stopDrag);
    widget.addEventListener("click", (e) => e.stopPropagation());
  }

  document.addEventListener("pointerdown", (event) => {
    if (!S.quickOrder) return;
    const w = $("chart-quick-order");
    if (w && (w === event.target || w.contains(event.target))) {
      return;
    }
    const isChartCanvas = (event.target as any)?.closest?.(".chart-canvas");
    if (!isChartCanvas) {
      closeChartQuickOrder();
    }
  });
}

function initChartTooltips() {
  const tooltip = $("chart-tooltip");
  if (!tooltip) return;

  const TOOLTIP_DATA: Record<string, { title: string; desc: string }> = {
    trades: {
      title: "Trades & Positions (TX)",
      desc: "Toggle this chart's trades, position and pending order lines. Drag an entry, TP or SL line to modify it; click ✕ on a TP/SL label to remove it.",
    },
    dom: {
      title: "Depth of Market (DOM)",
      desc: "Display live bid and ask order book volume bars beside prices on the chart canvas.",
    },
    trendline: {
      title: "Draw Trendline (TL)",
      desc: "Click chart twice to place a manual trendline. Drag endpoints to adjust; right-click to delete.",
    },
    "auto-tl": {
      title: "Auto Trendlines (ATL)",
      desc: "Automatically detects swing highs and swing lows to project dynamic support and resistance lines.",
    },
    sr: {
      title: "Support & Resistance (SR)",
      desc: "Computes and plots horizontal key pivot levels and price reaction zones.",
    },
    patterns: {
      title: "Pattern Recognition (PTN)",
      desc: "Detects candlestick patterns (Hammer, Engulfing, Doji, Star). Right-click for pattern settings.",
    },
    ma: {
      title: "EMA 10 / 20 / 200",
      desc: "Exponential moving average trend lines with 10-20 crossover markers.",
    },
    ind: {
      title: "Indicators (IND)",
      desc: "RSI(14) pane with 30/50/70 guides, RSI divergence and momentum candles.",
    },
    in: {
      title: "Zoom In (+)",
      desc: "Increase candlestick width to zoom in closer to recent price action.",
    },
    out: {
      title: "Zoom Out (−)",
      desc: "Decrease candlestick width to view more historical candles and wider price structure.",
    },
    reset: {
      title: "Reset View (↺)",
      desc: "Reset time and price zoom back to default and scroll to the latest live candle.",
    },
    expand: {
      title: "Maximize / Restore (⛶)",
      desc: "Maximize this chart pane to full screen, or click again / press Esc to restore 3-chart layout.",
    },
    "tools-toggle": {
      title: "Technical Tools (📐)",
      desc: "Toggle advanced chart indicators, overlays, and drawing tools panel.",
    },
    "chart-hide": {
      title: "Hide Chart (✕)",
      desc: "Hide this chart pane to allocate more screen space to other charts.",
    },
  };

  let activeBtn: HTMLElement | null = null;
  let pendingBtn: HTMLElement | null = null;
  let showTimer: any = null;

  const showTooltip = (btn: HTMLElement) => {
    if (activeBtn && activeBtn !== btn) {
      if (activeBtn.dataset.originalTitle) {
        activeBtn.setAttribute("title", activeBtn.dataset.originalTitle);
        delete activeBtn.dataset.originalTitle;
      }
    }

    const action = btn.dataset.chartAction ||
      (btn.classList.contains("pro-tools-toggle") ? "tools-toggle" : "") ||
      (btn.classList.contains("pane-close-btn") ? "chart-hide" : "");
    const info = TOOLTIP_DATA[action] || {
      title: btn.dataset.tooltipTitle || btn.getAttribute("aria-label") || "Chart Action",
      desc: btn.dataset.tooltipDesc || "",
    };

    if (!info.title && !info.desc) return;

    activeBtn = btn;
    if (btn.hasAttribute("title")) {
      btn.dataset.originalTitle = btn.getAttribute("title") || "";
      btn.removeAttribute("title");
    }

    tooltip.innerHTML = `
      <div class="chart-tooltip-title">${esc(info.title)}</div>
      ${info.desc ? `<div class="chart-tooltip-desc">${esc(info.desc)}</div>` : ""}
    `;
    tooltip.hidden = false;

    // Position tooltip
    const rect = btn.getBoundingClientRect();
    const tipWidth = tooltip.offsetWidth || 200;
    const tipHeight = tooltip.offsetHeight || 50;

    let left = rect.left + rect.width / 2 - tipWidth / 2;
    // Clamp horizontally
    left = Math.max(8, Math.min(left, window.innerWidth - tipWidth - 8));

    // Place below button, or above if close to bottom
    let top = rect.bottom + 6;
    if (top + tipHeight > window.innerHeight - 8) {
      top = Math.max(8, rect.top - tipHeight - 6);
    }

    tooltip.style.left = `${Math.round(left)}px`;
    tooltip.style.top = `${Math.round(top)}px`;
  };

  const hideTooltip = () => {
    clearTimeout(showTimer);
    pendingBtn = null;
    if (activeBtn) {
      if (activeBtn.dataset.originalTitle) {
        activeBtn.setAttribute("title", activeBtn.dataset.originalTitle);
        delete activeBtn.dataset.originalTitle;
      }
      activeBtn = null;
    }
    tooltip.hidden = true;
  };

  const targets = $$(".chart-tools button, .pro-tools-toggle, .pane-close-btn");
  targets.forEach((btn: any) => {
    let touchTimer: any = null;

    btn.addEventListener("pointerenter", (e: PointerEvent) => {
      if (e.pointerType === "touch") return;
      pendingBtn = btn;
      clearTimeout(showTimer);
      showTimer = setTimeout(() => {
        if (pendingBtn === btn) showTooltip(btn);
      }, 80);
    });
    btn.addEventListener("pointerleave", (e: PointerEvent) => {
      if (e.pointerType === "touch") return;
      if (pendingBtn === btn) {
        pendingBtn = null;
        clearTimeout(showTimer);
      }
      if (activeBtn === btn) {
        hideTooltip();
      }
    });
    btn.addEventListener("touchstart", () => {
      touchTimer = setTimeout(() => {
        showTooltip(btn);
      }, 350);
    }, { passive: true });
    btn.addEventListener("touchend", () => {
      clearTimeout(touchTimer);
    }, { passive: true });
    btn.addEventListener("touchcancel", () => {
      clearTimeout(touchTimer);
    }, { passive: true });
    btn.addEventListener("click", hideTooltip);
    btn.addEventListener("focus", () => showTooltip(btn));
    btn.addEventListener("blur", hideTooltip);
  });

  document.addEventListener("pointerdown", (e: PointerEvent) => {
    if (!tooltip.hidden && !tooltip.contains(e.target as Node) && activeBtn !== e.target) {
      hideTooltip();
    }
  });

  window.addEventListener("scroll", hideTooltip, { passive: true });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !tooltip.hidden) {
      hideTooltip();
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
      if (chart.consumeChartClickSuppression?.()) return;

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

  for (const [p, c] of Object.entries(S.charts) as [string, any][]) {
    if (p !== pane) {
      c?.clearOrderPreview?.();
      c?.clearTargetPreview?.();
      c?.clearStopPreview?.();
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

  // Default to a 1:1 risk/reward against the target until the user tunes it.
  let slPts = defaultPts;
  try {
    const savedSl = localStorage.getItem("scalper-sl-pts");
    if (savedSl) slPts = num(savedSl, defaultPts);
  } catch (_) {}
  if (!Number.isFinite(slPts) || slPts <= 0) slPts = defaultPts;
  slPts = Math.min(slPts, Math.max(0.05, price - 0.05));

  let slEnabled = true;
  try {
    if (localStorage.getItem("scalper-sl-enabled") === "false") slEnabled = false;
  } catch (_) {}

  const slPrice = Math.max(0.05, Math.round((price - slPts) * 20) / 20);

  const bootStopMode = String(S.boot?.exchangeStopMode || "off").toLowerCase();
  let exchangeStopEnabled = bootStopMode === "sl_m" || bootStopMode === "sl_l" || bootStopMode === "sl-m" || bootStopMode === "sl";
  try {
    const saved = localStorage.getItem("scalper-exchange-stop-enabled");
    if (saved === "true") exchangeStopEnabled = true;
    if (saved === "false") exchangeStopEnabled = false;
  } catch (_) {}
  let exchangeStopMode = "SL-M";
  try {
    const savedMode = localStorage.getItem("scalper-exchange-stop-mode");
    if (savedMode === "SL" || savedMode === "SL-L") exchangeStopMode = "SL";
    else if (savedMode === "SL-M" || savedMode === "SLM") exchangeStopMode = "SL-M";
  } catch (_) {}

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
    slPts,
    slPrice,
    slEnabled,
    exchangeStopEnabled,
    exchangeStopMode,
    exchangeStopPrice: 0,
  };

  const widget = $("chart-quick-order");
  if (!widget) return;

  $("cqo-title").textContent = `${S.activeIndex} ${fmtStrike(option.strike)} ${type}`;
  $("cqo-price").value = price.toFixed(2);
  $("cqo-buy-price").textContent = `@ ₹${price.toFixed(2)}`;
  $("cqo-sell-price").textContent = `@ ₹${price.toFixed(2)}`;
  renderQuickOrderQty();
  renderQuickOrderTarget();
  renderQuickOrderSL();
  renderQuickOrderExchangeStop();

  const frame = chart.container.closest(".chart-frame") || chart.container;
  frame.appendChild(widget);
  widget.hidden = false;

  const frameRect = frame.getBoundingClientRect();
  const widgetWidth = widget.offsetWidth || 250;
  const widgetHeight = widget.offsetHeight || 160;

  let left = x - widgetWidth / 2;
  left = Math.max(10, Math.min(left, frameRect.width - widgetWidth - 10));

  let top = y - widgetHeight - 12;
  if (top < 10) {
    top = y + 14;
  }
  top = Math.max(10, Math.min(top, frameRect.height - widgetHeight - 10));

  widget.style.left = `${Math.round(left)}px`;
  widget.style.top = `${Math.round(top)}px`;

  chart.showOrderPreview(price);
  if (targetEnabled) {
    chart.showTargetPreview(targetPrice, "TARGET");
  }
  if (slEnabled) {
    chart.showStopPreview(slPrice, `SL ₹${slPrice.toFixed(2)}`);
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
    if (document.activeElement !== targetPriceEl) {
      targetPriceEl.value = targetPrice.toFixed(2);
    }
    targetPriceEl.disabled = !targetEnabled;
  }

  const decBtn = $("cqo-target-dec");
  const incBtn = $("cqo-target-inc");
  if (decBtn) decBtn.disabled = !targetEnabled;
  if (incBtn) incBtn.disabled = !targetEnabled;

  const summaryEl = $("cqo-target-summary");
  if (summaryEl) summaryEl.style.opacity = targetEnabled ? "1" : "0.35";

  const ptsEl = $("cqo-target-pts");
  if (ptsEl) ptsEl.textContent = `${targetPts >= 0 ? "+" : ""}${targetPts.toFixed(2)} pts`;

  const pnlEl = $("cqo-target-pnl");
  if (pnlEl) pnlEl.textContent = `(Est. ${pnl >= 0 ? "+" : "−"}₹${Math.abs(pnl).toLocaleString("en-IN")})`;
}

function renderQuickOrderSL() {
  if (!S.quickOrder) return;
  const { slPts, slPrice, slEnabled, lotsCount, lot } = S.quickOrder;
  const shares = lotsCount * lot;
  const risk = Math.round(Math.abs(slPts) * shares);

  const enableEl = $("cqo-sl-enable");
  if (enableEl) enableEl.checked = slEnabled;

  const slPriceEl = $("cqo-sl-price");
  if (slPriceEl) {
    if (document.activeElement !== slPriceEl) {
      slPriceEl.value = slPrice.toFixed(2);
    }
    slPriceEl.disabled = !slEnabled;
  }

  const decBtn = $("cqo-sl-dec");
  const incBtn = $("cqo-sl-inc");
  if (decBtn) decBtn.disabled = !slEnabled;
  if (incBtn) incBtn.disabled = !slEnabled;

  const summaryEl = $("cqo-sl-summary");
  if (summaryEl) summaryEl.style.opacity = slEnabled ? "1" : "0.35";

  const ptsEl = $("cqo-sl-pts");
  if (ptsEl) ptsEl.textContent = `−${Math.abs(slPts).toFixed(2)} pts`;

  const pnlEl = $("cqo-sl-pnl");
  if (pnlEl) pnlEl.textContent = `(Risk −₹${risk.toLocaleString("en-IN")})`;

  renderQuickOrderExchangeStop();
}

function computeExchangeStopPrice(price, slPrice, mode = "SL-M") {
  const p = num(price);
  if (!Number.isFinite(p) || p <= 0) return 0;
  const distMode = String(S.boot?.exchangeStopDistanceMode || "multiplier").toLowerCase();
  const distVal = Math.max(0.01, num(S.boot?.exchangeStopDistanceValue, 2.5));
  let distance = 0;
  if (distMode === "percent") {
    distance = (p * distVal) / 100;
  } else if (distMode === "points") {
    distance = distVal;
  } else if (distMode === "ticks") {
    distance = distVal * 0.05;
  } else {
    const sl = num(slPrice);
    const normal = sl > 0 && sl < p ? p - sl : 0;
    distance = normal > 0 ? normal * distVal : (p * distVal) / 100;
  }
  if (!Number.isFinite(distance) || distance <= 0) return 0;
  const trigger = Math.round((p - distance) * 20) / 20;
  return Math.max(0.05, trigger);
}

function renderQuickOrderSLRiskWarning() {
  const el = $("cqo-sl-risk-warning");
  if (!el || !S.quickOrder) return;
  const { price, slPrice, slEnabled, exchangeStopEnabled, exchangeStopMode, lotsCount, lot } = S.quickOrder;
  let stop = 0;
  if (slEnabled && slPrice > 0 && slPrice < price) {
    stop = slPrice;
  } else if (exchangeStopEnabled) {
    stop = computeExchangeStopPrice(price, 0, exchangeStopMode);
  }
  const shares = Math.max(1, lotsCount * lot);
  const risk = stop > 0 && stop < price ? (price - stop) * shares : 0;
  const message = stopLossMarginWarning(risk);
  el.hidden = !message;
  el.textContent = message ? `⚠ ${message}` : "";
  el.title = "Informational only. Uses the server stop-loss when enabled, otherwise the exchange backstop.";
}

function renderQuickOrderExchangeStop() {
  if (!S.quickOrder) return;
  const { exchangeStopEnabled, exchangeStopMode, price, slPrice, slEnabled, lotsCount, lot } = S.quickOrder;

  const enableEl = $("cqo-exchange-stop-enable");
  if (enableEl) enableEl.checked = !!exchangeStopEnabled;
  const modeEl = $("cqo-exchange-stop-mode");
  if (modeEl) {
    modeEl.value = exchangeStopMode === "SL" ? "SL" : "SL-M";
    modeEl.disabled = !exchangeStopEnabled;
  }
  const summaryEl = $("cqo-exchange-stop-summary");
  if (summaryEl) summaryEl.style.opacity = exchangeStopEnabled ? "1" : "0.4";

  const priceEl = $("cqo-exchange-stop-price");
  const riskEl = $("cqo-exchange-stop-risk");
  if (!exchangeStopEnabled) {
    S.quickOrder.exchangeStopPrice = 0;
    if (priceEl) priceEl.textContent = "Off";
    if (riskEl) riskEl.textContent = "No live sell at Kotak";
    renderQuickOrderSLRiskWarning();
    return;
  }

  const trigger = computeExchangeStopPrice(price, slEnabled ? slPrice : 0, exchangeStopMode);
  S.quickOrder.exchangeStopPrice = trigger;
  const shares = Math.max(1, lotsCount * lot);
  const risk = Math.max(0, Math.round((price - trigger) * shares));
  if (priceEl) {
    priceEl.textContent = `${exchangeStopMode} ₹${trigger.toFixed(2)} · EXCH`;
  }
  if (riskEl) {
    riskEl.textContent = `Catastrophe risk ≈ ₹${risk.toLocaleString("en-IN")} (server SL still active)`;
  }
  renderQuickOrderSLRiskWarning();
}

function updateQuickOrderSLPts(newPts) {
  if (!S.quickOrder) return;
  const maxPts = Math.max(0.05, S.quickOrder.price - 0.05);
  const pts = Math.min(maxPts, Math.max(0.05, Math.round(num(newPts) * 20) / 20));
  S.quickOrder.slPts = pts;
  S.quickOrder.slPrice = Math.max(0.05, Math.round((S.quickOrder.price - pts) * 20) / 20);
  try {
    localStorage.setItem("scalper-sl-pts", String(pts));
  } catch (_) {}
  const slPriceEl = $("cqo-sl-price");
  if (slPriceEl) {
    slPriceEl.value = S.quickOrder.slPrice.toFixed(2);
  }
  renderQuickOrderSL();
  if (S.quickOrder.slEnabled) {
    S.charts[S.quickOrder.pane]?.showStopPreview(S.quickOrder.slPrice, `SL ₹${S.quickOrder.slPrice.toFixed(2)}`);
  }
}

function updateQuickOrderSLPrice(newSlPrice) {
  if (!S.quickOrder) return;
  const price = S.quickOrder.price;
  const sl = Math.min(Math.max(0.05, price - 0.05), Math.max(0.05, Math.round(num(newSlPrice) * 20) / 20));
  S.quickOrder.slPrice = sl;
  const pts = Math.max(0.05, Math.round((price - sl) * 20) / 20);
  S.quickOrder.slPts = pts;
  try {
    localStorage.setItem("scalper-sl-pts", String(pts));
  } catch (_) {}
  const slPriceEl = $("cqo-sl-price");
  if (slPriceEl) {
    slPriceEl.value = sl.toFixed(2);
  }
  renderQuickOrderSL();
  if (S.quickOrder.slEnabled) {
    S.charts[S.quickOrder.pane]?.showStopPreview(sl, `SL ₹${sl.toFixed(2)}`);
  }
}

function updateQuickOrderTargetPts(newPts) {
  if (!S.quickOrder) return;
  const pts = Math.max(0.05, Math.round(num(newPts) * 20) / 20);
  S.quickOrder.targetPts = pts;
  S.quickOrder.targetPrice = Math.round((S.quickOrder.price + pts) * 20) / 20;
  try {
    localStorage.setItem("scalper-target-pts", String(pts));
  } catch (_) {}
  const targetPriceEl = $("cqo-target-price");
  if (targetPriceEl) {
    targetPriceEl.value = S.quickOrder.targetPrice.toFixed(2);
  }
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
  const targetPriceEl = $("cqo-target-price");
  if (targetPriceEl) {
    targetPriceEl.value = tp.toFixed(2);
  }
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

  if (S.quickOrder.slEnabled) {
    const pts = Math.min(S.quickOrder.slPts, Math.max(0.05, price - 0.05));
    S.quickOrder.slPts = pts;
    S.quickOrder.slPrice = Math.max(0.05, Math.round((price - pts) * 20) / 20);
    renderQuickOrderSL();
    S.charts[S.quickOrder.pane]?.showStopPreview(S.quickOrder.slPrice, `SL ₹${S.quickOrder.slPrice.toFixed(2)}`);
  }
  renderQuickOrderExchangeStop();
}

function updateQuickOrderLots(delta) {
  if (!S.quickOrder) return;
  const newLots = Math.max(1, S.quickOrder.lotsCount + delta);
  S.quickOrder.lotsCount = newLots;
  S.qty[S.quickOrder.type] = S.unit[S.quickOrder.type] === "SHARES" ? newLots * S.quickOrder.lot : newLots;
  renderQuickOrderQty();
  renderQuickOrderTarget();
  renderQuickOrderSL();
  renderQuickOrderExchangeStop();
  renderOrderPad();
}

function closeChartQuickOrder() {
  if (S.quickOrder) {
    S.charts[S.quickOrder.pane]?.clearOrderPreview();
    S.charts[S.quickOrder.pane]?.clearTargetPreview();
    S.charts[S.quickOrder.pane]?.clearStopPreview();
    S.quickOrder = null;
  }
  const widget = $("chart-quick-order");
  if (widget) {
    widget.classList.remove("is-dragging");
    widget.hidden = true;
  }
}

async function submitQuickOrder(action) {
  if (!S.quickOrder) return;
  const { type, lotsCount, lot, targetEnabled, targetPrice, slEnabled, slPrice } = S.quickOrder;
  const inputPrice = num($("cqo-price")?.value);
  const price = Number.isFinite(inputPrice) && inputPrice > 0 ? inputPrice : S.quickOrder.price;
  const shares = lotsCount * lot;
  const inputTarget = num($("cqo-target-price")?.value);
  const tp = Number.isFinite(inputTarget) && inputTarget > 0 ? inputTarget : targetPrice;
  const autoTarget = (targetEnabled && action === "buy" && tp > price) ? tp : 0;
  const inputSl = num($("cqo-sl-price")?.value);
  const slp = Number.isFinite(inputSl) && inputSl > 0 ? inputSl : slPrice;
  const autoStop = (slEnabled && action === "buy" && slp > 0 && slp < price) ? Math.round(slp * 20) / 20 : 0;
  const exchangeStopEnabled = Boolean(S.quickOrder.exchangeStopEnabled) && action === "buy";
  // An explicit per-order "off" must override a global default that is on.
  const exchangeStopMode = exchangeStopEnabled ? S.quickOrder.exchangeStopMode : "off";
  const exchangeStopPrice = exchangeStopEnabled
    ? computeExchangeStopPrice(price, slEnabled ? slp : 0, exchangeStopMode)
    : 0;

  if (autoTarget === 0 && autoStop === 0 && !(exchangeStopEnabled && exchangeStopMode)) {
    const proceed = await confirmAction({
      title: "No exits armed",
      body: "This order has <b>no server TP</b>, <b>no server SL</b>, and <b>no exchange backstop</b>.<br><span class=\"warn\">The position will be completely unprotected until you exit manually.</span>",
      okLabel: "Place anyway",
      danger: true,
    });
    if (!proceed) return;
  }

  closeChartQuickOrder();
  await onLimitTrade(action, type, price, shares, autoTarget, autoStop, {
    mode: exchangeStopMode,
    price: exchangeStopPrice,
  });
  if (autoTarget > 0 || autoStop > 0 || (exchangeStopEnabled && exchangeStopMode)) {
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
    const res = await postJSON("/api/broker/square-off-all", { confirm: true });
    lastUiOrderTime = Date.now();
    playTradeSound("sell");
    const count = res?.closed_positions ?? openPositions.length;
    toast(`Live square-off completed (${count} position${count === 1 ? "" : "s"} closed).`, "ok");
    scheduleBooksRefresh(400);
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
      updateFooterVersion(data);
    }
  } catch (err) {
    console.debug("version check skipped:", err);
  }
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
  try {
    updateBiometricButtons();
  } catch (err) {
    console.warn("updateBiometricButtons error:", err);
  }
  const totpInput = $("totp-input");
  if (totpInput) {
    totpInput.value = "";
    totpInput.disabled = false;
  }
  const totpHint = $("totp-hint");
  if (totpHint) totpHint.textContent = "Invalid code? Sync your device time and try again.";
  if ($("mpin-input")) {
    $("mpin-input").value = "";
    $("mpin-input").disabled = true;
  }
  loginError("");
  const submitBtn = $("login-submit");
  if (submitBtn) submitBtn.textContent = "Continue";
  const modal = $("login-modal");
  if (modal) {
    modal.hidden = false;
    modal.style.display = "grid";
  }
  window.setTimeout(() => $("totp-input")?.focus(), 30);
}

function closeLogin() {
  const modal = $("login-modal");
  if (modal) {
    modal.hidden = true;
    modal.style.display = "none";
  }
  totpAccepted = false;
  if ($("totp-input")) {
    $("totp-input").value = "";
    $("totp-input").disabled = false;
  }
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
  if (sessionRefreshTimer) {
    window.clearInterval(sessionRefreshTimer);
    sessionRefreshTimer = null;
  }
  if (liveTimer) {
    window.clearInterval(liveTimer);
    liveTimer = null;
  }
  if (underlyingTimer) {
    window.clearInterval(underlyingTimer);
    underlyingTimer = null;
  }
  updateAuthButton();
  renderHeader();
  openLogin();
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

/* ── System Status Modal ──────────────────────────────── */

let statusRefreshTimer = null;
let statusBusy = false;

function formatRelativeTime(dateOrIso) {
  if (!dateOrIso) return "—";
  const time = typeof dateOrIso === "number" ? dateOrIso : new Date(dateOrIso).getTime();
  if (isNaN(time) || time <= 0) return "—";
  const diffSec = Math.round((Date.now() - time) / 1000);
  if (diffSec < 2) return "Just now";
  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.round(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHours = Math.round(diffMin / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  return new Date(time).toLocaleDateString();
}

function formatUptime(seconds) {
  if (seconds == null || isNaN(seconds) || seconds < 0) return "—";
  const s = Math.floor(seconds % 60);
  const m = Math.floor((seconds / 60) % 60);
  const h = Math.floor((seconds / 3600) % 24);
  const d = Math.floor(seconds / 86400);
  const parts = [];
  if (d > 0) parts.push(`${d}d`);
  if (h > 0 || d > 0) parts.push(`${h}h`);
  if (m > 0 || h > 0 || d > 0) parts.push(`${m}m`);
  parts.push(`${s}s`);
  return parts.join(" ");
}

function setStatusPill(el, statusType, text) {
  if (!el) return;
  el.className = `status-pill status-${statusType}`;
  el.textContent = text;
}

async function openStatusModal() {
  const modal = $("status-modal");
  if (!modal) return;
  modal.hidden = false;
  await refreshStatusModal(true);
  if (!statusRefreshTimer) {
    statusRefreshTimer = window.setInterval(() => refreshStatusModal(false), 3500);
  }
}

function closeStatusModal() {
  const modal = $("status-modal");
  if (modal) modal.hidden = true;
  if (statusRefreshTimer) {
    window.clearInterval(statusRefreshTimer);
    statusRefreshTimer = null;
  }
}

async function refreshStatusModal(force = false) {
  const modal = $("status-modal");
  if (!modal || modal.hidden) return;
  if (statusBusy && !force) return;
  statusBusy = true;
  const refreshBtn = $("status-refresh-button");
  if (force && refreshBtn) refreshBtn.textContent = "↻ Refreshing…";

  try {
    const res = await fetch("/api/health");
    let health = null;
    try {
      health = await res.json();
    } catch {}
    if (!health) {
      throw new Error(`HTTP ${res.status}`);
    }
    renderStatusModalData(health);
  } catch (err) {
    console.warn("failed to fetch /api/health", err);
    renderStatusModalOffline(err);
  } finally {
    statusBusy = false;
    if (refreshBtn) refreshBtn.textContent = "↻ Refresh Status";
  }
}

function renderStatusModalData(health) {
  const checks = health?.checks || {};

  // Overall status
  const overallPill = $("status-overall-pill");
  if (health?.status === "ok") {
    setStatusPill(overallPill, "ok", "SYSTEM HEALTHY");
  } else if (health?.status === "degraded") {
    setStatusPill(overallPill, "warn", "SYSTEM DEGRADED");
  } else {
    setStatusPill(overallPill, "fail", "ATTENTION NEEDED");
  }
  if ($("status-uptime-text")) {
    $("status-uptime-text").textContent = `Uptime: ${formatUptime(health?.uptime_seconds)}`;
  }
  if ($("status-last-checked")) {
    const now = new Date();
    $("status-last-checked").textContent = `Checked: ${now.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit", timeZone: "Asia/Kolkata" })} IST`;
  }

  // 1. Browser WebSocket
  const wsOpen = S.ws && S.ws.readyState === WebSocket.OPEN;
  const wsConnecting = S.ws && S.ws.readyState === WebSocket.CONNECTING;
  if (wsOpen) {
    setStatusPill($("status-card-ws-pill"), "ok", "LIVE");
    if ($("status-card-ws-state")) $("status-card-ws-state").textContent = "Connected (Open)";
  } else if (wsConnecting) {
    setStatusPill($("status-card-ws-pill"), "warn", "CONNECTING");
    if ($("status-card-ws-state")) $("status-card-ws-state").textContent = "Connecting…";
  } else {
    setStatusPill($("status-card-ws-pill"), "fail", "DISCONNECTED");
    if ($("status-card-ws-state")) $("status-card-ws-state").textContent = "Disconnected";
  }
  if ($("status-card-ws-ping")) {
    if (wsOpen) {
      $("status-card-ws-ping").textContent = S.lastWsEventTime ? `Active (${formatRelativeTime(S.lastWsEventTime)})` : "Active";
    } else {
      $("status-card-ws-ping").textContent = "Inactive";
    }
  }
  if ($("status-card-ws-endpoint")) {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    $("status-card-ws-endpoint").textContent = `${proto}//${location.host}/ws`;
  }

  // 2. Market Feed (HSM WebSocket)
  const mCheck = checks.market_feed || {};
  const isMarketLive = S.feed === "live" || mCheck.status === "connected";
  const isMarketDegraded = S.feed === "degraded" || mCheck.status === "degraded";
  const isMarketPolling = S.feed === "polling";
  if (isMarketLive) {
    setStatusPill($("status-card-market-pill"), "ok", "CONNECTED");
    if ($("status-card-market-status")) $("status-card-market-status").textContent = "Connected (Kotak SFeed)";
  } else if (isMarketDegraded) {
    setStatusPill($("status-card-market-pill"), "warn", "DEGRADED");
    if ($("status-card-market-status")) $("status-card-market-status").textContent = "Degraded (Reconnecting)";
  } else if (isMarketPolling) {
    setStatusPill($("status-card-market-pill"), "warn", "POLLING");
    if ($("status-card-market-status")) $("status-card-market-status").textContent = "Polling REST Quotes";
  } else {
    setStatusPill($("status-card-market-pill"), "neutral", "STOPPED");
    if ($("status-card-market-status")) $("status-card-market-status").textContent = "Stopped / Inactive";
  }
  const marketEndpoint = mCheck.endpoint || (S.feed === "live" ? "wss://tradeapi.kotaksecurities.com/websocket" : "—");
  if ($("status-card-market-endpoint")) {
    $("status-card-market-endpoint").textContent = marketEndpoint;
    $("status-card-market-endpoint").title = marketEndpoint;
  }
  if ($("status-card-market-event")) {
    $("status-card-market-event").textContent = mCheck.last_event ? formatRelativeTime(mCheck.last_event) : (isMarketLive ? "Streaming live" : "—");
  }

  // 3. Order Feed WebSocket
  const oCheck = checks.order_feed || {};
  const isOrderLive = S.orderFeed === "live" || oCheck.status === "connected";
  const isOrderConnecting = S.orderFeed === "connecting" || oCheck.status === "connecting";
  if (isOrderLive) {
    setStatusPill($("status-card-order-pill"), "ok", "CONNECTED");
    if ($("status-card-order-status")) $("status-card-order-status").textContent = "Connected (Kotak Neo)";
  } else if (isOrderConnecting) {
    setStatusPill($("status-card-order-pill"), "warn", "CONNECTING");
    if ($("status-card-order-status")) $("status-card-order-status").textContent = "Connecting to Order Stream…";
  } else {
    setStatusPill($("status-card-order-pill"), "fail", "DISCONNECTED");
    if ($("status-card-order-status")) $("status-card-order-status").textContent = "Disconnected";
  }
  const orderEndpoint = oCheck.endpoint || (isOrderLive ? "wss://tradeapi.kotaksecurities.com/realtime" : "—");
  if ($("status-card-order-endpoint")) {
    $("status-card-order-endpoint").textContent = orderEndpoint;
    $("status-card-order-endpoint").title = orderEndpoint;
  }
  if ($("status-card-order-event")) {
    $("status-card-order-event").textContent = oCheck.last_event ? formatRelativeTime(oCheck.last_event) : (isOrderLive ? "Listening for events" : "—");
  }

  // 4. Broker API / Session
  const bCheck = checks.session || checks.broker || {};
  const isAuth = Boolean(S.session?.authenticated || bCheck.authenticated || bCheck.session_active);
  const tokenPresent = Boolean(bCheck.token_present || bCheck.status === "ok");
  if (isAuth) {
    setStatusPill($("status-card-broker-pill"), "ok", "ACTIVE");
    const method = (bCheck.login_method || S.session?.login_method || "Token").toUpperCase();
    const exp = bCheck.expires_in != null ? ` (exp in ${bCheck.expires_in}m)` : "";
    if ($("status-card-broker-session")) $("status-card-broker-session").textContent = `Active via ${method}${exp}`;
  } else if (tokenPresent) {
    setStatusPill($("status-card-broker-pill"), "warn", "NOT SIGNED IN");
    if ($("status-card-broker-session")) $("status-card-broker-session").textContent = "Sign-in required to trade";
  } else {
    setStatusPill($("status-card-broker-pill"), "fail", "NO TOKEN");
    if ($("status-card-broker-session")) $("status-card-broker-session").textContent = "Consumer key not configured";
  }
  if ($("status-card-broker-ucc")) {
    $("status-card-broker-ucc").textContent = bCheck.ucc || S.session?.ucc || "—";
  }
  if ($("status-card-broker-token")) {
    $("status-card-broker-token").textContent = tokenPresent ? "Configured" : "Missing";
  }
  if ($("status-card-broker-ratelimit")) {
    const rl = health?.rate_limits;
    if (rl && (rl.remaining > 0 || rl.limit > 0)) {
      $("status-card-broker-ratelimit").textContent = `${rl.remaining} / ${rl.limit || "—"} (reset ${rl.reset || 0}s)`;
    } else {
      $("status-card-broker-ratelimit").textContent = "Normal / OK";
    }
  }
  const ipEl = $("status-card-broker-outbound-ip");
  if (ipEl && (ipEl.textContent === "—" || ipEl.textContent === "")) {
    ipEl.textContent = "Checking…";
    fetch("/api/diagnostics/ip")
      .then((r) => r.json())
      .then((data) => {
        if (data?.ip) {
          ipEl.textContent = data.ip;
          ipEl.title = `Outbound IP (cached: ${data.cached})`;
        } else {
          ipEl.textContent = "Unavailable";
        }
      })
      .catch(() => {
        ipEl.textContent = "Unavailable";
      });
  }

  // 5. SQLite Database
  const dbCheck = checks.database || {};
  if (dbCheck.status === "ok") {
    setStatusPill($("status-card-db-pill"), "ok", "CONNECTED");
    if ($("status-card-db-status")) $("status-card-db-status").textContent = "Operational (Read/Write)";
  } else {
    setStatusPill($("status-card-db-pill"), "fail", "ERROR");
    if ($("status-card-db-status")) $("status-card-db-status").textContent = "Unreachable";
  }
  if ($("status-card-db-latency")) {
    $("status-card-db-latency").textContent = dbCheck.latency_ms != null ? `${dbCheck.latency_ms} ms` : "— ms";
  }
  if ($("status-card-db-detail")) {
    $("status-card-db-detail").textContent = dbCheck.detail || (dbCheck.status === "ok" ? "Healthy" : "Offline");
  }

  // 6. Scrip Master Cache
  const sCheck = checks.scrip_master || {};
  if (sCheck.loaded) {
    setStatusPill($("status-card-scrip-pill"), "ok", "READY");
    if ($("status-card-scrip-status")) $("status-card-scrip-status").textContent = `Ready (${sCheck.source || "Database"})`;
    if ($("status-card-scrip-count")) $("status-card-scrip-count").textContent = Number(sCheck.count || 0).toLocaleString();
    if ($("status-card-scrip-source")) $("status-card-scrip-source").textContent = sCheck.source || "Database";
  } else {
    setStatusPill($("status-card-scrip-pill"), "warn", "NOT LOADED");
    if ($("status-card-scrip-status")) $("status-card-scrip-status").textContent = sCheck.detail || "Cache not ready";
    if ($("status-card-scrip-count")) $("status-card-scrip-count").textContent = "0";
    if ($("status-card-scrip-source")) $("status-card-scrip-source").textContent = sCheck.source || "None";
  }
}

function renderStatusModalOffline(err) {
  setStatusPill($("status-overall-pill"), "fail", "SERVER UNREACHABLE");
  if ($("status-uptime-text")) $("status-uptime-text").textContent = "Uptime: Unavailable";
  setStatusPill($("status-card-ws-pill"), "fail", "DISCONNECTED");
  if ($("status-card-ws-state")) $("status-card-ws-state").textContent = "Server unreachable";
  setStatusPill($("status-card-db-pill"), "fail", "UNKNOWN");
  if ($("status-card-db-status")) $("status-card-db-status").textContent = String(err?.message || "Fetch failed");
}

// ── WebAuthn / Passkey & Device Binding ──────────────────────
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

async function checkBiometricUnlockAvailable() {
  const savedDevId = localStorage.getItem("scalper_device_id");
  if (savedDevId) return true;
  try {
    const res = await fetch("/api/devices");
    if (!res.ok) return false;
    const data = await res.json();
    return !!(data.has_devices || (data.count && data.count > 0));
  } catch (_) {
    return false;
  }
}

async function updateBiometricButtons() {
  const available = await checkBiometricUnlockAvailable();
  const serveContainer = $("biometric-unlock-serve-container");
  if (serveContainer) serveContainer.hidden = !available;
}

async function tryAutoUnlockDevice() {
  const savedDevId = localStorage.getItem("scalper_device_id") || "";
  const hasBindingCookie = typeof document !== "undefined" && document.cookie.includes("scalper_device_binding=");
  if (!savedDevId && !hasBindingCookie) return false;
  try {
    const headers = { "Content-Type": "application/json" };
    if (savedDevId) headers["X-Device-ID"] = savedDevId;
    const res = await fetch("/api/auth/device-unlock", {
      method: "POST",
      credentials: "include",
      headers,
      body: JSON.stringify({ device_id: savedDevId }),
    });
    const data = await res.json().catch(() => ({}));
    if (res.ok && data.ok) {
      if (data.token) localStorage.setItem("scalper_serve_session", data.token);
      if (data.device_id) localStorage.setItem("scalper_device_id", data.device_id);
      const serveModal = $("serve-auth-modal");
      if (serveModal) {
        serveModal.style.display = "none";
        serveModal.hidden = true;
      }
      toast(`Unlocked via ${data.device_name || "Device Binding"}!`, "ok");
      startTerminalSession();
      return true;
    }
  } catch (_) {}
  return false;
}

async function unlockWithSavedDevice(devId = "") {
  const targetId = devId || localStorage.getItem("scalper_device_id") || "";
  const headers = { "Content-Type": "application/json" };
  if (targetId) headers["X-Device-ID"] = targetId;
  const res = await fetch("/api/auth/device-unlock", {
    method: "POST",
    credentials: "include",
    headers,
    body: JSON.stringify({ device_id: targetId }),
  });
  const data = await res.json().catch(() => ({}));
  if (res.ok && data.ok) {
    if (data.token) localStorage.setItem("scalper_serve_session", data.token);
    if (data.device_id) localStorage.setItem("scalper_device_id", data.device_id);
    const serveModal = $("serve-auth-modal");
    if (serveModal) {
      serveModal.style.display = "none";
      serveModal.hidden = true;
    }
    toast(`Unlocked via ${data.device_name || "Device"}!`, "ok");
    await startTerminalSession();
    return true;
  }
  throw new Error(data.error || "Device unlock failed. Please use password.");
}

async function handleBiometricUnlock() {
  const serveBtn = $("biometric-unlock-serve-btn");
  if (serveBtn) { serveBtn.disabled = true; serveBtn.textContent = "Authenticating..."; }

  try {
    const savedDevId = localStorage.getItem("scalper_device_id") || "";
    let webAuthnError = null;

    // 1. If WebAuthn is supported, try WebAuthn
    if (window.isSecureContext && window.PublicKeyCredential) {
      try {
        const beginRes = await fetch("/api/auth/webauthn/login/begin", {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" }
        });
        const data = await beginRes.json().catch(() => ({}));

        if (beginRes.ok && data.ok && data.has_webauthn && data.options) {
          const opts = data.options || data;
          // Omit rpId if it's an IP address or empty to avoid browser DOMException
          const safeRpId = (opts.rpId && !/^\d+\.\d+\.\d+\.\d+$/.test(opts.rpId) && opts.rpId !== "127.0.0.1")
            ? opts.rpId
            : undefined;

          const publicKey = {
            challenge: base64UrlToBuffer(opts.challenge),
            timeout: opts.timeout || 60000,
            rpId: safeRpId,
            userVerification: opts.userVerification || "preferred",
            allowCredentials: (opts.allowCredentials || []).map((c) => ({
              type: c.type || "public-key",
              id: base64UrlToBuffer(c.id),
              transports: c.transports,
            })),
          };

          const assertion: any = await navigator.credentials.get({ publicKey });
          if (assertion) {
            const finishBody = {
              id: assertion.id,
              rawId: bufferToBase64Url(assertion.rawId),
              type: assertion.type,
              response: {
                clientDataJSON: bufferToBase64Url(assertion.response.clientDataJSON),
                authenticatorData: bufferToBase64Url(assertion.response.authenticatorData),
                signature: bufferToBase64Url(assertion.response.signature),
                userHandle: assertion.response.userHandle ? bufferToBase64Url(assertion.response.userHandle) : null,
              },
            };

            const finRes = await fetch("/api/auth/webauthn/login/finish", {
              method: "POST",
              credentials: "include",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify(finishBody),
            });
            const finData = await finRes.json().catch(() => ({}));
            if (finRes.ok && finData.ok) {
              if (finData.token) localStorage.setItem("scalper_serve_session", finData.token);
              if (finData.device_id) localStorage.setItem("scalper_device_id", finData.device_id);
              const serveModal = $("serve-auth-modal");
              if (serveModal) {
                serveModal.style.display = "none";
                serveModal.hidden = true;
              }
              toast(`Unlocked via ${finData.device_name || "Biometrics"}!`, "ok");
              await startTerminalSession();
              return;
            } else {
              webAuthnError = finData.error || "Biometric verification rejected";
            }
          }
        }
      } catch (waErr) {
        console.warn("WebAuthn assertion attempt:", waErr);
        webAuthnError = waErr.name === "NotAllowedError" ? "Biometric authentication cancelled" : (waErr.message || "Biometric failed");
      }
    }

    // 2. Fallback to Device Binding unlock (works for paired mobile devices & trusted devices)
    try {
      const unlocked = await unlockWithSavedDevice(savedDevId);
      if (unlocked) return;
    } catch (devErr) {
      console.warn("Device unlock fallback error:", devErr);
      if (webAuthnError && webAuthnError !== "Biometric authentication cancelled") {
        throw new Error(webAuthnError);
      }
      throw devErr;
    }

    throw new Error(webAuthnError || "No paired device recognized. Please enter password.");
  } catch (err) {
    console.warn("biometric unlock error:", err);
    toast(err.message || "Biometric authentication failed", "warn");
  } finally {
    if (serveBtn) {
      serveBtn.disabled = false;
      serveBtn.innerHTML = "<span>⚡ Biometric Unlock (Touch ID / Fingerprint / Face ID)</span>";
    }
  }
}

async function handleRegisterDevice(deviceName = "", pairToken = "") {
  const registerBtn = $("register-device-button");
  const pairEnrollBtn = $("pair-register-this-btn");
  if (registerBtn) { registerBtn.disabled = true; registerBtn.textContent = "Enrolling..."; }
  if (pairEnrollBtn) { pairEnrollBtn.disabled = true; pairEnrollBtn.textContent = "Enrolling..."; }

  if (!window.isSecureContext || !window.PublicKeyCredential) {
    try {
      const headers = { "Content-Type": "application/json" };
      if (pairToken) headers["X-Pair-Token"] = pairToken;
      const res = await fetch("/api/devices/bind", {
        method: "POST",
        credentials: "include",
        headers,
        body: JSON.stringify({
          deviceName: deviceName || (window.navigator?.userAgentData?.platform || "My Device"),
          pairToken: pairToken || "",
        }),
      });
      const d = await res.json().catch(() => ({}));
      if (res.ok && d.ok) {
        if (d.device?.id) localStorage.setItem("scalper_device_id", d.device.id);
        if (d.device?.device_name) localStorage.setItem("scalper_device_name", d.device.device_name);
        if (d.token) localStorage.setItem("scalper_serve_session", d.token);
        toast(`Device "${d.device?.device_name || "Device"}" bound successfully!`, "ok");
        await loadDevicesList();
        if (pairToken) {
          const serveModal = $("serve-auth-modal");
          if (serveModal) {
            serveModal.style.display = "none";
            serveModal.hidden = true;
          }
          startTerminalSession();
        }
        return;
      }
      throw new Error(d.error || "Device binding failed");
    } catch (err) {
      toast(err.message || "Device binding failed", "warn");
    } finally {
      if (registerBtn) { registerBtn.disabled = false; registerBtn.textContent = "Register Device"; }
      if (pairEnrollBtn) { pairEnrollBtn.disabled = false; pairEnrollBtn.textContent = "⚡ Register This Device"; }
    }
    return;
  }

  try {
    const beginUrl = "/api/auth/webauthn/register/begin" + (pairToken ? `?pair_token=${encodeURIComponent(pairToken)}` : "");
    const headers = { "Content-Type": "application/json" };
    if (pairToken) headers["X-Pair-Token"] = pairToken;

    const beginRes = await fetch(beginUrl, { method: "POST", credentials: "include", headers });
    const data = await beginRes.json().catch(() => ({}));
    if (!beginRes.ok || !data.ok) {
      throw new Error(data.error || "Failed to initiate registration");
    }

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
      authenticatorSelection: opts.authenticatorSelection || { userVerification: "preferred" },
      timeout: opts.timeout || 60000,
      attestation: opts.attestation || "none",
    };

    const cred: any = await navigator.credentials.create({ publicKey });
    if (!cred) throw new Error("Registration was cancelled");

    const finishBody = {
      deviceName: deviceName || (window.navigator?.userAgentData?.platform || "My Device"),
      pairToken: pairToken || "",
      id: cred.id,
      rawId: bufferToBase64Url(cred.rawId),
      type: cred.type,
      response: {
        clientDataJSON: bufferToBase64Url(cred.response.clientDataJSON),
        attestationObject: bufferToBase64Url(cred.response.attestationObject),
      },
    };

    const finRes = await fetch("/api/auth/webauthn/register/finish", {
      method: "POST",
      credentials: "include",
      headers,
      body: JSON.stringify(finishBody),
    });
    const finData = await finRes.json().catch(() => ({}));
    if (!finRes.ok || !finData.ok) {
      throw new Error(finData.error || "Failed to verify registration");
    }

    if (finData.device?.id) localStorage.setItem("scalper_device_id", finData.device.id);
    if (finData.device?.device_name) localStorage.setItem("scalper_device_name", finData.device.device_name);
    if (finData.token) localStorage.setItem("scalper_serve_session", finData.token);

    toast(`Device "${finData.device?.device_name || "Passkey"}" registered!`, "ok");
    await updateBiometricButtons();
    await loadDevicesList();

    if (pairToken) {
      const serveModal = $("serve-auth-modal");
      if (serveModal) {
        serveModal.style.display = "none";
        serveModal.hidden = true;
      }
      startTerminalSession();
      const cleanUrl = window.location.protocol + "//" + window.location.host + window.location.pathname;
      window.history.replaceState({ path: cleanUrl }, "", cleanUrl);
    }
  } catch (err) {
    console.warn("device registration error:", err);
    toast(err.message || "Device registration failed", "warn");
  } finally {
    if (registerBtn) { registerBtn.disabled = false; registerBtn.textContent = "Register Device"; }
    if (pairEnrollBtn) { pairEnrollBtn.disabled = false; pairEnrollBtn.textContent = "⚡ Register This Device"; }
  }
}

async function openPairModal() {
  const modal = $("pair-modal");
  if (!modal) return;
  modal.hidden = false;
  modal.style.display = "grid";

  const qrContainer = $("pair-qr-svg");
  const urlInput = $("pair-url-input") as HTMLInputElement | null;
  if (qrContainer) qrContainer.innerHTML = "<span style='color:#333;font-size:12px;'>Generating QR code...</span>";
  if (urlInput) urlInput.value = "Generating...";

  try {
    const data = await requestJSON("/api/devices/pair-token", { method: "POST" });
    if (data && data.ok && data.url) {
      if (urlInput) urlInput.value = data.url;
      if (qrContainer) {
        qrContainer.innerHTML = generateQRCodeSVG(data.url, 220);
      }
    } else {
      if (urlInput) urlInput.value = "";
      if (qrContainer) qrContainer.innerHTML = `<span style='color:#c00;font-size:12px;'>${esc(data?.error || "Failed to generate pairing token")}</span>`;
    }
  } catch (e: any) {
    if (urlInput) urlInput.value = "";
    if (qrContainer) qrContainer.innerHTML = `<span style='color:#c00;font-size:12px;'>${esc(e?.message || "Error generating pairing token")}</span>`;
  }

  await loadDevicesList();
}

function closePairModal() {
  const modal = $("pair-modal");
  if (modal) {
    modal.hidden = true;
    modal.style.display = "none";
  }
}

async function onAskAI(type: string) {
  const option = selectedOption(type);
  if (!option) {
    toast(`Select a ${type} strike first.`, "warn");
    return;
  }
  if (!option.quoteAvailable || option.ltp <= 0) {
    toast(`No live quote available for ${type} option.`, "warn");
    return;
  }

  const askBtn = $(type === "CE" ? "btn-ask-ce" : "btn-ask-pe") as HTMLButtonElement | null;
  if (askBtn) {
    askBtn.disabled = true;
    askBtn.textContent = "…";
  }

  try {
    const payload = {
      underlying: S.activeIndex || "NIFTY",
      instrument_key: option.instrumentKey,
      symbol: option.symbol,
      strike: String(option.strike),
      option_type: type,
      ltp: option.ltp
    };

    const resp = await postJSON("/api/ai/evaluations/ask", payload);
    if (!resp || !resp.success) {
      toast("AI evaluation failed: " + (resp?.error || "unknown response"), "error");
      return;
    }

    const action = resp.action || "PASS";
    const score = resp.score ?? "—";
    const conf = resp.confidence ? `${Math.round(resp.confidence * 100)}%` : "—";
    const tgtText = resp.target_price ? ` · Tgt ₹${fmt(resp.target_price, 2)}` : "";
    const slText = resp.stop_loss_price ? ` · SL ₹${fmt(resp.stop_loss_price, 2)}` : "";

    if (action.startsWith("BUY")) {
      toast(`🎯 ${action} · Score ${score}/5 (${conf})${tgtText}${slText}`, "success", 4500);
    } else {
      toast(`✋ ${action} · Score ${score}/5 (${conf})`, "info", 3500);
    }

    updateAIWinRateChip();
    if (isAISignalsModalOpen()) {
      refreshAISignalsModal();
    }
  } catch (err: any) {
    toast("AI Ask failed: " + (err?.message || String(err)), "error");
  } finally {
    if (askBtn) {
      askBtn.disabled = false;
      askBtn.textContent = "Ask";
    }
  }
}

let aiModalRefreshTimer: number | null = null;

function isAISignalsModalOpen(): boolean {
  const modal = $("ai-signals-modal");
  return Boolean(modal && !modal.hidden && modal.style.display !== "none");
}

function openAISignalsModal() {
  const modal = $("ai-signals-modal");
  if (!modal) return;
  modal.hidden = false;
  modal.style.display = "grid";
  refreshAISignalsModal();
  if (aiModalRefreshTimer === null) {
    aiModalRefreshTimer = window.setInterval(() => {
      if (!isAISignalsModalOpen()) {
        if (aiModalRefreshTimer !== null) {
          window.clearInterval(aiModalRefreshTimer);
          aiModalRefreshTimer = null;
        }
        return;
      }
      refreshAISignalsModal(true);
    }, 10000);
  }
}

function closeAISignalsModal() {
  const modal = $("ai-signals-modal");
  if (modal) {
    modal.hidden = true;
    modal.style.display = "none";
  }
  if (aiModalRefreshTimer !== null) {
    window.clearInterval(aiModalRefreshTimer);
    aiModalRefreshTimer = null;
  }
}

function updateAIScannerStatus(scanner: any) {
  if (!scanner) return;
  S.aiScanner = scanner;
  renderAIScannerTimer();
  ensureAIScannerTicker();
}

function ensureAIScannerTicker() {
  if (S.aiScannerIntervalStarted) return;
  S.aiScannerIntervalStarted = true;
  window.setInterval(renderAIScannerTimer, 1000);
}

function renderAIScannerTimer() {
  const headerEl = $("ai-header-timer");
  const modalValEl = $("ai-modal-timer-val");
  const modalBadgeEl = $("ai-modal-scanner-badge");
  if (!headerEl && !modalValEl) return;

  const scanner = S.aiScanner;
  const scanErrEl = $("ai-modal-scan-error");
  if (scanErrEl) {
    const msg = scanner && scanner.last_error ? String(scanner.last_error) : "";
    if (msg) {
      scanErrEl.textContent = "⚠ Last scan error: " + msg;
      scanErrEl.style.display = "block";
    } else {
      scanErrEl.textContent = "";
      scanErrEl.style.display = "none";
    }
  }
  if (!scanner || !scanner.enabled) {
    if (headerEl) {
      headerEl.textContent = "⏱ Off";
      headerEl.title = "AI background scanner is disabled in settings";
      headerEl.style.opacity = "0.6";
    }
    if (modalValEl) {
      modalValEl.textContent = "Off";
      modalValEl.style.color = "var(--text-muted, #888)";
    }
    if (modalBadgeEl) modalBadgeEl.title = "AI background scanner is disabled in settings";
    return;
  }

  if (!scanner.running) {
    if (headerEl) {
      headerEl.textContent = "⏱ Paused";
      headerEl.title = "AI background scanner is paused";
      headerEl.style.opacity = "0.7";
    }
    if (modalValEl) {
      modalValEl.textContent = "Paused";
      modalValEl.style.color = "var(--text-muted, #888)";
    }
    return;
  }

  // If all selected underlyings are currently closed
  if (scanner.any_market_open === false) {
    const closedList = (scanner.closed_underlyings || []).join(", ");
    const tip = closedList
      ? `Markets are closed for ${closedList}. Scanner will resume at next market session.`
      : "Selected markets are currently closed. Scanner will resume at next market session.";

    if (headerEl) {
      headerEl.textContent = "⏱ Closed";
      headerEl.title = tip;
      headerEl.style.color = "var(--text-muted, #888)";
      headerEl.style.opacity = "0.75";
    }
    if (modalValEl) {
      modalValEl.textContent = "Closed";
      modalValEl.style.color = "var(--text-muted, #888)";
    }
    if (modalBadgeEl) modalBadgeEl.title = tip;
    return;
  }

  // If backend reports an active scan in progress
  if (scanner.scanning) {
    if (headerEl) {
      headerEl.textContent = "⏱ Scanning…";
      headerEl.title = "AI background scanner is actively evaluating market setups…";
      headerEl.style.color = "var(--accent, #f59e0b)";
      headerEl.style.opacity = "1";
    }
    if (modalValEl) {
      modalValEl.textContent = "Scanning…";
      modalValEl.style.color = "var(--accent, #f59e0b)";
    }
    return;
  }

  const nextUnix = Number(scanner.next_scan_unix || 0);
  if (!nextUnix || nextUnix <= 0) {
    if (headerEl) {
      headerEl.textContent = "⏱ --:--";
      headerEl.style.opacity = "0.7";
    }
    if (modalValEl) modalValEl.textContent = "--:--";
    return;
  }

  const nowSec = Math.floor(Date.now() / 1000);
  const remaining = Math.max(0, nextUnix - nowSec);

  let formatted = "";
  if (remaining <= 0) {
    formatted = "Scanning…";
  } else {
    const mins = Math.floor(remaining / 60);
    const secs = remaining % 60;
    formatted = `${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
  }

  const openList = (scanner.open_underlyings || []).join(", ");
  const closedList = (scanner.closed_underlyings || []).join(", ");
  let scopeTip = `interval: ${scanner.interval_minutes || 15}m`;
  if (openList && closedList) {
    scopeTip += ` · Active: ${openList} | Closed: ${closedList}`;
  } else if (openList) {
    scopeTip += ` · Active: ${openList}`;
  }
  const errSuffix = scanner.last_error
    ? ` · Last error: ${String(scanner.last_error).slice(0, 200)}`
    : "";

  if (headerEl) {
    headerEl.textContent = remaining <= 0 ? "⏱ Scanning…" : `⏱ ${formatted}`;
    headerEl.title = (remaining <= 0 ? "AI scan in progress…" : `Next AI background scan in ${remaining}s (${scopeTip})`) + errSuffix;
    headerEl.style.opacity = "1";
    if (remaining <= 0) {
      headerEl.style.color = "var(--accent, #f59e0b)";
    } else {
      headerEl.style.color = "inherit";
    }
  }

  if (modalValEl) {
    modalValEl.textContent = formatted;
    modalValEl.style.color = "var(--accent, #f59e0b)";
  }
  if (modalBadgeEl) {
    modalBadgeEl.title = `Next scan in ${remaining}s (${scopeTip})` + errSuffix;
  }

  // Auto-refresh when countdown hits zero to sync the next scan cycle and any new calls
  if (remaining === 0 && !S.aiScanRefreshTriggered) {
    S.aiScanRefreshTriggered = true;
    setTimeout(() => {
      S.aiScanRefreshTriggered = false;
      updateAIWinRateChip();
      if (isAISignalsModalOpen()) {
        refreshAISignalsModal();
      }
    }, 2500);
  }
}

async function updateAIWinRateChip() {
  try {
    const res = await getJSON("/api/ai/evaluations/stats");
    const stats = res?.stats;
    if (res?.scanner) {
      updateAIScannerStatus(res.scanner);
    }
    const chip = $("ai-winrate-chip");
    if (!chip || !stats) return;

    if (stats.total_calls > 0 && ((stats.resolved_calls ?? (stats.target_hits + stats.stop_loss_hits)) > 0)) {
      const winRate = Number(stats.win_rate_pct).toFixed(1);
      chip.textContent = `${winRate}%`;
      chip.style.color = stats.win_rate_pct >= 50 ? "var(--green, #22c55e)" : "var(--red, #ef4444)";
      chip.title = `${stats.target_hits || 0} target · ${stats.stop_loss_hits || 0} SL · ${stats.expired_calls || 0} expired · ${stats.pending_calls || 0} active`;
    } else if (stats.total_calls > 0) {
      chip.textContent = `${stats.pending_calls} active`;
      chip.style.color = "var(--accent, #f59e0b)";
      chip.title = "Win rate appears after the first call resolves";
    } else {
      chip.textContent = "--%";
      chip.style.color = "inherit";
      chip.title = "No AI paper calls yet";
    }
  } catch (_) {
    // ignore
  }
}

async function refreshAISignalsModal(silent = false) {
  try {
    const [statsRes, rowsRes] = await Promise.allSettled([
      getJSON("/api/ai/evaluations/stats"),
      fetch("/partials/ai/signals-rows", { headers: { Accept: "text/html" }, cache: "no-store" }).then(async (resp) => {
        if (!resp.ok) throw new Error((await resp.text()).trim() || `HTTP ${resp.status}`);
        return resp.text();
      })
    ]);

    if (statsRes.status === "fulfilled" && statsRes.value?.stats) {
      const s = statsRes.value.stats;
      if (statsRes.value?.scanner) {
        updateAIScannerStatus(statsRes.value.scanner);
      }

      const targetPct = Number(statsRes.value.target_pct || 0);
      const stopPct = Number(statsRes.value.stop_loss_pct || 0);
      const targetTh = $("ai-th-target");
      const stopTh = $("ai-th-stop-loss");
      if (targetTh) targetTh.textContent = targetPct > 0 ? `Target (+${fmt(targetPct, 1)}%)` : "Target";
      if (stopTh) stopTh.textContent = stopPct > 0 ? `SL (-${fmt(stopPct, 1)}%)` : "SL";

      const resolved = Number(s.resolved_calls ?? (Number(s.target_hits || 0) + Number(s.stop_loss_hits || 0)));
      const winRateEl = $("ai-modal-winrate");
      if (winRateEl) {
        if (resolved > 0) {
          winRateEl.textContent = `${Number(s.win_rate_pct).toFixed(1)}%`;
          winRateEl.style.color = s.win_rate_pct >= 50 ? "var(--green, #22c55e)" : "var(--red, #ef4444)";
        } else {
          winRateEl.textContent = "--%";
          winRateEl.style.color = "var(--text-muted, #888)";
        }
      }
      if ($("ai-modal-target-hits")) $("ai-modal-target-hits").textContent = String(s.target_hits || 0);
      if ($("ai-modal-sl-hits")) $("ai-modal-sl-hits").textContent = String(s.stop_loss_hits || 0);
      if ($("ai-modal-expired-calls")) $("ai-modal-expired-calls").textContent = String(s.expired_calls || 0);
      if ($("ai-modal-active-calls")) $("ai-modal-active-calls").textContent = String(s.pending_calls || 0);
      if ($("ai-modal-total-calls")) $("ai-modal-total-calls").textContent = String(s.total_calls || 0);

      const chip = $("ai-winrate-chip");
      if (chip) {
        if (resolved > 0) {
          chip.textContent = `${Number(s.win_rate_pct).toFixed(1)}%`;
          chip.style.color = s.win_rate_pct >= 50 ? "var(--green, #22c55e)" : "var(--red, #ef4444)";
          chip.title = `${s.target_hits || 0} target · ${s.stop_loss_hits || 0} SL · ${s.expired_calls || 0} expired · ${s.pending_calls || 0} active`;
        } else if (s.total_calls > 0) {
          chip.textContent = `${s.pending_calls} active`;
          chip.title = "Win rate appears after the first call resolves";
        }
      }
    }

    if (rowsRes.status === "fulfilled") {
      const tbody = $("ai-calls-tbody");
      if (tbody) tbody.innerHTML = rowsRes.value;
    }
  } catch (err: any) {
    if (!silent) {
      toast("Failed to refresh AI signals: " + (err?.message || String(err)), "error");
    }
  }
}

async function removeAICall(id: string, label: string): Promise<void> {
  const callID = Number(id);
  if (!Number.isFinite(callID) || callID <= 0) return;

  const ok = window.confirm(
    `Remove ${label} from AI signals?\n\nThe backend will stop tracking this call and it will no longer count toward win-rate stats.`
  );
  if (!ok) return;

  try {
    await requestJSON(`/api/ai/evaluations/${callID}`, { method: "DELETE" });
    toast(`Removed ${label} from AI signals`, "success", 3500);
    updateAIWinRateChip();
    await refreshAISignalsModal();
  } catch (err: any) {
    toast("Failed to remove call: " + (err?.message || String(err)), "error");
  }
}

function handleAICallEvent(eventType: string, data: any) {
  if (eventType === "ai_scanner_status" && data) {
    updateAIScannerStatus(data);
    return;
  }
  updateAIWinRateChip();
  if (isAISignalsModalOpen()) {
    refreshAISignalsModal();
  }
  if (eventType === "ai_call_outcome" && data) {
    if (data.status === "TARGET_HIT") {
      toast(`🎯 AI Target Hit: ${data.instrument_key || data.underlying} +${fmt(data.pnl_pct, 1)}%!`, "success", 6000);
    } else if (data.status === "SL_HIT") {
      toast(`🛑 AI Stop-Loss Hit: ${data.instrument_key || data.underlying} ${fmt(data.pnl_pct, 1)}%`, "warn", 6000);
    } else if (data.status === "EXPIRED") {
      toast(`⏳ AI Call Expired: ${data.instrument_key || data.underlying} ${fmt(data.pnl_pct, 1)}%`, "info", 5000);
    }
  } else if (eventType === "ai_call_new" && data) {
    const src = data.source === "agent+jev" ? "Agent+Jev" : data.source === "agent" ? "Agent" : "Jev";
    toast(`🤖 New AI Paper Call (${src}): ${data.action} ${data.underlying} @ ₹${fmt(data.option_ltp, 2)} · Score ${data.score || "—"}/5 · Target ₹${fmt(data.target_price, 2)}`, "info", 5500);
  }
}

async function loadDevicesList() {
  const tbody = $("devices-list-body");
  const tableWrap = $("devices-table-wrap");
  const noDevMsg = $("no-devices-msg");
  if (!tbody) return;

  try {
    const data = await requestJSON("/api/devices");
    const devices = data?.devices || [];

    if (devices.length === 0) {
      if (tableWrap) tableWrap.hidden = true;
      if (noDevMsg) noDevMsg.hidden = false;
      tbody.innerHTML = "";
      return;
    }

    if (noDevMsg) noDevMsg.hidden = true;
    if (tableWrap) tableWrap.hidden = false;

    tbody.innerHTML = devices.map((d: any) => {
      const lastUsed = d.last_used_at ? new Date(d.last_used_at).toLocaleDateString("en-IN", {
        month: "short", day: "numeric", hour: "2-digit", minute: "2-digit"
      }) : "—";
      return `
        <tr>
          <td style="padding:6px 4px;">
            <div class="device-name-cell">
              <span>📱</span>
              <strong>${esc(d.device_name || "Passkey Device")}</strong>
            </div>
          </td>
          <td style="padding:6px 4px; color:var(--text-muted, #888); font-size:11.5px;">${esc(lastUsed)}</td>
          <td style="padding:6px 4px; text-align:right;">
            <button type="button" class="btn-revoke-device" data-device-id="${esc(d.id)}">Revoke</button>
          </td>
        </tr>
      `;
    }).join("");

    tbody.querySelectorAll(".btn-revoke-device").forEach((btn: any) => {
      btn.addEventListener("click", async () => {
        const id = btn.dataset.deviceId;
        if (!id) return;
        btn.disabled = true;
        btn.textContent = "Revoking...";
        await handleRevokeDevice(id);
      });
    });
  } catch (err) {
    console.warn("failed to load devices:", err);
  }
}

async function handleRevokeDevice(id) {
  try {
    const data = await requestJSON(`/api/devices/${encodeURIComponent(id)}`, { method: "DELETE" });
    if (data && data.ok) {
      toast("Device revoked successfully", "ok");
      await loadDevicesList();
      await updateBiometricButtons();
    } else {
      toast(data?.error || "Failed to revoke device", "warn");
    }
  } catch (err: any) {
    toast(err?.message || "Error revoking device", "warn");
  }
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
    if (!collapsed && bottomDirty) renderTab();
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

  $("spot-current")?.addEventListener("click", () => {
    const menu = $("spot-menu");
    setSpotMenuOpen(menu?.hidden !== false);
  });
  $("spot-current")?.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setSpotMenuOpen(true);
    }
  });
  $("spot-menu")?.addEventListener("click", (event) => {
    const row = event.target.closest(".spot-menu-row");
    if (!row) return;
    const key = row.dataset.index;
    if (!key || key === "INDIA VIX") return;
    setSpotMenuOpen(false);
    if (key === S.activeIndex) return;
    switchIndex(key);
  });
  document.addEventListener("click", (event) => {
    const picker = $("spot-picker");
    if (picker && event.target instanceof Node && picker.contains(event.target)) return;
    setSpotMenuOpen(false);
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") setSpotMenuOpen(false);
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
      const chartKey = button.dataset.chart;
      const chart = S.charts[chartKey] || (chartKey === "underlying" ? S.charts.spot : null);
      if (!chart) return;
      if (action === "trades") {
        toggleChartTrades(chartKey);
        return;
      }
      if (action === "ind") {
        toggleIndicatorPopover(button);
        return;
      }
      if (action === "ma") {
        const nextState = chart.toggleEMA();
        button.classList.toggle("active", nextState);
        button.setAttribute("aria-pressed", nextState ? "true" : "false");
        try {
          if (chartKey === "spot" || chartKey === "underlying") {
            localStorage.setItem("scalper-ma-underlying", String(nextState));
            localStorage.setItem("scalper-ma-spot", String(nextState));
          } else {
            localStorage.setItem(`scalper-ma-${chartKey}`, String(nextState));
          }
        } catch (_) {}
        const label = chartKey === "ce" ? "CALL" : (chartKey === "pe" ? "PUT" : "UNDERLYING");
        toast(nextState ? `EMA 10/20/200 enabled (${label})` : `EMA 10/20/200 hidden (${label})`, "ok", 1500);
        return;
      }
      if (action === "dom") {
        const nextState = chart.toggleDepth();
        button.classList.toggle("active", nextState);
        button.setAttribute("aria-pressed", nextState ? "true" : "false");

        if (nextState) {
          const isSpot = chartKey === "underlying" || chartKey === "spot";
          const spotInst = activeSpotInstrument();
          let depth = null;
          if (chartKey === "ce") {
            const ceInst = chartInstrument("CE");
            depth = S.depths?.[ceInst] || (selectedOption("CE")?.bids?.length || selectedOption("CE")?.asks?.length ? { bids: selectedOption("CE").bids, asks: selectedOption("CE").asks } : null);
          } else if (chartKey === "pe") {
            const peInst = chartInstrument("PE");
            depth = S.depths?.[peInst] || (selectedOption("PE")?.bids?.length || selectedOption("PE")?.asks?.length ? { bids: selectedOption("PE").bids, asks: selectedOption("PE").asks } : null);
          } else if (isSpot) {
            depth = S.depths?.[spotInst] || null;
            if (S.chain && S.chain.length) {
              chart.setChainData?.(S.chain);
            }
          }
          if (depth) {
            chart.setDepth(depth);
          } else {
            chart.refreshDepth();
          }
        }

        try {
          localStorage.setItem("scalper-show-dom", String(nextState));
        } catch (_) {}
        const label = chartKey === "ce" ? "CALL" : (chartKey === "pe" ? "PUT" : "UNDERLYING");
        toast(nextState ? `Order Flow DOM enabled (${label})` : `Order Flow DOM hidden (${label})`, "ok", 1500);
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
      if (action === "patterns") {
        const nextState = chart.togglePatterns();
        button.classList.toggle("active", nextState);
        button.setAttribute("aria-pressed", nextState ? "true" : "false");
        try {
          localStorage.setItem("scalper-show-patterns", String(nextState));
        } catch {
          // ignore
        }
        toast(nextState ? "Patterns enabled (Micro, Geometric, Harmonics & Setups)" : "Patterns hidden", "ok", 1500);
        return;
      }
      if (action === "trendline") {
        for (const [otherPane, otherChart] of Object.entries(S.charts) as [string, any][]) {
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
      const strike = num(cell.dataset.strike);
      const targetType = cell.dataset.select || S.selected.type;
      selectStrike(strike, targetType);
      closeStrikeModal();
      if (window.innerWidth <= 768) {
        toast(`Selected ${S.activeIndex} ${fmtStrike(strike)} ${targetType}`, "ok", 2000);
      }
    }
  });

  $("strike-jump-atm-btn")?.addEventListener("click", () => {
    scrollATMIntoView(true);
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
  for (const button of $$(".unit-toggle[data-unit-toggle]")) {
    button.addEventListener("click", () => toggleQuantityUnit(button.dataset.unitToggle));
  }
  for (const button of $$(".validity-toggle")) {
    button.addEventListener("click", () => {
      const type = button.dataset.validityToggle;
      if (!type) return;
      if (!S.orderValidity) S.orderValidity = { CE: "DAY", PE: "DAY" };
      S.orderValidity[type] = S.orderValidity[type] === "IOC" ? "DAY" : "IOC";
      renderOrderPad();
    });
  }

  $("mobile-panic-exit-btn")?.addEventListener("click", onSquareOffAll);

  function openMobileDrawer() {
    const backdrop = $("mobile-drawer-backdrop");
    if (!backdrop) return;
    backdrop.hidden = false;
    const modeBadge = $("drawer-mode-badge");
    if (modeBadge) modeBadge.textContent = tradingMode().toUpperCase();
    const sessionStatus = $("drawer-session-status");
    if (sessionStatus) sessionStatus.textContent = S.session?.authenticated ? "Active" : "Signed Out";
    const authBtn = $("drawer-auth-btn");
    if (authBtn) authBtn.textContent = S.session?.authenticated ? "Sign Out" : "Sign In";
    const themeLabel = $("drawer-theme-label");
    if (themeLabel) themeLabel.textContent = `Switch to ${document.body.dataset.theme === "light" ? "Dark" : "Light"} Theme`;
    const installBtn = $("drawer-install-btn");
    if (installBtn && deferredInstallPrompt) installBtn.hidden = false;
  }

  function closeMobileDrawer() {
    const backdrop = $("mobile-drawer-backdrop");
    if (backdrop) backdrop.hidden = true;
  }

  $("mobile-menu-btn")?.addEventListener("click", openMobileDrawer);
  $("mobile-drawer-close")?.addEventListener("click", closeMobileDrawer);
  $("mobile-drawer-backdrop")?.addEventListener("click", (e) => {
    if (e.target.id === "mobile-drawer-backdrop") closeMobileDrawer();
  });

  $("drawer-status-btn")?.addEventListener("click", () => {
    closeMobileDrawer();
    openStatusModal();
  });
  $("drawer-pair-btn")?.addEventListener("click", () => {
    closeMobileDrawer();
    openPairModal();
  });
  $("drawer-register-btn")?.addEventListener("click", () => {
    closeMobileDrawer();
    handleRegisterDevice();
  });
  $("drawer-theme-btn")?.addEventListener("click", () => {
    setTheme(document.body.dataset.theme === "dark" ? "light" : "dark");
    const themeLabel = $("drawer-theme-label");
    if (themeLabel) themeLabel.textContent = `Switch to ${document.body.dataset.theme === "light" ? "Dark" : "Light"} Theme`;
  });
  $("drawer-unpair-btn")?.addEventListener("click", () => {
    closeMobileDrawer();
    try {
      localStorage.removeItem("scalper_server_url");
    } catch (_) {}
    window.location.href = window.Capacitor ? "capacitor://localhost/index.html" : "/static/index.html";
  });
  $("drawer-auth-btn")?.addEventListener("click", () => {
    closeMobileDrawer();
    if (S.session?.authenticated) logout();
    else openLogin();
  });
  $("drawer-install-btn")?.addEventListener("click", () => {
    closeMobileDrawer();
    if (deferredInstallPrompt) {
      deferredInstallPrompt.prompt();
      deferredInstallPrompt.userChoice.then(() => { deferredInstallPrompt = null; });
    }
  });
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
    if (S.tab === "brackets") {
      fetchBrackets().catch(() => {});
    }
    renderTab();
  });

  $("tab-content").addEventListener("click", (event) => {
    const exit = event.target.closest("[data-exit]");
    if (exit) { onExit(exit.dataset.exit); return; }
    const exitBracket = event.target.closest("[data-exit-bracket]");
    if (exitBracket) { onExitBracket(exitBracket.dataset.exitBracket); return; }
    const cancelBracket = event.target.closest("[data-cancel-bracket]");
    if (cancelBracket) { onCancelBracket(cancelBracket.dataset.cancelBracket); return; }
    const refreshBrackets = event.target.closest("#btn-refresh-brackets");
    if (refreshBrackets) {
      toast("Refreshing brackets…", "info", 1500);
      fetchBrackets().then(() => renderTab()).catch((err) => toast(`Refresh failed: ${err.message}`, "error"));
      return;
    }
    const clearBrackets = event.target.closest("#btn-clear-brackets");
    if (clearBrackets) { onClearFinishedBrackets(); return; }
    const cancelBtn = event.target.closest(".btn-cancel-order");
    if (cancelBtn) { onCancelOrder(cancelBtn.dataset.orderId); return; }
    const cancelAllBtn = event.target.closest("#btn-cancel-all-open");
    if (cancelAllBtn) { onCancelAllOpenOrders(); return; }
    const historyBtn = event.target.closest("[data-order-history]");
    if (historyBtn) { showOrderHistory(historyBtn.dataset.orderHistory); return; }
  });

  $("order-history-close")?.addEventListener("click", () => {
    const modal = $("order-history-modal");
    if (modal) modal.hidden = true;
  });
  $("order-history-close-btn")?.addEventListener("click", () => {
    const modal = $("order-history-modal");
    if (modal) modal.hidden = true;
  });
  $("order-history-modal")?.addEventListener("click", (e) => {
    if (e.target === $("order-history-modal")) {
      $("order-history-modal").hidden = true;
    }
  });

  const serveAuthModal = $("serve-auth-modal");
  const serveAuthForm = $("serve-auth-form");
  const serveAuthUser = $("serve-auth-user");
  const serveAuthPass = $("serve-auth-pass");
  const serveAuthErr = $("serve-auth-error");
  const serveAuthSubmit = $("serve-auth-submit");

  if (serveAuthForm && serveAuthModal) {
    serveAuthForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      if (serveAuthErr) serveAuthErr.hidden = true;
      if (serveAuthSubmit) {
        serveAuthSubmit.disabled = true;
        serveAuthSubmit.textContent = "Verifying...";
      }

      try {
        const resp = await fetch("/api/auth/serve-unlock", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            username: serveAuthUser ? serveAuthUser.value.trim() : "",
            password: serveAuthPass ? serveAuthPass.value : "",
            remember_device: $("serve-auth-remember") ? $("serve-auth-remember").checked : true,
            device_name: window.navigator?.userAgentData?.platform || (window.innerWidth <= 768 ? "Mobile Device" : "Browser")
          })
        });
        const data = await resp.json().catch(() => ({}));
        if (resp.ok && data.ok) {
          if (data.device_id) localStorage.setItem("scalper_device_id", data.device_id);
          if (data.token) localStorage.setItem("scalper_serve_session", data.token);
          serveAuthModal.style.display = "none";
          serveAuthModal.hidden = true;
          await startTerminalSession();
          if (!S.session?.authenticated) {
            openLogin();
          }
        } else {
          if (serveAuthErr) {
            serveAuthErr.textContent = data.error || "Invalid username or password";
            serveAuthErr.hidden = false;
          }
          if (serveAuthPass) {
            serveAuthPass.value = "";
            serveAuthPass.focus();
          }
        }
      } catch (err) {
        if (serveAuthErr) {
          serveAuthErr.textContent = "Verification request failed. Please retry.";
          serveAuthErr.hidden = false;
        }
      } finally {
        if (serveAuthSubmit) {
          serveAuthSubmit.disabled = false;
          serveAuthSubmit.textContent = "Unlock Terminal";
        }
      }
    });

    if (serveAuthModal && !serveAuthModal.hidden && serveAuthModal.style.display !== "none") {
      setTimeout(() => {
        if (serveAuthUser && !serveAuthUser.value) {
          serveAuthUser.focus();
        } else if (serveAuthPass) {
          serveAuthPass.focus();
        }
      }, 100);
    }
  }

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

  $("feed-badge")?.addEventListener("click", openStatusModal);
  $("order-feed-badge")?.addEventListener("click", openStatusModal);
  for (const id of ["feed-badge", "order-feed-badge"]) {
    $(id)?.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        openStatusModal();
      }
    });
  }
  $("status-close")?.addEventListener("click", closeStatusModal);
  $("status-modal-close-btn")?.addEventListener("click", closeStatusModal);
  $("status-refresh-button")?.addEventListener("click", () => refreshStatusModal(true));
  $("status-modal")?.addEventListener("click", (event) => {
    if (event.target === $("status-modal")) closeStatusModal();
  });

  // WebAuthn / Passkey & Device Binding events
  $("pair-device-button")?.addEventListener("click", (e) => {
    e.preventDefault();
    openPairModal();
  });
  $("register-device-button")?.addEventListener("click", (e) => {
    e.preventDefault();
    handleRegisterDevice();
  });
  $("pair-register-this-btn")?.addEventListener("click", (e) => {
    e.preventDefault();
    handleRegisterDevice();
  });
  $("pair-close")?.addEventListener("click", closePairModal);
  $("pair-modal-close-btn")?.addEventListener("click", closePairModal);
  $("pair-modal")?.addEventListener("click", (event) => {
    if (event.target === $("pair-modal")) closePairModal();
  });
  $("copy-pair-url-btn")?.addEventListener("click", async () => {
    const input = $("pair-url-input");
    if (input && input.value) {
      try {
        await navigator.clipboard.writeText(input.value);
        toast("Pairing link copied to clipboard!", "ok");
      } catch {
        input.select();
        document.execCommand("copy");
        toast("Pairing link copied!", "ok");
      }
    }
  });
  $("biometric-unlock-serve-btn")?.addEventListener("click", handleBiometricUnlock);

  // AI Signals & Win Rate Modal events
  $("ai-signals-button")?.addEventListener("click", (e) => {
    e.preventDefault();
    openAISignalsModal();
  });
  $("ai-signals-close")?.addEventListener("click", closeAISignalsModal);
  $("ai-signals-close-btn")?.addEventListener("click", closeAISignalsModal);
  $("ai-signals-modal")?.addEventListener("click", (event) => {
    if (event.target === $("ai-signals-modal")) closeAISignalsModal();
  });
  $("ai-refresh-stats-btn")?.addEventListener("click", () => {
    refreshAISignalsModal();
  });
  $("ai-calls-tbody")?.addEventListener("click", (event) => {
    const target = event.target as HTMLElement | null;
    const btn = target?.closest(".ai-call-remove") as HTMLButtonElement | null;
    if (!btn) return;
    btn.disabled = true;
    void removeAICall(btn.dataset.aiEvalId || "", btn.dataset.aiEvalLabel || "this call")
      .finally(() => { btn.disabled = false; });
  });
  $("ai-scan-now-btn")?.addEventListener("click", async () => {
    const btn = $("ai-scan-now-btn") as HTMLButtonElement | null;
    if (btn) {
      btn.disabled = true;
      btn.textContent = "Scanning…";
    }
    try {
      const underlying = S.activeIndex || "NIFTY";
      const res = await postJSON(`/api/ai/evaluations/scan?underlying=${encodeURIComponent(underlying)}`, {});
      if (res && res.scanner) {
        updateAIScannerStatus(res.scanner);
      }
      if (res && res.success) {
        toast(res.message || "Scan completed", "success");
      } else {
        toast("Scan failed: " + (res?.error || "unknown"), "error");
      }
      await refreshAISignalsModal();
    } catch (err: any) {
      toast("Scan error: " + (err?.message || String(err)), "error");
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.textContent = "⚡ Scan Now (Agent + Jev)";
      }
    }
  });

  // Pattern popover & filters
  const patternPopover = $("pattern-popover");
  const ptnCandleCheck = $("ptn-filter-candlestick");
  const ptnGeoCheck = $("ptn-filter-geometric");
  const ptnHarmCheck = $("ptn-filter-harmonic");
  const ptnLiqCheck = $("ptn-filter-liquidity");
  const ptnCloseBtn = $("pattern-popover-close");

  let savedConfig = { candlestick: true, geometric: true, harmonic: true, liquidity: true };
  try {
    const raw = localStorage.getItem("scalper-pattern-config");
    if (raw) savedConfig = { ...savedConfig, ...JSON.parse(raw) };
  } catch {}

  const applyPatternConfigToAll = (config) => {
    for (const c of (Object.values(S.charts) as any[])) {
      if (c && typeof c.setPatternConfig === "function") {
        c.setPatternConfig(config);
      }
    }
    try {
      localStorage.setItem("scalper-pattern-config", JSON.stringify(config));
    } catch {}
  };

  applyPatternConfigToAll(savedConfig);

  if (ptnCandleCheck) ptnCandleCheck.checked = savedConfig.candlestick !== false;
  if (ptnGeoCheck) ptnGeoCheck.checked = savedConfig.geometric !== false;
  if (ptnHarmCheck) ptnHarmCheck.checked = savedConfig.harmonic !== false;
  if (ptnLiqCheck) ptnLiqCheck.checked = savedConfig.liquidity !== false;

  const onFilterChange = () => {
    savedConfig = {
      candlestick: Boolean(ptnCandleCheck?.checked),
      geometric: Boolean(ptnGeoCheck?.checked),
      harmonic: Boolean(ptnHarmCheck?.checked),
      liquidity: Boolean(ptnLiqCheck?.checked),
    };
    applyPatternConfigToAll(savedConfig);
  };

  ptnCandleCheck?.addEventListener("change", onFilterChange);
  ptnGeoCheck?.addEventListener("change", onFilterChange);
  ptnHarmCheck?.addEventListener("change", onFilterChange);
  ptnLiqCheck?.addEventListener("change", onFilterChange);

  ptnCloseBtn?.addEventListener("click", () => {
    if (patternPopover) patternPopover.style.display = "none";
  });

  document.addEventListener("click", (e) => {
    if (patternPopover && patternPopover.style.display !== "none") {
      if (!patternPopover.contains(e.target as any) && !(e.target as any)?.closest?.('[data-chart-action="patterns"]')) {
        patternPopover.style.display = "none";
      }
    }
  });

  $$('button[data-chart-action="patterns"]').forEach((btn) => {
    btn.addEventListener("contextmenu", (e) => {
      e.preventDefault();
      if (!patternPopover) return;
      const rect = btn.getBoundingClientRect();
      patternPopover.style.display = "block";
      patternPopover.style.position = "fixed";
      patternPopover.style.top = `${Math.min(window.innerHeight - 200, rect.bottom + 4)}px`;
      patternPopover.style.left = `${Math.max(10, Math.min(window.innerWidth - 260, rect.left - 100))}px`;
    });
  });

  // Chart indicator popover (RSI / divergence / momentum candles)
  const indicatorPopover = $("indicator-popover");
  for (const id of ["ind-rsi", "ind-divergence", "ind-momentum"]) {
    $(id)?.addEventListener("change", applyIndicatorPrefs);
  }
  $("indicator-popover-close")?.addEventListener("click", () => {
    if (indicatorPopover) indicatorPopover.style.display = "none";
  });
  document.addEventListener("click", (e) => {
    if (indicatorPopover && indicatorPopover.style.display !== "none") {
      if (!indicatorPopover.contains(e.target as any) && !(e.target as any)?.closest?.('[data-chart-action="ind"]')) {
        indicatorPopover.style.display = "none";
      }
    }
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
    if (!$("pair-modal")?.hidden) {
      closePairModal();
      return;
    }
    if (!$("status-modal")?.hidden) {
      closeStatusModal();
      return;
    }
    if (!$("order-history-modal")?.hidden) {
      $("order-history-modal").hidden = true;
      return;
    }
    if (!$("strike-modal").hidden) {
      closeStrikeModal();
      return;
    }
    if (S.focusedChart) setFocusedChart(null);
  });

  $$("#chart-toggle-group button, .chart-vis-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const pane = btn.dataset.chartToggle || btn.dataset.chartVis;
      const norm = (pane === "spot" ? "underlying" : pane);
      const isVis = isPaneVisible(norm);
      setPaneVisible(norm, !isVis);
    });
  });

  $$(".pane-close-btn[data-chart-hide]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const pane = btn.dataset.chartHide;
      setPaneVisible(pane, false);
    });
  });

  $$(".pro-tools-toggle").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const pane = btn.closest(".pane");
      if (!pane) return;
      const isOpen = pane.classList.toggle("pro-tools-open");
      btn.classList.toggle("active", isOpen);
      btn.setAttribute("aria-expanded", isOpen ? "true" : "false");
      const label = btn.querySelector(".tools-label");
      if (label) {
        label.textContent = isOpen ? "Hide" : "Tools";
      }
    });
  });

  initChartQuickOrder();
  bindChartClickEvents();
  initChartTooltips();
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
  if (!S.session?.authenticated) return;
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
  await ensureSavedStrikesLoaded();
  selectDefaultStrike();
  renderAll();
  loadCharts();
  expandChainToPremium();
}

async function switchExpiry(date) {
  if (!date) return;
  try { localStorage.setItem(`scalper-last-expiry-${S.activeIndex}`, date); } catch (_) {}
  if (!S.session?.authenticated) return;
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
  await ensureSavedStrikesLoaded();
  selectDefaultStrike();
  renderAll();
  loadCharts();
  expandChainToPremium();
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
  if (clock) {
    clock.textContent = `${clockFormatter.format(new Date())} IST`;
  }
  updateSessionCountdown();
  // Per-underlying statuses change at different times (NSE/BSE vs MCX).
  renderSpotPicker();
}

function isCommodityKey(key: string): boolean {
  return (S.boot?.commodities || []).some((commodity) => commodity.key === key);
}

function marketOpenForKey(key: string): boolean {
  const parts = marketStateFormatter.formatToParts(new Date());
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  const date = `${values.year}-${values.month}-${values.day}`;
  if (values.weekday === "Sat" || values.weekday === "Sun" ||
      (S.boot?.marketSession?.holidays || []).includes(date)) return false;
  const toMinutes = (value, fallback) => {
    const match = /^(\d{2}):(\d{2})$/.exec(value || "");
    return match ? Number(match[1]) * 60 + Number(match[2]) : fallback;
  };
  let open;
  let close;
  if (isCommodityKey(key)) {
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

function currentMarketState() {
  return marketOpenForKey(S.activeIndex);
}

function updateMarketPhase(reloadOnChange = true) {
  const open = currentMarketState();
  if (S.marketOpen === open) return;
  const wasKnown = S.marketOpen !== null;
  S.marketOpen = open;
  renderSpotPicker();
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

/* ── Mobile Navigation & PWA ───────────────────────────── */

function updateMobileBottomNav(activeTab) {
  for (const btn of $$(".mobile-bnav-btn")) {
    const isCur = btn.dataset.bnav === activeTab;
    btn.classList.toggle("active", isCur);
    btn.setAttribute("aria-pressed", isCur ? "true" : "false");
  }
}

function setMobileView(view) {
  if (view === "chain") {
    openStrikeModal();
    return;
  }
  S.mobileView = view;
  const terminal = $("charts-row")?.closest(".terminal");
  if (terminal) {
    terminal.dataset.mobileView = view;
  }
  for (const btn of $$(".mobile-nav-btn")) {
    const isActive = btn.dataset.mobileView === view;
    btn.classList.toggle("active", isActive);
    btn.setAttribute("aria-pressed", isActive ? "true" : "false");
  }

  if (view === "positions") {
    updateMobileBottomNav("positions");
  } else if (view === "orders") {
    updateMobileBottomNav("orders");
  } else {
    S.lastActiveContractView = view;
    updateMobileBottomNav("terminal");
  }

  requestAnimationFrame(() => {
    for (const chart of (Object.values(S.charts) as any[])) {
      chart.schedule();
    }
  });
}

function initMobileNavigation() {
  const mobileNav = $("mobile-nav");
  if (mobileNav) {
    mobileNav.addEventListener("click", (e) => {
      const btn = e.target.closest(".mobile-nav-btn");
      if (!btn) return;
      const view = btn.dataset.mobileView;
      if (view) {
        setMobileView(view);
      }
    });
  }

  const bottomNav = $("mobile-bottom-nav");
  if (bottomNav) {
    bottomNav.addEventListener("click", (e) => {
      const btn = e.target.closest(".mobile-bnav-btn");
      if (!btn) return;
      const bnav = btn.dataset.bnav;
      if (bnav === "terminal") {
        const bottomPanel = $("bottom-panel");
        if (bottomPanel) {
          bottomPanel.classList.add("bottom-collapsed");
        }
        setMobileView(S.lastActiveContractView || "ce");
        updateMobileBottomNav("terminal");
      } else if (bnav === "chain") {
        openStrikeModal();
      } else if (bnav === "positions") {
        setMobileView("positions");
        switchTab("positions");
        const bottomPanel = $("bottom-panel");
        if (bottomPanel) {
          bottomPanel.classList.remove("bottom-collapsed");
        }
        updateMobileBottomNav("positions");
      } else if (bnav === "orders") {
        setMobileView("orders");
        switchTab("orders");
        const bottomPanel = $("bottom-panel");
        if (bottomPanel) {
          bottomPanel.classList.remove("bottom-collapsed");
        }
        updateMobileBottomNav("orders");
      }
    });
  }
}

let deferredInstallPrompt = null;

function registerServiceWorker() {
  if (!("serviceWorker" in navigator)) return;
  const register = () => {
    navigator.serviceWorker.register("/sw.js", { scope: "/" })
      .then((reg) => {
        reg.addEventListener("updatefound", () => {
          const installing = reg.installing;
          if (installing) {
            installing.addEventListener("statechange", () => {
              if (installing.state === "installed" && navigator.serviceWorker.controller) {
                console.log("[PWA] Update ready; reload to activate.");
              }
            });
          }
        });
      })
      .catch((err) => {
        console.warn("[PWA] Service Worker registration failed:", err);
      });
  };

  if (document.readyState === "complete") {
    register();
  } else {
    window.addEventListener("load", register);
  }
}

function initPWAInstallPrompt() {
  const isStandalone = window.matchMedia("(display-mode: standalone)").matches ||
                       window.navigator.standalone === true;
  if (isStandalone) {
    document.body.classList.add("is-pwa");
    return;
  }

  window.addEventListener("beforeinstallprompt", (e) => {
    e.preventDefault();
    deferredInstallPrompt = e;
    showInstallPromptUI();
  });

  window.addEventListener("appinstalled", () => {
    deferredInstallPrompt = null;
    hideInstallPromptUI();
    document.body.classList.add("is-pwa");
    toast("Scalper installed successfully!", "ok");
  });

  const installBtn = $("install-button");
  if (installBtn) {
    installBtn.addEventListener("click", triggerPWAInstall);
  }

  const bannerInstallBtn = $("banner-install-btn");
  if (bannerInstallBtn) {
    bannerInstallBtn.addEventListener("click", triggerPWAInstall);
  }

  const bannerDismissBtn = $("banner-dismiss-btn");
  if (bannerDismissBtn) {
    bannerDismissBtn.addEventListener("click", () => {
      hideInstallBanner();
      try {
        sessionStorage.setItem("scalper-install-banner-dismissed", "true");
      } catch (_) {}
    });
  }
}

async function triggerPWAInstall() {
  if (!deferredInstallPrompt) return;
  deferredInstallPrompt.prompt();
  try {
    const choice = await deferredInstallPrompt.userChoice;
    if (choice.outcome === "accepted") {
      toast("Installing Scalper...", "ok");
    }
  } catch (err) {
    console.warn("[PWA] Prompt choice error:", err);
  }
  deferredInstallPrompt = null;
  hideInstallPromptUI();
}

function showInstallPromptUI() {
  const installBtn = $("install-button");
  if (installBtn) installBtn.hidden = false;

  let dismissed = false;
  try {
    dismissed = sessionStorage.getItem("scalper-install-banner-dismissed") === "true";
  } catch (_) {}

  const banner = $("install-banner");
  if (banner && !dismissed && window.innerWidth <= 768) {
    banner.hidden = false;
  }
}

function hideInstallBanner() {
  const banner = $("install-banner");
  if (banner) banner.hidden = true;
}

function hideInstallPromptUI() {
  const installBtn = $("install-button");
  if (installBtn) installBtn.hidden = true;
  hideInstallBanner();
}

window.addEventListener("resize", () => {
  requestAnimationFrame(() => {
    for (const chart of (Object.values(S.charts) as any[])) {
      chart.schedule();
    }
  });
});

/* ── Android Hardware / Gesture Back Button Handling ───── */

function handleAppBackButton() {
  // 1. Close mobile drawer if open
  const drawerBackdrop = $("mobile-drawer-backdrop");
  if (drawerBackdrop && !drawerBackdrop.hidden) {
    closeMobileDrawer();
    return true;
  }

  // 2. Close pattern popover if open
  const patternPopover = $("pattern-popover");
  if (patternPopover && !patternPopover.hidden && patternPopover.style.display !== "none") {
    patternPopover.hidden = true;
    patternPopover.style.display = "none";
    return true;
  }

  // 3. Close open modal backdrops (highest priority first)
  const modalClosers = [
    { id: "strike-modal", fn: closeStrikeModal },
    { id: "confirm-modal", fn: () => closeConfirm(false) },
    { id: "pair-modal", fn: closePairModal },
    { id: "status-modal", fn: closeStatusModal },
    { id: "login-modal", fn: closeLogin },
  ];

  for (const m of modalClosers) {
    const el = $(m.id);
    if (el && !el.hidden && el.style.display !== "none") {
      if (typeof m.fn === "function") {
        m.fn();
      } else {
        el.hidden = true;
        el.style.display = "none";
      }
      return true;
    }
  }

  // 4. Close quick order dialog if visible
  const orderDialog = $("order-dialog");
  if (orderDialog && !orderDialog.hidden && orderDialog.style.display !== "none") {
    orderDialog.hidden = true;
    orderDialog.style.display = "none";
    return true;
  }

  // 5. If in a mobile subview (e.g. POSITIONS or ORDERS or CE or PE), return to SPOT chart
  if (S.mobileView && S.mobileView !== "spot") {
    setMobileView("spot");
    return true;
  }

  return false;
}
window.handleAppBackButton = handleAppBackButton;

document.addEventListener("backbutton", (e) => {
  if (handleAppBackButton()) {
    e.preventDefault();
    e.stopPropagation();
  }
});

window.addEventListener("hardwareBackButton", (e) => {
  if (handleAppBackButton()) {
    e.preventDefault();
  }
});

window.addEventListener("popstate", () => {
  handleAppBackButton();
});

if (window.Capacitor?.Plugins?.App) {
  try {
    window.Capacitor.Plugins.App.addListener("backButton", ({ canGoBack }) => {
      if (handleAppBackButton()) return;
      if (canGoBack) window.history.back();
      else window.Capacitor.Plugins.App.exitApp();
    });
  } catch (_) {}
}

/* ── Boot ──────────────────────────────────────────────── */

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
