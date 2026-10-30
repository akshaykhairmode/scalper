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
      bars: [
        {
          style: "fill",
          upColor: `${colors.up}80`,
          downColor: `${colors.down}80`,
          noChangeColor: `${colors.text}80`,
        },
      ],
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
    const months = { jan: 0, feb: 1, mar: 2, apr: 3, may: 4, jun: 5, jul: 6, aug: 7, sep: 8, oct: 9, nov: 10, dec: 11 };
    const day = parseInt(m[1], 10);
    const mon = isNaN(m[2]) ? (months[m[2].toLowerCase()] ?? 0) : parseInt(m[2], 10) - 1;
    const year = parseInt(m[3], 10);
    const hour = parseInt(m[4] || 0, 10);
    const min = parseInt(m[5] || 0, 10);
    const sec = parseInt(m[6] || 0, 10);
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

let overlaysRegistered = false;
function registerCustomOverlays() {
  const kc = window.klinecharts;
  if (!kc || overlaysRegistered) return;
  overlaysRegistered = true;

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
      const tagText = `OPEN ${sideStr}: ${qty}@ ₹${priceStr}`;

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

      return figures;
    },
  });
}

export class CandleChart {
  constructor(container, { showVolume = true, showSR = false, showTrades = true, showAutoTrendlines = false } = {}) {
    this.container = container;
    this.data = [];
    this.showVolume = showVolume;
    this.showSR = showSR;
    this.showTrades = showTrades;
    this.showAutoTrendlines = showAutoTrendlines;
    this.srOverlayIds = [];
    this.autoTrendlineOverlayIds = [];
    this.tradeOverlayIds = [];
    this.positionOverlayId = null;
    this.orderPreviewOverlayId = null;
    this.targetPreviewOverlayId = null;
    this.openOrderOverlayIds = [];
    this.openOrdersData = [];
    this.tradesData = [];
    this.positionData = null;
    this.isSpotChart = false;
    this.drawingTrendlineId = null;
    this.drawingTrendlineBtn = null;
    this.drawingEscHandler = null;
    this.colors = chartColors(container);
    this.loaderCallback = null;
    this.subscriberCallback = null;

    const kc = window.klinecharts;
    if (!kc) {
      console.error("klinecharts library not available on window.klinecharts");
      return;
    }

    registerCustomOverlays();

    this.chart = kc.init(container, {
      styles: buildStyles(this.colors),
    });

    if (!this.chart) return;

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

    this.createVolumeBadge();
    this.bindEvents();
  }

  formatCandle(c) {
    if (!c || !Number.isFinite(c.time)) return null;
    const ts = c.time > 1e11 ? c.time : c.time * 1000;
    return {
      timestamp: ts,
      open: Number(c.open),
      high: Number(c.high),
      low: Number(c.low),
      close: Number(c.close),
      volume: Number.isFinite(c.volume) ? Number(c.volume) : 0,
      turnover: Number.isFinite(c.turnover) ? Number(c.turnover) : 0,
      dayVolume: c.dayVolume,
    };
  }

  formatCandles(candles) {
    if (!Array.isArray(candles)) return [];
    const result = [];
    for (const c of candles) {
      const item = this.formatCandle(c);
      if (item) result.push(item);
    }
    return result;
  }

  createVolumeBadge() {
    this.volBadge = document.createElement("div");
    this.volBadge.className = "chart-vol-badge";
    this.volBadge.setAttribute("aria-hidden", "true");
    this.volBadge.textContent = "Vol: —";
    this.container.parentElement?.appendChild(this.volBadge);
  }

  bindEvents() {
    if (!this.chart) return;
    this.chart.subscribeAction("onCrosshairChange", (data) => {
      const bar = data?.kLineData;
      if (bar) {
        this.updateVolumeBadge(bar.volume, bar.dayVolume);
      } else {
        this.updateVolumeBadge();
      }
    });
  }

  updateVolumeBadge(customVol, customDayVol) {
    if (!this.volBadge) return;
    const latest = this.data.length ? this.data[this.data.length - 1] : null;
    const vol = Number.isFinite(customVol) ? customVol : latest?.volume;
    const dayVol = Number.isFinite(customDayVol) ? customDayVol : latest?.dayVolume;
    if (Number.isFinite(vol)) {
      this.volBadge.textContent = Number.isFinite(dayVol) && dayVol > 0
        ? `Vol: ${formatVolume(vol)} (Day: ${formatVolume(dayVol)})`
        : `Vol: ${formatVolume(vol)}`;
    } else {
      this.volBadge.textContent = "Vol: —";
    }
  }

  toggleSR(forceState) {
    this.showSR = typeof forceState === "boolean" ? forceState : !this.showSR;
    this.refreshSRLines();
    return this.showSR;
  }

  clearSRLines() {
    if (!this.chart) return;
    for (const id of this.srOverlayIds) {
      try {
        this.chart.removeOverlay(id);
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

  toggleAutoTrendlines(forceState) {
    this.showAutoTrendlines = typeof forceState === "boolean" ? forceState : !this.showAutoTrendlines;
    this.refreshAutoTrendlines();
    return this.showAutoTrendlines;
  }

  clearAutoTrendlines() {
    if (!this.chart) return;
    for (const id of this.autoTrendlineOverlayIds) {
      try {
        this.chart.removeOverlay(id);
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

  toggleTrendline(button, { onStart, onComplete, onCancel } = {}) {
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
        this.chart?.removeOverlay(this.drawingTrendlineId);
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

  getPriceFromPixel(pixelY, pixelX = 50) {
    if (!this.chart) return null;
    try {
      const res = this.chart.convertFromPixel({ x: pixelX, y: pixelY });
      if (res && Number.isFinite(res.value) && res.value > 0) {
        return Math.round(res.value * 20) / 20;
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
        points: [{ value: price }],
        lock: true,
        extendData: { price },
      });
    } catch (_) {}
  }

  clearOrderPreview() {
    if (!this.chart || !this.orderPreviewOverlayId) return;
    try {
      this.chart.removeOverlay(this.orderPreviewOverlayId);
    } catch (_) {}
    this.orderPreviewOverlayId = null;
  }

  showTargetPreview(price, label = "") {
    this.clearTargetPreview();
    if (!this.chart || !Number.isFinite(price) || price <= 0) return;
    try {
      this.targetPreviewOverlayId = this.chart.createOverlay({
        name: "targetPreviewLine",
        points: [{ value: price }],
        lock: true,
        extendData: { price, label },
      });
    } catch (_) {}
  }

  clearTargetPreview() {
    if (!this.chart || !this.targetPreviewOverlayId) return;
    try {
      this.chart.removeOverlay(this.targetPreviewOverlayId);
    } catch (_) {}
    this.targetPreviewOverlayId = null;
  }

  setData(data, { symbol = "", resetScale = true } = {}) {
    if (symbol && symbol !== this.currentSymbol) {
      this.currentSymbol = symbol;
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

    this.updateVolumeBadge();
    this.refreshSRLines();
    this.refreshAutoTrendlines();
    this.refreshTradeOverlays();
    this.refreshPositionOverlay();
    this.refreshOpenOrders();
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
    if (!this.chart) return;
    for (const id of this.openOrderOverlayIds) {
      try {
        this.chart.removeOverlay(id);
      } catch (_) {}
    }
    this.openOrderOverlayIds = [];
  }

  refreshOpenOrders() {
    this.clearOpenOrders();
    if (!this.chart || !this.data.length || !this.openOrdersData.length) return;
    for (const o of this.openOrdersData) {
      const price = Number(o.price);
      if (!Number.isFinite(price) || price <= 0) continue;
      try {
        const id = this.chart.createOverlay({
          name: "openOrderLine",
          points: [{ value: price }],
          lock: true,
          extendData: {
            orderId: o.id,
            side: o.side,
            qty: o.quantity - (Number(o.filled_quantity) || 0),
            price,
          },
        });
        if (id) this.openOrderOverlayIds.push(id);
      } catch (_) {}
    }
  }

  toggleTrades(forceState) {
    this.showTrades = typeof forceState === "boolean" ? forceState : !this.showTrades;
    this.refreshTradeOverlays();
    this.refreshPositionOverlay();
    return this.showTrades;
  }

  clearTradeOverlays() {
    if (!this.chart) return;
    for (const id of this.tradeOverlayIds) {
      try {
        this.chart.removeOverlay(id);
      } catch (_) {}
    }
    this.tradeOverlayIds = [];
  }

  clearPositionOverlay() {
    if (!this.chart) return;
    if (this.positionOverlayId) {
      try {
        this.chart.removeOverlay(this.positionOverlayId);
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
    if (lastIdx >= 0 && this.data[lastIdx].time === candle.time) {
      this.data[lastIdx] = { ...this.data[lastIdx], ...candle };
    } else {
      this.data.push(candle);
      if (this.showAutoTrendlines) {
        this.refreshAutoTrendlines();
      }
    }
    const formatted = this.formatCandle(candle);
    if (formatted && this.subscriberCallback) {
      this.subscriberCallback(formatted);
    }
    this.updateVolumeBadge(candle.volume, candle.dayVolume);
  }

  zoomIn() {
    if (this.chart) this.chart.zoomAtCoordinate(1.25);
  }

  zoomOut() {
    if (this.chart) this.chart.zoomAtCoordinate(0.8);
  }

  resetView() {
    if (this.chart) {
      this.resetPriceScale();
      this.chart.scrollToRealTime();
    }
  }

  applyTheme() {
    this.colors = chartColors(this.container);
    if (this.chart) {
      this.chart.setStyles(buildStyles(this.colors));
    }
    this.refreshSRLines();
    this.refreshAutoTrendlines();
  }

  schedule() {
    if (this.chart) this.chart.resize();
  }

  destroy() {
    this.clearOrderPreview();
    this.clearTargetPreview();
    this.clearOpenOrders();
    this.clearTradeOverlays();
    this.clearPositionOverlay();
    this.clearSRLines();
    this.clearAutoTrendlines();
    this.volBadge?.remove();
    if (this.chart && window.klinecharts) {
      window.klinecharts.dispose(this.chart);
    }
  }
}
