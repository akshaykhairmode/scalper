import * as Patterns from "./chart/patterns";
import * as Indicators from "./chart/indicators";
import { calculateFibLevels } from "./chart/fibonacci";
import { buildVolumeProfile, suggestVolumeRanges } from "./chart/volumeProfile";

// Minimum gap between volume-profile rebuilds while ticks stream in.
const VOLUME_PROFILE_REFRESH_MS = 5000;

const chartColors = (element) => {
  const styles = getComputedStyle(element);
  const color = (name, fallback) => styles.getPropertyValue(name).trim() || fallback;
  return {
    bg: color("--chart-bg", "#07090c"),
    grid: color("--chart-grid", "#141c24"),
    text: color("--chart-text", "#8292a2"),
    crosshair: color("--chart-crosshair", "#8293a3"),
    up: color("--green", "#35c46a"),
    down: color("--red", "#ff5d5d"),
    border: color("--line", "#24303a"),
    resistance: color("--chart-res", "#f05a5a"),
    support: color("--chart-sup", "#29b360"),
    accent: color("--accent", "#f0b429"),
  };
};

const toChartTimestamp = (time) => (time > 1e11 ? time : time * 1000);

const istDayFormatter = new Intl.DateTimeFormat("en-GB", {
  timeZone: "Asia/Kolkata", year: "numeric", month: "2-digit", day: "2-digit",
});
const istDayKey = (time) => {
  const parts = istDayFormatter.formatToParts(new Date(toChartTimestamp(time)));
  const pick = (type) => parts.find((part) => part.type === type)?.value || "";
  return `${pick("year")}-${pick("month")}-${pick("day")}`;
};

const formatVolume = (val) => {
  if (!Number.isFinite(val) || val <= 0) return "0";
  if (val >= 1e7) return `${(val / 1e7).toFixed(2)}Cr`;
  if (val >= 1e5) return `${(val / 1e5).toFixed(2)}L`;
  if (val >= 1e3) return `${(val / 1e3).toFixed(1)}K`;
  return String(Math.round(val));
};

function calculateSRLevels(candles, maxLevels = 3) {
  if (!Array.isArray(candles) || candles.length < 5) {
    return { supports: [], resistances: [] };
  }
  const currentPrice = candles[candles.length - 1].close;
  const highs = [];
  const lows = [];
  const windowSize = 2;

  for (let i = windowSize; i < candles.length - windowSize; i++) {
    const c = candles[i];
    let isHigh = true;
    let isLow = true;
    for (let j = i - windowSize; j <= i + windowSize; j++) {
      if (j === i) continue;
      if (candles[j].high > c.high) isHigh = false;
      if (candles[j].low < c.low) isLow = false;
    }
    if (isHigh) highs.push({ price: c.high, time: c.time });
    if (isLow) lows.push({ price: c.low, time: c.time });
  }

  function cluster(points) {
    if (!points.length) return [];
    const sorted = [...points].sort((a, b) => a.price - b.price);
    const clusters = [];
    for (const p of sorted) {
      const match = clusters.find((cl) => Math.abs(cl.avg - p.price) / cl.avg < 0.006);
      if (match) {
        match.prices.push(p.price);
        match.count += 1;
        match.avg = match.prices.reduce((a, b) => a + b, 0) / match.prices.length;
        match.latestTime = Math.max(match.latestTime, p.time);
      } else {
        clusters.push({ avg: p.price, prices: [p.price], count: 1, latestTime: p.time });
      }
    }
    return clusters;
  }

  const resClusters = cluster(highs.filter((h) => h.price > currentPrice));
  const supClusters = cluster(lows.filter((l) => l.price < currentPrice));

  resClusters.sort((a, b) => a.avg - b.avg);
  supClusters.sort((a, b) => b.avg - a.avg);

  return {
    resistances: resClusters.slice(0, maxLevels).map((c) => Number(c.avg.toFixed(2))),
    supports: supClusters.slice(0, maxLevels).map((c) => Number(c.avg.toFixed(2))),
  };
}

export function calculateAutoTrendlines(candles, maxLookback = 120) {
  if (!Array.isArray(candles) || candles.length < 15) {
    return { resistance: null, support: null };
  }
  const slice = candles.length > maxLookback ? candles.slice(-maxLookback) : candles;
  const n = slice.length;
  const lastIndex = n - 1;
  const currentPrice = slice[lastIndex].close;
  if (!Number.isFinite(currentPrice) || currentPrice <= 0) {
    return { resistance: null, support: null };
  }

  const windowSize = 2;
  const highs = [];
  const lows = [];

  for (let i = windowSize; i < n - windowSize; i++) {
    const c = slice[i];
    let isHigh = true;
    let isLow = true;
    for (let j = i - windowSize; j <= i + windowSize; j++) {
      if (j === i) continue;
      if (slice[j].high > c.high) isHigh = false;
      if (slice[j].low < c.low) isLow = false;
    }
    if (isHigh) highs.push({ index: i, price: c.high, time: c.time });
    if (isLow) lows.push({ index: i, price: c.low, time: c.time });
  }

  const touchTol = Math.max(0.0006, Math.min(0.006, 1.2 / currentPrice));
  const breachTol = touchTol * 1.6;

  // Resistance: Connect prominent swing highs
  let bestResistance = null;
  let bestResScore = -Infinity;

  for (let a = 0; a < highs.length; a++) {
    for (let b = a + 1; b < highs.length; b++) {
      const p1 = highs[a];
      const p2 = highs[b];
      const span = p2.index - p1.index;
      if (span < 5) continue;

      const slope = (p2.price - p1.price) / span;
      const lastLinePrice = p1.price + slope * (lastIndex - p1.index);

      // Resistance must remain at or above current prices
      if (lastLinePrice < currentPrice * (1 - breachTol * 2)) continue;
      // Not excessively far above current price (keep relevant)
      if (lastLinePrice > currentPrice * 1.25) continue;

      let breaches = 0;
      let wickBreaches = 0;
      let extraTouches = 0;
      let lastTouchIdx = -999;

      for (let k = p1.index; k <= lastIndex; k++) {
        if (k === p1.index || k === p2.index) continue;
        const linePx = p1.price + slope * (k - p1.index);
        const bar = slice[k];

        if (bar.close > linePx * (1 + breachTol)) {
          breaches++;
          break;
        }
        if (bar.high > linePx * (1 + breachTol * 2)) {
          wickBreaches++;
          break;
        }
        if (Math.abs(bar.high - linePx) / linePx <= touchTol && bar.close <= linePx * (1 + touchTol * 0.5)) {
          if (k - lastTouchIdx >= 3) {
            extraTouches++;
            lastTouchIdx = k;
          }
        }
      }

      if (breaches > 0 || wickBreaches > 0) continue;

      const totalTouches = 2 + extraTouches;
      const recency = p2.index / lastIndex;
      const distPct = Math.abs(lastLinePrice - currentPrice) / currentPrice;
      const score = (totalTouches * 20) + (span * 0.3) + (recency * 20) - (distPct * 60);

      if (score > bestResScore) {
        bestResScore = score;
        bestResistance = {
          p1: { time: p1.time, price: p1.price },
          p2: { time: p2.time, price: p2.price },
          span,
          touches: totalTouches,
          score: Number(score.toFixed(1)),
          slope: Number(slope.toFixed(4)),
          projectedPrice: Number(lastLinePrice.toFixed(2)),
        };
      }
    }
  }

  // Support: Connect prominent swing lows
  let bestSupport = null;
  let bestSupScore = -Infinity;

  for (let a = 0; a < lows.length; a++) {
    for (let b = a + 1; b < lows.length; b++) {
      const p1 = lows[a];
      const p2 = lows[b];
      const span = p2.index - p1.index;
      if (span < 5) continue;

      const slope = (p2.price - p1.price) / span;
      const lastLinePrice = p1.price + slope * (lastIndex - p1.index);

      // Support must remain at or below current prices
      if (lastLinePrice > currentPrice * (1 + breachTol * 2)) continue;
      // Not excessively far below current price
      if (lastLinePrice < currentPrice * 0.75) continue;

      let breaches = 0;
      let wickBreaches = 0;
      let extraTouches = 0;
      let lastTouchIdx = -999;

      for (let k = p1.index; k <= lastIndex; k++) {
        if (k === p1.index || k === p2.index) continue;
        const linePx = p1.price + slope * (k - p1.index);
        const bar = slice[k];

        if (bar.close < linePx * (1 - breachTol)) {
          breaches++;
          break;
        }
        if (bar.low < linePx * (1 - breachTol * 2)) {
          wickBreaches++;
          break;
        }
        if (Math.abs(bar.low - linePx) / linePx <= touchTol && bar.close >= linePx * (1 - touchTol * 0.5)) {
          if (k - lastTouchIdx >= 3) {
            extraTouches++;
            lastTouchIdx = k;
          }
        }
      }

      if (breaches > 0 || wickBreaches > 0) continue;

      const totalTouches = 2 + extraTouches;
      const recency = p2.index / lastIndex;
      const distPct = Math.abs(currentPrice - lastLinePrice) / currentPrice;
      const score = (totalTouches * 20) + (span * 0.3) + (recency * 20) - (distPct * 60);

      if (score > bestSupScore) {
        bestSupScore = score;
        bestSupport = {
          p1: { time: p1.time, price: p1.price },
          p2: { time: p2.time, price: p2.price },
          span,
          touches: totalTouches,
          score: Number(score.toFixed(1)),
          slope: Number(slope.toFixed(4)),
          projectedPrice: Number(lastLinePrice.toFixed(2)),
        };
      }
    }
  }

  return { resistance: bestResistance, support: bestSupport };
}

function buildStyles(colors) {
  return {
    grid: {
      show: true,
      horizontal: { show: true, size: 1, color: colors.grid, style: "dashed", dashedValue: [2, 2] },
      vertical: { show: true, size: 1, color: colors.grid, style: "dashed", dashedValue: [2, 2] },
    },
    candle: {
      type: "candle_solid",
      bar: {
        upColor: colors.up,
        downColor: colors.down,
        noChangeColor: colors.text,
        upBorderColor: colors.up,
        downBorderColor: colors.down,
        noChangeBorderColor: colors.text,
        upWickColor: colors.up,
        downWickColor: colors.down,
        noChangeWickColor: colors.text,
      },
      priceMark: {
        show: true,
        high: { show: true, color: colors.text, textSize: 10 },
        low: { show: true, color: colors.text, textSize: 10 },
        last: {
          show: true,
          upColor: colors.up,
          downColor: colors.down,
          noChangeColor: colors.text,
          line: { show: true, style: "dashed", dashedValue: [4, 4], size: 1, color: colors.crosshair },
          text: { show: true, color: "#ffffff", backgroundColor: colors.up, size: 11 },
        },
      },
      tooltip: {
        showRule: "follow_cross",
        showType: "standard",
        text: { size: 11, color: colors.text },
      },
    },
    indicator: {
      lastValueMark: {
        show: true,
        text: {
          show: true,
          style: "fill",
          color: "#ffffff",
          size: 11,
          weight: "bold",
          paddingLeft: 4,
          paddingTop: 3,
          paddingRight: 4,
          paddingBottom: 3,
          borderRadius: 2,
        },
      },
      lines: [
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#e16a2b" },
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#2196f3" },
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#9c27b0" },
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#00b4d8" },
      ],
      bars: [
        {
          style: "fill",
          upColor: `${colors.up}80`,
          downColor: `${colors.down}80`,
          noChangeColor: `${colors.text}80`,
        },
      ],
      tooltip: {
        showRule: "always",
        showType: "standard",
        title: {
          show: true,
          showName: true,
          showParams: true,
          size: 11,
          color: colors.text,
          marginLeft: 8,
          marginTop: 4,
          marginRight: 8,
          marginBottom: 4,
        },
        legend: {
          size: 11,
          color: colors.text,
          marginLeft: 8,
          marginTop: 4,
          marginRight: 8,
          marginBottom: 4,
        },
      },
    },
    xAxis: {
      axisLine: { show: true, size: 1, color: colors.border },
      tickLine: { show: true, size: 1, color: colors.border },
      tickText: { show: true, color: colors.text, size: 11 },
    },
    yAxis: {
      axisLine: { show: true, size: 1, color: colors.border },
      tickLine: { show: true, size: 1, color: colors.border },
      tickText: { show: true, color: colors.text, size: 11 },
    },
    separator: { size: 1, color: colors.border },
    crosshair: {
      show: true,
      horizontal: {
        show: true,
        line: { show: true, style: "dashed", dashedValue: [4, 2], size: 1, color: colors.crosshair },
        text: { show: true, color: "#ffffff", backgroundColor: colors.crosshair, size: 11 },
      },
      vertical: {
        show: true,
        line: { show: true, style: "dashed", dashedValue: [4, 2], size: 1, color: colors.crosshair },
        text: { show: true, color: "#ffffff", backgroundColor: colors.crosshair, size: 11 },
      },
    },
    overlay: {
      line: { color: colors.crosshair, size: 1.5 },
      point: { color: colors.up, radius: 4 },
    },
  };
}

export function parseTimestamp(ts) {
  if (!ts) return 0;
  if (typeof ts === "number") return ts < 1e11 ? ts * 1000 : ts;
  const numVal = Number(ts);
  if (Number.isFinite(numVal) && numVal > 0) return numVal < 1e11 ? numVal * 1000 : numVal;
  const str = String(ts).trim();
  const m = str.match(/^(\d{1,2})[-\/]([A-Za-z]{3}|\d{1,2})[-\/](\d{4})(?:\s+(\d{1,2}):(\d{2})(?::(\d{2}))?)?/);
  if (m) {
    const months: Record<string, number> = { jan: 0, feb: 1, mar: 2, apr: 3, may: 4, jun: 5, jul: 6, aug: 7, sep: 8, oct: 9, nov: 10, dec: 11 };
    const day = parseInt(m[1], 10);
    const mon = isNaN(Number(m[2])) ? (months[m[2].toLowerCase()] ?? 0) : parseInt(m[2], 10) - 1;
    const year = parseInt(m[3], 10);
    const hour = parseInt(m[4] || "0", 10);
    const min = parseInt(m[5] || "0", 10);
    const sec = parseInt(m[6] || "0", 10);
    return new Date(year, mon, day, hour, min, sec).getTime();
  }
  const d = new Date(str);
  return Number.isNaN(d.getTime()) ? 0 : d.getTime();
}

export function pairTrades(trades) {
  if (!Array.isArray(trades) || trades.length < 2) return [];
  const sorted = [...trades].sort((a, b) => parseTimestamp(a.timestamp) - parseTimestamp(b.timestamp));
  const pairs = [];
  const openLots = [];

  for (const t of sorted) {
    const isBuy = t.side === "B" || t.side === "BUY";
    let curQty = Math.abs(Number(t.quantity || 0));
    if (curQty <= 0) continue;

    while (curQty > 0 && openLots.length > 0) {
      const opp = openLots[0];
      const oppIsBuy = opp.trade.side === "B" || opp.trade.side === "BUY";
      if (oppIsBuy === isBuy) break;

      const matchQty = Math.min(curQty, opp.remainingQty);
      const entry = opp.trade;
      const exit = t;
      const entryPx = Number(entry.price || 0);
      const exitPx = Number(exit.price || 0);
      const pnl = oppIsBuy ? (exitPx - entryPx) * matchQty : (entryPx - exitPx) * matchQty;
      const pnlPct = entryPx > 0 ? (oppIsBuy ? ((exitPx - entryPx) / entryPx) * 100 : ((entryPx - exitPx) / entryPx) * 100) : 0;

      pairs.push({
        entry,
        exit,
        qty: matchQty,
        pnl,
        pnlPct,
      });

      curQty -= matchQty;
      opp.remainingQty -= matchQty;
      if (opp.remainingQty <= 0) openLots.shift();
    }

    if (curQty > 0) {
      openLots.push({ trade: t, remainingQty: curQty });
    }
  }
  return pairs;
}

function findClosestCandle(candles, timestampMs) {
  if (!Array.isArray(candles) || !candles.length) return null;
  const tsSec = timestampMs > 1e11 ? Math.floor(timestampMs / 1000) : timestampMs;
  let best = candles[0];
  let minDiff = Math.abs(candles[0].time - tsSec);
  for (let i = 1; i < candles.length; i++) {
    const diff = Math.abs(candles[i].time - tsSec);
    if (diff < minDiff) {
      minDiff = diff;
      best = candles[i];
    } else if (diff > minDiff && candles[i].time > tsSec) {
      break;
    }
  }
  return best;
}

export function formatDOMQty(q: number | string): string {
  const n = Number(q || 0);
  if (n <= 0) return "";
  if (n >= 1e7) return `${(n / 1e7).toFixed(1)}Cr`;
  if (n >= 1e5) return `${(n / 1e5).toFixed(1)}L`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return String(Math.round(n));
}

export function computeDOMInterval(minPrice: number, maxPrice: number, heightPx = 500): number {
  const span = maxPrice - minPrice;
  const isSpotOrLarge = minPrice > 5000;
  if (!Number.isFinite(span) || span <= 0) return isSpotOrLarge ? 10 : 5;

  const desiredRows = Math.max(6, Math.min(20, Math.floor(heightPx / 28)));
  const rawStep = span / desiredRows;

  const candidateSteps = isSpotOrLarge
    ? [5, 10, 20, 25, 50, 100, 200, 500]
    : [0.5, 1, 2, 2.5, 5, 10, 20, 25, 50, 100];

  for (const step of candidateSteps) {
    if (step >= rawStep) {
      return step;
    }
  }
  return candidateSteps[candidateSteps.length - 1];
}

export function calculateOrderFlowBuckets(candles: any[], depthData: any, chainData: any[], minPrice: number, maxPrice: number, step: number) {
  const buckets = new Map();

  const startP = Math.floor((minPrice - step) / step) * step;
  const endP = Math.ceil((maxPrice + step) / step) * step;

  for (let p = startP; p <= endP + 1e-6; p = Math.round((p + step) * 100) / 100) {
    const key = Math.round(p * 100) / 100;
    buckets.set(key, {
      price: key,
      buyVol: 0,
      sellVol: 0,
      buyOrders: 0,
      sellOrders: 0,
    });
  }

  const recent = Array.isArray(candles) ? candles.slice(-80) : [];
  for (const c of recent) {
    const low = Number(c.low);
    const high = Number(c.high);
    const open = Number(c.open);
    const close = Number(c.close);
    const vol = Number(c.volume || 0);

    if (!low || !high || high <= 0 || low <= 0) continue;
    const range = Math.max(0.01, high - low);

    let buyRatio = 0.5;
    if (high > low) {
      buyRatio = Math.max(0.1, Math.min(0.9, (close - low) / range));
    } else if (close > open) {
      buyRatio = 0.75;
    } else if (close < open) {
      buyRatio = 0.25;
    }

    const cBuyVol = vol > 0 ? vol * buyRatio : (buyRatio >= 0.5 ? Math.round(buyRatio * 25) : Math.round(buyRatio * 8));
    const cSellVol = vol > 0 ? vol * (1 - buyRatio) : (buyRatio < 0.5 ? Math.round((1 - buyRatio) * 25) : Math.round((1 - buyRatio) * 8));
    const cBuyOrd = buyRatio >= 0.5 ? Math.max(1, Math.round(buyRatio * 6)) : 1;
    const cSellOrd = (1 - buyRatio) >= 0.5 ? Math.max(1, Math.round((1 - buyRatio) * 6)) : 1;

    const bStart = Math.floor(low / step) * step;
    const bEnd = Math.ceil(high / step) * step;

    for (let p = bStart; p <= bEnd + 1e-6; p = Math.round((p + step) * 100) / 100) {
      const bKey = Math.round(p * 100) / 100;
      const b = buckets.get(bKey);
      if (!b) continue;

      const overlapLow = Math.max(low, p - step / 2);
      const overlapHigh = Math.min(high, p + step / 2);
      const overlapLen = Math.max(0, overlapHigh - overlapLow);
      const weight = overlapLen / range;

      if (weight > 0) {
        b.buyVol += cBuyVol * weight;
        b.sellVol += cSellVol * weight;
        b.buyOrders += cBuyOrd * weight;
        b.sellOrders += cSellOrd * weight;
      }
    }
  }

  if (Array.isArray(chainData) && chainData.length) {
    for (const row of chainData) {
      const strike = Number(row.strike || 0);
      if (strike <= 0) continue;
      const bKey = Math.round(Math.round(strike / step) * step * 100) / 100;
      const b = buckets.get(bKey);
      if (!b) continue;

      const callVol = Number(row.call?.volume || 0);
      const putVol = Number(row.put?.volume || 0);
      const callOI = Number(row.call?.oi || 0);
      const putOI = Number(row.put?.oi || 0);

      const spotBull = callVol + (putOI * 0.1);
      const spotBear = putVol + (callOI * 0.1);

      if (spotBull > 0) {
        b.buyVol += spotBull * 0.2;
        b.buyOrders += Math.max(1, Math.round(spotBull / 500));
      }
      if (spotBear > 0) {
        b.sellVol += spotBear * 0.2;
        b.sellOrders += Math.max(1, Math.round(spotBear / 500));
      }
    }
  }

  if (depthData) {
    const bids = Array.isArray(depthData.bids) ? depthData.bids : [];
    const asks = Array.isArray(depthData.asks) ? depthData.asks : [];

    for (const bid of bids) {
      const px = Number(bid.price || 0);
      const qty = Number(bid.quantity || 0);
      const ord = Number(bid.orders || 1);
      if (px <= 0 || qty <= 0) continue;
      const bKey = Math.round(Math.round(px / step) * step * 100) / 100;
      let b = buckets.get(bKey);
      if (!b && bKey >= startP && bKey <= endP) {
        b = { price: bKey, buyVol: 0, sellVol: 0, buyOrders: 0, sellOrders: 0 };
        buckets.set(bKey, b);
      }
      if (b) {
        b.buyVol += qty;
        b.buyOrders += ord;
      }
    }

    for (const ask of asks) {
      const px = Number(ask.price || 0);
      const qty = Number(ask.quantity || 0);
      const ord = Number(ask.orders || 1);
      if (px <= 0 || qty <= 0) continue;
      const bKey = Math.round(Math.round(px / step) * step * 100) / 100;
      let b = buckets.get(bKey);
      if (!b && bKey >= startP && bKey <= endP) {
        b = { price: bKey, buyVol: 0, sellVol: 0, buyOrders: 0, sellOrders: 0 };
        buckets.set(bKey, b);
      }
      if (b) {
        b.sellVol += qty;
        b.sellOrders += ord;
      }
    }
  }

  const result = [];
  let maxVol = 1;
  for (const b of buckets.values()) {
    b.buyVol = Math.round(b.buyVol);
    b.sellVol = Math.round(b.sellVol);
    b.buyOrders = Math.round(b.buyOrders);
    b.sellOrders = Math.round(b.sellOrders);
    const total = Math.max(b.buyVol, b.sellVol);
    if (total > maxVol) maxVol = total;
    if (b.buyVol > 0 || b.sellVol > 0) {
      result.push(b);
    }
  }

  return { buckets: result, maxVol, step };
}

let overlaysRegistered = false;
function registerCustomOverlays() {
  const kc = window.klinecharts;
  if (!kc || overlaysRegistered) return;
  overlaysRegistered = true;

  // Vertical marker at each trading-day boundary. Bars are spaced by index, so
  // a new day is otherwise only visible in the x-axis labels.
  kc.registerOverlay({
    name: "dayDividerLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.x)) return [];
      const data = overlay.extendData || {};
      const color = data.color || "#536a7d";
      const height = bounding?.height || 400;
      return [{
        type: "line",
        attrs: { coordinates: [{ x: coord.x, y: 0 }, { x: coord.x, y: height }] },
        styles: { style: "dashed", dashedValue: [2, 4], size: 1, color },
        ignoreEvent: true,
      }];
    },
  });

  kc.registerOverlay({
    name: "tradeMarker",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.x) || !Number.isFinite(coord.y)) return [];

      const data = overlay.extendData || {};
      const isBuy = data.side === "B" || data.side === "BUY" || data.isBull;
      const isSpot = Boolean(data.isSpot);

      const color = isBuy ? "#29b360" : "#f05a5a";
      const figures = [];

      if (isSpot) {
        const arrowUp = isBuy;
        const tipY = arrowUp ? coord.y + 4 : coord.y - 4;
        const baseY = arrowUp ? coord.y + 12 : coord.y - 12;
        const textY = arrowUp ? coord.y + 14 : coord.y - 14;
        const baseline = arrowUp ? "top" : "bottom";

        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: tipY },
              { x: coord.x - 5, y: baseY },
              { x: coord.x + 5, y: baseY },
            ],
          },
          styles: { style: "fill", color },
          ignoreEvent: true,
        });

        const label = data.text || (arrowUp ? "▲ BUY" : "▼ SELL");
        figures.push({
          type: "text",
          attrs: {
            x: coord.x,
            y: textY,
            text: label,
            align: "center",
            baseline,
          },
          styles: {
            color: "#ffffff",
            backgroundColor: color,
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 5,
            paddingRight: 5,
            paddingTop: 1.5,
            paddingBottom: 1.5,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      } else {
        const tipY = isBuy ? coord.y + 3 : coord.y - 3;
        const baseY = isBuy ? coord.y + 11 : coord.y - 11;
        const textY = isBuy ? coord.y + 13 : coord.y - 13;
        const baseline = isBuy ? "top" : "bottom";

        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: tipY },
              { x: coord.x - 4.5, y: baseY },
              { x: coord.x + 4.5, y: baseY },
            ],
          },
          styles: { style: "fill", color },
          ignoreEvent: true,
        });

        const sideStr = isBuy ? "BUY" : "SELL";
        const qtyStr = data.qty ? `${data.qty} @ ` : "";
        const priceStr = Number.isFinite(data.price) ? Number(data.price).toFixed(2) : "";
        const label = `${sideStr} ${qtyStr}${priceStr}`.trim();

        figures.push({
          type: "text",
          attrs: {
            x: coord.x,
            y: textY,
            text: label,
            align: "center",
            baseline,
          },
          styles: {
            color: "#ffffff",
            backgroundColor: color,
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 5,
            paddingRight: 5,
            paddingTop: 1.5,
            paddingBottom: 1.5,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "depthLevelBar",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const height = bounding?.height || 500;
      if (y < -20 || y > height + 20) return [];

      const width = bounding?.width || 800;
      const data = overlay.extendData || {};

      // ── Anchored Dual-Sided DOM Row (Fixed Interval) ──
      if (data.buyVol != null || data.sellVol != null) {
        const buyVol = Number(data.buyVol || 0);
        const sellVol = Number(data.sellVol || 0);
        const maxVol = Number(data.maxVol || Math.max(buyVol, sellVol, 1));

        const panelWidth = Math.min(240, Math.max(150, Math.round(width * 0.28)));
        const xRight = width - 4;
        const xLeft = xRight - panelWidth;
        const xCenter = Math.round(xLeft + panelWidth * 0.5);
        const halfWidth = Math.round((panelWidth - 14) * 0.48);

        const barHeight = 14;
        const yTop = y - barHeight / 2;
        const yBottom = y + barHeight / 2;

        const figures = [];

        // Background container pill for readability
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: xLeft, y: yTop },
              { x: xRight, y: yTop },
              { x: xRight, y: yBottom },
              { x: xLeft, y: yBottom },
            ],
          },
          styles: { style: "fill", color: "rgba(15, 23, 42, 0.45)" },
          ignoreEvent: true,
        });

        // Center balance line / tick
        figures.push({
          type: "line",
          attrs: {
            coordinates: [
              { x: xCenter, y: yTop - 1 },
              { x: xCenter, y: yBottom + 1 },
            ],
          },
          styles: { style: "solid", size: 1.5, color: "rgba(255, 255, 255, 0.22)" },
          ignoreEvent: true,
        });

        // 1. Buyers (Green bar on left of center)
        if (buyVol > 0) {
          const buyRatio = Math.max(0.05, Math.min(1.0, buyVol / maxVol));
          const buyBarW = Math.max(6, Math.round(buyRatio * halfWidth));
          const xBuyRight = xCenter - 3;
          const xBuyLeft = xBuyRight - buyBarW;

          figures.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: xBuyLeft, y: yTop },
                { x: xBuyRight, y: yTop },
                { x: xBuyRight, y: yBottom },
                { x: xBuyLeft, y: yBottom },
              ],
            },
            styles: { style: "fill", color: "rgba(34, 197, 94, 0.35)" },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: xBuyLeft, y: yTop },
                { x: xBuyLeft, y: yBottom },
              ],
            },
            styles: { style: "solid", size: 2.5, color: "#22c55e" },
            ignoreEvent: true,
          });

          const buyText = formatDOMQty(buyVol);
          figures.push({
            type: "text",
            attrs: {
              x: xBuyRight - 4,
              y,
              text: buyText,
              align: "right",
              baseline: "middle",
            },
            styles: {
              color: "#86efac",
              size: 9,
              weight: "bold",
              family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
            },
            ignoreEvent: true,
          });
        }

        // 2. Sellers (Red bar on right of center)
        if (sellVol > 0) {
          const sellRatio = Math.max(0.05, Math.min(1.0, sellVol / maxVol));
          const sellBarW = Math.max(6, Math.round(sellRatio * halfWidth));
          const xSellLeft = xCenter + 3;
          const xSellRight = xSellLeft + sellBarW;

          figures.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: xSellLeft, y: yTop },
                { x: xSellRight, y: yTop },
                { x: xSellRight, y: yBottom },
                { x: xSellLeft, y: yBottom },
              ],
            },
            styles: { style: "fill", color: "rgba(239, 68, 68, 0.35)" },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: xSellRight, y: yTop },
                { x: xSellRight, y: yBottom },
              ],
            },
            styles: { style: "solid", size: 2.5, color: "#ef4444" },
            ignoreEvent: true,
          });

          const sellText = formatDOMQty(sellVol);
          figures.push({
            type: "text",
            attrs: {
              x: xSellLeft + 4,
              y,
              text: sellText,
              align: "left",
              baseline: "middle",
            },
            styles: {
              color: "#fca5a5",
              size: 9,
              weight: "bold",
              family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
            },
            ignoreEvent: true,
          });
        }

        return figures;
      }

      // Single-sided fallback
      const isBuy = data.side === "BUY" || data.side === "B";
      const orders = Number(data.orders || 0);
      const qty = Number(data.quantity || 0);
      const maxQty = Number(data.maxQty || qty || 1);

      const baseColor = isBuy ? "#29b360" : "#f05a5a";
      const fillColor = isBuy ? "rgba(41, 179, 96, 0.35)" : "rgba(240, 90, 90, 0.35)";
      const barHeight = 16;

      const ratio = Math.max(0.08, Math.min(1.0, qty / (maxQty || 1)));
      const maxBarWidth = Math.min(220, Math.max(90, width * 0.26));
      const barWidth = Math.round(ratio * maxBarWidth);

      const x2 = width - 4;
      const x1 = x2 - barWidth;
      const yTop = y - barHeight / 2;
      const yBottom = y + barHeight / 2;

      const figures = [];

      figures.push({
        type: "polygon",
        attrs: {
          coordinates: [
            { x: x1, y: yTop },
            { x: x2, y: yTop },
            { x: x2, y: yBottom },
            { x: x1, y: yBottom },
          ],
        },
        styles: { style: "fill", color: fillColor },
        ignoreEvent: true,
      });

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: x1, y: yTop },
            { x: x1, y: yBottom },
          ],
        },
        styles: { style: "solid", size: 2.5, color: baseColor },
        ignoreEvent: true,
      });

      const orderLabel = orders > 0 ? `${orders}o · ` : "";
      const qtyLabel = formatDOMQty(qty) || String(qty);
      const label = `${orderLabel}${qtyLabel}`;

      figures.push({
        type: "text",
        attrs: {
          x: x2 - 5,
          y,
          text: label,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: isBuy ? "rgba(16, 80, 42, 0.88)" : "rgba(127, 29, 29, 0.88)",
          paddingLeft: 4,
          paddingRight: 4,
          paddingTop: 1,
          paddingBottom: 1,
          borderRadius: 2,
          size: 9,
          weight: "bold",
          family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "positionLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const pnl = Number(data.pnl || 0);
      const isProfit = pnl >= 0;
      const lineColor = isProfit ? "#29b360" : "#f05a5a";
      const width = bounding?.width || 800;

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: 0, y },
            { x: width, y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [5, 4],
          size: 1.5,
          color: lineColor,
        },
        ignoreEvent: true,
      });

      const qty = Number(data.qty || 0);
      const qtySign = qty > 0 ? "+" : "";
      const pnlSign = pnl >= 0 ? "+₹" : "-₹";
      const pnlVal = Math.abs(Math.round(pnl)).toLocaleString("en-IN");
      const avgPrice = Number(data.avgPrice || 0).toFixed(2);
      const tagText = `POS: ${qtySign}${qty} @ ${avgPrice} (${pnlSign}${pnlVal})`;

      figures.push({
        type: "text",
        attrs: {
          x: width - 8,
          y,
          text: tagText,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: lineColor,
          size: 10,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 6,
          paddingRight: 6,
          paddingTop: 2,
          paddingBottom: 2,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "tradeConnector",
    totalStep: 2,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || coordinates.length < 2) return [];
      const c1 = coordinates[0];
      const c2 = coordinates[1];
      if (!c1 || !c2 || !Number.isFinite(c1.x) || !Number.isFinite(c1.y) ||
          !Number.isFinite(c2.x) || !Number.isFinite(c2.y)) return [];

      const data = overlay.extendData || {};
      const pnl = Number(data.pnl || 0);
      const isProfit = pnl >= 0;
      const color = isProfit ? "#29b36099" : "#f05a5a99";
      const midX = (c1.x + c2.x) / 2;
      const midY = (c1.y + c2.y) / 2;

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: c1.x, y: c1.y },
            { x: c2.x, y: c2.y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [3, 3],
          size: 1.2,
          color,
        },
        ignoreEvent: true,
      });

      const pnlSign = pnl >= 0 ? "+₹" : "-₹";
      const pnlVal = Math.abs(Math.round(pnl)).toLocaleString("en-IN");
      const pctVal = Number.isFinite(data.pnlPct) ? ` (${data.pnlPct >= 0 ? "+" : ""}${data.pnlPct.toFixed(1)}%)` : "";
      const pillText = `${pnlSign}${pnlVal}${pctVal}`;

      figures.push({
        type: "text",
        attrs: {
          x: midX,
          y: midY,
          text: pillText,
          align: "center",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: isProfit ? "#1e824c" : "#b03a2e",
          size: 9.5,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 4,
          paddingRight: 4,
          paddingTop: 1.5,
          paddingBottom: 1.5,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "orderPreviewLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const price = Number(data.price || overlay.points?.[0]?.value || 0);
      const width = bounding?.width || 800;
      const accent = "#f0b429";

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: 0, y },
            { x: width, y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [5, 3],
          size: 1.5,
          color: accent,
        },
        ignoreEvent: true,
      });

      figures.push({
        type: "text",
        attrs: {
          x: width - 8,
          y,
          text: `LIMIT: ₹${price.toFixed(2)}`,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#000000",
          backgroundColor: accent,
          size: 10,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 6,
          paddingRight: 6,
          paddingTop: 2,
          paddingBottom: 2,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "maxPainLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const price = Number(data.price || overlay.points?.[0]?.value || 0);
      const width = bounding?.width || 800;
      const color = data.color || "#c084fc";
      return [
        {
          type: "line",
          attrs: { coordinates: [{ x: 0, y }, { x: width, y }] },
          styles: { style: "dashed", dashedValue: [6, 4], size: 1.2, color },
          ignoreEvent: true,
        },
        {
          type: "text",
          attrs: {
            x: width - 8,
            y,
            text: `MP: ${Math.round(price).toLocaleString("en-IN")}`,
            align: "right",
            baseline: "middle",
          },
          styles: {
            color: "#0b0710",
            backgroundColor: color,
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 6,
            paddingRight: 6,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3,
          },
          ignoreEvent: true,
        },
      ];
    },
  });

  kc.registerOverlay({
    name: "volumeProfile",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding, yAxis }) {
      const data = overlay.extendData || {};
      const nodes = Array.isArray(data.nodes) ? data.nodes : [];
      const priceHigh = Number(data.priceHigh);
      const priceLow = Number(data.priceLow);
      if (!nodes.length || !coordinates || coordinates.length < 2) return [];
      if (!Number.isFinite(priceHigh) || !Number.isFinite(priceLow) || priceHigh <= priceLow) return [];
      const top = coordinates[0];
      const bottom = coordinates[1];
      if (!Number.isFinite(top?.y) || !Number.isFinite(bottom?.y)) return [];

      const width = bounding?.width || 800;
      const span = priceHigh - priceLow;
      // Prefer the axis conversion: it stays correct on log/percentage scales.
      // The linear interpolation between the two anchor points is the fallback.
      const yOf = (price) => {
        const value = Number(price);
        if (!Number.isFinite(value)) return NaN;
        if (yAxis && typeof yAxis.convertToPixel === "function") {
          const pixel = Number(yAxis.convertToPixel(value));
          if (Number.isFinite(pixel)) return pixel;
        }
        return top.y + ((priceHigh - value) / span) * (bottom.y - top.y);
      };
      const palette = data.colors || {};
      const figures = [];

      // Zones first so the histogram bars sit on top of them. When the profile
      // says "wait", keep the shelves visible but barely tinted so they do not
      // read as an entry signal.
      const waiting = data.bias === "none";
      const band = (range, color, dimColor) => {
        if (!range) return;
        const y1 = yOf(Number(range.high));
        const y2 = yOf(Number(range.low));
        if (!Number.isFinite(y1) || !Number.isFinite(y2)) return;
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: 0, y: y1 },
              { x: width, y: y1 },
              { x: width, y: y2 },
              { x: 0, y: y2 },
            ],
          },
          styles: {
            style: "fill",
            color: waiting ? dimColor : color,
            borderColor: "transparent",
            borderSize: 0,
          },
          ignoreEvent: true,
        });
      };
      band(data.buy, "rgba(41, 179, 96, 0.12)", "rgba(41, 179, 96, 0.04)");
      band(data.sell, "rgba(240, 90, 90, 0.12)", "rgba(240, 90, 90, 0.04)");

      let maxVolume = 0;
      for (const node of nodes) {
        if (Number(node?.volume) > maxVolume) maxVolume = Number(node.volume);
      }
      const maxBar = Math.max(20, width * 0.16);
      if (maxVolume > 0) {
        for (const node of nodes) {
          const volume = Number(node?.volume);
          if (!(volume > 0)) continue;
          const y1 = yOf(Number(node.high));
          const y2 = yOf(Number(node.low));
          if (!Number.isFinite(y1) || !Number.isFinite(y2)) continue;
          const barWidth = Math.max(1, (volume / maxVolume) * maxBar);
          const rising = Number(node.buy) >= Number(node.sell);
          figures.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: width - barWidth, y: y1 },
                { x: width, y: y1 },
                { x: width, y: y2 },
                { x: width - barWidth, y: y2 },
              ],
            },
            styles: {
              style: "fill",
              color: rising ? "rgba(53, 196, 106, 0.30)" : "rgba(255, 93, 93, 0.30)",
              borderColor: "transparent",
              borderSize: 0,
            },
            ignoreEvent: true,
          });
        }
      }

      const levelColor = palette.valueArea || "rgba(148, 163, 184, 0.7)";
      const pocColor = palette.poc || "rgba(240, 180, 41, 0.95)";
      const level = (price, color, dashed, size) => {
        const value = Number(price);
        if (!Number.isFinite(value)) return;
        const y = yOf(value);
        figures.push({
          type: "line",
          attrs: { coordinates: [{ x: 0, y }, { x: width, y }] },
          styles: {
            style: dashed ? "dashed" : "solid",
            dashedValue: [5, 4],
            size,
            color,
          },
          ignoreEvent: true,
        });
      };
      level(data.val, levelColor, true, 1);
      level(data.vah, levelColor, true, 1);
      level(data.poc, pocColor, false, 1.4);

      const label = (price, text, color) => {
        const value = Number(price);
        if (!Number.isFinite(value)) return;
        figures.push({
          type: "text",
          attrs: { x: 8, y: yOf(value) - 5, text, align: "left", baseline: "bottom" },
          styles: {
            color,
            size: 9,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
          },
          ignoreEvent: true,
        });
      };
      label(data.poc, "POC", pocColor);
      label(data.vah, "VAH", levelColor);
      label(data.val, "VAL", levelColor);
      return figures;
    },
  });

  kc.registerOverlay({
    name: "fibRetracement",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const data = overlay.extendData || {};
      const levels = Array.isArray(data.levels) ? data.levels : [];
      if (!levels.length) return [];
      const width = bounding?.width || 800;
      const figures = [];
      const rows = [];
      for (let i = 0; i < levels.length; i++) {
        const level = levels[i];
        const coord = coordinates[i];
        if (!coord || !Number.isFinite(coord.y) || !Number.isFinite(level?.price)) continue;
        rows.push({ level, y: coord.y, labelY: coord.y });
      }
      const ordered = [...rows].sort((a, b) => a.y - b.y);
      let lastLabelY = -Infinity;
      for (const row of ordered) {
        row.labelY = Math.max(row.y, lastLabelY + 11);
        lastLabelY = row.labelY;
      }

      for (const row of rows) {
        const { level, y, labelY } = row;
        const color = level.color || "#2dd4bf";
        figures.push({
          type: "line",
          attrs: { coordinates: [{ x: 0, y }, { x: width, y }] },
          styles: {
            style: level.golden ? "solid" : "dashed",
            dashedValue: [4, 3],
            size: level.golden ? 1.4 : 1,
            color,
          },
          ignoreEvent: true,
        });
        figures.push({
          type: "text",
          attrs: {
            x: width - 8,
            y: labelY,
            text: `${level.label}  ${Math.round(level.price).toLocaleString("en-IN")}`,
            align: "right",
            baseline: "middle",
          },
          styles: {
            color: "#041014",
            backgroundColor: color,
            size: 9,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 5,
            paddingRight: 5,
            paddingTop: 1.5,
            paddingBottom: 1.5,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }
      return figures;
    },
  });

  kc.registerOverlay({
    name: "targetPreviewLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const price = Number(data.price || overlay.points?.[0]?.value || 0);
      const width = bounding?.width || 800;
      const green = "#29b360";

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: 0, y },
            { x: width, y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [4, 3],
          size: 1.5,
          color: green,
        },
        ignoreEvent: true,
      });

      const label = data.label || `TARGET: ₹${price.toFixed(2)}`;
      figures.push({
        type: "text",
        attrs: {
          x: width - 8,
          y,
          text: label,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: "#1e7e44",
          size: 10,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 6,
          paddingRight: 6,
          paddingTop: 2,
          paddingBottom: 2,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "bracketLevelLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const price = Number(data.price || overlay.points?.[0]?.value || 0);
      const width = bounding?.width || 800;
      const isTarget = data.kind === "TARGET";
      const isStop = data.kind === "STOP";
      const isBuy = data.side === "B" || data.side === "BUY";
      const lineColor = isTarget ? "#29b360" : isStop ? "#f05a5a" : isBuy ? "#3b82f6" : "#f97316";
      const bgColor = isTarget ? "#1e7e44" : isStop ? "#aa2e2e" : isBuy ? "#1d4ed8" : "#c2410c";
      const defaultKindLabel = isTarget ? "TARGET" : isStop ? "SL" : "ENTRY";
      const label = data.label || `${defaultKindLabel}: ₹${price.toFixed(2)}`;
      // Every draggable bracket level exposes a grab handle and a remove badge.
      // The handle keeps price-badge clicks free for chart interactions while
      // still giving a clear affordance for moving the level.
      const showRemove = Boolean(data.draggable);
      const showGrip = Boolean(data.draggable);
      const removeX = showRemove ? width - 14 : null;
      const gripX = showGrip ? (showRemove ? width - 34 : width - 14) : null;
      let labelX = width - 8;
      if (showGrip) labelX = gripX - 11;
      if (!showGrip && showRemove) labelX = removeX - 10;
      if (showGrip) {
        data.gripX = gripX;
        data.gripY = y;
      }
      if (showRemove) {
        data.badgeX = removeX;
        data.badgeY = y;
      }

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: 0, y },
            { x: width, y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: isTarget ? [4, 3] : isStop ? [2, 2] : [6, 4],
          size: 1.5,
          color: lineColor,
        },
        ignoreEvent: true,
      });

      figures.push({
        type: "text",
        attrs: {
          x: labelX,
          y,
          text: label,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: bgColor,
          size: 10,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 6,
          paddingRight: 6,
          paddingTop: 2,
          paddingBottom: 2,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      if (showGrip) {
        figures.push({
          type: "text",
          attrs: {
            x: gripX,
            y,
            text: "⋮⋮",
            align: "center",
            baseline: "middle",
          },
          styles: {
            color: "#e2e8f0",
            backgroundColor: "#334155",
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 3,
            paddingRight: 3,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }

      if (showRemove) {
        figures.push({
          type: "text",
          attrs: {
            x: removeX,
            y,
            text: "✕",
            align: "center",
            baseline: "middle",
          },
          styles: {
            color: "#ffffff",
            backgroundColor: "#7f1d1d",
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 3,
            paddingRight: 3,
            paddingTop: 1,
            paddingBottom: 1,
            borderRadius: 6,
          },
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "dragLevelLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const price = Number(data.price || overlay.points?.[0]?.value || 0);
      const width = bounding?.width || 800;
      const accent = "#f0b429";
      const label = data.label || `MOVE: ₹${price.toFixed(2)}`;

      return [
        {
          type: "line",
          attrs: {
            coordinates: [
              { x: 0, y },
              { x: width, y },
            ],
          },
          styles: {
            style: "dashed",
            dashedValue: [7, 4],
            size: 2.5,
            color: accent,
          },
          ignoreEvent: true,
        },
        {
          type: "text",
          attrs: {
            x: width - 8,
            y,
            text: label,
            align: "right",
            baseline: "middle",
          },
          styles: {
            color: "#000000",
            backgroundColor: accent,
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 6,
            paddingRight: 6,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3,
          },
          ignoreEvent: true,
        },
      ];
    },
  });

  kc.registerOverlay({
    name: "openOrderLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const isBuy = data.side === "B" || data.side === "BUY";
      const color = isBuy ? "#3b82f6" : "#f97316";
      const width = bounding?.width || 800;

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: 0, y },
            { x: width, y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [5, 3],
          size: 1.5,
          color,
        },
        ignoreEvent: true,
      });

      const sideStr = isBuy ? "BUY" : "SELL";
      const qty = data.qty ? `${data.qty} ` : "";
      const priceStr = Number(data.price || 0).toFixed(2);
      const tagText = `OPEN ${sideStr}: ${qty}@ ₹${priceStr} · EXCH`;

      const showRemove = Boolean(data.draggable);
      const showGrip = Boolean(data.draggable);
      const removeX = showRemove ? width - 14 : null;
      const gripX = showGrip ? (showRemove ? width - 34 : width - 14) : null;
      let labelX = width - 8;
      if (showGrip) labelX = gripX - 11;
      if (showGrip) {
        data.gripX = gripX;
        data.gripY = y;
      }
      if (showRemove) {
        data.badgeX = removeX;
        data.badgeY = y;
      }

      figures.push({
        type: "text",
        attrs: {
          x: labelX,
          y,
          text: tagText,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: color,
          size: 10,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 6,
          paddingRight: 6,
          paddingTop: 2,
          paddingBottom: 2,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      if (showGrip) {
        figures.push({
          type: "text",
          attrs: {
            x: gripX,
            y,
            text: "⋮⋮",
            align: "center",
            baseline: "middle",
          },
          styles: {
            color: "#e2e8f0",
            backgroundColor: "#334155",
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 3,
            paddingRight: 3,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }

      if (showRemove) {
        figures.push({
          type: "text",
          attrs: {
            x: removeX,
            y,
            text: "✕",
            align: "center",
            baseline: "middle",
          },
          styles: {
            color: "#ffffff",
            backgroundColor: "#7f1d1d",
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 3,
            paddingRight: 3,
            paddingTop: 1,
            paddingBottom: 1,
            borderRadius: 6,
          },
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "candlestickPatternMarker",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.x) || !Number.isFinite(coord.y)) return [];

      const data = overlay.extendData || {};
      const bias = data.bias || "neutral";
      const isBull = bias === "bullish";
      const isBear = bias === "bearish";

      const bgColor = isBull ? "#166534" : (isBear ? "#991b1b" : "#92400e");
      const arrowColor = isBull ? "#22c55e" : (isBear ? "#ef4444" : "#f59e0b");
      const figures = [];

      if (isBull) {
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: coord.y + 4 },
              { x: coord.x - 4, y: coord.y + 11 },
              { x: coord.x + 4, y: coord.y + 11 },
            ],
          },
          styles: { style: "fill", color: arrowColor },
          ignoreEvent: true,
        });
      } else if (isBear) {
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: coord.y - 4 },
              { x: coord.x - 4, y: coord.y - 11 },
              { x: coord.x + 4, y: coord.y - 11 },
            ],
          },
          styles: { style: "fill", color: arrowColor },
          ignoreEvent: true,
        });
      }

      const textY = isBull ? coord.y + 13 : coord.y - 13;
      const baseline = isBull ? "top" : "bottom";
      const label = data.label || data.name || "PATT";

      figures.push({
        type: "text",
        attrs: {
          x: coord.x,
          y: textY,
          text: label,
          align: "center",
          baseline,
        },
        styles: {
          color: "#ffffff",
          backgroundColor: bgColor,
          size: 9,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 4,
          paddingRight: 4,
          paddingTop: 1.5,
          paddingBottom: 1.5,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "geometricPatternOverlay",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || coordinates.length < 2) return [];
      const data = overlay.extendData || {};
      const bias = data.bias || "neutral";
      const isBull = bias === "bullish";
      const isBear = bias === "bearish";
      const accentColor = isBull ? "#22c55e" : (isBear ? "#ef4444" : "#38bdf8");
      const figures = [];

      if (data.resistance && data.support && coordinates.length >= 4) {
        const r1 = coordinates[0];
        const r2 = coordinates[1];
        const s1 = coordinates[2];
        const s2 = coordinates[3];

        if (r1 && r2 && s1 && s2) {
          figures.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: r1.x, y: r1.y },
                { x: r2.x, y: r2.y },
                { x: s2.x, y: s2.y },
                { x: s1.x, y: s1.y },
              ],
            },
            styles: {
              style: "fill",
              color: isBull ? "rgba(34, 197, 94, 0.08)" : (isBear ? "rgba(239, 68, 68, 0.08)" : "rgba(56, 189, 248, 0.08)"),
            },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: r1.x, y: r1.y }, { x: r2.x, y: r2.y }] },
            styles: { style: "dashed", dashedValue: [6, 3], size: 1.6, color: "#f05a5a" },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: s1.x, y: s1.y }, { x: s2.x, y: s2.y }] },
            styles: { style: "dashed", dashedValue: [6, 3], size: 1.6, color: "#29b360" },
            ignoreEvent: true,
          });

          const maxX = Math.max(r2.x, s2.x);
          const midY = (r2.y + s2.y) / 2;
          figures.push({
            type: "text",
            attrs: {
              x: maxX + 6,
              y: midY,
              text: data.label || data.name,
              align: "left",
              baseline: "middle",
            },
            styles: {
              color: "#ffffff",
              backgroundColor: isBull ? "#166534" : (isBear ? "#991b1b" : "#0284c7"),
              size: 9.5,
              weight: "bold",
              family: "system-ui, -apple-system, sans-serif",
              paddingLeft: 5,
              paddingRight: 5,
              paddingTop: 2,
              paddingBottom: 2,
              borderRadius: 3,
            },
            ignoreEvent: true,
          });

          return figures;
        }
      }

      const lineCoords = coordinates.filter(c => c && Number.isFinite(c.x) && Number.isFinite(c.y));
      if (lineCoords.length >= 2) {
        figures.push({
          type: "line",
          attrs: { coordinates: lineCoords.map(c => ({ x: c.x, y: c.y })) },
          styles: { style: "solid", size: 1.6, color: accentColor },
          ignoreEvent: true,
        });

        for (let i = 0; i < lineCoords.length; i++) {
          const pt = lineCoords[i];
          const ptData = data.points?.[i] || {};
          figures.push({
            type: "circle",
            attrs: { x: pt.x, y: pt.y, r: 3 },
            styles: { style: "fill", color: accentColor, borderColor: "#ffffff", borderSize: 1 },
            ignoreEvent: true,
          });

          if (ptData.label) {
            figures.push({
              type: "text",
              attrs: {
                x: pt.x,
                y: pt.y - 7,
                text: ptData.label,
                align: "center",
                baseline: "bottom",
              },
              styles: {
                color: "#e2e8f0",
                backgroundColor: "rgba(15, 23, 42, 0.8)",
                size: 8.5,
                weight: "bold",
                family: "system-ui, -apple-system, sans-serif",
                paddingLeft: 3,
                paddingRight: 3,
                paddingTop: 1,
                paddingBottom: 1,
                borderRadius: 2,
              },
              ignoreEvent: true,
            });
          }
        }

        const lastPt = lineCoords[lineCoords.length - 1];
        const targetStr = data.targetPrice ? ` 🎯 ₹${Number(data.targetPrice).toFixed(1)}` : "";
        figures.push({
          type: "text",
          attrs: {
            x: lastPt.x + 8,
            y: lastPt.y,
            text: `${data.label}${targetStr}`,
            align: "left",
            baseline: "middle",
          },
          styles: {
            color: "#ffffff",
            backgroundColor: isBull ? "#166534" : (isBear ? "#991b1b" : "#0284c7"),
            size: 9.5,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 5,
            paddingRight: 5,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "harmonicPatternOverlay",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || coordinates.length < 4) return [];
      const data = overlay.extendData || {};
      const isBull = data.bias === "bullish";
      const lineColor = isBull ? "#22c55e" : "#ef4444";
      const fillXAB = isBull ? "rgba(34, 197, 94, 0.14)" : "rgba(239, 68, 68, 0.14)";
      const fillBCD = isBull ? "rgba(34, 197, 94, 0.20)" : "rgba(239, 68, 68, 0.20)";
      const figures = [];

      if (coordinates.length >= 5) {
        const X = coordinates[0];
        const A = coordinates[1];
        const B = coordinates[2];
        const C = coordinates[3];
        const D = coordinates[4];

        if (X && A && B && C && D) {
          figures.push({
            type: "polygon",
            attrs: { coordinates: [{ x: X.x, y: X.y }, { x: A.x, y: A.y }, { x: B.x, y: B.y }] },
            styles: { style: "fill", color: fillXAB },
            ignoreEvent: true,
          });

          figures.push({
            type: "polygon",
            attrs: { coordinates: [{ x: B.x, y: B.y }, { x: C.x, y: C.y }, { x: D.x, y: D.y }] },
            styles: { style: "fill", color: fillBCD },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: X.x, y: X.y },
                { x: A.x, y: A.y },
                { x: B.x, y: B.y },
                { x: C.x, y: C.y },
                { x: D.x, y: D.y },
              ],
            },
            styles: { style: "solid", size: 1.5, color: lineColor },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: X.x, y: X.y }, { x: B.x, y: B.y }] },
            styles: { style: "dashed", dashedValue: [3, 3], size: 1, color: "rgba(255,255,255,0.4)" },
            ignoreEvent: true,
          });
          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: A.x, y: A.y }, { x: C.x, y: C.y }] },
            styles: { style: "dashed", dashedValue: [3, 3], size: 1, color: "rgba(255,255,255,0.4)" },
            ignoreEvent: true,
          });
          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: X.x, y: X.y }, { x: D.x, y: D.y }] },
            styles: { style: "dashed", dashedValue: [3, 3], size: 1, color: "rgba(255,255,255,0.4)" },
            ignoreEvent: true,
          });

          const labels = ["X", "A", "B", "C", "D"];
          const pts = [X, A, B, C, D];
          for (let i = 0; i < 5; i++) {
            figures.push({
              type: "circle",
              attrs: { x: pts[i].x, y: pts[i].y, r: 3.5 },
              styles: { style: "fill", color: lineColor, borderColor: "#ffffff", borderSize: 1 },
              ignoreEvent: true,
            });
            figures.push({
              type: "text",
              attrs: {
                x: pts[i].x,
                y: pts[i].y - 8,
                text: labels[i],
                align: "center",
                baseline: "bottom",
              },
              styles: {
                color: "#ffffff",
                backgroundColor: "rgba(15, 23, 42, 0.85)",
                size: 9,
                weight: "bold",
                paddingLeft: 3,
                paddingRight: 3,
                paddingTop: 1,
                paddingBottom: 1,
                borderRadius: 2,
              },
              ignoreEvent: true,
            });
          }

          if (data.ratios?.xb) {
            figures.push({
              type: "text",
              attrs: {
                x: (X.x + B.x) / 2,
                y: (X.y + B.y) / 2,
                text: `${data.ratios.xb}`,
                align: "center",
                baseline: "middle",
              },
              styles: {
                color: "#fbbf24",
                backgroundColor: "rgba(15, 23, 42, 0.85)",
                size: 8,
                weight: "bold",
                paddingLeft: 2,
                paddingRight: 2,
              },
              ignoreEvent: true,
            });
          }

          const przText = data.prz ? `PRZ: ₹${data.prz.low.toFixed(1)} - ₹${data.prz.high.toFixed(1)}` : "PRZ";
          figures.push({
            type: "text",
            attrs: {
              x: D.x + 8,
              y: D.y,
              text: `🎯 ${data.name.toUpperCase()} [${przText}]`,
              align: "left",
              baseline: "middle",
            },
            styles: {
              color: "#ffffff",
              backgroundColor: isBull ? "#15803d" : "#b91c1c",
              size: 9.5,
              weight: "bold",
              family: "system-ui, -apple-system, sans-serif",
              paddingLeft: 5,
              paddingRight: 5,
              paddingTop: 2,
              paddingBottom: 2,
              borderRadius: 3,
            },
            ignoreEvent: true,
          });
        }
      } else if (coordinates.length === 4) {
        const A = coordinates[0];
        const B = coordinates[1];
        const C = coordinates[2];
        const D = coordinates[3];
        if (A && B && C && D) {
          figures.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: A.x, y: A.y },
                { x: B.x, y: B.y },
                { x: C.x, y: C.y },
                { x: D.x, y: D.y },
              ],
            },
            styles: { style: "solid", size: 1.5, color: lineColor },
            ignoreEvent: true,
          });
          figures.push({
            type: "text",
            attrs: {
              x: D.x + 8,
              y: D.y,
              text: `🎯 ${data.name}`,
              align: "left",
              baseline: "middle",
            },
            styles: {
              color: "#ffffff",
              backgroundColor: isBull ? "#15803d" : "#b91c1c",
              size: 9.5,
              weight: "bold",
              paddingLeft: 5,
              paddingRight: 5,
              paddingTop: 2,
              paddingBottom: 2,
              borderRadius: 3,
            },
            ignoreEvent: true,
          });
        }
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "liquidityPatternOverlay",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];

      const data = overlay.extendData || {};
      const isBull = data.bias === "bullish";
      const color = isBull ? "#22c55e" : "#ef4444";
      const width = bounding?.width || 800;
      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: Math.max(0, coord.x - 70), y: coord.y },
            { x: Math.min(width, coord.x + 70), y: coord.y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [4, 3],
          size: 1.5,
          color,
        },
        ignoreEvent: true,
      });

      const label = data.label || data.name || "LIQUIDITY SETUP";
      figures.push({
        type: "text",
        attrs: {
          x: coord.x,
          y: isBull ? coord.y + 14 : coord.y - 14,
          text: label,
          align: "center",
          baseline: isBull ? "top" : "bottom",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: isBull ? "#14532d" : "#7f1d1d",
          size: 9,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 5,
          paddingRight: 5,
          paddingTop: 1.5,
          paddingBottom: 1.5,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "signalMarker",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.x) || !Number.isFinite(coord.y)) return [];
      const data = overlay.extendData || {};
      const dir = data.dir === "down" ? "down" : "up";
      const up = dir === "up";
      const color = data.color || (up ? "#29b360" : "#f05a5a");
      const offset = Number.isFinite(data.offset) ? data.offset : 10;
      const size = Number.isFinite(data.size) ? data.size : 4;
      const tipY = up ? coord.y + offset : coord.y - offset;
      const baseY = up ? tipY + size * 2 : tipY - size * 2;

      const figures: any[] = [
        {
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: tipY },
              { x: coord.x - size, y: baseY },
              { x: coord.x + size, y: baseY },
            ],
          },
          styles: { style: "fill", color },
          ignoreEvent: true,
        },
      ];

      if (data.label) {
        figures.push({
          type: "text",
          attrs: {
            x: coord.x,
            y: up ? baseY + 2 : baseY - 2,
            text: String(data.label),
            align: "center",
            baseline: up ? "top" : "bottom",
          },
          styles: {
            color: "#ffffff",
            backgroundColor: color,
            size: 9,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 4,
            paddingRight: 4,
            paddingTop: 1,
            paddingBottom: 1,
            borderRadius: 2,
          },
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "divergenceMarker",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || coordinates.length < 2) return [];
      const [a, b] = coordinates;
      if (!a || !b || !Number.isFinite(a.x) || !Number.isFinite(b.x)) return [];
      const data = overlay.extendData || {};
      const isBull = data.kind === "bull";
      const color = isBull ? "#29b360" : "#f05a5a";

      const figures: any[] = [
        {
          type: "line",
          attrs: {
            coordinates: [
              { x: a.x, y: a.y },
              { x: b.x, y: b.y },
            ],
          },
          styles: { style: "solid", size: 1.5, color },
          ignoreEvent: true,
        },
      ];

      for (const point of [a, b]) {
        figures.push({
          type: "circle",
          attrs: { x: point.x, y: point.y, r: 3 },
          styles: { style: "fill", color },
          ignoreEvent: true,
        });
      }

      figures.push({
        type: "text",
        attrs: {
          x: b.x,
          y: isBull ? b.y + 16 : b.y - 16,
          text: isBull ? "BULL DIV" : "BEAR DIV",
          align: "center",
          baseline: isBull ? "top" : "bottom",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: isBull ? "#14532d" : "#7f1d1d",
          size: 9,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 5,
          paddingRight: 5,
          paddingTop: 1.5,
          paddingBottom: 1.5,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });
}

function registerCustomIndicators() {
  const kc = window.klinecharts;
  if (!kc || !kc.registerIndicator) return;

  function createVolumeFigure() {
    return {
      key: "volume",
      title: null, // Omit from top header legend so it never moves below into a second row
      type: "bar",
      baseValue: 0,
      styles: function(data) {
        const current = data?.data?.current;
        const defaultStyles = data?.defaultStyles;
        const indicator = data?.indicator;
        const fallbackColor = defaultStyles?.bars?.[0]?.noChangeColor || "#8292a2";
        let color = fallbackColor;
        if (current) {
          if (current.close > current.open) {
            color = indicator?.styles?.bars?.[0]?.upColor || defaultStyles?.bars?.[0]?.upColor || "#35c46a80";
          } else if (current.close < current.open) {
            color = indicator?.styles?.bars?.[0]?.downColor || defaultStyles?.bars?.[0]?.downColor || "#ff5d5d80";
          }
        }
        return { color };
      },
    };
  }

  kc.registerIndicator({
    name: "VOL",
    shortName: "VOL",
    series: "volume",
    calcParams: [5, 10, 20],
    shouldFormatBigNumber: true,
    precision: 0,
    minValue: 0,
    figures: [
      { key: "ma1", title: "MA5: ", type: "line" },
      { key: "ma2", title: "MA10: ", type: "line" },
      { key: "ma3", title: "MA20: ", type: "line" },
      createVolumeFigure(),
    ],
    regenerateFigures: function(calcParams) {
      const figs = (calcParams || []).map((p, idx) => ({
        key: `ma${idx + 1}`,
        title: `MA${p}: `,
        type: "line",
      }));
      figs.push(createVolumeFigure());
      return figs;
    },
    calc: function(dataList, indicator) {
      const params = indicator.calcParams || [];
      const sums = [];
      return (dataList || []).map((candle, idx) => {
        const vol = Number.isFinite(candle?.volume) ? candle.volume : 0;
        const result = {
          volume: vol,
          open: candle?.open,
          close: candle?.close,
        };
        params.forEach((period, pIdx) => {
          sums[pIdx] = (sums[pIdx] || 0) + vol;
          if (idx >= period - 1) {
            result[`ma${pIdx + 1}`] = sums[pIdx] / period;
            const prevBar = dataList[idx - (period - 1)];
            const oldVol = prevBar?.volume;
            sums[pIdx] -= Number.isFinite(oldVol) ? oldVol : 0;
          } else {
            result[`ma${pIdx + 1}`] = null;
          }
        });
        return result;
      });
    },
  });

  kc.registerIndicator({
    name: "TD",
    shortName: "TD",
    calcParams: [],
    precision: 0,
    shouldFormatBigNumber: true,
    figures: [
      { key: "ce", title: "CE decay: ", type: "line", styles: { color: "#4c8dff", size: 1.5 } },
      { key: "pe", title: "PE decay: ", type: "line", styles: { color: "#ff9f43", size: 1.5 } },
      { key: "zero", title: null, type: "line", styles: { color: "rgba(130,146,162,0.35)", size: 1 } },
    ],
    calc: function(dataList) {
      return (dataList || []).map((candle) => ({
        ce: Number.isFinite(candle?.tdCE) ? candle.tdCE : null,
        pe: Number.isFinite(candle?.tdPE) ? candle.tdPE : null,
        zero: 0,
      }));
    },
  });

  kc.registerIndicator({
    name: "RSI14",
    shortName: "RSI",
    calcParams: [14],
    precision: 2,
    minValue: 0,
    maxValue: 100,
    figures: [
      { key: "rsi", title: "RSI: ", type: "line" },
      { key: "overbought", title: null, type: "line", styles: { color: "rgba(240,90,90,0.45)", size: 1 } },
      { key: "mid", title: null, type: "line", styles: { color: "rgba(208,215,222,0.5)", size: 1 } },
      { key: "oversold", title: null, type: "line", styles: { color: "rgba(41,179,96,0.45)", size: 1 } },
    ],
    calc: function(dataList, indicator) {
      const period = Number(indicator?.calcParams?.[0]) || 14;
      const candles = (dataList || []).map((candle) => ({
        time: candle?.timestamp,
        open: candle?.open,
        high: candle?.high,
        low: candle?.low,
        close: candle?.close,
      }));
      const series = Indicators.rsiSeries(candles, period);
      return series.map((value) => ({
        rsi: Number.isFinite(value) ? value : null,
        overbought: 70,
        mid: 50,
        oversold: 30,
      }));
    },
  });
}

function patchIndicatorLastValueView(chart) {
  try {
    const yAxisWidget = chart?._drawPanes?.[0]?._yAxisWidgets?.values()?.next()?.value;
    const view = yAxisWidget?._indicatorLastValueView;
    if (!view) return;
    const proto = Object.getPrototypeOf(view);
    if (!proto || proto._scalperPatched) return;
    proto._scalperPatched = true;

    proto.drawImp = function(ctx) {
      const widget = this.getWidget();
      const pane = widget.getPane();
      const bounding = widget.getBounding();
      const chartStore = pane.getChart().getChartStore();
      const styles = chartStore.getStyles().indicator;
      const lastValueMark = styles.lastValueMark;
      const textStyle = lastValueMark?.text;

      if (!lastValueMark?.show) return;

      const axis = widget.getAxisComponent();
      const range = axis.getRange();
      const dataList = chartStore.getDataList();
      const lastIdx = dataList.length - 1;
      if (lastIdx < 0) return;

      const yAxisIds = new Set([axis.id]);
      const defaultYAxisId = pane.getDefaultYAxisId();
      if (pane.isManualYAxis(axis.id) && defaultYAxisId != null) {
        yAxisIds.add(defaultYAxisId);
      }

      const indicators = chartStore.getIndicatorsByPaneId(pane.getId()).filter((ind) => yAxisIds.has(ind.yAxisId));
      const formatter = chartStore.getInnerFormatter();
      const decimalFold = chartStore.getDecimalFold();
      const thousands = chartStore.getThousandsSeparator();

      indicators.forEach((ind) => {
        const lastResult = ind.result?.[lastIdx];
        if (!lastResult || !ind.visible) return;

        const figures = ind.figures || [];
        figures.forEach((fig, figIdx) => {
          // If VOL indicator, ONLY draw lastValueMark for live volume on the axis, skip MAs
          if (ind.name === "VOL" && fig.key !== "volume") return;

          const val = lastResult[fig.key];
          if (!Number.isFinite(val)) return;

          const y = axis.convertToNicePixel(val);
          let text = axis.displayValueToText(
            axis.realValueToDisplayValue(axis.valueToRealValue(val, { range }), { range }),
            ind.precision
          );
          if (ind.shouldFormatBigNumber && formatter) {
            text = formatter.formatBigNumber(text);
          }
          if (decimalFold && thousands) {
            text = decimalFold.format(thousands.format(text));
          }

          const isFromZero = typeof axis.isFromZero === "function" ? axis.isFromZero() : false;
          const x = isFromZero ? 0 : bounding.width;
          const align = isFromZero ? "left" : "right";

          const lastBar = dataList[lastIdx];
          const isUp = lastBar && lastBar.close >= lastBar.open;
          let bgColor = fig.key === "volume"
            ? (isUp ? (styles.bars?.[0]?.upColor || "#35c46a") : (styles.bars?.[0]?.downColor || "#ff5d5d"))
            : (styles.lines?.[figIdx]?.color || ind.styles?.lines?.[figIdx]?.color || "#2196f3");

          // Solid color on the axis badge
          if (typeof bgColor === "string" && bgColor.startsWith("#") && bgColor.length === 9) {
            bgColor = bgColor.slice(0, 7);
          }

          const figure = this.createFigure({
            name: "text",
            attrs: { x, y, text, align, baseline: "middle" },
            styles: Object.assign({}, textStyle, { backgroundColor: bgColor }),
          });
          if (figure) figure.draw(ctx);
        });
      });
    };
  } catch (err) {
    console.warn("patchIndicatorLastValueView error:", err);
  }
}

export class CandleChart {
  [key: string]: any;
  constructor(container, { showVolume = true, showSR = false, showTrades = true, showAutoTrendlines = false, showPatterns = false, showFib = false, showMA = false, showEMA = undefined, showRSI = false, showDivergence = false, showMomentum = false, showTheta = false, showMaxPain = false, showVolumeProfile = false, showDepth = true, onLevelDrop = null, onLevelRemove = null, onVolumeProfile = null } = {}) {
    this.container = container;
    this.data = [];
    this.showVolume = showVolume;
    this.showSR = showSR;
    this.showTrades = showTrades;
    this.showAutoTrendlines = showAutoTrendlines;
    this.showPatterns = showPatterns;
    this.showFib = Boolean(showFib);
    this.showEMA = Boolean(showEMA ?? showMA);
    this.showRSI = Boolean(showRSI);
    this.showDivergence = Boolean(showDivergence);
    this.showMomentum = Boolean(showMomentum);
    this.showTheta = Boolean(showTheta);
    this.showMaxPain = Boolean(showMaxPain);
    this.maxPainValue = null;
    this.maxPainMeta = null;
    this.showVolumeProfile = Boolean(showVolumeProfile);
    this.vpOverlayId = null;
    this.vpLastKey = "";
    this.vpSuggestion = null;
    this._vpTimer = null;
    this._vpLastRefresh = 0;
    this.onVolumeProfile = typeof onVolumeProfile === "function" ? onVolumeProfile : null;
    this.thetaMeta = null;
    this.thetaDataReady = false;
    this.showDepth = Boolean(showDepth);
    this.emaCrossOverlayIds = [];
    this.divergenceOverlayIds = [];
    this.momentumOverlayIds = [];
    this.indicatorSignature = "";
    this.depthOverlayIds = [];
    this.depthData = null;
    this.chainData = [];
    this._depthTimer = null;
    this.patternConfig = { candlestick: true, geometric: true, harmonic: true, liquidity: true };
    this.srOverlayIds = [];
    this.autoTrendlineOverlayIds = [];
    this.patternOverlayIds = [];
    this.fibOverlayIds = [];
    this.tradeOverlayIds = [];
    this.positionOverlayId = null;
    this.orderPreviewOverlayId = null;
    this.targetPreviewOverlayId = null;
    this.bracketLevelOverlayIds = [];
    this.bracketLevelsKey = "";
    this.bracketData = null;
    this.dayDividerOverlayIds = [];
    this.dayDividerKey = "";
    this.stopPreviewOverlayId = null;
    this.openOrderOverlayIds = [];
    this.openOrdersData = [];
    this.tradesData = [];
    this.positionData = null;
    this.isSpotChart = false;
    this.drawingTrendlineId = null;
    this.drawingTrendlineBtn = null;
    this.drawingEscHandler = null;
    this.onLevelDrop = typeof onLevelDrop === "function" ? onLevelDrop : null;
    this.onLevelRemove = typeof onLevelRemove === "function" ? onLevelRemove : null;
    this.draggableOrderLevels = [];
    this.draggableBracketLevels = [];
    this.levelDrag = null;
    this.levelDragPreviewId = null;
    this.suppressChartClickUntil = 0;
    this.levelPointerHandlers = null;
    this.colors = chartColors(container);
    this.loaderCallback = null;
    this.subscriberCallback = null;

    const kc = window.klinecharts;
    if (!kc) {
      console.error("klinecharts library not available on window.klinecharts");
      return;
    }

    registerCustomOverlays();
    registerCustomIndicators();

    this.chart = kc.init(container, {
      styles: buildStyles(this.colors),
    });

    if (!this.chart) return;

    patchIndicatorLastValueView(this.chart);

    const origRemoveOverlay = this.chart.removeOverlay.bind(this.chart);
    this.chart.removeOverlay = (filter) => {
      if (typeof filter === "string") {
        return origRemoveOverlay({ id: filter });
      }
      return origRemoveOverlay(filter);
    };

    const origCreateIndicator = this.chart.createIndicator.bind(this.chart);
    this.chart.createIndicator = (value, isStack, paneOptions, callback) => {
      let indicator = typeof value === "string" ? { name: value } : Object.assign({}, value);
      if ((indicator.name === "MA" || indicator.name === "EMA" || indicator.name === "RSI14") && paneOptions && paneOptions.id && !indicator.paneId) {
        indicator.paneId = paneOptions.id;
      }
      return origCreateIndicator(indicator, isStack, paneOptions, callback);
    };

    const origRemoveIndicator = this.chart.removeIndicator.bind(this.chart);
    this.chart.removeIndicator = (filter, name) => {
      if (typeof filter === "string" && typeof name === "string") {
        return origRemoveIndicator({ paneId: filter, name: name });
      }
      if (typeof filter === "string") {
        return origRemoveIndicator({ name: filter });
      }
      return origRemoveIndicator(filter);
    };

    this.chart.setTimezone("Asia/Kolkata");
    this.chart.setSymbol({ ticker: container.id || "CHART", pricePrecision: 2, volumePrecision: 0 });
    this.chart.setPeriod({ span: 1, type: "minute" });

    this.chart.setDataLoader({
      getBars: ({ callback }) => {
        this.loaderCallback = callback;
        callback(this.formatCandles(this.data), { forward: false, backward: false });
      },
      subscribeBar: ({ callback }) => {
        this.subscriberCallback = callback;
      },
    });

    if (showVolume) {
      try {
        this.chart.createIndicator("VOL", true, { id: "candle_pane" });
      } catch (e) {
        console.warn("createIndicator VOL:", e);
      }
    }

    if (this.showTheta) {
      this.ensureThetaIndicator();
    }

    if (this.showEMA) {
      try {
        this.chart.createIndicator(
          { name: "EMA", calcParams: [10, 20, 200], paneId: "candle_pane" },
          true,
          { id: "candle_pane" }
        );
      } catch (e) {
        console.warn("createIndicator EMA:", e);
      }
    }

    if (this.showRSI) {
      this.ensureRSIIndicator();
    }

    if (this.container) {
      this.container.addEventListener("wheel", () => {
        if (this.showDepth) this.debounceDepthRefresh();
      }, { passive: true });
    }

    try {
      this.chart?.subscribeAction?.("onVisibleRangeChange", () => {
        if (this.showDepth) this.debounceDepthRefresh();
      });
    } catch (_) {}

    this.initLevelDragging();
  }

  formatCandle(c) {
    if (!c || !Number.isFinite(c.time)) return null;
    const ts = c.time > 1e11 ? c.time : c.time * 1000;
    let o = Number(c.open);
    let h = Number(c.high);
    let l = Number(c.low);
    let cl = Number(c.close);

    if (!Number.isFinite(o) || !Number.isFinite(h) || !Number.isFinite(l) || !Number.isFinite(cl)) return null;
    if (o <= 0 && h <= 0 && l <= 0 && cl <= 0) return null;

    // Sanitize zero / non-positive prices from upstream broker glitches
    if (l <= 0) {
      const pos = [o, h, cl].filter(v => v > 0);
      l = pos.length ? Math.min(...pos) : 0;
    }
    if (o <= 0) o = l > 0 ? l : cl;
    if (cl <= 0) cl = o > 0 ? o : l;
    if (h <= 0 || h < Math.max(o, l, cl)) h = Math.max(o, l, cl);
    if (l <= 0 || o <= 0 || cl <= 0 || h <= 0) return null;

    const formatted: any = {
      timestamp: ts,
      open: o,
      high: Math.max(h, o, cl),
      low: Math.min(l, o, cl),
      close: cl,
      volume: Number.isFinite(c.volume) && c.volume >= 0 ? Number(c.volume) : 0,
      turnover: Number.isFinite(c.turnover) && c.turnover >= 0 ? Number(c.turnover) : 0,
      dayVolume: c.dayVolume,
    };
    if (Number.isFinite(c.tdCE)) formatted.tdCE = Number(c.tdCE);
    if (Number.isFinite(c.tdPE)) formatted.tdPE = Number(c.tdPE);
    return formatted;
  }

  formatCandles(candles) {
    if (!Array.isArray(candles)) return [];
    const result = [];
    for (const c of candles) {
      const item = this.formatCandle(c);
      if (item && item.low > 0 && item.open > 0 && item.close > 0 && item.high >= item.low) {
        result.push(item);
      }
    }
    return result;
  }

  toggleEMA(forceState) {
    this.showEMA = typeof forceState === "boolean" ? forceState : !this.showEMA;
    if (!this.chart) return this.showEMA;
    if (this.showEMA) {
      try {
        this.chart.createIndicator(
          { name: "EMA", calcParams: [10, 20, 200], paneId: "candle_pane" },
          true,
          { id: "candle_pane" }
        );
      } catch (e) {
        console.warn("createIndicator EMA:", e);
      }
      this.refreshEMACrosses();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "candle_pane", name: "EMA" });
      } catch (e) {
        console.warn("removeIndicator EMA:", e);
      }
      this.clearEMACrosses();
    }
    return this.showEMA;
  }

  /** @deprecated use toggleEMA */
  toggleMA(forceState) {
    return this.toggleEMA(forceState);
  }

  ensureRSIIndicator() {
    if (!this.chart) return;
    try {
      this.chart.createIndicator(
        { name: "RSI14", calcParams: [14], paneId: "pane_rsi" },
        false,
        { id: "pane_rsi", height: 110 }
      );
    } catch (e) {
      console.warn("createIndicator RSI:", e);
    }
  }

  toggleRSI(forceState) {
    this.showRSI = typeof forceState === "boolean" ? forceState : !this.showRSI;
    if (!this.chart) return this.showRSI;
    if (this.showRSI) {
      this.ensureRSIIndicator();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "pane_rsi", name: "RSI14" });
      } catch (e) {
        console.warn("removeIndicator RSI:", e);
      }
    }
    return this.showRSI;
  }

  toggleDivergence(forceState) {
    this.showDivergence = typeof forceState === "boolean" ? forceState : !this.showDivergence;
    this.refreshDivergence();
    return this.showDivergence;
  }

  clearDivergence() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "rsi_divergence" });
    } catch (_) {}
    for (const id of this.divergenceOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.divergenceOverlayIds = [];
  }

  refreshDivergence() {
    this.clearDivergence();
    if (!this.showDivergence || !this.chart || this.data.length < 20) return;

    const rsi = Indicators.rsiSeries(this.data, 14);
    const divergences = Indicators.detectDivergences(this.data, rsi, { lookback: 150, span: 3, minRsiDelta: 1, max: 4 });

    for (const divergence of divergences) {
      try {
        const id = this.chart.createOverlay({
          name: "divergenceMarker",
          groupId: "rsi_divergence",
          points: [
            { timestamp: toChartTimestamp(divergence.from.time), value: divergence.from.price },
            { timestamp: toChartTimestamp(divergence.to.time), value: divergence.to.price },
          ],
          lock: true,
          extendData: { kind: divergence.kind },
        });
        if (id) this.divergenceOverlayIds.push(id);
      } catch (_) {}
    }
  }

  toggleMomentumCandles(forceState) {
    this.showMomentum = typeof forceState === "boolean" ? forceState : !this.showMomentum;
    this.refreshMomentumCandles();
    return this.showMomentum;
  }

  clearMomentumCandles() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "momentum_candle" });
    } catch (_) {}
    for (const id of this.momentumOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.momentumOverlayIds = [];
  }

  refreshMomentumCandles() {
    this.clearMomentumCandles();
    if (!this.showMomentum || !this.chart || this.data.length < 20) return;

    const atr = Indicators.atrSeries(this.data, 14);
    const candles = Indicators.detectMomentumCandles(this.data, atr, {
      lookback: 150,
      multiplier: 1.5,
      maxBodyRatio: 0.6,
      max: 30,
    });

    for (const candle of candles) {
      try {
        const id = this.chart.createOverlay({
          name: "signalMarker",
          groupId: "momentum_candle",
          points: [{ timestamp: toChartTimestamp(candle.time), value: candle.price }],
          lock: true,
          extendData: {
            dir: candle.kind === "bull" ? "up" : "down",
            color: candle.kind === "bull" ? "#29b360" : "#f05a5a",
            offset: 8,
            size: 3,
          },
        });
        if (id) this.momentumOverlayIds.push(id);
      } catch (_) {}
    }
  }

  ensureThetaIndicator() {
    if (!this.chart) return;
    try {
      this.chart.createIndicator(
        { name: "TD", calcParams: [], paneId: "pane_theta" },
        false,
        { id: "pane_theta", height: 90 }
      );
    } catch (e) {
      console.warn("createIndicator TD:", e);
    }
  }

  toggleTheta(forceState) {
    this.showTheta = typeof forceState === "boolean" ? forceState : !this.showTheta;
    if (!this.chart) return this.showTheta;
    if (this.showTheta) {
      this.ensureThetaIndicator();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "pane_theta", name: "TD" });
      } catch (e) {
        console.warn("removeIndicator TD:", e);
      }
    }
    return this.showTheta;
  }

  setThetaSeries(byTime, { full = false } = {}) {
    this.thetaMeta = null;
    for (const candle of this.data) {
      const point = byTime?.get?.(candle.time);
      candle.tdCE = point && Number.isFinite(point.ce) ? point.ce : null;
      candle.tdPE = point && Number.isFinite(point.pe) ? point.pe : null;
    }
    if (!this.showTheta) return;
    if (full || !this.thetaDataReady) {
      this.thetaDataReady = true;
      this.setData(this.data, { resetScale: false });
      this.thetaDataReady = true;
      return;
    }
    const latest = this.data[this.data.length - 1];
    const formatted = latest ? this.formatCandle(latest) : null;
    if (formatted && this.subscriberCallback) this.subscriberCallback(formatted);
  }

  setThetaMeta(meta) {
    this.thetaMeta = meta || null;
  }

  toggleMaxPain(forceState) {
    this.showMaxPain = typeof forceState === "boolean" ? forceState : !this.showMaxPain;
    this.refreshMaxPain();
    return this.showMaxPain;
  }

  setMaxPain(value, meta = null) {
    this.maxPainValue = Number.isFinite(value) && value > 0 ? value : null;
    this.maxPainMeta = meta || null;
    this.refreshMaxPain();
  }

  clearMaxPain() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "max_pain" });
    } catch (_) {}
    try {
      this.chart.removeOverlay({ name: "maxPainLine" });
    } catch (_) {}
  }

  refreshMaxPain() {
    this.clearMaxPain();
    if (!this.showMaxPain || !this.chart || !this.maxPainValue || !this.data.length) return;
    try {
      this.chart.createOverlay({
        name: "maxPainLine",
        groupId: "max_pain",
        points: [{ value: this.maxPainValue }],
        lock: true,
        extendData: {
          price: this.maxPainValue,
          color: this.colors.accent,
        },
      });
    } catch (e) {
      console.warn("createOverlay maxPainLine:", e);
    }
  }

  refreshIndicatorOverlays() {
    const last = this.data[this.data.length - 1];
    const signature = `${this.data.length}:${last?.time || 0}:${last?.close || 0}`;
    if (signature === this.indicatorSignature) return;
    this.indicatorSignature = signature;
    this.refreshEMACrosses();
    this.refreshDivergence();
    this.refreshMomentumCandles();
  }

  toggleVolumeProfile(forceState) {
    if (this._vpTimer) {
      clearTimeout(this._vpTimer);
      this._vpTimer = null;
    }
    this._vpLastRefresh = 0;
    this.showVolumeProfile = typeof forceState === "boolean" ? forceState : !this.showVolumeProfile;
    this.vpLastKey = "";
    this.refreshVolumeProfile(true);
    return this.showVolumeProfile;
  }

  clearVolumeProfile() {
    this.vpOverlayId = null;
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "volume_profile" });
    } catch (_) {}
    try {
      this.chart.removeOverlay({ name: "volumeProfile" });
    } catch (_) {}
  }

  emitVolumeProfile() {
    if (!this.onVolumeProfile) return;
    try {
      this.onVolumeProfile(this.vpSuggestion);
    } catch (e) {
      console.warn("onVolumeProfile handler:", e);
    }
  }

  // Throttle with a trailing edge: ticks can arrive far faster than the redraw
  // budget, so a pure debounce would never fire during an active session.
  scheduleVolumeProfile() {
    if (this._vpTimer) return;
    const now = Date.now();
    const elapsed = now - (this._vpLastRefresh || 0);
    const wait = elapsed >= VOLUME_PROFILE_REFRESH_MS ? 0 : VOLUME_PROFILE_REFRESH_MS - elapsed;
    this._vpTimer = setTimeout(() => {
      this._vpTimer = null;
      this._vpLastRefresh = Date.now();
      if (this.showVolumeProfile) this.refreshVolumeProfile();
    }, wait);
  }

  refreshVolumeProfile(force = false) {
    if (!this.showVolumeProfile || !this.chart || this.data.length < 5) {
      this.clearVolumeProfile();
      this.vpSuggestion = null;
      this.vpLastKey = "";
      this.emitVolumeProfile();
      return;
    }
    const profile = buildVolumeProfile(this.data, { buckets: 48, lookback: 300 });
    const last = this.data[this.data.length - 1];
    const suggestion = profile ? suggestVolumeRanges(profile, last?.close) : null;
    if (!profile || !suggestion) {
      this.clearVolumeProfile();
      this.vpSuggestion = null;
      this.vpLastKey = "";
      this.emitVolumeProfile();
      return;
    }
    // The key covers the levels AND the distribution: without the volume sum
    // and the last bar time the histogram could keep stale bars while the
    // actionable lines look current.
    const key = [
      profile.candles,
      last?.time || 0,
      Math.round(profile.total),
      profile.poc.toFixed(4),
      profile.vah.toFixed(4),
      profile.val.toFixed(4),
      suggestion.bias,
    ].join(":");
    if (!force && key === this.vpLastKey && this.vpOverlayId) return;
    this.vpLastKey = key;
    this.clearVolumeProfile();
    try {
      const id = this.chart.createOverlay({
        name: "volumeProfile",
        groupId: "volume_profile",
        points: [{ value: profile.high }, { value: profile.low }],
        lock: true,
        extendData: {
          priceHigh: profile.high,
          priceLow: profile.low,
          nodes: profile.nodes,
          bias: suggestion.bias,
          colors: {
            poc: this.colors.accent,
            valueArea: this.colors.text,
          },
          poc: profile.poc,
          vah: profile.vah,
          val: profile.val,
          buy: suggestion.buy,
          sell: suggestion.sell,
        },
      });
      if (id) this.vpOverlayId = id;
    } catch (e) {
      console.warn("createOverlay volumeProfile:", e);
    }
    this.vpSuggestion = { ...suggestion, profile };
    this.emitVolumeProfile();
  }

  clearEMACrosses() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "ema_cross" });
    } catch (_) {}
    for (const id of this.emaCrossOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.emaCrossOverlayIds = [];
  }

  refreshEMACrosses() {
    this.clearEMACrosses();
    if (!this.showEMA || !this.chart || this.data.length < 25) return;

    const closes = this.data.map((candle) => candle.close);
    const fast = Indicators.emaSeries(closes, 10);
    const slow = Indicators.emaSeries(closes, 20);
    const crosses = Indicators.detectEmaCrosses(this.data, fast, slow, { lookback: 150, max: 12 });

    for (const cross of crosses) {
      const bull = cross.kind === "bull";
      try {
        const id = this.chart.createOverlay({
          name: "signalMarker",
          groupId: "ema_cross",
          points: [{ timestamp: toChartTimestamp(cross.time), value: cross.price }],
          lock: true,
          extendData: {
            dir: bull ? "up" : "down",
            color: bull ? "#29b360" : "#f05a5a",
            label: bull ? "10/20 \u2191" : "10/20 \u2193",
            offset: 12,
            size: 4,
          },
        });
        if (id) this.emaCrossOverlayIds.push(id);
      } catch (_) {}
    }
  }

  toggleDepth(forceState) {
    this.showDepth = typeof forceState === "boolean" ? forceState : !this.showDepth;
    if (!this.showDepth) {
      this.clearDepth();
    } else {
      this.refreshDepth();
    }
    return this.showDepth;
  }

  clearDepth() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "depth_bars" });
    } catch (_) {}
    for (const id of this.depthOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.depthOverlayIds = [];
  }

  getVisiblePriceRange() {
    if (!this.chart || !this.container) return null;
    let minP = Infinity;
    let maxP = -Infinity;
    const height = this.container.clientHeight || 500;

    // 1. Calculate directly from visible candles on screen using getVisibleRange()
    try {
      const vr = typeof this.chart.getVisibleRange === "function" ? this.chart.getVisibleRange() : null;
      if (vr && Array.isArray(this.data) && this.data.length > 0) {
        const from = Math.max(0, Math.floor(vr.from != null ? vr.from : 0));
        const to = Math.min(this.data.length - 1, Math.ceil(vr.to != null ? vr.to : (this.data.length - 1)));
        for (let i = from; i <= to; i++) {
          const c = this.data[i];
          if (c) {
            const l = Number(c.low);
            const h = Number(c.high);
            if (l > 0 && l < minP) minP = l;
            if (h > 0 && h > maxP) maxP = h;
          }
        }
      }
    } catch (_) {}

    // 2. Fallback to recent slice of candles if visible range didn't produce valid bounds
    if (!Number.isFinite(minP) || !Number.isFinite(maxP) || maxP <= minP) {
      if (Array.isArray(this.data) && this.data.length > 0) {
        const slice = this.data.slice(-45);
        for (const c of slice) {
          const l = Number(c.low);
          const h = Number(c.high);
          if (l > 0 && l < minP) minP = l;
          if (h > 0 && h > maxP) maxP = h;
        }
      }
    }

    if (Number.isFinite(minP) && Number.isFinite(maxP) && maxP > minP && minP > 0) {
      const pad = (maxP - minP) * 0.05;
      return { min: Math.max(0.1, minP - pad), max: maxP + pad, height };
    }
    return null;
  }

  debounceDepthRefresh() {
    if (this._depthTimer) clearTimeout(this._depthTimer);
    this._depthTimer = setTimeout(() => {
      this._depthTimer = null;
      if (this.showDepth) this.refreshDepth();
    }, 120);
  }

  setDepth(depth) {
    this.depthData = depth || null;
    this.debounceDepthRefresh();
  }

  setChainData(chain) {
    this.chainData = Array.isArray(chain) ? chain : [];
    if (this.showDepth) this.debounceDepthRefresh();
  }

  refreshDepth() {
    this.clearDepth();
    if (!this.showDepth || !this.chart) return;
    if (!this.data.length && !this.depthData) return;

    const range = this.getVisiblePriceRange();
    if (!range || range.max <= range.min) return;

    const step = computeDOMInterval(range.min, range.max, range.height);
    const { buckets, maxVol } = calculateOrderFlowBuckets(this.data, this.depthData, this.chainData, range.min, range.max, step);
    if (!buckets.length) return;

    for (const b of buckets) {
      if (b.price <= 0 || (b.buyVol <= 0 && b.sellVol <= 0)) continue;
      try {
        const id = this.chart.createOverlay({
          name: "depthLevelBar",
          groupId: "depth_bars",
          paneId: "candle_pane",
          points: [{ value: b.price }],
          lock: true,
          extendData: {
            price: b.price,
            buyVol: b.buyVol,
            sellVol: b.sellVol,
            buyOrders: b.buyOrders,
            sellOrders: b.sellOrders,
            maxVol,
            step,
          },
        });
        if (id) this.depthOverlayIds.push(id);
      } catch (_) {}
    }
  }

  toggleSR(forceState) {
    this.showSR = typeof forceState === "boolean" ? forceState : !this.showSR;
    this.refreshSRLines();
    return this.showSR;
  }

  clearSRLines() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "sr" });
    } catch (_) {}
    for (const id of this.srOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.srOverlayIds = [];
  }

  refreshSRLines() {
    this.clearSRLines();
    if (!this.showSR || !this.data.length || !this.chart) return;

    const { supports, resistances } = calculateSRLevels(this.data);
    for (let i = 0; i < resistances.length; i++) {
      const price = resistances[i];
      try {
        const id = this.chart.createOverlay({
          name: "horizontalStraightLine",
          groupId: "sr",
          points: [{ value: price }],
          lock: true,
          styles: {
            line: { color: this.colors.resistance, style: "dashed", size: 1 },
            text: { color: this.colors.resistance, size: 10 },
          },
        });
        if (id) this.srOverlayIds.push(id);
      } catch (_) {}
    }

    for (let i = 0; i < supports.length; i++) {
      const price = supports[i];
      try {
        const id = this.chart.createOverlay({
          name: "horizontalStraightLine",
          groupId: "sr",
          points: [{ value: price }],
          lock: true,
          styles: {
            line: { color: this.colors.support, style: "dashed", size: 1 },
            text: { color: this.colors.support, size: 10 },
          },
        });
        if (id) this.srOverlayIds.push(id);
      } catch (_) {}
    }
  }

  toggleFib(forceState) {
    this.showFib = typeof forceState === "boolean" ? forceState : !this.showFib;
    this.refreshFib();
    return this.showFib;
  }

  clearFib() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "fib_retracement" });
    } catch (_) {}
    for (const id of this.fibOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.fibOverlayIds = [];
  }

  refreshFib() {
    this.clearFib();
    if (!this.showFib || !this.chart || !this.data.length) return;
    const fib = calculateFibLevels(this.data);
    if (!fib || !fib.levels.length) return;
    try {
      const id = this.chart.createOverlay({
        name: "fibRetracement",
        groupId: "fib_retracement",
        points: fib.levels.map((level) => ({ value: level.price })),
        lock: true,
        extendData: fib,
      });
      if (id) this.fibOverlayIds.push(id);
    } catch (e) {
      console.warn("createOverlay fibRetracement:", e);
    }
  }

  toggleAutoTrendlines(forceState) {
    this.showAutoTrendlines = typeof forceState === "boolean" ? forceState : !this.showAutoTrendlines;
    this.refreshAutoTrendlines();
    return this.showAutoTrendlines;
  }

  clearAutoTrendlines() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "auto_trendline" });
    } catch (_) {}
    for (const id of this.autoTrendlineOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.autoTrendlineOverlayIds = [];
  }

  refreshAutoTrendlines() {
    this.clearAutoTrendlines();
    if (!this.showAutoTrendlines || !this.data.length || !this.chart) return;

    const { resistance, support } = calculateAutoTrendlines(this.data);

    if (resistance) {
      try {
        const id = this.chart.createOverlay({
          name: "rayLine",
          groupId: "auto_trendline",
          points: [
            { timestamp: resistance.p1.time > 1e11 ? resistance.p1.time : resistance.p1.time * 1000, value: resistance.p1.price },
            { timestamp: resistance.p2.time > 1e11 ? resistance.p2.time : resistance.p2.time * 1000, value: resistance.p2.price },
          ],
          lock: true,
          styles: {
            line: {
              color: this.colors.resistance,
              style: "dashed",
              dashedValue: [6, 3],
              size: 1.5,
            },
            point: {
              color: this.colors.resistance,
              borderColor: "#ffffff",
              borderSize: 1,
              radius: 3,
            },
          },
        });
        if (id) this.autoTrendlineOverlayIds.push(id);
      } catch (e) {
        console.warn("createAutoTrendline resistance:", e);
      }
    }

    if (support) {
      try {
        const id = this.chart.createOverlay({
          name: "rayLine",
          groupId: "auto_trendline",
          points: [
            { timestamp: support.p1.time > 1e11 ? support.p1.time : support.p1.time * 1000, value: support.p1.price },
            { timestamp: support.p2.time > 1e11 ? support.p2.time : support.p2.time * 1000, value: support.p2.price },
          ],
          lock: true,
          styles: {
            line: {
              color: this.colors.support,
              style: "dashed",
              dashedValue: [6, 3],
              size: 1.5,
            },
            point: {
              color: this.colors.support,
              borderColor: "#ffffff",
              borderSize: 1,
              radius: 3,
            },
          },
        });
        if (id) this.autoTrendlineOverlayIds.push(id);
      } catch (e) {
        console.warn("createAutoTrendline support:", e);
      }
    }
  }

  togglePatterns(forceState) {
    this.showPatterns = typeof forceState === "boolean" ? forceState : !this.showPatterns;
    this.refreshPatterns();
    return this.showPatterns;
  }

  setPatternConfig(config) {
    this.patternConfig = { ...this.patternConfig, ...config };
    if (this.showPatterns) {
      this.refreshPatterns();
    }
  }

  clearPatterns() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "pattern_candlestick" });
      this.chart.removeOverlay({ groupId: "pattern_geometric" });
      this.chart.removeOverlay({ groupId: "pattern_harmonic" });
      this.chart.removeOverlay({ groupId: "pattern_liquidity" });
    } catch (_) {}
    for (const id of this.patternOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.patternOverlayIds = [];
  }

  refreshPatterns() {
    this.clearPatterns();
    if (!this.showPatterns || !this.data.length || !this.chart) return;

    const detected = Patterns.detectAllPatterns(this.data, {
      candlestick: this.patternConfig.candlestick !== false,
      geometric: this.patternConfig.geometric !== false,
      harmonic: this.patternConfig.harmonic !== false,
      liquidity: this.patternConfig.liquidity !== false,
      lookback: 90,
    });

    // 1. Candlestick overlays
    for (const p of (detected.candlestick || [])) {
      try {
        const id = this.chart.createOverlay({
          name: "candlestickPatternMarker",
          groupId: "pattern_candlestick",
          points: [{
            timestamp: p.time > 1e11 ? p.time : p.time * 1000,
            value: p.price,
          }],
          lock: true,
          extendData: p,
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay candlestick:", e);
      }
    }

    // 2. Geometric overlays
    for (const p of (detected.geometric || [])) {
      try {
        let points = [];
        if (Array.isArray(p.points)) {
          points = p.points.map(pt => ({
            timestamp: pt.time > 1e11 ? pt.time : pt.time * 1000,
            value: pt.price,
          }));
        } else if (p.resistance && p.support) {
          points = [
            { timestamp: p.resistance.p1.time > 1e11 ? p.resistance.p1.time : p.resistance.p1.time * 1000, value: p.resistance.p1.price },
            { timestamp: p.resistance.p2.time > 1e11 ? p.resistance.p2.time : p.resistance.p2.time * 1000, value: p.resistance.p2.price },
            { timestamp: p.support.p1.time > 1e11 ? p.support.p1.time : p.support.p1.time * 1000, value: p.support.p1.price },
            { timestamp: p.support.p2.time > 1e11 ? p.support.p2.time : p.support.p2.time * 1000, value: p.support.p2.price },
          ];
        }
        if (!points.length) continue;

        const id = this.chart.createOverlay({
          name: "geometricPatternOverlay",
          groupId: "pattern_geometric",
          points,
          lock: true,
          extendData: p,
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay geometric:", e);
      }
    }

    // 3. Harmonic overlays
    for (const p of (detected.harmonic || [])) {
      try {
        const points = (p.points || []).map(pt => ({
          timestamp: pt.time > 1e11 ? pt.time : pt.time * 1000,
          value: pt.price,
        }));
        if (!points.length) continue;

        const id = this.chart.createOverlay({
          name: "harmonicPatternOverlay",
          groupId: "pattern_harmonic",
          points,
          lock: true,
          extendData: p,
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay harmonic:", e);
      }
    }

    // 4. Liquidity overlays
    for (const p of (detected.liquidity || [])) {
      try {
        const id = this.chart.createOverlay({
          name: "liquidityPatternOverlay",
          groupId: "pattern_liquidity",
          points: [{
            timestamp: p.time > 1e11 ? p.time : p.time * 1000,
            value: p.levelPrice || p.sweepPrice || 0,
          }],
          lock: true,
          extendData: p,
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay liquidity:", e);
      }
    }
  }

  toggleTrendline(button: any, { onStart, onComplete, onCancel }: any = {}) {
    if (!this.chart) return null;
    if (this.drawingTrendlineId) {
      this.cancelTrendline();
      if (onCancel) onCancel();
      return null;
    }

    const frame = this.container.closest(".chart-frame") || this.container;
    const lineColor = this.colors.accent || "#f0b429";

    if (button) {
      button.classList.add("active");
      button.setAttribute("aria-pressed", "true");
    }
    if (frame) frame.classList.add("drawing-mode");

    const escHandler = (e) => {
      if (e.key === "Escape" && this.drawingTrendlineId) {
        this.cancelTrendline();
        if (onCancel) onCancel();
      }
    };
    window.addEventListener("keydown", escHandler);

    try {
      const id = this.chart.createOverlay({
        name: "segment",
        styles: {
          line: {
            color: lineColor,
            size: 2,
            style: "solid",
          },
          point: {
            color: lineColor,
            borderColor: "#ffffff",
            borderSize: 1.5,
            radius: 5,
            activeColor: "#ffffff",
            activeBorderColor: lineColor,
            activeBorderSize: 2,
            activeRadius: 6,
          },
        },
        onDrawEnd: () => {
          this.drawingTrendlineId = null;
          this.drawingTrendlineBtn = null;
          if (this.drawingEscHandler) {
            window.removeEventListener("keydown", this.drawingEscHandler);
            this.drawingEscHandler = null;
          }
          if (button) {
            button.classList.remove("active");
            button.setAttribute("aria-pressed", "false");
          }
          if (frame) frame.classList.remove("drawing-mode");
          if (onComplete) onComplete();
        },
      });

      this.drawingTrendlineId = id;
      this.drawingTrendlineBtn = button;
      this.drawingEscHandler = escHandler;
      if (onStart) onStart();
      return id;
    } catch (e) {
      console.warn("createTrendline:", e);
      this.cancelTrendline();
      return null;
    }
  }

  cancelTrendline() {
    if (this.drawingEscHandler) {
      window.removeEventListener("keydown", this.drawingEscHandler);
      this.drawingEscHandler = null;
    }
    if (this.drawingTrendlineId) {
      try {
        this.chart?.removeOverlay({ id: this.drawingTrendlineId });
      } catch (_) {}
      this.drawingTrendlineId = null;
    }
    if (this.drawingTrendlineBtn) {
      this.drawingTrendlineBtn.classList.remove("active");
      this.drawingTrendlineBtn.setAttribute("aria-pressed", "false");
      this.drawingTrendlineBtn = null;
    }
    const frame = this.container?.closest(".chart-frame") || this.container;
    if (frame) frame.classList.remove("drawing-mode");
  }

  createTrendline(button, callbacks) {
    return this.toggleTrendline(button, callbacks);
  }

  resetPriceScale() {
    if (!this.chart) return;
    try {
      if (typeof this.chart._resetYAxisAutoCalcTickFlag === "function") {
        this.chart._resetYAxisAutoCalcTickFlag();
      }
      this.chart.layout?.({ measureWidth: true, update: true, buildYAxisTick: true });
    } catch (_) {}
  }

  getPriceFromPixel(pixelY, pixelX = 50, snapToTick = true) {
    if (!this.chart) return null;
    try {
      const res = this.chart.convertFromPixel({ x: pixelX, y: pixelY });
      if (res && Number.isFinite(res.value) && res.value > 0) {
        // Dragging must follow the pointer continuously; snapping to the
        // exchange tick only on drop avoids visible jumps on cheap options.
        return snapToTick ? Math.round(res.value * 20) / 20 : res.value;
      }
    } catch (e) {
      console.warn("getPriceFromPixel:", e);
    }
    return null;
  }

  showOrderPreview(price) {
    this.clearOrderPreview();
    if (!this.chart || !Number.isFinite(price) || price <= 0) return;
    try {
      this.orderPreviewOverlayId = this.chart.createOverlay({
        name: "orderPreviewLine",
        groupId: "order_preview",
        points: [{ value: price }],
        lock: true,
        extendData: { price },
      });
    } catch (_) {}
  }

  clearOrderPreview() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "order_preview" });
    } catch (_) {}
    if (this.orderPreviewOverlayId) {
      try {
        this.chart.removeOverlay({ id: this.orderPreviewOverlayId });
      } catch (_) {}
      this.orderPreviewOverlayId = null;
    }
  }

  showTargetPreview(price, label = "") {
    this.clearTargetPreview();
    if (!this.chart || !Number.isFinite(price) || price <= 0) return;
    try {
      this.targetPreviewOverlayId = this.chart.createOverlay({
        name: "targetPreviewLine",
        groupId: "target_preview",
        points: [{ value: price }],
        lock: true,
        extendData: { price, label },
      });
    } catch (_) {}
  }

  clearTargetPreview() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "target_preview" });
    } catch (_) {}
    if (this.targetPreviewOverlayId) {
      try {
        this.chart.removeOverlay({ id: this.targetPreviewOverlayId });
      } catch (_) {}
      this.targetPreviewOverlayId = null;
    }
  }

  /**
   * Draw the persisted entry/target/stop levels of a server-side bracket order.
   * A pending (resting entry) bracket shows its entry too; an in-position
   * bracket only needs the target/stop because the position line marks the entry.
   */
  showBracketLevels({
    bracketId = "",
    entryPrice = 0,
    targetPrice = 0,
    stopPrice = 0,
    side = "BUY",
    pending = false,
    exchangeStopPrice = 0,
    exchangeStopMode = "",
    exchangeStopStatus = "",
  } = {}) {
    this.bracketData = {
      bracketId, entryPrice, targetPrice, stopPrice, side, pending,
      exchangeStopPrice, exchangeStopMode, exchangeStopStatus,
    };
    this.renderBracketLevels();
  }

  renderBracketLevels() {
    if (!this.chart || !this.bracketData || !this.showTrades) {
      this.clearBracketOverlays();
      return;
    }
    const {
      bracketId = "", entryPrice = 0, targetPrice = 0, stopPrice = 0,
      side = "BUY", pending = false, exchangeStopPrice = 0,
      exchangeStopMode = "", exchangeStopStatus = "",
    } = this.bracketData;
    const entry = Number(entryPrice);
    const target = Number(targetPrice);
    const stop = Number(stopPrice);
    const exchangeStop = Number(exchangeStopPrice);
    const isBuy = side === "B" || side === "BUY" || side === "buy";
    const levels = [];
    if (pending && Number.isFinite(entry) && entry > 0) {
      levels.push({ kind: "ENTRY", price: entry, label: `ENTRY ₹${entry.toFixed(2)} (PENDING) · EXCH`, placement: "exchange" });
    }
    if (Number.isFinite(target) && target > 0) {
      levels.push({ kind: "TARGET", price: target, label: `TARGET ₹${target.toFixed(2)} · SRV`, placement: "server" });
    }
    if (Number.isFinite(stop) && stop > 0) {
      levels.push({ kind: "STOP", price: stop, label: `SL ₹${stop.toFixed(2)} · SRV`, placement: "server" });
    }
    if (Number.isFinite(exchangeStop) && exchangeStop > 0) {
      const type = String(exchangeStopMode || "SL-M").toUpperCase() === "SL" ? "SL-L" : "SL-M";
      const state = String(exchangeStopStatus || "").toUpperCase();
      const suffix = state && state !== "WORKING" ? ` · ${state}` : "";
      levels.push({
        kind: "EXCHANGE_STOP",
        price: exchangeStop,
        label: `${type} ₹${exchangeStop.toFixed(2)} · EXCH${suffix}`,
        placement: "exchange",
      });
    }

    const key = levels.map((l) => l.label).join("|") + `|${isBuy ? "B" : "S"}|${bracketId}`;
    if (key === this.bracketLevelsKey && this.bracketLevelOverlayIds.length === levels.length) return;

    this.clearBracketOverlays();
    this.bracketLevelsKey = key;
    for (const level of levels) {
      const draggable = level.kind !== "EXCHANGE_STOP";
      try {
        const id = this.chart.createOverlay({
          name: "bracketLevelLine",
          groupId: "bracket_levels",
          points: [{ value: level.price }],
          lock: true,
          extendData: {
            bracketId,
            kind: level.kind,
            price: level.price,
            label: level.label,
            side: level.kind === "EXCHANGE_STOP"
              ? (isBuy ? "S" : "B")
              : (isBuy ? "B" : "S"),
            placement: level.placement,
            draggable,
          },
        });
        if (id) {
          this.bracketLevelOverlayIds.push(id);
          if (draggable) {
            this.draggableBracketLevels.push({
              source: "bracket",
              bracketId,
              kind: level.kind,
              price: level.price,
              side: isBuy ? "B" : "S",
            });
          }
        }
      } catch (_) {}
    }
  }

  clearBracketOverlays() {
    this.bracketLevelsKey = "";
    this.draggableBracketLevels = [];
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "bracket_levels" });
    } catch (_) {}
    for (const id of this.bracketLevelOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.bracketLevelOverlayIds = [];
  }

  clearBracketLevels() {
    this.bracketData = null;
    this.clearBracketOverlays();
  }

  /** One dashed vertical line at the first bar of each trading day. */
  refreshDayDividers() {
    if (!this.chart) return;
    const boundaries = [];
    let previousDay = "";
    for (const candle of this.data) {
      if (!Number.isFinite(candle?.time)) continue;
      const day = istDayKey(candle.time);
      if (previousDay && day !== previousDay) boundaries.push({ time: candle.time, day });
      previousDay = day;
    }
    const key = boundaries.map((boundary) => boundary.time).join(",");
    if (key === this.dayDividerKey) return;
    this.clearDayDividers();
    this.dayDividerKey = key;
    for (const boundary of boundaries) {
      try {
        const id = this.chart.createOverlay({
          name: "dayDividerLine",
          groupId: "day_divider",
          points: [{ timestamp: toChartTimestamp(boundary.time) }],
          lock: true,
          extendData: { color: this.colors.border, day: boundary.day },
        });
        if (id) this.dayDividerOverlayIds.push(id);
      } catch (_) {}
    }
  }

  clearDayDividers() {
    this.dayDividerKey = "";
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "day_divider" });
    } catch (_) {}
    for (const id of this.dayDividerOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.dayDividerOverlayIds = [];
  }

  /** Stop-loss preview while composing a bracket in the quick order panel. */
  showStopPreview(price, label = "") {
    this.clearStopPreview();
    if (!this.chart || !Number.isFinite(price) || price <= 0) return;
    try {
      this.stopPreviewOverlayId = this.chart.createOverlay({
        name: "bracketLevelLine",
        groupId: "stop_preview",
        points: [{ value: price }],
        lock: true,
        extendData: { kind: "STOP", price, label: label || `SL ₹${price.toFixed(2)}`, side: "B" },
      });
    } catch (_) {}
  }

  clearStopPreview() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "stop_preview" });
    } catch (_) {}
    if (this.stopPreviewOverlayId) {
      try {
        this.chart.removeOverlay({ id: this.stopPreviewOverlayId });
      } catch (_) {}
      this.stopPreviewOverlayId = null;
    }
  }

  setData(data, { symbol = "", resetScale = true } = {}) {
    this.thetaDataReady = false;
    if (symbol && symbol !== this.currentSymbol) {
      this.currentSymbol = symbol;
      // Rebuild cached bracket overlays for the new symbol.
      this.bracketLevelOverlayIds = [];
      this.draggableBracketLevels = [];
      this.bracketLevelsKey = "";
      this.bracketData = null;
      this.indicatorSignature = "";
      this.vpLastKey = "";
      try {
        this.chart?.setSymbol({ ticker: symbol, pricePrecision: 2, volumePrecision: 0 });
      } catch (_) {}
    }
    const byTime = new Map();
    if (Array.isArray(data)) {
      for (const candle of data) {
        if (!Number.isFinite(candle?.time) || !Number.isFinite(candle?.open) ||
            !Number.isFinite(candle?.high) || !Number.isFinite(candle?.low) ||
            !Number.isFinite(candle?.close)) continue;
        byTime.set(candle.time, candle);
      }
    }
    this.data = [...byTime.values()].sort((a, b) => a.time - b.time);
    const formatted = this.formatCandles(this.data);

    if (this.loaderCallback) {
      this.loaderCallback(formatted, { forward: false, backward: false });
    }
    try {
      this.chart?.resetData();
    } catch (_) {}

    if (resetScale) {
      this.resetPriceScale();
      try {
        this.chart?.scrollToRealTime();
      } catch (_) {}
    }

    this.refreshSRLines();
    this.refreshFib();
    this.refreshMaxPain();
    this.refreshDayDividers();
    this.refreshAutoTrendlines();
    this.refreshPatterns();
    this.refreshIndicatorOverlays();
    this.refreshTradeOverlays();
    this.refreshPositionOverlay();
    this.refreshOpenOrders();
    this.refreshDepth();
    this.refreshVolumeProfile();
  }

  setTrades(trades, { isSpot = false } = {}) {
    this.tradesData = Array.isArray(trades) ? trades : [];
    this.isSpotChart = Boolean(isSpot);
    this.refreshTradeOverlays();
  }

  setPosition(position) {
    this.positionData = position || null;
    this.refreshPositionOverlay();
  }

  setOpenOrders(openOrders) {
    this.openOrdersData = Array.isArray(openOrders) ? openOrders : [];
    this.refreshOpenOrders();
  }

  clearOpenOrders() {
    this.draggableOrderLevels = [];
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "open_orders" });
    } catch (_) {}
    for (const id of this.openOrderOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.openOrderOverlayIds = [];
  }

  refreshOpenOrders() {
    this.clearOpenOrders();
    if (!this.showTrades || !this.chart || !this.data.length || !this.openOrdersData.length) return;
    for (const o of this.openOrdersData) {
      const price = Number(o.price);
      if (!Number.isFinite(price) || price <= 0) continue;
      // A pending bracket entry is drawn by showBracketLevels so dragging it
      // updates the bracket instead of leaving the bracket out of sync.
      if (o.bracketId) continue;
      try {
        const id = this.chart.createOverlay({
          name: "openOrderLine",
          groupId: "open_orders",
          points: [{ value: price }],
          lock: true,
          extendData: {
            orderId: o.id,
            kind: "ENTRY",
            side: o.side,
            qty: o.quantity - (Number(o.filled_quantity) || 0),
            price,
            draggable: true,
          },
        });
        if (id) {
          this.openOrderOverlayIds.push(id);
          this.draggableOrderLevels.push({
            source: "order",
            orderId: o.id,
            kind: "ENTRY",
            price,
            side: o.side,
          });
        }
      } catch (_) {}
    }
  }

  toggleTrades(forceState) {
    this.showTrades = typeof forceState === "boolean" ? forceState : !this.showTrades;
    this.refreshTradeOverlays();
    this.refreshPositionOverlay();
    this.refreshOpenOrders();
    this.renderBracketLevels();
    return this.showTrades;
  }

  initLevelDragging() {
    if (!this.container || this.levelPointerHandlers) return;
    const down = (event) => this.onLevelPointerDown(event);
    const move = (event) => this.onLevelPointerMove(event);
    const up = (event) => this.onLevelPointerUp(event);
    this.container.addEventListener("pointerdown", down, true);
    this.container.addEventListener("pointermove", move, true);
    this.container.addEventListener("pointerup", up, true);
    this.container.addEventListener("pointercancel", up, true);
    this.levelPointerHandlers = { down, move, up };
  }

  getDraggableLevels() {
    return [...this.draggableBracketLevels, ...this.draggableOrderLevels];
  }

  overlayHitTargets() {
    if (!this.chart) return [];
    const groups = ["bracket_levels", "open_orders"];
    const targets = [];
    for (const groupId of groups) {
      let overlays = [];
      try {
        overlays = this.chart.getOverlays({ groupId });
      } catch (_) {
        continue;
      }
      for (const overlay of overlays || []) {
        const data = overlay?.extendData || {};
        if (!data.draggable) continue;
        targets.push(data);
      }
    }
    return targets;
  }

  resolveDraggableLevel(data) {
    if (!data) return null;
    const isOrder = data.orderId !== undefined && data.orderId !== null && data.orderId !== "";
    const list = isOrder ? this.draggableOrderLevels : this.draggableBracketLevels;
    const match = list.find((item) => {
      if (String(item.kind) !== String(data.kind)) return false;
      if (Math.abs(Number(item.price) - Number(data.price)) >= 1e-6) return false;
      return isOrder
        ? String(item.orderId) === String(data.orderId)
        : String(item.bracketId) === String(data.bracketId);
    });
    if (match) return match;
    return {
      source: isOrder ? "order" : "bracket",
      orderId: isOrder ? data.orderId : undefined,
      bracketId: isOrder ? undefined : data.bracketId,
      kind: data.kind,
      price: Number(data.price),
      side: data.side,
    };
  }

  /** Only the drawn grab handle starts a drag; the price badge stays clickable. */
  hitTestLevel(clientX, clientY) {
    if (!this.showTrades || !this.chart || !this.container) return null;
    const rect = this.container.getBoundingClientRect();
    const x = clientX - rect.left;
    const y = clientY - rect.top;
    if (y < 0 || y > rect.height) return null;
    for (const data of this.overlayHitTargets()) {
      const gripX = Number(data.gripX);
      const gripY = Number(data.gripY);
      if (!Number.isFinite(gripX) || !Number.isFinite(gripY)) continue;
      if (Math.abs(x - gripX) > 10 || Math.abs(y - gripY) > 10) continue;
      return this.resolveDraggableLevel(data);
    }
    return null;
  }

  hitTestRemoveBadge(clientX, clientY) {
    if (!this.showTrades || !this.container || !this.chart) return null;
    const rect = this.container.getBoundingClientRect();
    const x = clientX - rect.left;
    const y = clientY - rect.top;
    for (const data of this.overlayHitTargets()) {
      const badgeX = Number(data.badgeX);
      const badgeY = Number(data.badgeY);
      if (!Number.isFinite(badgeX) || !Number.isFinite(badgeY)) continue;
      if (Math.abs(x - badgeX) > 12 || Math.abs(y - badgeY) > 12) continue;
      return this.resolveDraggableLevel(data);
    }
    return null;
  }

  levelDragLabel(level, price) {
    const kind = String(level?.kind || "ENTRY").toUpperCase();
    const prefix = kind === "TARGET" ? "MOVE TARGET" : kind === "STOP" ? "MOVE SL" : "MOVE ENTRY";
    const shown = Number(price) < 1 ? Number(price).toFixed(3) : Number(price).toFixed(2);
    return `${prefix}: ₹${shown}`;
  }

  showLevelDragPreview(level, price) {
    this.clearLevelDragPreview();
    if (!this.chart) return;
    try {
      this.levelDragPreviewId = this.chart.createOverlay({
        name: "dragLevelLine",
        groupId: "level_drag",
        points: [{ value: price }],
        lock: true,
        extendData: { price, label: this.levelDragLabel(level, price) },
      });
    } catch (_) {}
  }

  updateLevelDragPreview(level, price) {
    if (!this.levelDragPreviewId) {
      this.showLevelDragPreview(level, price);
      return;
    }
    try {
      this.chart.overrideOverlay({
        id: this.levelDragPreviewId,
        points: [{ value: price }],
        extendData: { price, label: this.levelDragLabel(level, price) },
      });
    } catch (_) {
      this.showLevelDragPreview(level, price);
    }
  }

  clearLevelDragPreview() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "level_drag" });
    } catch (_) {}
    if (this.levelDragPreviewId) {
      try {
        this.chart.removeOverlay({ id: this.levelDragPreviewId });
      } catch (_) {}
      this.levelDragPreviewId = null;
    }
  }

  onLevelPointerDown(event) {
    if (event.button !== undefined && event.button !== 0) return;
    if (this.levelDrag || this.drawingTrendlineId) return;
    const removeLevel = this.hitTestRemoveBadge(event.clientX, event.clientY);
    if (removeLevel) {
      event.preventDefault();
      event.stopPropagation();
      this.suppressChartClickUntil = Date.now() + 600;
      if (typeof this.onLevelRemove === "function") {
        try {
          this.onLevelRemove({ ...removeLevel });
        } catch (error) {
          console.warn("onLevelRemove:", error);
        }
      }
      return;
    }
    const level = this.hitTestLevel(event.clientX, event.clientY);
    if (!level) return;
    event.preventDefault();
    event.stopPropagation();
    this.levelDrag = {
      level,
      pointerId: event.pointerId,
      startY: event.clientY,
      moved: false,
      price: Number(level.price),
    };
    this.suppressChartClickUntil = Date.now() + 600;
    this.container.style.cursor = "grabbing";
    try {
      this.container.setPointerCapture(event.pointerId);
    } catch (_) {}
    this.showLevelDragPreview(level, Number(level.price));
  }

  onLevelPointerMove(event) {
    if (!this.container) return;
    if (!this.levelDrag) {
      if (this.hitTestRemoveBadge(event.clientX, event.clientY)) {
        this.container.style.cursor = "pointer";
      } else {
        this.container.style.cursor = this.hitTestLevel(event.clientX, event.clientY) ? "grab" : "";
      }
      return;
    }
    if (event.pointerId !== this.levelDrag.pointerId) return;
    event.preventDefault();
    event.stopPropagation();
    const rect = this.container.getBoundingClientRect();
    const y = Math.max(0, Math.min(rect.height - 1, event.clientY - rect.top));
    const x = Math.max(1, Math.min(rect.width - 1, rect.width / 2));
    const price = this.getPriceFromPixel(y, x, false);
    if (!Number.isFinite(price) || price <= 0) return;
    if (Math.abs(event.clientY - this.levelDrag.startY) >= 2) {
      this.levelDrag.moved = true;
      this.container.style.cursor = "grabbing";
    }
    this.levelDrag.price = price;
    this.updateLevelDragPreview(this.levelDrag.level, price);
  }

  onLevelPointerUp(event) {
    if (!this.levelDrag) return;
    if (event.pointerId !== undefined && event.pointerId !== this.levelDrag.pointerId) return;
    const drag = this.levelDrag;
    this.levelDrag = null;
    this.suppressChartClickUntil = Date.now() + 400;
    if (this.container) {
      this.container.style.cursor = "";
      try {
        if (this.container.hasPointerCapture(event.pointerId)) {
          this.container.releasePointerCapture(event.pointerId);
        }
      } catch (_) {}
    }
    this.clearLevelDragPreview();
    if (event.type === "pointercancel") return;
    event.preventDefault();
    event.stopPropagation();
    if (!drag.moved) return;
    const price = Math.round(Number(drag.price) * 20) / 20;
    if (!Number.isFinite(price) || price <= 0) return;
    if (typeof this.onLevelDrop === "function") {
      try {
        this.onLevelDrop({ ...drag.level, price, previousPrice: Number(drag.level.price) });
      } catch (error) {
        console.warn("onLevelDrop:", error);
      }
    }
  }

  consumeChartClickSuppression() {
    if (Date.now() < this.suppressChartClickUntil) {
      this.suppressChartClickUntil = 0;
      return true;
    }
    return false;
  }

  clearTradeOverlays() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "trades" });
    } catch (_) {}
    for (const id of this.tradeOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.tradeOverlayIds = [];
  }

  clearPositionOverlay() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "position" });
    } catch (_) {}
    if (this.positionOverlayId) {
      try {
        this.chart.removeOverlay({ id: this.positionOverlayId });
      } catch (_) {}
      this.positionOverlayId = null;
    }
  }

  refreshTradeOverlays() {
    this.clearTradeOverlays();
    if (!this.showTrades || !this.tradesData.length || !this.chart || !this.data.length) return;

    if (this.isSpotChart) {
      for (const trade of this.tradesData) {
        const ts = parseTimestamp(trade.timestamp);
        if (!ts) continue;
        const candle = findClosestCandle(this.data, ts);
        if (!candle) continue;

        const isBuy = trade.isBull ?? (trade.side === "B" || trade.side === "BUY");
        const priceVal = isBuy ? candle.low : candle.high;
        const sideText = trade.side === "B" ? "BUY" : "EXIT";
        const optText = trade.strike ? `${trade.strike} ${trade.option_type || ""}`.trim() : (trade.option_type || "");
        const actionText = `${sideText} ${optText}`.trim();

        try {
          const id = this.chart.createOverlay({
            name: "tradeMarker",
            groupId: "trades",
            points: [{ timestamp: candle.time > 1e11 ? candle.time : candle.time * 1000, value: priceVal }],
            lock: true,
            extendData: {
              side: trade.side,
              isBull: isBuy,
              isSpot: true,
              text: actionText,
              qty: trade.quantity,
              price: trade.price,
            },
          });
          if (id) this.tradeOverlayIds.push(id);
        } catch (_) {}
      }
    } else {
      // Option chart:
      // 1. Individual trade markers (Buy / Sell)
      for (const trade of this.tradesData) {
        const ts = parseTimestamp(trade.timestamp);
        const price = Number(trade.price);
        if (!ts || !Number.isFinite(price) || price <= 0) continue;

        try {
          const id = this.chart.createOverlay({
            name: "tradeMarker",
            groupId: "trades",
            points: [{ timestamp: ts, value: price }],
            lock: true,
            extendData: {
              side: trade.side,
              qty: trade.quantity,
              price,
              isSpot: false,
            },
          });
          if (id) this.tradeOverlayIds.push(id);
        } catch (_) {}
      }

      // 2. Connect paired trades (Entry -> Exit) with dashed connector & P&L pill
      const pairs = pairTrades(this.tradesData);
      for (const pair of pairs) {
        const t1 = parseTimestamp(pair.entry.timestamp);
        const p1 = Number(pair.entry.price);
        const t2 = parseTimestamp(pair.exit.timestamp);
        const p2 = Number(pair.exit.price);
        if (!t1 || !t2 || !Number.isFinite(p1) || !Number.isFinite(p2)) continue;

        try {
          const id = this.chart.createOverlay({
            name: "tradeConnector",
            groupId: "trades",
            points: [
              { timestamp: t1, value: p1 },
              { timestamp: t2, value: p2 },
            ],
            lock: true,
            extendData: {
              pnl: pair.pnl,
              pnlPct: pair.pnlPct,
              qty: pair.qty,
            },
          });
          if (id) this.tradeOverlayIds.push(id);
        } catch (_) {}
      }
    }
  }

  refreshPositionOverlay() {
    this.clearPositionOverlay();
    if (!this.showTrades || !this.positionData || !this.chart || !this.data.length) return;

    const pos = this.positionData;
    const qty = Number(pos.quantity || 0);
    const avg = Number(pos.average_price || pos.averagePrice || 0);
    if (!qty || !Number.isFinite(avg) || avg <= 0) return;

    const pnl = Number(pos.unrealised_pnl != null ? pos.unrealised_pnl : (pos.pnl || 0));

    try {
      const id = this.chart.createOverlay({
        name: "positionLine",
        groupId: "position",
        points: [{ value: avg }],
        lock: true,
        extendData: {
          qty,
          avgPrice: avg,
          pnl,
        },
      });
      if (id) this.positionOverlayId = id;
    } catch (_) {}
  }

  update(candle) {
    if (!candle || !Number.isFinite(candle.time) || !this.chart) return;
    const lastIdx = this.data.length - 1;
    const startedNewBar = !(lastIdx >= 0 && this.data[lastIdx].time === candle.time);
    if (!startedNewBar) {
      this.data[lastIdx] = { ...this.data[lastIdx], ...candle };
    } else {
      this.data.push(candle);
      this.refreshDayDividers();
      if (this.showFib) {
        this.refreshFib();
      }
      if (this.showAutoTrendlines) {
        this.refreshAutoTrendlines();
      }
      if (this.showPatterns) {
        this.refreshPatterns();
      }
    }
    const latest = this.data[this.data.length - 1];
    const formatted = this.formatCandle(latest ? { ...latest, ...candle } : candle);
    if (formatted && this.subscriberCallback) {
      this.subscriberCallback(formatted);
    }
    if (this.showDepth) {
      this.debounceDepthRefresh();
    }
    if (this.showVolumeProfile) {
      // A closed bar changes the distribution; intra-bar ticks only move the
      // last candle, so throttle those.
      if (startedNewBar) this.refreshVolumeProfile();
      else this.scheduleVolumeProfile();
    }
  }

  zoomIn() {
    if (this.chart) {
      this.chart.zoomAtCoordinate(1.25);
      if (this.showDepth) this.debounceDepthRefresh();
    }
  }

  zoomOut() {
    if (this.chart) {
      this.chart.zoomAtCoordinate(0.8);
      if (this.showDepth) this.debounceDepthRefresh();
    }
  }

  resetView() {
    if (this.chart) {
      this.resetPriceScale();
      this.chart.scrollToRealTime();
      if (this.showDepth) this.debounceDepthRefresh();
    }
  }

  applyTheme() {
    this.colors = chartColors(this.container);
    if (this.chart) {
      this.chart.setStyles(buildStyles(this.colors));
    }
    this.refreshSRLines();
    this.refreshFib();
    this.refreshMaxPain();
    this.refreshAutoTrendlines();
    this.refreshPatterns();
    this.refreshDepth();
  }

  schedule() {
    if (this.chart) this.chart.resize();
  }

  resize() {
    if (this.chart) this.chart.resize();
  }

  destroy() {
    if (this._depthTimer) {
      clearTimeout(this._depthTimer);
      this._depthTimer = null;
    }
    if (this._vpTimer) {
      clearTimeout(this._vpTimer);
      this._vpTimer = null;
    }
    this.levelDrag = null;
    this.clearLevelDragPreview();
    if (this.container && this.levelPointerHandlers) {
      this.container.removeEventListener("pointerdown", this.levelPointerHandlers.down, true);
      this.container.removeEventListener("pointermove", this.levelPointerHandlers.move, true);
      this.container.removeEventListener("pointerup", this.levelPointerHandlers.up, true);
      this.container.removeEventListener("pointercancel", this.levelPointerHandlers.up, true);
      this.levelPointerHandlers = null;
    }
    this.clearOrderPreview();
    this.clearTargetPreview();
    this.clearOpenOrders();
    this.clearTradeOverlays();
    this.clearPositionOverlay();
    this.clearBracketLevels();
    this.clearSRLines();
    this.clearDayDividers();
    this.clearAutoTrendlines();
    this.clearDepth();
    this.clearPatterns();
    if (this.chart && window.klinecharts) {
      window.klinecharts.dispose(this.chart);
    }
  }
}
