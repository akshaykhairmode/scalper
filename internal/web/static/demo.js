// Simulated market/paper engine used when `fake_data.enabled` is on, or when the
// backend market endpoints are not available yet. It is intentionally isolated
// behind the same shapes the real API returns, so wiring the backend later does
// not require UI changes.

const SPECS = {
  NIFTY: { base: 22749.75, step: 50, lot: 75, strikes: 25, drift: 0.00045 },
  BANKNIFTY: { base: 48231.5, step: 100, lot: 35, strikes: 25, drift: 0.0006 },
  MIDCPNIFTY: { base: 12856.2, step: 25, lot: 120, strikes: 25, drift: 0.0007 },
  SENSEX: { base: 73598.67, step: 100, lot: 20, strikes: 25, drift: 0.0005 },
};

const INDEX_META = {
  NIFTY: { segment: "nse_fo", spotSegment: "nse_cm", spotSymbol: "Nifty 50" },
  BANKNIFTY: { segment: "nse_fo", spotSegment: "nse_cm", spotSymbol: "Nifty Bank" },
  MIDCPNIFTY: { segment: "nse_fo", spotSegment: "nse_cm", spotSymbol: "Nifty Midcap Select" },
  SENSEX: { segment: "bse_fo", spotSegment: "bse_cm", spotSymbol: "SENSEX" },
};

const INDIA_VIX = { key: "INDIA VIX", spotSegment: "nse_cm", base: 16.4 };

const round2 = (n) => Math.round(n * 100) / 100;
const roundTick = (n, tick = 0.05) => Math.round(n / tick) * tick;

function istDate(date = new Date()) {
  return new Date(date.toLocaleString("en-US", { timeZone: "Asia/Kolkata" }));
}

function isoDate(date) {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, "0");
  const d = String(date.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

function expiryLabel(iso) {
  const d = new Date(iso + "T00:00:00");
  return d.toLocaleDateString("en-GB", { day: "2-digit", month: "short" }).toUpperCase();
}

export function buildExpiries(indexKey, mode = "both") {
  const today = istDate();
  today.setHours(0, 0, 0, 0);
  const dates = [];
  const cursor = new Date(today);
  // SENSEX expires on Friday in this simulation; NSE indices on Thursday.
  const targetDay = indexKey === "SENSEX" ? 5 : 4;
  while (dates.length < 8) {
    if (cursor.getDay() === targetDay) dates.push(new Date(cursor));
    cursor.setDate(cursor.getDate() + 1);
  }
  const lastInMonth = new Map();
  for (const d of dates) {
    const month = `${d.getFullYear()}-${d.getMonth()}`;
    lastInMonth.set(month, isoDate(d));
  }
  return dates.map((d) => {
    const date = isoDate(d);
    const monthly = lastInMonth.get(`${d.getFullYear()}-${d.getMonth()}`) === date;
    return {
      date,
      label: expiryLabel(date),
      weekly: mode !== "monthly" || monthly,
      monthly: mode === "monthly" || mode === "both" ? monthly : mode === "both",
      expired: false,
    };
  });
}

function daysToExpiry(iso) {
  const target = new Date(iso + "T15:30:00+05:30").getTime();
  return Math.max(0.35, (target - Date.now()) / 86400000);
}

export class DemoMarket {
  constructor(boot) {
    this.boot = boot;
    this.mode = boot.mode || "paper";
    this.specs = SPECS;
    this.spots = new Map();
    this.prevSpots = new Map();
    this.chains = new Map(); // `${index}|${expiry}` -> rows
    this.optionIndex = new Map();
    this.candles = new Map();
    this.positions = new Map();
    this.orders = [];
    this.trades = [];
    this.realised = 0;
    this.cash = Number(boot.startingCash) || 1000000;
    this.startingCash = this.cash;
    this.shortMarginPct = Number(boot.shortMarginPct) || 15;
    this.orderSeq = 1;
    this.tradeSeq = 1;
    this.lastMinute = Math.floor(Date.now() / 60000) * 60;

    this.seedSpots();
    const first = boot.indices?.[0]?.key || "NIFTY";
    this.activeIndex = first;
    this.activeExpiry = buildExpiries(first, boot.indices?.[0]?.expiryMode || "both")[0]?.date;
    this.chainFor(first, this.activeExpiry);
    this.seedCandles();
  }

  indexMeta(key) {
    return INDEX_META[key] || { segment: "nse_fo", spotSegment: "nse_cm", spotSymbol: key };
  }

  seedSpots() {
    const list = this.boot.indices || [];
    for (const idx of list) {
      const spec = this.specs[idx.key] || { base: 10000, step: 50, lot: 75, strikes: 21 };
      const ltp = round2(spec.base * (0.998 + Math.random() * 0.004));
      const prevClose = round2(spec.base * (0.995 + Math.random() * 0.01));
      this.spots.set(idx.key, {
        key: idx.key,
        displayName: idx.displayName || idx.key,
        exchangeSegment: idx.exchangeSegment,
        indexSegment: idx.indexSegment,
        symbol: idx.indexSymbol,
        ltp,
        prevClose,
        close: prevClose,
        change: round2(ltp - prevClose),
        changePct: round2(((ltp - prevClose) / prevClose) * 100),
        volume: 0,
        spotKey: `${idx.indexSegment}|${idx.key}`,
      });
      this.prevSpots.set(idx.key, ltp);
    }
    if (this.boot.indices?.length) {
      const vix = round2(INDIA_VIX.base + (Math.random() - 0.5) * 0.6);
      this.spots.set("INDIA VIX", {
        key: "INDIA VIX",
        displayName: "INDIA VIX",
        exchangeSegment: "nse_cm",
        indexSegment: "nse_cm",
        symbol: "INDIA VIX",
        ltp: vix,
        prevClose: round2(vix + (Math.random() - 0.5) * 0.4),
        close: vix,
        change: round2((Math.random() - 0.5) * 0.4),
        changePct: round2((Math.random() - 0.5) * 2),
        volume: 0,
        spotKey: "nse_cm|INDIA VIX",
      });
    }
  }

  spot(key) {
    return this.spots.get(key);
  }

  spotList() {
    return [...this.spots.values()];
  }

  expiries(indexKey) {
    const idx = (this.boot.indices || []).find((i) => i.key === indexKey);
    return buildExpiries(indexKey, idx?.expiryMode || "both");
  }

  chainFor(indexKey, expiry) {
    const first = this.expiries(indexKey)[0]?.date;
    const date = expiry || first;
    if (!date) return [];
    const cacheKey = `${indexKey}|${date}`;
    if (this.chains.has(cacheKey)) return this.chains.get(cacheKey);

    const spec = this.specs[indexKey] || { step: 50, lot: 75, strikes: 21 };
    const spot = this.spot(indexKey)?.ltp || spec.base;
    const atm = Math.round(spot / spec.step) * spec.step;
    const dte = daysToExpiry(date);
    const timeScale = Math.min(1.7, Math.sqrt(dte / 3.5));
    const rows = [];
    const half = Math.floor(spec.strikes / 2);

    for (let i = -half; i <= half; i++) {
      const strike = atm + i * spec.step;
      if (strike <= 0) continue;
      const row = { strike, atm: i === 0, indexKey, expiry: date, call: null, put: null };
      row.call = this.makeOption(indexKey, date, strike, "CE", spot, spec, timeScale, i);
      row.put = this.makeOption(indexKey, date, strike, "PE", spot, spec, timeScale, i);
      rows.push(row);
    }
    this.chains.set(cacheKey, rows);
    return rows;
  }

  makeOption(indexKey, expiry, strike, type, spot, spec, timeScale, distance) {
    const meta = this.indexMeta(indexKey);
    const intrinsic = type === "CE" ? Math.max(0, spot - strike) : Math.max(0, strike - spot);
    const base = spot * 0.0026 * timeScale;
    const skew = type === "PE" ? 1.06 : 1;
    const timeValue = Math.max(0.15, base * Math.exp(-0.5 * Math.pow(distance / 4.4, 2)) * skew);
    const noise = 0.96 + Math.random() * 0.08;
    const ltp = roundTick(Math.max(0.05, (intrinsic + timeValue) * noise));
    const prevClose = roundTick(Math.max(0.05, ltp * (0.86 + Math.random() * 0.28)));
    const oi = Math.round((180000 + Math.random() * 1900000) * (1 - Math.min(0.65, Math.abs(distance) / 18)));
    const volume = Math.round(20000 + Math.random() * 400000);
    const strikeKey = Number.isInteger(strike) ? String(strike) : String(round2(strike));
    const instrumentKey = `${meta.segment}|${indexKey}|${expiry}|${strikeKey}|${type}`;
    const opt = {
      instrumentKey,
      exchangeSegment: meta.segment,
      underlying: indexKey,
      expiry,
      strike,
      strikePaise: Math.round(strike * 100),
      type,
      optionType: type,
      lotSize: spec.lot,
      symbol: `${indexKey} ${expiry} ${strike} ${type}`,
      pSymbol: `SIM${indexKey}${strike}${type}`,
      pTrdSymbol: `${indexKey}${expiry.replaceAll("-", "")}${strike}${type}`,
      ltp,
      prevClose,
      close: prevClose,
      change: round2(ltp - prevClose),
      changePct: round2(((ltp - prevClose) / prevClose) * 100),
      oi,
      volume,
      bid: roundTick(Math.max(0.05, ltp - 0.05)),
      ask: roundTick(ltp + 0.05),
    };
    this.optionIndex.set(instrumentKey, opt);
    return opt;
  }

  activeChain() {
    return this.chainFor(this.activeIndex, this.activeExpiry);
  }

  setActive(indexKey, expiry) {
    if (indexKey) this.activeIndex = indexKey;
    if (expiry) this.activeExpiry = expiry;
    if (!this.activeExpiry) this.activeExpiry = this.expiries(this.activeIndex)[0]?.date;
    return this.activeChain();
  }

  option(key) {
    return this.optionIndex.get(key);
  }

  lotSize(indexKey) {
    return (this.specs[indexKey] || {}).lot || 75;
  }

  seedCandles() {
    for (const spot of this.spots.values()) {
      if (spot.key === "INDIA VIX") continue;
      this.candles.set(spot.spotKey, this.generateCandles(spot.ltp, 180, false, 0.0014));
    }
    // Pre-seed the active chain so the three panes have history immediately.
    for (const row of this.activeChain()) {
      this.candles.set(row.call.instrumentKey, this.generateCandles(row.call.ltp, 180, true, 0.02));
      this.candles.set(row.put.instrumentKey, this.generateCandles(row.put.ltp, 180, true, 0.02));
    }
  }

  generateCandles(lastPrice, count, withVolume, volatility) {
    const end = Math.floor(Date.now() / 60000) * 60;
    const candles = [];
    let price = Math.max(0.05, lastPrice * (0.9 + Math.random() * 0.15));
    for (let i = count - 1; i >= 0; i--) {
      const open = price;
      const move = open * (Math.random() - 0.5) * volatility;
      const close = Math.max(0.05, open + move);
      const high = Math.max(open, close) * (1 + Math.random() * volatility * 0.45);
      const low = Math.min(open, close) * (1 - Math.random() * volatility * 0.45);
      const volume = withVolume ? Math.round(800 + Math.random() * 12000) : 0;
      candles.push({
        time: end - i * 60,
        open: round2(open),
        high: round2(high),
        low: round2(low),
        close: round2(close),
        volume,
      });
      price = close;
    }
    const last = candles[candles.length - 1];
    const drift = lastPrice - last.close;
    for (const c of candles) {
      c.open = round2(Math.max(0.05, c.open + drift));
      c.high = round2(Math.max(0.05, c.high + drift));
      c.low = round2(Math.max(0.05, c.low + drift));
      c.close = round2(Math.max(0.05, c.close + drift));
    }
    return candles;
  }

  candlesFor(priceKey, timeframe = "1m") {
    let raw = this.candles.get(priceKey);
    if (!raw) {
      const opt = this.option(priceKey);
      const spot = this.spots.get(priceKey.replace(/^.*\|/, ""));
      const price = opt?.ltp || spot?.ltp || 100;
      raw = this.generateCandles(price, 180, Boolean(opt), opt ? 0.02 : 0.0014);
      this.candles.set(priceKey, raw);
    }
    const minutes = { "1m": 1, "3m": 3, "5m": 5, "15m": 15, "1h": 60 }[timeframe] || 1;
    if (minutes === 1) return raw.slice();
    const out = [];
    for (const c of raw) {
      const bucket = Math.floor(c.time / (minutes * 60)) * minutes * 60;
      const last = out[out.length - 1];
      if (last && last.time === bucket) {
        last.high = Math.max(last.high, c.high);
        last.low = Math.min(last.low, c.low);
        last.close = c.close;
        last.volume += c.volume;
      } else {
        out.push({ time: bucket, open: c.open, high: c.high, low: c.low, close: c.close, volume: c.volume });
      }
    }
    return out;
  }

  pushPrice(priceKey, price, volume = 0) {
    const arr = this.candles.get(priceKey);
    if (!arr) return;
    const minute = Math.floor(Date.now() / 60000) * 60;
    let last = arr[arr.length - 1];
    if (minute > last.time) {
      last = { time: minute, open: price, high: price, low: price, close: price, volume: 0 };
      arr.push(last);
      if (arr.length > 1200) arr.shift();
    } else {
      last.close = price;
      last.high = Math.max(last.high, price);
      last.low = Math.min(last.low, price);
    }
    last.volume += Math.max(0, Math.round(volume));
  }

  tick() {
    for (const spot of this.spots.values()) {
      if (spot.key === "INDIA VIX") {
        spot.ltp = round2(Math.max(8, spot.ltp + (Math.random() - 0.5) * 0.08));
      } else {
        const spec = this.specs[spot.key] || { drift: 0.0005 };
        const move = spot.ltp * (Math.random() - 0.49) * spec.drift;
        spot.ltp = round2(Math.max(1, spot.ltp + move));
        this.pushPrice(spot.spotKey, spot.ltp, 0);
      }
      spot.change = round2(spot.ltp - spot.prevClose);
      spot.changePct = round2((spot.change / spot.prevClose) * 100);
    }

    for (const opt of this.optionIndex.values()) {
      const spot = this.spots.get(opt.underlying);
      if (!spot) continue;
      const prev = this.prevSpots.get(opt.underlying) ?? spot.ltp;
      const delta = spot.ltp - prev;
      const spec = this.specs[opt.underlying] || { step: 50 };
      const distance = (opt.strike - spot.ltp) / spec.step;
      const factor = Math.exp(-0.5 * Math.pow(distance / 5, 2));
      const direction = opt.type === "CE" ? 1 : -1;
      const noise = opt.ltp * (Math.random() - 0.5) * 0.012;
      opt.ltp = roundTick(Math.max(0.05, opt.ltp + delta * direction * factor * 0.55 + noise));
      opt.change = round2(opt.ltp - opt.prevClose);
      opt.changePct = round2((opt.change / opt.prevClose) * 100);
      opt.bid = roundTick(Math.max(0.05, opt.ltp - 0.05));
      opt.ask = roundTick(opt.ltp + 0.05);
      opt.oi = Math.max(1000, opt.oi + Math.round((Math.random() - 0.5) * 4000));
      opt.volume += Math.round(Math.random() * 900);
      this.pushPrice(opt.instrumentKey, opt.ltp, Math.random() * 900);
    }

    for (const key of this.spots.keys()) {
      if (key !== "INDIA VIX") this.prevSpots.set(key, this.spots.get(key).ltp);
    }
  }

  depthFor(opt) {
    const levels = [];
    const tick = 0.05;
    let bid = opt.bid || Math.max(0.05, opt.ltp - tick);
    let ask = opt.ask || opt.ltp + tick;
    for (let i = 0; i < 5; i++) {
      levels.push({
        bid: round2(Math.max(0.05, bid - i * tick)),
        bidQty: Math.round(750 + Math.random() * 8000),
        ask: round2(ask + i * tick),
        askQty: Math.round(750 + Math.random() * 8000),
      });
    }
    return levels;
  }

  availableMargin() {
    return round2(this.cash);
  }

  requiredMargin(opt, shares, side) {
    if (!opt) return 0;
    const spot = this.spot(opt.underlying)?.ltp || 0;
    if (side === "B") return round2(opt.ltp * shares);
    return round2(spot * shares * (this.shortMarginPct / 100) + opt.ltp * shares);
  }

  positionList() {
    const out = [];
    for (const pos of this.positions.values()) {
      if (!pos.qty) continue;
      const opt = this.option(pos.instrumentKey);
      const spot = opt ? this.spot(opt.underlying) : null;
      const ltp = opt?.ltp || spot?.ltp || pos.avg;
      out.push({
        instrument_key: pos.instrumentKey,
        exchange_segment: pos.exchangeSegment,
        symbol: opt?.symbol || pos.symbol,
        underlying: opt?.underlying || pos.underlying,
        expiry: opt?.expiry || pos.expiry,
        strike: opt?.strike || pos.strike,
        option_type: opt?.type || pos.optionType,
        lot_size: opt?.lotSize || pos.lotSize,
        quantity: pos.qty,
        average_price: round2(pos.avg),
        ltp,
        pnl: round2((ltp - pos.avg) * pos.qty),
        product: pos.product || "MIS",
      });
    }
    return out;
  }

  orderList() {
    return this.orders.slice().reverse();
  }

  tradeList() {
    return this.trades.slice().reverse();
  }

  realisedPnl() {
    return round2(this.realised);
  }

  unrealisedPnl() {
    return round2(this.positionList().reduce((sum, p) => sum + p.pnl, 0));
  }

  placeOrder({ instrumentKey, side, qty, product = "MIS", intent = "ENTRY" }) {
    const opt = this.option(instrumentKey);
    if (!opt) throw new Error("Unknown contract");
    const shares = Math.abs(Number(qty) || 0);
    if (!shares) throw new Error("Quantity must be greater than zero");
    const slip = 0.05;
    const price = roundTick(side === "B" ? opt.ltp + slip : Math.max(0.05, opt.ltp - slip));
    const cost = price * shares;

    if (side === "B" && cost > this.cash + 1e-6) {
      throw new Error(`Insufficient paper cash: need ₹${cost.toFixed(2)}, available ₹${this.cash.toFixed(2)}`);
    }

    const signed = side === "B" ? shares : -shares;
    let pos = this.positions.get(instrumentKey);
    if (!pos) {
      pos = {
        instrumentKey,
        exchangeSegment: opt.exchangeSegment,
        symbol: opt.symbol,
        underlying: opt.underlying,
        expiry: opt.expiry,
        strike: opt.strike,
        optionType: opt.type,
        lotSize: opt.lotSize,
        qty: 0,
        avg: 0,
        product,
      };
      this.positions.set(instrumentKey, pos);
    }

    if (pos.qty === 0 || Math.sign(pos.qty) === Math.sign(signed)) {
      const totalQty = Math.abs(pos.qty) + shares;
      pos.avg = round2((pos.avg * Math.abs(pos.qty) + price * shares) / totalQty);
      pos.qty += signed;
    } else {
      const closing = Math.min(shares, Math.abs(pos.qty));
      this.realised += (price - pos.avg) * closing * Math.sign(pos.qty);
      const remaining = shares - closing;
      if (remaining > 0) {
        pos.qty = Math.sign(signed) * remaining;
        pos.avg = price;
      } else {
        pos.qty += signed;
      }
    }

    if (side === "B") this.cash -= cost;
    else this.cash += cost;

    if (!pos.qty) this.positions.delete(instrumentKey);

    const now = Math.floor(Date.now() / 1000);
    const order = {
      id: `SIM-${String(this.orderSeq++).padStart(4, "0")}`,
      client_order_id: `sim-${Date.now()}`,
      instrument_key: instrumentKey,
      exchange_segment: opt.exchangeSegment,
      symbol: opt.symbol,
      underlying: opt.underlying,
      side,
      buy_sell: side,
      qty: shares,
      quantity: shares,
      price,
      order_type: "MARKET",
      product,
      intent,
      status: "EXECUTED",
      mode: "paper",
      created_at: now,
      timestamp: now,
      reason: "",
    };
    this.orders.push(order);
    this.trades.push({
      id: order.id,
      order_id: order.id,
      instrument_key: instrumentKey,
      symbol: opt.symbol,
      side,
      buy_sell: side,
      qty: shares,
      quantity: shares,
      price,
      executed_at: now,
      timestamp: now,
    });

    return order;
  }

  squareOffAll() {
    const fills = [];
    for (const pos of [...this.positions.values()]) {
      if (!pos.qty) continue;
      const side = pos.qty > 0 ? "S" : "B";
      fills.push(this.placeOrder({
        instrumentKey: pos.instrumentKey,
        side,
        qty: Math.abs(pos.qty),
        product: pos.product || "MIS",
        intent: "EXIT",
      }));
    }
    return fills;
  }

  reset() {
    this.positions.clear();
    this.orders = [];
    this.trades = [];
    this.realised = 0;
    this.cash = this.startingCash;
  }
}
