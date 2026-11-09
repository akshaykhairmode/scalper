// src/chart/patterns.ts
function calculateATR(candles, period = 14) {
  if (!candles || candles.length < 2) return [];
  const atrs = new Array(candles.length).fill(0);
  const trs = new Array(candles.length).fill(0);
  trs[0] = candles[0].high - candles[0].low;
  for (let i = 1; i < candles.length; i++) {
    const hl = candles[i].high - candles[i].low;
    const hc = Math.abs(candles[i].high - candles[i - 1].close);
    const lc = Math.abs(candles[i].low - candles[i - 1].close);
    trs[i] = Math.max(hl, hc, lc);
  }
  let sum = 0;
  for (let i = 0; i < Math.min(period, trs.length); i++) {
    sum += trs[i];
  }
  atrs[Math.min(period - 1, trs.length - 1)] = sum / Math.min(period, trs.length);
  for (let i = period; i < candles.length; i++) {
    atrs[i] = (atrs[i - 1] * (period - 1) + trs[i]) / period;
  }
  return atrs;
}
function calculateVolumeMA(candles, period = 20) {
  if (!candles || !candles.length) return [];
  const ma = new Array(candles.length).fill(0);
  let sum = 0;
  for (let i = 0; i < candles.length; i++) {
    const vol = Number(candles[i].volume || 0);
    sum += vol;
    if (i >= period) {
      sum -= Number(candles[i - period].volume || 0);
      ma[i] = sum / period;
    } else {
      ma[i] = sum / (i + 1);
    }
  }
  return ma;
}
function extractPivots(candles, { windowSize = 2, maxPivots = 50 } = {}) {
  if (!candles || candles.length < windowSize * 2 + 1) return [];
  const n = candles.length;
  const rawPivots = [];
  for (let i = windowSize; i < n - windowSize; i++) {
    const c = candles[i];
    let isHigh = true;
    let isLow = true;
    for (let j = i - windowSize; j <= i + windowSize; j++) {
      if (j === i) continue;
      if (candles[j].high > c.high) isHigh = false;
      if (candles[j].low < c.low) isLow = false;
    }
    if (isHigh && !isLow) {
      rawPivots.push({ index: i, time: c.time, price: c.high, type: "HIGH", candle: c });
    } else if (isLow && !isHigh) {
      rawPivots.push({ index: i, time: c.time, price: c.low, type: "LOW", candle: c });
    } else if (isHigh && isLow) {
      if (c.close >= c.open) {
        rawPivots.push({ index: i, time: c.time, price: c.high, type: "HIGH", candle: c });
      } else {
        rawPivots.push({ index: i, time: c.time, price: c.low, type: "LOW", candle: c });
      }
    }
  }
  const alternating = [];
  for (const p of rawPivots) {
    if (!alternating.length) {
      alternating.push(p);
      continue;
    }
    const last = alternating[alternating.length - 1];
    if (last.type === p.type) {
      if (p.type === "HIGH" && p.price > last.price) {
        alternating[alternating.length - 1] = p;
      } else if (p.type === "LOW" && p.price < last.price) {
        alternating[alternating.length - 1] = p;
      }
    } else {
      alternating.push(p);
    }
  }
  return alternating.slice(-maxPivots);
}
function detectCandlestickPatterns(candles, { lookback = 80 } = {}) {
  if (!candles || candles.length < 3) return [];
  const patterns = [];
  const startIdx = Math.max(2, candles.length - lookback);
  const atrs = calculateATR(candles, 14);
  for (let i = startIdx; i < candles.length; i++) {
    const c = candles[i];
    const prev1 = candles[i - 1];
    const prev2 = candles[i - 2];
    const prev3 = i >= 3 ? candles[i - 3] : null;
    const prev4 = i >= 4 ? candles[i - 4] : null;
    const atr = atrs[i] || c.high - c.low || 1;
    const body = Math.abs(c.close - c.open);
    const range = c.high - c.low;
    if (range <= 0) continue;
    const isGreen = c.close >= c.open;
    const isRed = c.close < c.open;
    const upperWick = c.high - Math.max(c.open, c.close);
    const lowerWick = Math.min(c.open, c.close) - c.low;
    const bodyRatio = body / range;
    const upperRatio = upperWick / range;
    const lowerRatio = lowerWick / range;
    const prevTrend = i >= 4 ? candles[i - 1].close > candles[i - 4].close ? "up" : "down" : "neutral";
    if (lowerRatio >= 0.55 && upperRatio <= 0.18 && bodyRatio <= 0.35 && prevTrend === "down") {
      patterns.push({
        id: `p_hmr_${c.time}`,
        type: "candlestick",
        name: "Hammer",
        label: "\u{1F528} HAMMER",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish hammer rejection at swing low"
      });
      continue;
    }
    if (upperRatio >= 0.55 && lowerRatio <= 0.18 && bodyRatio <= 0.35 && prevTrend === "down") {
      patterns.push({
        id: `p_ihmr_${c.time}`,
        type: "candlestick",
        name: "Inverted Hammer",
        label: "\u{1F528} INV HAMMER",
        bias: "bullish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bullish inverted hammer reversal test"
      });
      continue;
    }
    if (lowerRatio >= 0.55 && upperRatio <= 0.18 && bodyRatio <= 0.35 && prevTrend === "up") {
      patterns.push({
        id: `p_hng_${c.time}`,
        type: "candlestick",
        name: "Hanging Man",
        label: "\u26A0\uFE0F HANGING MAN",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish hanging man warning at top"
      });
      continue;
    }
    if (upperRatio >= 0.55 && lowerRatio <= 0.18 && bodyRatio <= 0.35 && prevTrend === "up") {
      patterns.push({
        id: `p_star_${c.time}`,
        type: "candlestick",
        name: "Shooting Star",
        label: "\u2B50 SHOOTING STAR",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish shooting star rejection wick at top"
      });
      continue;
    }
    if (bodyRatio <= 0.08 && lowerRatio >= 0.65 && upperRatio <= 0.12) {
      patterns.push({
        id: `p_dfd_${c.time}`,
        type: "candlestick",
        name: "Dragonfly Doji",
        label: "\u2726 DRAGONFLY",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish dragonfly doji rejection"
      });
      continue;
    }
    if (bodyRatio <= 0.08 && upperRatio >= 0.65 && lowerRatio <= 0.12) {
      patterns.push({
        id: `p_gsd_${c.time}`,
        type: "candlestick",
        name: "Gravestone Doji",
        label: "\u2726 GRAVESTONE",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish gravestone doji rejection"
      });
      continue;
    }
    if (bodyRatio <= 0.08 && upperRatio >= 0.3 && lowerRatio >= 0.3) {
      patterns.push({
        id: `p_lld_${c.time}`,
        type: "candlestick",
        name: "Long-Legged Doji",
        label: "\u2726 DOJI",
        bias: "neutral",
        time: c.time,
        price: (c.high + c.low) / 2,
        candleIndex: i,
        description: "Indecision doji with balanced long shadows"
      });
      continue;
    }
    if (bodyRatio >= 0.1 && bodyRatio <= 0.32 && upperRatio >= 0.28 && lowerRatio >= 0.28) {
      patterns.push({
        id: `p_spt_${c.time}`,
        type: "candlestick",
        name: "Spinning Top",
        label: "\u2722 SPINNING TOP",
        bias: "neutral",
        time: c.time,
        price: (c.high + c.low) / 2,
        candleIndex: i,
        description: "Equilibrium indecision spinning top"
      });
      continue;
    }
    if (bodyRatio >= 0.88 && body >= atr * 0.75) {
      patterns.push({
        id: `p_mrb_${c.time}`,
        type: "candlestick",
        name: isGreen ? "Bullish Marubozu" : "Bearish Marubozu",
        label: isGreen ? "\u26A1 MARUBOZU (BULL)" : "\u26A1 MARUBOZU (BEAR)",
        bias: isGreen ? "bullish" : "bearish",
        time: c.time,
        price: isGreen ? c.high : c.low,
        candleIndex: i,
        description: isGreen ? "Strong institutional bullish expansion" : "Strong institutional bearish expansion"
      });
      continue;
    }
    const prevBody = Math.abs(prev1.close - prev1.open);
    const prevRange = prev1.high - prev1.low;
    const prevIsGreen = prev1.close >= prev1.open;
    const prevIsRed = prev1.close < prev1.open;
    if (prevIsRed && isGreen && c.open >= prev1.open && body >= atr * 0.7 && prevBody >= atr * 0.7) {
      patterns.push({
        id: `p_bkck_${c.time}`,
        type: "candlestick",
        name: "Bullish Kicker",
        label: "\u{1F680} BULL KICKER",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Explosive bullish kicker gap reversal"
      });
      continue;
    }
    if (prevIsGreen && isRed && c.open <= prev1.open && body >= atr * 0.7 && prevBody >= atr * 0.7) {
      patterns.push({
        id: `p_skck_${c.time}`,
        type: "candlestick",
        name: "Bearish Kicker",
        label: "\u{1F4C9} BEAR KICKER",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Explosive bearish kicker gap reversal"
      });
      continue;
    }
    if (prevIsRed && isGreen && c.close >= prev1.open && c.open <= prev1.close && body > prevBody * 1.05) {
      patterns.push({
        id: `p_beng_${c.time}`,
        type: "candlestick",
        name: "Bullish Engulfing",
        label: "\u25B2 ENGULFING",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish engulfing candle reversal"
      });
      continue;
    }
    if (prevIsGreen && isRed && c.close <= prev1.open && c.open >= prev1.close && body > prevBody * 1.05) {
      patterns.push({
        id: `p_seng_${c.time}`,
        type: "candlestick",
        name: "Bearish Engulfing",
        label: "\u25BC ENGULFING",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish engulfing candle reversal"
      });
      continue;
    }
    if (prevIsRed && isGreen && c.open >= prev1.close && c.close <= prev1.open && prevBody > atr * 0.55) {
      patterns.push({
        id: `p_bhar_${c.time}`,
        type: "candlestick",
        name: "Bullish Harami",
        label: "\u229E HARAMI (BUY)",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish inside bar contraction setup"
      });
      continue;
    }
    if (prevIsGreen && isRed && c.open <= prev1.close && c.close >= prev1.open && prevBody > atr * 0.55) {
      patterns.push({
        id: `p_shar_${c.time}`,
        type: "candlestick",
        name: "Bearish Harami",
        label: "\u229E HARAMI (SELL)",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish inside bar contraction setup"
      });
      continue;
    }
    if (prevIsRed && isGreen && c.open < prev1.low && c.close > (prev1.open + prev1.close) / 2 && c.close < prev1.open) {
      patterns.push({
        id: `p_prc_${c.time}`,
        type: "candlestick",
        name: "Piercing Line",
        label: "\u25B2 PIERCING",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish piercing line piercing >50% of prior candle"
      });
      continue;
    }
    if (prevIsGreen && isRed && c.open > prev1.high && c.close < (prev1.open + prev1.close) / 2 && c.close > prev1.open) {
      patterns.push({
        id: `p_dcc_${c.time}`,
        type: "candlestick",
        name: "Dark Cloud Cover",
        label: "\u25BC DARK CLOUD",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish dark cloud cover penetrating >50% of prior candle"
      });
      continue;
    }
    if (prevIsRed && isGreen && Math.abs(c.low - prev1.low) / (c.low || 1) <= 8e-4 && lowerRatio >= 0.32) {
      patterns.push({
        id: `p_twb_${c.time}`,
        type: "candlestick",
        name: "Tweezer Bottom",
        label: "\u264A TWEEZER BTM",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Dual rejection at identical low level"
      });
      continue;
    }
    if (prevIsGreen && isRed && Math.abs(c.high - prev1.high) / (c.high || 1) <= 8e-4 && upperRatio >= 0.32) {
      patterns.push({
        id: `p_twt_${c.time}`,
        type: "candlestick",
        name: "Tweezer Top",
        label: "\u264A TWEEZER TOP",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Dual rejection at identical high level"
      });
      continue;
    }
    const prev2IsRed = prev2.close < prev2.open;
    const prev2IsGreen = prev2.close >= prev2.open;
    const prev2Body = Math.abs(prev2.close - prev2.open);
    if (prev2IsRed && prev2Body > atr * 0.45 && prevBody < prev2Body * 0.45 && isGreen && c.close >= (prev2.open + prev2.close) / 2) {
      patterns.push({
        id: `p_mst_${c.time}`,
        type: "candlestick",
        name: "Morning Star",
        label: "\u2605 MORNING STAR",
        bias: "bullish",
        time: c.time,
        price: prev1.low,
        candleIndex: i,
        description: "Bullish morning star 3-candle reversal"
      });
      continue;
    }
    if (prev2IsGreen && prev2Body > atr * 0.45 && prevBody < prev2Body * 0.45 && isRed && c.close <= (prev2.open + prev2.close) / 2) {
      patterns.push({
        id: `p_est_${c.time}`,
        type: "candlestick",
        name: "Evening Star",
        label: "\u2605 EVENING STAR",
        bias: "bearish",
        time: c.time,
        price: prev1.high,
        candleIndex: i,
        description: "Bearish evening star 3-candle reversal"
      });
      continue;
    }
    if (prev2IsRed && prevIsGreen && prev1.open >= prev2.close && prev1.close <= prev2.open && isGreen && c.close > prev2.high) {
      patterns.push({
        id: `p_3iu_${c.time}`,
        type: "candlestick",
        name: "Three Inside Up",
        label: "\u25B2 3 INSIDE UP",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Confirmed bullish harami breakout"
      });
      continue;
    }
    if (prev2IsGreen && prevIsRed && prev1.open <= prev2.close && prev1.close >= prev2.open && isRed && c.close < prev2.low) {
      patterns.push({
        id: `p_3id_${c.time}`,
        type: "candlestick",
        name: "Three Inside Down",
        label: "\u25BC 3 INSIDE DOWN",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Confirmed bearish harami breakdown"
      });
      continue;
    }
    if (prev2IsRed && prevIsGreen && prev1.close >= prev2.open && prev1.open <= prev2.close && isGreen && c.close > prev1.high) {
      patterns.push({
        id: `p_3ou_${c.time}`,
        type: "candlestick",
        name: "Three Outside Up",
        label: "\u25B2 3 OUTSIDE UP",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Confirmed bullish engulfing expansion"
      });
      continue;
    }
    if (prev2IsGreen && prevIsRed && prev1.close <= prev2.open && prev1.open >= prev2.close && isRed && c.close < prev1.low) {
      patterns.push({
        id: `p_3od_${c.time}`,
        type: "candlestick",
        name: "Three Outside Down",
        label: "\u25BC 3 OUTSIDE DOWN",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Confirmed bearish engulfing breakdown"
      });
      continue;
    }
    if (prev2IsGreen && prevIsGreen && isGreen && c.close > prev1.close && prev1.close > prev2.close && c.open > prev1.open && prev1.open > prev2.open && bodyRatio > 0.55 && upperRatio < 0.25) {
      patterns.push({
        id: `p_3ws_${c.time}`,
        type: "candlestick",
        name: "Three White Soldiers",
        label: "\u25B2 3 SOLDIERS",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Three consecutive strong bullish candles"
      });
      continue;
    }
    if (prev2IsRed && prevIsRed && isRed && c.close < prev1.close && prev1.close < prev2.close && c.open < prev1.open && prev1.open < prev2.open && bodyRatio > 0.55 && lowerRatio < 0.25) {
      patterns.push({
        id: `p_3bc_${c.time}`,
        type: "candlestick",
        name: "Three Black Crows",
        label: "\u25BC 3 CROWS",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Three consecutive strong bearish candles"
      });
      continue;
    }
    if (prev4 && prev4.close > prev4.open && isGreen && c.close > prev4.high) {
      const p4Body = prev4.close - prev4.open;
      const b1 = prev3.close < prev3.open && prev3.low >= prev4.low && prev3.high <= prev4.high;
      const b2 = prev2.close < prev2.open && prev2.low >= prev4.low && prev2.high <= prev4.high;
      const b3 = prev1.close < prev1.open && prev1.low >= prev4.low && prev1.high <= prev4.high;
      if (b1 && b2 && b3 && p4Body > atr * 0.7) {
        patterns.push({
          id: `p_r3m_${c.time}`,
          type: "candlestick",
          name: "Rising Three Methods",
          label: "\u26A1 RISING 3 METHODS",
          bias: "bullish",
          time: c.time,
          price: c.low,
          candleIndex: i,
          description: "Institutional pullback and continuation rally"
        });
        continue;
      }
    }
    if (prev4 && prev4.close < prev4.open && isRed && c.close < prev4.low) {
      const p4Body = prev4.open - prev4.close;
      const b1 = prev3.close > prev3.open && prev3.high <= prev4.high && prev3.low >= prev4.low;
      const b2 = prev2.close > prev2.open && prev2.high <= prev4.high && prev2.low >= prev4.low;
      const b3 = prev1.close > prev1.open && prev1.high <= prev4.high && prev1.low >= prev4.low;
      if (b1 && b2 && b3 && p4Body > atr * 0.7) {
        patterns.push({
          id: `p_f3m_${c.time}`,
          type: "candlestick",
          name: "Falling Three Methods",
          label: "\u26A1 FALLING 3 METHODS",
          bias: "bearish",
          time: c.time,
          price: c.high,
          candleIndex: i,
          description: "Institutional consolidation and continuation breakdown"
        });
        continue;
      }
    }
  }
  return patterns;
}
function detectGeometricPatterns(candles, pivots, { lookbackPivots = 30 } = {}) {
  if (!candles || !pivots || pivots.length < 4) return [];
  const patterns = [];
  const recent = pivots.slice(-lookbackPivots);
  const n = recent.length;
  const lastPrice = candles[candles.length - 1]?.close || 0;
  const atrs = calculateATR(candles, 14);
  const currentATR = atrs[atrs.length - 1] || 10;
  for (let i = 0; i <= n - 4; i++) {
    const p0 = recent[i];
    const p1 = recent[i + 1];
    const p2 = recent[i + 2];
    const p3 = recent[i + 3];
    const p4 = i + 4 < n ? recent[i + 4] : null;
    const p5 = i + 5 < n ? recent[i + 5] : null;
    if (p1.type === "HIGH" && p2.type === "LOW" && p3.type === "HIGH") {
      const topDiff = Math.abs(p1.price - p3.price) / p1.price;
      const height = (p1.price + p3.price) / 2 - p2.price;
      if (topDiff <= 0.012 && height > 0) {
        const neckline = p2.price;
        const target = neckline - height;
        const confirmed = lastPrice < neckline;
        patterns.push({
          id: `geo_dtop_${p1.time}_${p3.time}`,
          type: "geometric",
          subType: "double_top",
          name: "Double Top (M)",
          label: "DOUBLE TOP (M)",
          bias: "bearish",
          points: [
            { time: p0.time, price: p0.price, label: "Start" },
            { time: p1.time, price: p1.price, label: "Top 1" },
            { time: p2.time, price: p2.price, label: "Neck" },
            { time: p3.time, price: p3.price, label: "Top 2" }
          ],
          necklinePrice: neckline,
          targetPrice: target,
          confirmed,
          triggerTime: p3.time
        });
      }
    }
    if (p1.type === "LOW" && p2.type === "HIGH" && p3.type === "LOW") {
      const btmDiff = Math.abs(p1.price - p3.price) / p1.price;
      const height = p2.price - (p1.price + p3.price) / 2;
      if (btmDiff <= 0.012 && height > 0) {
        const neckline = p2.price;
        const target = neckline + height;
        const confirmed = lastPrice > neckline;
        patterns.push({
          id: `geo_dbtm_${p1.time}_${p3.time}`,
          type: "geometric",
          subType: "double_bottom",
          name: "Double Bottom (W)",
          label: "DOUBLE BOTTOM (W)",
          bias: "bullish",
          points: [
            { time: p0.time, price: p0.price, label: "Start" },
            { time: p1.time, price: p1.price, label: "Btm 1" },
            { time: p2.time, price: p2.price, label: "Neck" },
            { time: p3.time, price: p3.price, label: "Btm 2" }
          ],
          necklinePrice: neckline,
          targetPrice: target,
          confirmed,
          triggerTime: p3.time
        });
      }
    }
    if (p5 && i + 5 < n) {
      if (p1.type === "HIGH" && p2.type === "LOW" && p3.type === "HIGH" && p4.type === "LOW" && p5.type === "HIGH") {
        const diff1 = Math.abs(p1.price - p3.price) / p1.price;
        const diff2 = Math.abs(p3.price - p5.price) / p3.price;
        if (diff1 <= 0.012 && diff2 <= 0.012) {
          const avgNeckline = (p2.price + p4.price) / 2;
          const avgTop = (p1.price + p3.price + p5.price) / 3;
          patterns.push({
            id: `geo_ttop_${p5.time}`,
            type: "geometric",
            subType: "triple_top",
            name: "Triple Top",
            label: "TRIPLE TOP",
            bias: "bearish",
            points: [
              { time: p1.time, price: p1.price, label: "Top 1" },
              { time: p2.time, price: p2.price, label: "Neck 1" },
              { time: p3.time, price: p3.price, label: "Top 2" },
              { time: p4.time, price: p4.price, label: "Neck 2" },
              { time: p5.time, price: p5.price, label: "Top 3" }
            ],
            necklinePrice: avgNeckline,
            targetPrice: avgNeckline - (avgTop - avgNeckline),
            confirmed: lastPrice < avgNeckline,
            triggerTime: p5.time
          });
        }
      }
      if (p1.type === "LOW" && p2.type === "HIGH" && p3.type === "LOW" && p4.type === "HIGH" && p5.type === "LOW") {
        const diff1 = Math.abs(p1.price - p3.price) / p1.price;
        const diff2 = Math.abs(p3.price - p5.price) / p3.price;
        if (diff1 <= 0.012 && diff2 <= 0.012) {
          const avgNeckline = (p2.price + p4.price) / 2;
          const avgBtm = (p1.price + p3.price + p5.price) / 3;
          patterns.push({
            id: `geo_tbtm_${p5.time}`,
            type: "geometric",
            subType: "triple_bottom",
            name: "Triple Bottom",
            label: "TRIPLE BOTTOM",
            bias: "bullish",
            points: [
              { time: p1.time, price: p1.price, label: "Btm 1" },
              { time: p2.time, price: p2.price, label: "Neck 1" },
              { time: p3.time, price: p3.price, label: "Btm 2" },
              { time: p4.time, price: p4.price, label: "Neck 2" },
              { time: p5.time, price: p5.price, label: "Btm 3" }
            ],
            necklinePrice: avgNeckline,
            targetPrice: avgNeckline + (avgNeckline - avgBtm),
            confirmed: lastPrice > avgNeckline,
            triggerTime: p5.time
          });
        }
      }
    }
    if (p4 && i + 4 < n) {
      if (p0.type === "HIGH" && p1.type === "LOW" && p2.type === "HIGH" && p3.type === "LOW" && p4.type === "HIGH") {
        const headHigher = p2.price > p0.price && p2.price > p4.price;
        const shoulderDiff = Math.abs(p0.price - p4.price) / p0.price;
        if (headHigher && shoulderDiff <= 0.025) {
          const avgNeckline = (p1.price + p3.price) / 2;
          const target = avgNeckline - (p2.price - avgNeckline);
          patterns.push({
            id: `geo_hs_${p2.time}`,
            type: "geometric",
            subType: "head_and_shoulders",
            name: "Head & Shoulders",
            label: "HEAD & SHOULDERS",
            bias: "bearish",
            points: [
              { time: p0.time, price: p0.price, label: "LS" },
              { time: p1.time, price: p1.price, label: "V1" },
              { time: p2.time, price: p2.price, label: "HEAD" },
              { time: p3.time, price: p3.price, label: "V2" },
              { time: p4.time, price: p4.price, label: "RS" }
            ],
            necklinePrice: avgNeckline,
            targetPrice: target,
            confirmed: lastPrice < avgNeckline,
            triggerTime: p4.time
          });
        }
      }
      if (p0.type === "LOW" && p1.type === "HIGH" && p2.type === "LOW" && p3.type === "HIGH" && p4.type === "LOW") {
        const headLower = p2.price < p0.price && p2.price < p4.price;
        const shoulderDiff = Math.abs(p0.price - p4.price) / p0.price;
        if (headLower && shoulderDiff <= 0.025) {
          const avgNeckline = (p1.price + p3.price) / 2;
          const target = avgNeckline + (avgNeckline - p2.price);
          patterns.push({
            id: `geo_ihs_${p2.time}`,
            type: "geometric",
            subType: "inv_head_and_shoulders",
            name: "Inverse Head & Shoulders",
            label: "INV HEAD & SHOULDERS",
            bias: "bullish",
            points: [
              { time: p0.time, price: p0.price, label: "LS" },
              { time: p1.time, price: p1.price, label: "P1" },
              { time: p2.time, price: p2.price, label: "HEAD" },
              { time: p3.time, price: p3.price, label: "P2" },
              { time: p4.time, price: p4.price, label: "RS" }
            ],
            necklinePrice: avgNeckline,
            targetPrice: target,
            confirmed: lastPrice > avgNeckline,
            triggerTime: p4.time
          });
        }
      }
    }
  }
  for (let i = 2; i < candles.length - 1; i++) {
    const cPrev = candles[i - 1];
    const cCurr = candles[i];
    const cNext = candles[i + 1];
    const drop = cPrev.open - cCurr.low;
    const rally = cNext.close - cCurr.low;
    if (drop >= currentATR * 2 && rally >= currentATR * 1.8 && cCurr.low === Math.min(cPrev.low, cCurr.low, cNext.low)) {
      patterns.push({
        id: `geo_vbtm_${cCurr.time}`,
        type: "geometric",
        subType: "v_reversal",
        name: "V-Bottom Reversal",
        label: "\u26A1 V-BOTTOM",
        bias: "bullish",
        points: [
          { time: cPrev.time, price: cPrev.open },
          { time: cCurr.time, price: cCurr.low },
          { time: cNext.time, price: cNext.close }
        ],
        targetPrice: cPrev.open,
        triggerTime: cCurr.time
      });
    }
    const pump = cCurr.high - cPrev.open;
    const dump = cCurr.high - cNext.close;
    if (pump >= currentATR * 2 && dump >= currentATR * 1.8 && cCurr.high === Math.max(cPrev.high, cCurr.high, cNext.high)) {
      patterns.push({
        id: `geo_vtop_${cCurr.time}`,
        type: "geometric",
        subType: "v_reversal",
        name: "V-Top Reversal",
        label: "\u26A1 V-TOP",
        bias: "bearish",
        points: [
          { time: cPrev.time, price: cPrev.open },
          { time: cCurr.time, price: cCurr.high },
          { time: cNext.time, price: cNext.close }
        ],
        targetPrice: cPrev.open,
        triggerTime: cCurr.time
      });
    }
  }
  const highs = recent.filter((p) => p.type === "HIGH");
  const lows = recent.filter((p) => p.type === "LOW");
  if (highs.length >= 3 && lows.length >= 3) {
    const h1 = highs[highs.length - 3];
    const h2 = highs[highs.length - 2];
    const h3 = highs[highs.length - 1];
    const l1 = lows[lows.length - 3];
    const l2 = lows[lows.length - 2];
    const l3 = lows[lows.length - 1];
    const highSlopeNormalized = (h3.price - h1.price) / h1.price;
    const lowSlopeNormalized = (l3.price - l1.price) / l1.price;
    if (Math.abs(highSlopeNormalized) <= 5e-3 && lowSlopeNormalized >= 8e-3) {
      patterns.push({
        id: `geo_asctri_${h3.time}`,
        type: "geometric",
        subType: "ascending_triangle",
        name: "Ascending Triangle",
        label: "\u25B2 ASCENDING TRIANGLE",
        bias: "bullish",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time)
      });
    } else if (highSlopeNormalized <= -8e-3 && Math.abs(lowSlopeNormalized) <= 5e-3) {
      patterns.push({
        id: `geo_dsctri_${h3.time}`,
        type: "geometric",
        subType: "descending_triangle",
        name: "Descending Triangle",
        label: "\u25BC DESCENDING TRIANGLE",
        bias: "bearish",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time)
      });
    } else if (highSlopeNormalized <= -6e-3 && lowSlopeNormalized >= 6e-3) {
      patterns.push({
        id: `geo_symtri_${h3.time}`,
        type: "geometric",
        subType: "symmetrical_triangle",
        name: "Symmetrical Triangle",
        label: "\u25C8 SYMMETRICAL TRIANGLE",
        bias: "neutral",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time)
      });
    } else if (highSlopeNormalized >= 8e-3 && lowSlopeNormalized <= -8e-3) {
      patterns.push({
        id: `geo_megaph_${h3.time}`,
        type: "geometric",
        subType: "broadening_wedge",
        name: "Broadening Wedge (Megaphone)",
        label: "\u{1F4E2} BROADENING WEDGE",
        bias: "neutral",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time)
      });
    } else if (highSlopeNormalized > 6e-3 && lowSlopeNormalized > highSlopeNormalized * 1.25) {
      patterns.push({
        id: `geo_rwdg_${h3.time}`,
        type: "geometric",
        subType: "rising_wedge",
        name: "Rising Wedge",
        label: "\u25BC RISING WEDGE",
        bias: "bearish",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time)
      });
    } else if (highSlopeNormalized < -6e-3 && lowSlopeNormalized < 0 && Math.abs(highSlopeNormalized) > Math.abs(lowSlopeNormalized) * 1.25) {
      patterns.push({
        id: `geo_fwdg_${h3.time}`,
        type: "geometric",
        subType: "falling_wedge",
        name: "Falling Wedge",
        label: "\u25B2 FALLING WEDGE",
        bias: "bullish",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time)
      });
    } else if (Math.abs(highSlopeNormalized) <= 3e-3 && Math.abs(lowSlopeNormalized) <= 3e-3) {
      patterns.push({
        id: `geo_rect_${h3.time}`,
        type: "geometric",
        subType: "rectangle_range",
        name: "Rectangle Range",
        label: "\u25AD RECTANGLE RANGE",
        bias: "neutral",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time)
      });
    }
  }
  if (candles.length >= 15) {
    const last15 = candles.slice(-15);
    const poleStart = last15[0];
    const poleEnd = last15[4];
    const flagSlice = last15.slice(4);
    const bullPole = poleEnd.close - poleStart.open;
    const bearPole = poleStart.open - poleEnd.close;
    if (bullPole >= currentATR * 3) {
      const flagHigh = Math.max(...flagSlice.map((c) => c.high));
      const flagLow = Math.min(...flagSlice.map((c) => c.low));
      const retracement = (flagHigh - flagLow) / bullPole;
      if (retracement <= 0.45 && last15[last15.length - 1].close >= flagHigh * 0.995) {
        patterns.push({
          id: `geo_bflag_${poleEnd.time}`,
          type: "geometric",
          subType: "bull_flag",
          name: "Bull Flag",
          label: "\u{1F6A9} BULL FLAG",
          bias: "bullish",
          points: [
            { time: poleStart.time, price: poleStart.open, label: "Pole" },
            { time: poleEnd.time, price: poleEnd.close, label: "Peak" },
            { time: last15[last15.length - 1].time, price: flagLow, label: "Flag" }
          ],
          targetPrice: poleEnd.close + bullPole,
          triggerTime: poleEnd.time
        });
      }
    } else if (bearPole >= currentATR * 3) {
      const flagHigh = Math.max(...flagSlice.map((c) => c.high));
      const flagLow = Math.min(...flagSlice.map((c) => c.low));
      const retracement = (flagHigh - flagLow) / bearPole;
      if (retracement <= 0.45 && last15[last15.length - 1].close <= flagLow * 1.005) {
        patterns.push({
          id: `geo_sflag_${poleEnd.time}`,
          type: "geometric",
          subType: "bear_flag",
          name: "Bear Flag",
          label: "\u{1F6A9} BEAR FLAG",
          bias: "bearish",
          points: [
            { time: poleStart.time, price: poleStart.open, label: "Pole" },
            { time: poleEnd.time, price: poleEnd.close, label: "Trough" },
            { time: last15[last15.length - 1].time, price: flagHigh, label: "Flag" }
          ],
          targetPrice: poleEnd.close - bearPole,
          triggerTime: poleEnd.time
        });
      }
    }
  }
  return patterns.slice(-6);
}
function detectHarmonicPatterns(pivots, { maxLookback = 25 } = {}) {
  if (!pivots || pivots.length < 5) return [];
  const harmonics = [];
  const recent = pivots.slice(-maxLookback);
  const n = recent.length;
  for (let i = 0; i <= n - 5; i++) {
    const X = recent[i];
    const A = recent[i + 1];
    const B = recent[i + 2];
    const C = recent[i + 3];
    const D = recent[i + 4];
    const isBull = X.type === "LOW" && A.type === "HIGH" && B.type === "LOW" && C.type === "HIGH" && D.type === "LOW";
    const isBear = X.type === "HIGH" && A.type === "LOW" && B.type === "HIGH" && C.type === "LOW" && D.type === "HIGH";
    if (!isBull && !isBear) continue;
    const XA = Math.abs(A.price - X.price);
    const AB = Math.abs(B.price - A.price);
    const BC = Math.abs(C.price - B.price);
    const CD = Math.abs(D.price - C.price);
    if (XA <= 0 || AB <= 0 || BC <= 0) continue;
    const rXB = AB / XA;
    const rAC = BC / AB;
    const rBD = CD / BC;
    const rXD = Math.abs(D.price - X.price) / XA;
    let matchedPattern = null;
    if (rXB >= 0.55 && rXB <= 0.68 && rAC >= 0.35 && rAC <= 0.9 && (isBull ? D.price > X.price : D.price < X.price)) {
      const gartleyXD = isBull ? (A.price - D.price) / XA : (D.price - A.price) / XA;
      if (gartleyXD >= 0.72 && gartleyXD <= 0.85) {
        matchedPattern = "Gartley";
      }
    }
    if (!matchedPattern && rXB >= 0.35 && rXB <= 0.53 && rAC >= 0.35 && rAC <= 0.9) {
      const batXD = isBull ? (A.price - D.price) / XA : (D.price - A.price) / XA;
      if (batXD >= 0.82 && batXD <= 0.95) {
        matchedPattern = "Bat";
      }
    }
    if (!matchedPattern && rXB >= 0.72 && rXB <= 0.85 && rAC >= 0.35 && rAC <= 0.9) {
      const butterflyXD = isBull ? (A.price - D.price) / XA : (D.price - A.price) / XA;
      if (butterflyXD >= 1.2 && butterflyXD <= 1.7) {
        matchedPattern = "Butterfly";
      }
    }
    if (!matchedPattern && (rXB >= 0.35 && rXB <= 0.65 || rXB >= 0.82 && rXB <= 0.92) && rAC >= 0.35 && rAC <= 0.9) {
      const crabXD = isBull ? (A.price - D.price) / XA : (D.price - A.price) / XA;
      if (crabXD >= 1.55 && crabXD <= 1.75) {
        matchedPattern = rXB > 0.75 ? "Deep Crab" : "Crab";
      }
    }
    if (!matchedPattern && rXB >= 0.35 && rXB <= 0.65) {
      const rXC = isBull ? (C.price - X.price) / XA : (X.price - C.price) / XA;
      if (rXC >= 1.1 && rXC <= 1.45) {
        matchedPattern = "Cypher";
      }
    }
    if (!matchedPattern && rAC >= 1.1 && rAC <= 1.65) {
      matchedPattern = "Shark";
    }
    if (matchedPattern) {
      const bias = isBull ? "bullish" : "bearish";
      const przSpread = XA * 0.05;
      const przLow = isBull ? D.price - przSpread : D.price;
      const przHigh = isBull ? D.price : D.price + przSpread;
      const target1 = isBull ? D.price + CD * 0.382 : D.price - CD * 0.382;
      const target2 = isBull ? D.price + CD * 0.618 : D.price - CD * 0.618;
      harmonics.push({
        id: `harm_${matchedPattern.toLowerCase().replace(/\s+/g, "_")}_${D.time}`,
        type: "harmonic",
        name: `${matchedPattern} (${bias === "bullish" ? "Bullish" : "Bearish"})`,
        label: `${matchedPattern.toUpperCase()} PRZ`,
        bias,
        points: [
          { time: X.time, price: X.price, label: "X" },
          { time: A.time, price: A.price, label: "A" },
          { time: B.time, price: B.price, label: "B" },
          { time: C.time, price: C.price, label: "C" },
          { time: D.time, price: D.price, label: "D" }
        ],
        ratios: {
          xb: rXB.toFixed(3),
          ac: rAC.toFixed(3),
          bd: rBD.toFixed(3),
          xd: rXD.toFixed(3)
        },
        prz: { low: przLow, high: przHigh },
        targets: { t1: target1, t2: target2 },
        triggerTime: D.time
      });
    }
  }
  for (let i = 0; i <= n - 4; i++) {
    const A = recent[i];
    const B = recent[i + 1];
    const C = recent[i + 2];
    const D = recent[i + 3];
    const isBull = A.type === "HIGH" && B.type === "LOW" && C.type === "HIGH" && D.type === "LOW";
    const isBear = A.type === "LOW" && B.type === "HIGH" && C.type === "LOW" && D.type === "HIGH";
    if (!isBull && !isBear) continue;
    const AB = Math.abs(B.price - A.price);
    const BC = Math.abs(C.price - B.price);
    const CD = Math.abs(D.price - C.price);
    if (AB <= 0 || BC <= 0) continue;
    const ratioABCD = CD / AB;
    const retracementBC = BC / AB;
    if (ratioABCD >= 0.9 && ratioABCD <= 1.1 && retracementBC >= 0.55 && retracementBC <= 0.85) {
      const bias = isBull ? "bullish" : "bearish";
      harmonics.push({
        id: `harm_abcd_${D.time}`,
        type: "harmonic",
        name: `AB=CD (${bias === "bullish" ? "Bullish" : "Bearish"})`,
        label: "AB=CD SETUP",
        bias,
        points: [
          { time: A.time, price: A.price, label: "A" },
          { time: B.time, price: B.price, label: "B" },
          { time: C.time, price: C.price, label: "C" },
          { time: D.time, price: D.price, label: "D" }
        ],
        ratios: {
          bc: retracementBC.toFixed(3),
          abcd: ratioABCD.toFixed(3)
        },
        targets: { t1: isBull ? D.price + CD * 0.382 : D.price - CD * 0.382 },
        triggerTime: D.time
      });
    }
  }
  return harmonics.slice(-4);
}
function detectLiquiditySetups(candles, pivots, { lookback = 60 } = {}) {
  if (!candles || candles.length < 3) return [];
  const setups = [];
  const startIdx = Math.max(3, candles.length - lookback);
  const volumeMAs = calculateVolumeMA(candles, 20);
  const recentPivots = (pivots || []).slice(-15);
  const supportPivots = recentPivots.filter((p) => p.type === "LOW");
  const resistancePivots = recentPivots.filter((p) => p.type === "HIGH");
  for (let i = startIdx; i < candles.length; i++) {
    const c = candles[i];
    const vol = Number(c.volume || 0);
    const volMA = volumeMAs[i] || 1;
    const isHighVol = vol >= volMA * 1.05 || vol === 0;
    for (const sup of supportPivots) {
      if (sup.time >= c.time) continue;
      const supLevel = sup.price;
      const dipped = c.low < supLevel;
      const sweptDepth = (supLevel - c.low) / supLevel;
      const closedInside = c.close >= supLevel && c.close > c.open;
      if (dipped && closedInside && sweptDepth <= 0.02 && isHighVol) {
        setups.push({
          id: `liq_spr_${c.time}`,
          type: "liquidity",
          subType: "wyckoff_spring",
          name: "Wyckoff Spring",
          label: "\u26A1 SPRING (SWEEP)",
          bias: "bullish",
          time: c.time,
          levelPrice: supLevel,
          sweepPrice: c.low,
          description: `Liquidity sweep below \u20B9${supLevel.toFixed(2)} with strong recovery`
        });
        break;
      }
    }
    for (const res of resistancePivots) {
      if (res.time >= c.time) continue;
      const resLevel = res.price;
      const spiked = c.high > resLevel;
      const sweptDepth = (c.high - resLevel) / resLevel;
      const closedInside = c.close <= resLevel && c.close < c.open;
      if (spiked && closedInside && sweptDepth <= 0.02 && isHighVol) {
        setups.push({
          id: `liq_utad_${c.time}`,
          type: "liquidity",
          subType: "wyckoff_upthrust",
          name: "Wyckoff Upthrust",
          label: "\u26A1 UPTHRUST (SWEEP)",
          bias: "bearish",
          time: c.time,
          levelPrice: resLevel,
          sweepPrice: c.high,
          description: `Liquidity sweep above \u20B9${resLevel.toFixed(2)} with strong rejection`
        });
        break;
      }
    }
  }
  if (recentPivots.length >= 4) {
    const p1 = recentPivots[recentPivots.length - 4];
    const p2 = recentPivots[recentPivots.length - 3];
    const p3 = recentPivots[recentPivots.length - 2];
    const p4 = recentPivots[recentPivots.length - 1];
    if (p1.type === "LOW" && p2.type === "HIGH" && p3.type === "LOW" && p4.type === "HIGH") {
      const lowerLow = p3.price < p1.price;
      const higherHigh = p4.price > p2.price;
      if (lowerLow && higherHigh) {
        setups.push({
          id: `liq_qm_bull_${p4.time}`,
          type: "liquidity",
          subType: "quasimodo",
          name: "Quasimodo Bullish (QML)",
          label: "\u{1F3AF} QML DEMAND ZONE",
          bias: "bullish",
          time: p4.time,
          levelPrice: p1.price,
          targetPrice: p4.price,
          description: `Bullish QML structure shift; buy zone at \u20B9${p1.price.toFixed(2)}`
        });
      }
    }
    if (p1.type === "HIGH" && p2.type === "LOW" && p3.type === "HIGH" && p4.type === "LOW") {
      const higherHigh = p3.price > p1.price;
      const lowerLow = p4.price < p2.price;
      if (higherHigh && lowerLow) {
        setups.push({
          id: `liq_qm_bear_${p4.time}`,
          type: "liquidity",
          subType: "quasimodo",
          name: "Quasimodo Bearish (QML)",
          label: "\u{1F3AF} QML SUPPLY ZONE",
          bias: "bearish",
          time: p4.time,
          levelPrice: p1.price,
          targetPrice: p4.price,
          description: `Bearish QML structure shift; sell zone at \u20B9${p1.price.toFixed(2)}`
        });
      }
    }
  }
  for (let i = startIdx + 2; i < candles.length; i++) {
    const c1 = candles[i - 2];
    const c2 = candles[i - 1];
    const c3 = candles[i];
    if (c2.high < c1.low && c3.low > c2.high) {
      setups.push({
        id: `liq_isl_bull_${c3.time}`,
        type: "liquidity",
        subType: "island_reversal",
        name: "Island Reversal",
        label: "\u{1F3DD}\uFE0F ISLAND REVERSAL (BUY)",
        bias: "bullish",
        time: c2.time,
        levelPrice: c2.low,
        description: "Isolated bottom island reversal"
      });
    }
    if (c2.low > c1.high && c3.high < c2.low) {
      setups.push({
        id: `liq_isl_bear_${c3.time}`,
        type: "liquidity",
        subType: "island_reversal",
        name: "Island Reversal",
        label: "\u{1F3DD}\uFE0F ISLAND REVERSAL (SELL)",
        bias: "bearish",
        time: c2.time,
        levelPrice: c2.high,
        description: "Isolated top island reversal"
      });
    }
  }
  return setups.slice(-5);
}
function detectAllPatterns(candles, {
  candlestick = true,
  geometric = true,
  harmonic = true,
  liquidity = true,
  lookback = 100
} = {}) {
  if (!candles || candles.length < 3) {
    return { candlestick: [], geometric: [], harmonic: [], liquidity: [] };
  }
  const result = {
    candlestick: [],
    geometric: [],
    harmonic: [],
    liquidity: []
  };
  if (candlestick) {
    result.candlestick = detectCandlestickPatterns(candles, { lookback });
  }
  const pivots = geometric || harmonic || liquidity ? extractPivots(candles) : [];
  if (geometric && pivots.length >= 4) {
    result.geometric = detectGeometricPatterns(candles, pivots);
  }
  if (harmonic && pivots.length >= 5) {
    result.harmonic = detectHarmonicPatterns(pivots);
  }
  if (liquidity && candles.length >= 3) {
    result.liquidity = detectLiquiditySetups(candles, pivots, { lookback });
  }
  return result;
}

// src/charts.ts
var chartColors = (element) => {
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
    accent: color("--accent", "#f0b429")
  };
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
      const match = clusters.find((cl) => Math.abs(cl.avg - p.price) / cl.avg < 6e-3);
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
    supports: supClusters.slice(0, maxLevels).map((c) => Number(c.avg.toFixed(2)))
  };
}
function calculateAutoTrendlines(candles, maxLookback = 120) {
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
  const touchTol = Math.max(6e-4, Math.min(6e-3, 1.2 / currentPrice));
  const breachTol = touchTol * 1.6;
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
      if (lastLinePrice < currentPrice * (1 - breachTol * 2)) continue;
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
      const score = totalTouches * 20 + span * 0.3 + recency * 20 - distPct * 60;
      if (score > bestResScore) {
        bestResScore = score;
        bestResistance = {
          p1: { time: p1.time, price: p1.price },
          p2: { time: p2.time, price: p2.price },
          span,
          touches: totalTouches,
          score: Number(score.toFixed(1)),
          slope: Number(slope.toFixed(4)),
          projectedPrice: Number(lastLinePrice.toFixed(2))
        };
      }
    }
  }
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
      if (lastLinePrice > currentPrice * (1 + breachTol * 2)) continue;
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
      const score = totalTouches * 20 + span * 0.3 + recency * 20 - distPct * 60;
      if (score > bestSupScore) {
        bestSupScore = score;
        bestSupport = {
          p1: { time: p1.time, price: p1.price },
          p2: { time: p2.time, price: p2.price },
          span,
          touches: totalTouches,
          score: Number(score.toFixed(1)),
          slope: Number(slope.toFixed(4)),
          projectedPrice: Number(lastLinePrice.toFixed(2))
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
      vertical: { show: true, size: 1, color: colors.grid, style: "dashed", dashedValue: [2, 2] }
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
        noChangeWickColor: colors.text
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
          text: { show: true, color: "#ffffff", backgroundColor: colors.up, size: 11 }
        }
      },
      tooltip: {
        showRule: "follow_cross",
        showType: "standard",
        text: { size: 11, color: colors.text }
      }
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
          borderRadius: 2
        }
      },
      lines: [
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#e16a2b" },
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#2196f3" },
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#9c27b0" },
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#00b4d8" }
      ],
      bars: [
        {
          style: "fill",
          upColor: `${colors.up}80`,
          downColor: `${colors.down}80`,
          noChangeColor: `${colors.text}80`
        }
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
          marginBottom: 4
        },
        legend: {
          size: 11,
          color: colors.text,
          marginLeft: 8,
          marginTop: 4,
          marginRight: 8,
          marginBottom: 4
        }
      }
    },
    xAxis: {
      axisLine: { show: true, size: 1, color: colors.border },
      tickLine: { show: true, size: 1, color: colors.border },
      tickText: { show: true, color: colors.text, size: 11 }
    },
    yAxis: {
      axisLine: { show: true, size: 1, color: colors.border },
      tickLine: { show: true, size: 1, color: colors.border },
      tickText: { show: true, color: colors.text, size: 11 }
    },
    separator: { size: 1, color: colors.border },
    crosshair: {
      show: true,
      horizontal: {
        show: true,
        line: { show: true, style: "dashed", dashedValue: [4, 2], size: 1, color: colors.crosshair },
        text: { show: true, color: "#ffffff", backgroundColor: colors.crosshair, size: 11 }
      },
      vertical: {
        show: true,
        line: { show: true, style: "dashed", dashedValue: [4, 2], size: 1, color: colors.crosshair },
        text: { show: true, color: "#ffffff", backgroundColor: colors.crosshair, size: 11 }
      }
    },
    overlay: {
      line: { color: colors.crosshair, size: 1.5 },
      point: { color: colors.up, radius: 4 }
    }
  };
}
function parseTimestamp(ts) {
  if (!ts) return 0;
  if (typeof ts === "number") return ts < 1e11 ? ts * 1e3 : ts;
  const numVal = Number(ts);
  if (Number.isFinite(numVal) && numVal > 0) return numVal < 1e11 ? numVal * 1e3 : numVal;
  const str = String(ts).trim();
  const m = str.match(/^(\d{1,2})[-\/]([A-Za-z]{3}|\d{1,2})[-\/](\d{4})(?:\s+(\d{1,2}):(\d{2})(?::(\d{2}))?)?/);
  if (m) {
    const months = { jan: 0, feb: 1, mar: 2, apr: 3, may: 4, jun: 5, jul: 6, aug: 7, sep: 8, oct: 9, nov: 10, dec: 11 };
    const day = parseInt(m[1], 10);
    const mon = isNaN(Number(m[2])) ? months[m[2].toLowerCase()] ?? 0 : parseInt(m[2], 10) - 1;
    const year = parseInt(m[3], 10);
    const hour = parseInt(m[4] || "0", 10);
    const min = parseInt(m[5] || "0", 10);
    const sec = parseInt(m[6] || "0", 10);
    return new Date(year, mon, day, hour, min, sec).getTime();
  }
  const d = new Date(str);
  return Number.isNaN(d.getTime()) ? 0 : d.getTime();
}
function pairTrades(trades) {
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
      const pnlPct = entryPx > 0 ? oppIsBuy ? (exitPx - entryPx) / entryPx * 100 : (entryPx - exitPx) / entryPx * 100 : 0;
      pairs.push({
        entry,
        exit,
        qty: matchQty,
        pnl,
        pnlPct
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
  const tsSec = timestampMs > 1e11 ? Math.floor(timestampMs / 1e3) : timestampMs;
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
function formatDOMQty(q) {
  const n = Number(q || 0);
  if (n <= 0) return "";
  if (n >= 1e7) return `${(n / 1e7).toFixed(1)}Cr`;
  if (n >= 1e5) return `${(n / 1e5).toFixed(1)}L`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return String(Math.round(n));
}
function computeDOMInterval(minPrice, maxPrice, heightPx = 500) {
  const span = maxPrice - minPrice;
  const isSpotOrLarge = minPrice > 5e3;
  if (!Number.isFinite(span) || span <= 0) return isSpotOrLarge ? 10 : 5;
  const desiredRows = Math.max(6, Math.min(20, Math.floor(heightPx / 28)));
  const rawStep = span / desiredRows;
  const candidateSteps = isSpotOrLarge ? [5, 10, 20, 25, 50, 100, 200, 500] : [0.5, 1, 2, 2.5, 5, 10, 20, 25, 50, 100];
  for (const step of candidateSteps) {
    if (step >= rawStep) {
      return step;
    }
  }
  return candidateSteps[candidateSteps.length - 1];
}
function calculateOrderFlowBuckets(candles, depthData, chainData, minPrice, maxPrice, step) {
  const buckets = /* @__PURE__ */ new Map();
  const startP = Math.floor((minPrice - step) / step) * step;
  const endP = Math.ceil((maxPrice + step) / step) * step;
  for (let p = startP; p <= endP + 1e-6; p = Math.round((p + step) * 100) / 100) {
    const key = Math.round(p * 100) / 100;
    buckets.set(key, {
      price: key,
      buyVol: 0,
      sellVol: 0,
      buyOrders: 0,
      sellOrders: 0
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
    const cBuyVol = vol > 0 ? vol * buyRatio : buyRatio >= 0.5 ? Math.round(buyRatio * 25) : Math.round(buyRatio * 8);
    const cSellVol = vol > 0 ? vol * (1 - buyRatio) : buyRatio < 0.5 ? Math.round((1 - buyRatio) * 25) : Math.round((1 - buyRatio) * 8);
    const cBuyOrd = buyRatio >= 0.5 ? Math.max(1, Math.round(buyRatio * 6)) : 1;
    const cSellOrd = 1 - buyRatio >= 0.5 ? Math.max(1, Math.round((1 - buyRatio) * 6)) : 1;
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
      const spotBull = callVol + putOI * 0.1;
      const spotBear = putVol + callOI * 0.1;
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
var overlaysRegistered = false;
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
              { x: coord.x + 5, y: baseY }
            ]
          },
          styles: { style: "fill", color },
          ignoreEvent: true
        });
        const label = data.text || (arrowUp ? "\u25B2 BUY" : "\u25BC SELL");
        figures.push({
          type: "text",
          attrs: {
            x: coord.x,
            y: textY,
            text: label,
            align: "center",
            baseline
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
            borderRadius: 3
          },
          ignoreEvent: true
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
              { x: coord.x + 4.5, y: baseY }
            ]
          },
          styles: { style: "fill", color },
          ignoreEvent: true
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
            baseline
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
            borderRadius: 3
          },
          ignoreEvent: true
        });
      }
      return figures;
    }
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
      if (data.buyVol != null || data.sellVol != null) {
        const buyVol = Number(data.buyVol || 0);
        const sellVol = Number(data.sellVol || 0);
        const maxVol = Number(data.maxVol || Math.max(buyVol, sellVol, 1));
        const panelWidth = Math.min(240, Math.max(150, Math.round(width * 0.28)));
        const xRight = width - 4;
        const xLeft = xRight - panelWidth;
        const xCenter = Math.round(xLeft + panelWidth * 0.5);
        const halfWidth = Math.round((panelWidth - 14) * 0.48);
        const barHeight2 = 14;
        const yTop2 = y - barHeight2 / 2;
        const yBottom2 = y + barHeight2 / 2;
        const figures2 = [];
        figures2.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: xLeft, y: yTop2 },
              { x: xRight, y: yTop2 },
              { x: xRight, y: yBottom2 },
              { x: xLeft, y: yBottom2 }
            ]
          },
          styles: { style: "fill", color: "rgba(15, 23, 42, 0.45)" },
          ignoreEvent: true
        });
        figures2.push({
          type: "line",
          attrs: {
            coordinates: [
              { x: xCenter, y: yTop2 - 1 },
              { x: xCenter, y: yBottom2 + 1 }
            ]
          },
          styles: { style: "solid", size: 1.5, color: "rgba(255, 255, 255, 0.22)" },
          ignoreEvent: true
        });
        if (buyVol > 0) {
          const buyRatio = Math.max(0.05, Math.min(1, buyVol / maxVol));
          const buyBarW = Math.max(6, Math.round(buyRatio * halfWidth));
          const xBuyRight = xCenter - 3;
          const xBuyLeft = xBuyRight - buyBarW;
          figures2.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: xBuyLeft, y: yTop2 },
                { x: xBuyRight, y: yTop2 },
                { x: xBuyRight, y: yBottom2 },
                { x: xBuyLeft, y: yBottom2 }
              ]
            },
            styles: { style: "fill", color: "rgba(34, 197, 94, 0.35)" },
            ignoreEvent: true
          });
          figures2.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: xBuyLeft, y: yTop2 },
                { x: xBuyLeft, y: yBottom2 }
              ]
            },
            styles: { style: "solid", size: 2.5, color: "#22c55e" },
            ignoreEvent: true
          });
          const buyText = formatDOMQty(buyVol);
          figures2.push({
            type: "text",
            attrs: {
              x: xBuyRight - 4,
              y,
              text: buyText,
              align: "right",
              baseline: "middle"
            },
            styles: {
              color: "#86efac",
              size: 9,
              weight: "bold",
              family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
            },
            ignoreEvent: true
          });
        }
        if (sellVol > 0) {
          const sellRatio = Math.max(0.05, Math.min(1, sellVol / maxVol));
          const sellBarW = Math.max(6, Math.round(sellRatio * halfWidth));
          const xSellLeft = xCenter + 3;
          const xSellRight = xSellLeft + sellBarW;
          figures2.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: xSellLeft, y: yTop2 },
                { x: xSellRight, y: yTop2 },
                { x: xSellRight, y: yBottom2 },
                { x: xSellLeft, y: yBottom2 }
              ]
            },
            styles: { style: "fill", color: "rgba(239, 68, 68, 0.35)" },
            ignoreEvent: true
          });
          figures2.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: xSellRight, y: yTop2 },
                { x: xSellRight, y: yBottom2 }
              ]
            },
            styles: { style: "solid", size: 2.5, color: "#ef4444" },
            ignoreEvent: true
          });
          const sellText = formatDOMQty(sellVol);
          figures2.push({
            type: "text",
            attrs: {
              x: xSellLeft + 4,
              y,
              text: sellText,
              align: "left",
              baseline: "middle"
            },
            styles: {
              color: "#fca5a5",
              size: 9,
              weight: "bold",
              family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
            },
            ignoreEvent: true
          });
        }
        return figures2;
      }
      const isBuy = data.side === "BUY" || data.side === "B";
      const orders = Number(data.orders || 0);
      const qty = Number(data.quantity || 0);
      const maxQty = Number(data.maxQty || qty || 1);
      const baseColor = isBuy ? "#29b360" : "#f05a5a";
      const fillColor = isBuy ? "rgba(41, 179, 96, 0.35)" : "rgba(240, 90, 90, 0.35)";
      const barHeight = 16;
      const ratio = Math.max(0.08, Math.min(1, qty / (maxQty || 1)));
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
            { x: x1, y: yBottom }
          ]
        },
        styles: { style: "fill", color: fillColor },
        ignoreEvent: true
      });
      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: x1, y: yTop },
            { x: x1, y: yBottom }
          ]
        },
        styles: { style: "solid", size: 2.5, color: baseColor },
        ignoreEvent: true
      });
      const orderLabel = orders > 0 ? `${orders}o \xB7 ` : "";
      const qtyLabel = formatDOMQty(qty) || String(qty);
      const label = `${orderLabel}${qtyLabel}`;
      figures.push({
        type: "text",
        attrs: {
          x: x2 - 5,
          y,
          text: label,
          align: "right",
          baseline: "middle"
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
          family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
        },
        ignoreEvent: true
      });
      return figures;
    }
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
            { x: width, y }
          ]
        },
        styles: {
          style: "dashed",
          dashedValue: [5, 4],
          size: 1.5,
          color: lineColor
        },
        ignoreEvent: true
      });
      const qty = Number(data.qty || 0);
      const qtySign = qty > 0 ? "+" : "";
      const pnlSign = pnl >= 0 ? "+\u20B9" : "-\u20B9";
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
          baseline: "middle"
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
          borderRadius: 3
        },
        ignoreEvent: true
      });
      return figures;
    }
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
      if (!c1 || !c2 || !Number.isFinite(c1.x) || !Number.isFinite(c1.y) || !Number.isFinite(c2.x) || !Number.isFinite(c2.y)) return [];
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
            { x: c2.x, y: c2.y }
          ]
        },
        styles: {
          style: "dashed",
          dashedValue: [3, 3],
          size: 1.2,
          color
        },
        ignoreEvent: true
      });
      const pnlSign = pnl >= 0 ? "+\u20B9" : "-\u20B9";
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
          baseline: "middle"
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
          borderRadius: 3
        },
        ignoreEvent: true
      });
      return figures;
    }
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
            { x: width, y }
          ]
        },
        styles: {
          style: "dashed",
          dashedValue: [5, 3],
          size: 1.5,
          color: accent
        },
        ignoreEvent: true
      });
      figures.push({
        type: "text",
        attrs: {
          x: width - 8,
          y,
          text: `LIMIT: \u20B9${price.toFixed(2)}`,
          align: "right",
          baseline: "middle"
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
          borderRadius: 3
        },
        ignoreEvent: true
      });
      return figures;
    }
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
            { x: width, y }
          ]
        },
        styles: {
          style: "dashed",
          dashedValue: [4, 3],
          size: 1.5,
          color: green
        },
        ignoreEvent: true
      });
      const label = data.label || `TARGET: \u20B9${price.toFixed(2)}`;
      figures.push({
        type: "text",
        attrs: {
          x: width - 8,
          y,
          text: label,
          align: "right",
          baseline: "middle"
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
          borderRadius: 3
        },
        ignoreEvent: true
      });
      return figures;
    }
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
            { x: width, y }
          ]
        },
        styles: {
          style: "dashed",
          dashedValue: [5, 3],
          size: 1.5,
          color
        },
        ignoreEvent: true
      });
      const sideStr = isBuy ? "BUY" : "SELL";
      const qty = data.qty ? `${data.qty} ` : "";
      const priceStr = Number(data.price || 0).toFixed(2);
      const tagText = `OPEN ${sideStr}: ${qty}@ \u20B9${priceStr}`;
      figures.push({
        type: "text",
        attrs: {
          x: width - 8,
          y,
          text: tagText,
          align: "right",
          baseline: "middle"
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
          borderRadius: 3
        },
        ignoreEvent: true
      });
      return figures;
    }
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
      const bgColor = isBull ? "#166534" : isBear ? "#991b1b" : "#92400e";
      const arrowColor = isBull ? "#22c55e" : isBear ? "#ef4444" : "#f59e0b";
      const figures = [];
      if (isBull) {
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: coord.y + 4 },
              { x: coord.x - 4, y: coord.y + 11 },
              { x: coord.x + 4, y: coord.y + 11 }
            ]
          },
          styles: { style: "fill", color: arrowColor },
          ignoreEvent: true
        });
      } else if (isBear) {
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: coord.y - 4 },
              { x: coord.x - 4, y: coord.y - 11 },
              { x: coord.x + 4, y: coord.y - 11 }
            ]
          },
          styles: { style: "fill", color: arrowColor },
          ignoreEvent: true
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
          baseline
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
          borderRadius: 3
        },
        ignoreEvent: true
      });
      return figures;
    }
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
      const accentColor = isBull ? "#22c55e" : isBear ? "#ef4444" : "#38bdf8";
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
                { x: s1.x, y: s1.y }
              ]
            },
            styles: {
              style: "fill",
              color: isBull ? "rgba(34, 197, 94, 0.08)" : isBear ? "rgba(239, 68, 68, 0.08)" : "rgba(56, 189, 248, 0.08)"
            },
            ignoreEvent: true
          });
          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: r1.x, y: r1.y }, { x: r2.x, y: r2.y }] },
            styles: { style: "dashed", dashedValue: [6, 3], size: 1.6, color: "#f05a5a" },
            ignoreEvent: true
          });
          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: s1.x, y: s1.y }, { x: s2.x, y: s2.y }] },
            styles: { style: "dashed", dashedValue: [6, 3], size: 1.6, color: "#29b360" },
            ignoreEvent: true
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
              baseline: "middle"
            },
            styles: {
              color: "#ffffff",
              backgroundColor: isBull ? "#166534" : isBear ? "#991b1b" : "#0284c7",
              size: 9.5,
              weight: "bold",
              family: "system-ui, -apple-system, sans-serif",
              paddingLeft: 5,
              paddingRight: 5,
              paddingTop: 2,
              paddingBottom: 2,
              borderRadius: 3
            },
            ignoreEvent: true
          });
          return figures;
        }
      }
      const lineCoords = coordinates.filter((c) => c && Number.isFinite(c.x) && Number.isFinite(c.y));
      if (lineCoords.length >= 2) {
        figures.push({
          type: "line",
          attrs: { coordinates: lineCoords.map((c) => ({ x: c.x, y: c.y })) },
          styles: { style: "solid", size: 1.6, color: accentColor },
          ignoreEvent: true
        });
        for (let i = 0; i < lineCoords.length; i++) {
          const pt = lineCoords[i];
          const ptData = data.points?.[i] || {};
          figures.push({
            type: "circle",
            attrs: { x: pt.x, y: pt.y, r: 3 },
            styles: { style: "fill", color: accentColor, borderColor: "#ffffff", borderSize: 1 },
            ignoreEvent: true
          });
          if (ptData.label) {
            figures.push({
              type: "text",
              attrs: {
                x: pt.x,
                y: pt.y - 7,
                text: ptData.label,
                align: "center",
                baseline: "bottom"
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
                borderRadius: 2
              },
              ignoreEvent: true
            });
          }
        }
        const lastPt = lineCoords[lineCoords.length - 1];
        const targetStr = data.targetPrice ? ` \u{1F3AF} \u20B9${Number(data.targetPrice).toFixed(1)}` : "";
        figures.push({
          type: "text",
          attrs: {
            x: lastPt.x + 8,
            y: lastPt.y,
            text: `${data.label}${targetStr}`,
            align: "left",
            baseline: "middle"
          },
          styles: {
            color: "#ffffff",
            backgroundColor: isBull ? "#166534" : isBear ? "#991b1b" : "#0284c7",
            size: 9.5,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 5,
            paddingRight: 5,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3
          },
          ignoreEvent: true
        });
      }
      return figures;
    }
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
            ignoreEvent: true
          });
          figures.push({
            type: "polygon",
            attrs: { coordinates: [{ x: B.x, y: B.y }, { x: C.x, y: C.y }, { x: D.x, y: D.y }] },
            styles: { style: "fill", color: fillBCD },
            ignoreEvent: true
          });
          figures.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: X.x, y: X.y },
                { x: A.x, y: A.y },
                { x: B.x, y: B.y },
                { x: C.x, y: C.y },
                { x: D.x, y: D.y }
              ]
            },
            styles: { style: "solid", size: 1.5, color: lineColor },
            ignoreEvent: true
          });
          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: X.x, y: X.y }, { x: B.x, y: B.y }] },
            styles: { style: "dashed", dashedValue: [3, 3], size: 1, color: "rgba(255,255,255,0.4)" },
            ignoreEvent: true
          });
          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: A.x, y: A.y }, { x: C.x, y: C.y }] },
            styles: { style: "dashed", dashedValue: [3, 3], size: 1, color: "rgba(255,255,255,0.4)" },
            ignoreEvent: true
          });
          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: X.x, y: X.y }, { x: D.x, y: D.y }] },
            styles: { style: "dashed", dashedValue: [3, 3], size: 1, color: "rgba(255,255,255,0.4)" },
            ignoreEvent: true
          });
          const labels = ["X", "A", "B", "C", "D"];
          const pts = [X, A, B, C, D];
          for (let i = 0; i < 5; i++) {
            figures.push({
              type: "circle",
              attrs: { x: pts[i].x, y: pts[i].y, r: 3.5 },
              styles: { style: "fill", color: lineColor, borderColor: "#ffffff", borderSize: 1 },
              ignoreEvent: true
            });
            figures.push({
              type: "text",
              attrs: {
                x: pts[i].x,
                y: pts[i].y - 8,
                text: labels[i],
                align: "center",
                baseline: "bottom"
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
                borderRadius: 2
              },
              ignoreEvent: true
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
                baseline: "middle"
              },
              styles: {
                color: "#fbbf24",
                backgroundColor: "rgba(15, 23, 42, 0.85)",
                size: 8,
                weight: "bold",
                paddingLeft: 2,
                paddingRight: 2
              },
              ignoreEvent: true
            });
          }
          const przText = data.prz ? `PRZ: \u20B9${data.prz.low.toFixed(1)} - \u20B9${data.prz.high.toFixed(1)}` : "PRZ";
          figures.push({
            type: "text",
            attrs: {
              x: D.x + 8,
              y: D.y,
              text: `\u{1F3AF} ${data.name.toUpperCase()} [${przText}]`,
              align: "left",
              baseline: "middle"
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
              borderRadius: 3
            },
            ignoreEvent: true
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
                { x: D.x, y: D.y }
              ]
            },
            styles: { style: "solid", size: 1.5, color: lineColor },
            ignoreEvent: true
          });
          figures.push({
            type: "text",
            attrs: {
              x: D.x + 8,
              y: D.y,
              text: `\u{1F3AF} ${data.name}`,
              align: "left",
              baseline: "middle"
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
              borderRadius: 3
            },
            ignoreEvent: true
          });
        }
      }
      return figures;
    }
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
            { x: Math.min(width, coord.x + 70), y: coord.y }
          ]
        },
        styles: {
          style: "dashed",
          dashedValue: [4, 3],
          size: 1.5,
          color
        },
        ignoreEvent: true
      });
      const label = data.label || data.name || "LIQUIDITY SETUP";
      figures.push({
        type: "text",
        attrs: {
          x: coord.x,
          y: isBull ? coord.y + 14 : coord.y - 14,
          text: label,
          align: "center",
          baseline: isBull ? "top" : "bottom"
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
          borderRadius: 3
        },
        ignoreEvent: true
      });
      return figures;
    }
  });
}
function registerCustomIndicators() {
  const kc = window.klinecharts;
  if (!kc || !kc.registerIndicator) return;
  function createVolumeFigure() {
    return {
      key: "volume",
      title: null,
      // Omit from top header legend so it never moves below into a second row
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
      }
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
      createVolumeFigure()
    ],
    regenerateFigures: function(calcParams) {
      const figs = (calcParams || []).map((p, idx) => ({
        key: `ma${idx + 1}`,
        title: `MA${p}: `,
        type: "line"
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
          close: candle?.close
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
    }
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
      const yAxisIds = /* @__PURE__ */ new Set([axis.id]);
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
          const x2 = isFromZero ? 0 : bounding.width;
          const align = isFromZero ? "left" : "right";
          const lastBar = dataList[lastIdx];
          const isUp = lastBar && lastBar.close >= lastBar.open;
          let bgColor = fig.key === "volume" ? isUp ? styles.bars?.[0]?.upColor || "#35c46a" : styles.bars?.[0]?.downColor || "#ff5d5d" : styles.lines?.[figIdx]?.color || ind.styles?.lines?.[figIdx]?.color || "#2196f3";
          if (typeof bgColor === "string" && bgColor.startsWith("#") && bgColor.length === 9) {
            bgColor = bgColor.slice(0, 7);
          }
          const figure = this.createFigure({
            name: "text",
            attrs: { x: x2, y, text, align, baseline: "middle" },
            styles: Object.assign({}, textStyle, { backgroundColor: bgColor })
          });
          if (figure) figure.draw(ctx);
        });
      });
    };
  } catch (err) {
    console.warn("patchIndicatorLastValueView error:", err);
  }
}
var CandleChart = class {
  constructor(container, { showVolume = true, showSR = false, showTrades = true, showAutoTrendlines = false, showPatterns = false, showMA = false, showDepth = true } = {}) {
    this.container = container;
    this.data = [];
    this.showVolume = showVolume;
    this.showSR = showSR;
    this.showTrades = showTrades;
    this.showAutoTrendlines = showAutoTrendlines;
    this.showPatterns = showPatterns;
    this.showMA = Boolean(showMA);
    this.showDepth = Boolean(showDepth);
    this.depthOverlayIds = [];
    this.depthData = null;
    this.chainData = [];
    this._depthTimer = null;
    this.patternConfig = { candlestick: true, geometric: true, harmonic: true, liquidity: true };
    this.srOverlayIds = [];
    this.autoTrendlineOverlayIds = [];
    this.patternOverlayIds = [];
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
    registerCustomIndicators();
    this.chart = kc.init(container, {
      styles: buildStyles(this.colors)
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
      if (indicator.name === "MA" && paneOptions && paneOptions.id && !indicator.paneId) {
        indicator.paneId = paneOptions.id;
      }
      return origCreateIndicator(indicator, isStack, paneOptions, callback);
    };
    const origRemoveIndicator = this.chart.removeIndicator.bind(this.chart);
    this.chart.removeIndicator = (filter, name) => {
      if (typeof filter === "string" && typeof name === "string") {
        return origRemoveIndicator({ paneId: filter, name });
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
      }
    });
    if (showVolume) {
      try {
        this.chart.createIndicator("VOL", true, { id: "candle_pane" });
      } catch (e) {
        console.warn("createIndicator VOL:", e);
      }
    }
    if (this.showMA) {
      try {
        this.chart.createIndicator(
          { name: "MA", calcParams: [9, 21, 50], paneId: "candle_pane" },
          true,
          { id: "candle_pane" }
        );
      } catch (e) {
        console.warn("createIndicator MA:", e);
      }
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
    } catch (_) {
    }
  }
  formatCandle(c) {
    if (!c || !Number.isFinite(c.time)) return null;
    const ts = c.time > 1e11 ? c.time : c.time * 1e3;
    let o = Number(c.open);
    let h = Number(c.high);
    let l = Number(c.low);
    let cl = Number(c.close);
    if (!Number.isFinite(o) || !Number.isFinite(h) || !Number.isFinite(l) || !Number.isFinite(cl)) return null;
    if (o <= 0 && h <= 0 && l <= 0 && cl <= 0) return null;
    if (l <= 0) {
      const pos = [o, h, cl].filter((v) => v > 0);
      l = pos.length ? Math.min(...pos) : 0;
    }
    if (o <= 0) o = l > 0 ? l : cl;
    if (cl <= 0) cl = o > 0 ? o : l;
    if (h <= 0 || h < Math.max(o, l, cl)) h = Math.max(o, l, cl);
    if (l <= 0 || o <= 0 || cl <= 0 || h <= 0) return null;
    return {
      timestamp: ts,
      open: o,
      high: Math.max(h, o, cl),
      low: Math.min(l, o, cl),
      close: cl,
      volume: Number.isFinite(c.volume) && c.volume >= 0 ? Number(c.volume) : 0,
      turnover: Number.isFinite(c.turnover) && c.turnover >= 0 ? Number(c.turnover) : 0,
      dayVolume: c.dayVolume
    };
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
  toggleMA(forceState) {
    this.showMA = typeof forceState === "boolean" ? forceState : !this.showMA;
    if (!this.chart) return this.showMA;
    if (this.showMA) {
      try {
        this.chart.createIndicator(
          { name: "MA", calcParams: [9, 21, 50], paneId: "candle_pane" },
          true,
          { id: "candle_pane" }
        );
      } catch (e) {
        console.warn("createIndicator MA:", e);
      }
    } else {
      try {
        this.chart.removeIndicator({ paneId: "candle_pane", name: "MA" });
      } catch (e) {
        console.warn("removeIndicator MA:", e);
      }
    }
    return this.showMA;
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
    } catch (_) {
    }
    for (const id of this.depthOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {
      }
    }
    this.depthOverlayIds = [];
  }
  getVisiblePriceRange() {
    if (!this.chart || !this.container) return null;
    let minP = Infinity;
    let maxP = -Infinity;
    const height = this.container.clientHeight || 500;
    try {
      const vr = typeof this.chart.getVisibleRange === "function" ? this.chart.getVisibleRange() : null;
      if (vr && Array.isArray(this.data) && this.data.length > 0) {
        const from = Math.max(0, Math.floor(vr.from != null ? vr.from : 0));
        const to = Math.min(this.data.length - 1, Math.ceil(vr.to != null ? vr.to : this.data.length - 1));
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
    } catch (_) {
    }
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
      if (b.price <= 0 || b.buyVol <= 0 && b.sellVol <= 0) continue;
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
            step
          }
        });
        if (id) this.depthOverlayIds.push(id);
      } catch (_) {
      }
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
    } catch (_) {
    }
    for (const id of this.srOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {
      }
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
            text: { color: this.colors.resistance, size: 10 }
          }
        });
        if (id) this.srOverlayIds.push(id);
      } catch (_) {
      }
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
            text: { color: this.colors.support, size: 10 }
          }
        });
        if (id) this.srOverlayIds.push(id);
      } catch (_) {
      }
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
    } catch (_) {
    }
    for (const id of this.autoTrendlineOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {
      }
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
            { timestamp: resistance.p1.time > 1e11 ? resistance.p1.time : resistance.p1.time * 1e3, value: resistance.p1.price },
            { timestamp: resistance.p2.time > 1e11 ? resistance.p2.time : resistance.p2.time * 1e3, value: resistance.p2.price }
          ],
          lock: true,
          styles: {
            line: {
              color: this.colors.resistance,
              style: "dashed",
              dashedValue: [6, 3],
              size: 1.5
            },
            point: {
              color: this.colors.resistance,
              borderColor: "#ffffff",
              borderSize: 1,
              radius: 3
            }
          }
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
            { timestamp: support.p1.time > 1e11 ? support.p1.time : support.p1.time * 1e3, value: support.p1.price },
            { timestamp: support.p2.time > 1e11 ? support.p2.time : support.p2.time * 1e3, value: support.p2.price }
          ],
          lock: true,
          styles: {
            line: {
              color: this.colors.support,
              style: "dashed",
              dashedValue: [6, 3],
              size: 1.5
            },
            point: {
              color: this.colors.support,
              borderColor: "#ffffff",
              borderSize: 1,
              radius: 3
            }
          }
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
    } catch (_) {
    }
    for (const id of this.patternOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {
      }
    }
    this.patternOverlayIds = [];
  }
  refreshPatterns() {
    this.clearPatterns();
    if (!this.showPatterns || !this.data.length || !this.chart) return;
    const detected = detectAllPatterns(this.data, {
      candlestick: this.patternConfig.candlestick !== false,
      geometric: this.patternConfig.geometric !== false,
      harmonic: this.patternConfig.harmonic !== false,
      liquidity: this.patternConfig.liquidity !== false,
      lookback: 90
    });
    for (const p of detected.candlestick || []) {
      try {
        const id = this.chart.createOverlay({
          name: "candlestickPatternMarker",
          groupId: "pattern_candlestick",
          points: [{
            timestamp: p.time > 1e11 ? p.time : p.time * 1e3,
            value: p.price
          }],
          lock: true,
          extendData: p
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay candlestick:", e);
      }
    }
    for (const p of detected.geometric || []) {
      try {
        let points = [];
        if (Array.isArray(p.points)) {
          points = p.points.map((pt) => ({
            timestamp: pt.time > 1e11 ? pt.time : pt.time * 1e3,
            value: pt.price
          }));
        } else if (p.resistance && p.support) {
          points = [
            { timestamp: p.resistance.p1.time > 1e11 ? p.resistance.p1.time : p.resistance.p1.time * 1e3, value: p.resistance.p1.price },
            { timestamp: p.resistance.p2.time > 1e11 ? p.resistance.p2.time : p.resistance.p2.time * 1e3, value: p.resistance.p2.price },
            { timestamp: p.support.p1.time > 1e11 ? p.support.p1.time : p.support.p1.time * 1e3, value: p.support.p1.price },
            { timestamp: p.support.p2.time > 1e11 ? p.support.p2.time : p.support.p2.time * 1e3, value: p.support.p2.price }
          ];
        }
        if (!points.length) continue;
        const id = this.chart.createOverlay({
          name: "geometricPatternOverlay",
          groupId: "pattern_geometric",
          points,
          lock: true,
          extendData: p
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay geometric:", e);
      }
    }
    for (const p of detected.harmonic || []) {
      try {
        const points = (p.points || []).map((pt) => ({
          timestamp: pt.time > 1e11 ? pt.time : pt.time * 1e3,
          value: pt.price
        }));
        if (!points.length) continue;
        const id = this.chart.createOverlay({
          name: "harmonicPatternOverlay",
          groupId: "pattern_harmonic",
          points,
          lock: true,
          extendData: p
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay harmonic:", e);
      }
    }
    for (const p of detected.liquidity || []) {
      try {
        const id = this.chart.createOverlay({
          name: "liquidityPatternOverlay",
          groupId: "pattern_liquidity",
          points: [{
            timestamp: p.time > 1e11 ? p.time : p.time * 1e3,
            value: p.levelPrice || p.sweepPrice || 0
          }],
          lock: true,
          extendData: p
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay liquidity:", e);
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
            style: "solid"
          },
          point: {
            color: lineColor,
            borderColor: "#ffffff",
            borderSize: 1.5,
            radius: 5,
            activeColor: "#ffffff",
            activeBorderColor: lineColor,
            activeBorderSize: 2,
            activeRadius: 6
          }
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
        }
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
      } catch (_) {
      }
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
    } catch (_) {
    }
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
        groupId: "order_preview",
        points: [{ value: price }],
        lock: true,
        extendData: { price }
      });
    } catch (_) {
    }
  }
  clearOrderPreview() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "order_preview" });
    } catch (_) {
    }
    if (this.orderPreviewOverlayId) {
      try {
        this.chart.removeOverlay({ id: this.orderPreviewOverlayId });
      } catch (_) {
      }
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
        extendData: { price, label }
      });
    } catch (_) {
    }
  }
  clearTargetPreview() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "target_preview" });
    } catch (_) {
    }
    if (this.targetPreviewOverlayId) {
      try {
        this.chart.removeOverlay({ id: this.targetPreviewOverlayId });
      } catch (_) {
      }
      this.targetPreviewOverlayId = null;
    }
  }
  setData(data, { symbol = "", resetScale = true } = {}) {
    if (symbol && symbol !== this.currentSymbol) {
      this.currentSymbol = symbol;
      try {
        this.chart?.setSymbol({ ticker: symbol, pricePrecision: 2, volumePrecision: 0 });
      } catch (_) {
      }
    }
    const byTime = /* @__PURE__ */ new Map();
    if (Array.isArray(data)) {
      for (const candle of data) {
        if (!Number.isFinite(candle?.time) || !Number.isFinite(candle?.open) || !Number.isFinite(candle?.high) || !Number.isFinite(candle?.low) || !Number.isFinite(candle?.close)) continue;
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
    } catch (_) {
    }
    if (resetScale) {
      this.resetPriceScale();
      try {
        this.chart?.scrollToRealTime();
      } catch (_) {
      }
    }
    this.refreshSRLines();
    this.refreshAutoTrendlines();
    this.refreshPatterns();
    this.refreshTradeOverlays();
    this.refreshPositionOverlay();
    this.refreshOpenOrders();
    this.refreshDepth();
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
    try {
      this.chart.removeOverlay({ groupId: "open_orders" });
    } catch (_) {
    }
    for (const id of this.openOrderOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {
      }
    }
    this.openOrderOverlayIds = [];
  }
  refreshOpenOrders() {
    this.clearOpenOrders();
    if (!this.showTrades || !this.chart || !this.data.length || !this.openOrdersData.length) return;
    for (const o of this.openOrdersData) {
      const price = Number(o.price);
      if (!Number.isFinite(price) || price <= 0) continue;
      try {
        const id = this.chart.createOverlay({
          name: "openOrderLine",
          groupId: "open_orders",
          points: [{ value: price }],
          lock: true,
          extendData: {
            orderId: o.id,
            side: o.side,
            qty: o.quantity - (Number(o.filled_quantity) || 0),
            price
          }
        });
        if (id) this.openOrderOverlayIds.push(id);
      } catch (_) {
      }
    }
  }
  toggleTrades(forceState) {
    this.showTrades = typeof forceState === "boolean" ? forceState : !this.showTrades;
    this.refreshTradeOverlays();
    this.refreshPositionOverlay();
    this.refreshOpenOrders();
    return this.showTrades;
  }
  clearTradeOverlays() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "trades" });
    } catch (_) {
    }
    for (const id of this.tradeOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {
      }
    }
    this.tradeOverlayIds = [];
  }
  clearPositionOverlay() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "position" });
    } catch (_) {
    }
    if (this.positionOverlayId) {
      try {
        this.chart.removeOverlay({ id: this.positionOverlayId });
      } catch (_) {
      }
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
        const optText = trade.strike ? `${trade.strike} ${trade.option_type || ""}`.trim() : trade.option_type || "";
        const actionText = `${sideText} ${optText}`.trim();
        try {
          const id = this.chart.createOverlay({
            name: "tradeMarker",
            groupId: "trades",
            points: [{ timestamp: candle.time > 1e11 ? candle.time : candle.time * 1e3, value: priceVal }],
            lock: true,
            extendData: {
              side: trade.side,
              isBull: isBuy,
              isSpot: true,
              text: actionText,
              qty: trade.quantity,
              price: trade.price
            }
          });
          if (id) this.tradeOverlayIds.push(id);
        } catch (_) {
        }
      }
    } else {
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
              isSpot: false
            }
          });
          if (id) this.tradeOverlayIds.push(id);
        } catch (_) {
        }
      }
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
              { timestamp: t2, value: p2 }
            ],
            lock: true,
            extendData: {
              pnl: pair.pnl,
              pnlPct: pair.pnlPct,
              qty: pair.qty
            }
          });
          if (id) this.tradeOverlayIds.push(id);
        } catch (_) {
        }
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
    const pnl = Number(pos.unrealised_pnl != null ? pos.unrealised_pnl : pos.pnl || 0);
    try {
      const id = this.chart.createOverlay({
        name: "positionLine",
        groupId: "position",
        points: [{ value: avg }],
        lock: true,
        extendData: {
          qty,
          avgPrice: avg,
          pnl
        }
      });
      if (id) this.positionOverlayId = id;
    } catch (_) {
    }
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
      if (this.showPatterns) {
        this.refreshPatterns();
      }
    }
    const formatted = this.formatCandle(candle);
    if (formatted && this.subscriberCallback) {
      this.subscriberCallback(formatted);
    }
    if (this.showDepth) {
      this.debounceDepthRefresh();
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
    this.clearOrderPreview();
    this.clearTargetPreview();
    this.clearOpenOrders();
    this.clearTradeOverlays();
    this.clearPositionOverlay();
    this.clearSRLines();
    this.clearAutoTrendlines();
    this.clearDepth();
    this.clearPatterns();
    if (this.chart && window.klinecharts) {
      window.klinecharts.dispose(this.chart);
    }
  }
};

// src/utils/qrcode.ts
var EXP = new Uint8Array(256);
var LOG = new Uint8Array(256);
var x = 1;
for (let i = 0; i < 255; i++) {
  EXP[i] = x;
  LOG[x] = i;
  x <<= 1;
  if (x & 256) x ^= 285;
}
EXP[255] = EXP[0];
function gfMul(a, b) {
  if (a === 0 || b === 0) return 0;
  return EXP[(LOG[a] + LOG[b]) % 255];
}
function polyMul(p1, p2) {
  const r = new Uint8Array(p1.length + p2.length - 1);
  for (let i = 0; i < p1.length; i++) {
    for (let j = 0; j < p2.length; j++) {
      r[i + j] ^= gfMul(p1[i], p2[j]);
    }
  }
  return r;
}
function getGenPoly(degree) {
  let g = new Uint8Array([1]);
  for (let i = 0; i < degree; i++) {
    g = polyMul(g, new Uint8Array([1, EXP[i]]));
  }
  return g;
}
function calcEC(dataBlock, ecCount) {
  const gen = getGenPoly(ecCount);
  const work = new Uint8Array(dataBlock.length + ecCount);
  work.set(dataBlock, 0);
  for (let i = 0; i < dataBlock.length; i++) {
    const coef = work[i];
    if (coef !== 0) {
      for (let j = 0; j < gen.length; j++) {
        work[i + j] ^= gfMul(gen[j], coef);
      }
    }
  }
  return work.slice(dataBlock.length);
}
var VERSIONS = [
  null,
  { v: 1, dim: 21, ec: 7, blocks: [{ count: 1, data: 19 }], align: [] },
  { v: 2, dim: 25, ec: 10, blocks: [{ count: 1, data: 34 }], align: [6, 18] },
  { v: 3, dim: 29, ec: 15, blocks: [{ count: 1, data: 55 }], align: [6, 22] },
  { v: 4, dim: 33, ec: 20, blocks: [{ count: 1, data: 80 }], align: [6, 26] },
  { v: 5, dim: 37, ec: 26, blocks: [{ count: 1, data: 108 }], align: [6, 30] },
  { v: 6, dim: 41, ec: 18, blocks: [{ count: 2, data: 68 }], align: [6, 34] },
  { v: 7, dim: 45, ec: 20, blocks: [{ count: 2, data: 78 }], align: [6, 22, 38] },
  { v: 8, dim: 49, ec: 24, blocks: [{ count: 2, data: 97 }], align: [6, 24, 42] },
  { v: 9, dim: 53, ec: 30, blocks: [{ count: 2, data: 116 }], align: [6, 26, 46] },
  { v: 10, dim: 57, ec: 18, blocks: [{ count: 2, data: 68 }, { count: 2, data: 69 }], align: [6, 28, 50] }
];
function selectVersion(byteLen) {
  for (let v = 1; v <= 10; v++) {
    const totalData = VERSIONS[v].blocks.reduce((acc, b) => acc + b.count * b.data, 0);
    const lenBits = v <= 9 ? 8 : 16;
    const reqBits = 4 + lenBits + byteLen * 8 + 4;
    if (Math.ceil(reqBits / 8) <= totalData) {
      return VERSIONS[v];
    }
  }
  return VERSIONS[10];
}
function createQRCode(text) {
  const encoder = new TextEncoder();
  const rawData = encoder.encode(text);
  const vInfo = selectVersion(rawData.length);
  const version = vInfo.v;
  const dim = vInfo.dim;
  const totalData = vInfo.blocks.reduce((acc, b) => acc + b.count * b.data, 0);
  const bits = [];
  function putBits(num2, len) {
    for (let i = len - 1; i >= 0; i--) bits.push(num2 >>> i & 1);
  }
  putBits(4, 4);
  putBits(rawData.length, version <= 9 ? 8 : 16);
  for (const b of rawData) putBits(b, 8);
  const maxBits = totalData * 8;
  const termLen = Math.min(4, maxBits - bits.length);
  putBits(0, termLen);
  while (bits.length % 8 !== 0) bits.push(0);
  const padBytes = [236, 17];
  let padIdx = 0;
  while (bits.length < maxBits) {
    putBits(padBytes[padIdx % 2], 8);
    padIdx++;
  }
  const dataCodewords = new Uint8Array(totalData);
  for (let i = 0; i < totalData; i++) {
    let byte = 0;
    for (let j = 0; j < 8; j++) byte = byte << 1 | bits[i * 8 + j];
    dataCodewords[i] = byte;
  }
  const dataBlocks = [];
  const ecBlocks = [];
  let cwOffset = 0;
  for (const b of vInfo.blocks) {
    for (let i = 0; i < b.count; i++) {
      const dBlock = dataCodewords.slice(cwOffset, cwOffset + b.data);
      cwOffset += b.data;
      dataBlocks.push(dBlock);
      ecBlocks.push(calcEC(dBlock, vInfo.ec));
    }
  }
  const finalCodewords = [];
  const maxBlockLen = Math.max(...dataBlocks.map((b) => b.length));
  for (let i = 0; i < maxBlockLen; i++) {
    for (let b = 0; b < dataBlocks.length; b++) {
      if (i < dataBlocks[b].length) finalCodewords.push(dataBlocks[b][i]);
    }
  }
  for (let i = 0; i < vInfo.ec; i++) {
    for (let b = 0; b < ecBlocks.length; b++) {
      finalCodewords.push(ecBlocks[b][i]);
    }
  }
  const matrix = Array.from({ length: dim }, () => new Int8Array(dim).fill(-1));
  const isFunc = Array.from({ length: dim }, () => new Uint8Array(dim).fill(0));
  function markRegion(x2, y, w, h) {
    for (let r = y; r < y + h; r++) {
      for (let c = x2; c < x2 + w; c++) {
        if (r >= 0 && r < dim && c >= 0 && c < dim) isFunc[r][c] = 1;
      }
    }
  }
  function setFinder(x2, y) {
    for (let r = 0; r < 7; r++) {
      for (let c = 0; c < 7; c++) {
        const isBlack = r === 0 || r === 6 || c === 0 || c === 6 || r >= 2 && r <= 4 && c >= 2 && c <= 4;
        matrix[y + r][x2 + c] = isBlack ? 1 : 0;
      }
    }
    for (let r = -1; r <= 7; r++) {
      for (let c = -1; c <= 7; c++) {
        const nr = y + r, nc = x2 + c;
        if (nr >= 0 && nr < dim && nc >= 0 && nc < dim) {
          if (matrix[nr][nc] === -1) matrix[nr][nc] = 0;
        }
      }
    }
  }
  setFinder(0, 0);
  setFinder(dim - 7, 0);
  setFinder(0, dim - 7);
  markRegion(0, 0, 9, 9);
  markRegion(dim - 8, 0, 8, 9);
  markRegion(0, dim - 8, 9, 8);
  for (let i = 8; i < dim - 8; i++) {
    matrix[6][i] = i % 2 === 0 ? 1 : 0;
    matrix[i][6] = i % 2 === 0 ? 1 : 0;
    isFunc[6][i] = 1;
    isFunc[i][6] = 1;
  }
  for (const cx of vInfo.align) {
    for (const cy of vInfo.align) {
      if (cx === 6 && cy === 6 || cx === 6 && cy === dim - 7 || cx === dim - 7 && cy === 6) continue;
      for (let r = -2; r <= 2; r++) {
        for (let c = -2; c <= 2; c++) {
          const isBlack = Math.abs(r) === 2 || Math.abs(c) === 2 || r === 0 && c === 0;
          matrix[cy + r][cx + c] = isBlack ? 1 : 0;
          isFunc[cy + r][cx + c] = 1;
        }
      }
    }
  }
  matrix[dim - 8][8] = 1;
  isFunc[dim - 8][8] = 1;
  if (version >= 7) {
    markRegion(dim - 11, 0, 3, 6);
    markRegion(0, dim - 11, 6, 3);
    let rem = version << 12;
    for (let i = 17; i >= 12; i--) {
      if (rem >>> i & 1) rem ^= 7973 << i - 12;
    }
    const verBits = version << 12 | rem;
    for (let i = 0; i < 18; i++) {
      const bit = verBits >>> i & 1;
      const r = Math.floor(i / 3), c = dim - 11 + i % 3;
      matrix[r][c] = bit;
      matrix[c][r] = bit;
    }
  }
  const finalBits = [];
  for (const b of finalCodewords) {
    for (let i = 7; i >= 0; i--) finalBits.push(b >>> i & 1);
  }
  let bitIdx = 0;
  let readingUp = true;
  for (let col = dim - 1; col > 0; col -= 2) {
    if (col === 6) col--;
    for (let i = 0; i < dim; i++) {
      const r = readingUp ? dim - 1 - i : i;
      for (let offset = 0; offset < 2; offset++) {
        const c = col - offset;
        if (!isFunc[r][c]) {
          const bit = bitIdx < finalBits.length ? finalBits[bitIdx++] : 0;
          const mask = (r + c) % 2 === 0 ? 1 : 0;
          matrix[r][c] = bit ^ mask;
        }
      }
    }
    readingUp = !readingUp;
  }
  const formatVal = 30660;
  const fBits = [];
  for (let i = 14; i >= 0; i--) fBits.push(formatVal >>> i & 1);
  let fbIdx = 0;
  for (let c = 0; c <= 8; c++) {
    if (c !== 6) matrix[8][c] = fBits[fbIdx++];
  }
  for (let r = 7; r >= 0; r--) {
    if (r !== 6) matrix[r][8] = fBits[fbIdx++];
  }
  fbIdx = 0;
  for (let r = dim - 1; r >= dim - 7; r--) {
    matrix[r][8] = fBits[fbIdx++];
  }
  for (let c = dim - 8; c < dim; c++) {
    matrix[8][c] = fBits[fbIdx++];
  }
  return {
    getModuleCount: () => dim,
    isDark: (r, c) => matrix[r][c] === 1
  };
}
function generateQRCodeSVG(text, size = 220) {
  const qr = createQRCode(text);
  const count = qr.getModuleCount();
  const margin = 3;
  const viewBoxSize = count + margin * 2;
  const pathParts = [];
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (qr.isDark(r, c)) {
        pathParts.push(`M${c + margin},${r + margin}h1v1h-1z`);
      }
    }
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${viewBoxSize} ${viewBoxSize}" width="${size}" height="${size}" shape-rendering="crispEdges">
    <rect width="${viewBoxSize}" height="${viewBoxSize}" fill="#ffffff"/>
    <path d="${pathParts.join(" ")}" fill="#000000"/>
  </svg>`;
}

// src/app.ts
var $ = (id) => document.getElementById(id);
var $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
var esc = (value) => String(value ?? "").replace(/[&<>"']/g, (c) => ({
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
  "'": "&#39;"
})[c]);
var num = (value, fallback = 0) => {
  const n = Number(value);
  return Number.isFinite(n) ? n : fallback;
};
function fmt(value, digits = 2) {
  if (!Number.isFinite(value)) return "\u2014";
  return value.toLocaleString("en-IN", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}
function fmtSigned(value, digits = 2) {
  if (!Number.isFinite(value)) return "\u2014";
  const sign = value > 0 ? "+" : value < 0 ? "\u2212" : "";
  return sign + fmt(Math.abs(value), digits);
}
function fmtCompact(value) {
  if (!Number.isFinite(value)) return "\u2014";
  if (Math.abs(value) >= 1e7) return `${fmt(value / 1e7, 2)}Cr`;
  if (Math.abs(value) >= 1e5) return `${fmt(value / 1e5, 2)}L`;
  if (Math.abs(value) >= 1e3) return `${fmt(value / 1e3, 1)}K`;
  return fmt(value, 0);
}
function fmtStrike(value) {
  if (!Number.isFinite(value)) return "\u2014";
  return Number.isInteger(value) ? String(value) : fmt(value, 2);
}
function directionClass(value) {
  return value > 0 ? "up" : value < 0 ? "down" : "";
}
function fmtTime(ts) {
  if (!ts) return "\u2014";
  const date = typeof ts === "number" ? new Date(ts < 1e12 ? ts * 1e3 : ts) : new Date(ts);
  if (Number.isNaN(date.getTime())) return "\u2014";
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
var S = {
  boot: {},
  activeIndex: "NIFTY",
  expiry: "",
  timeframe: "5m",
  expiries: [],
  chain: [],
  chainRequestID: 0,
  chainMore: { above: false, below: false },
  chainLoading: false,
  spots: /* @__PURE__ */ new Map(),
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
  candles: {},
  historicalCache: /* @__PURE__ */ new Map(),
  chartLoadID: 0,
  quickOrder: null,
  autoTargets: [],
  marketOpen: null,
  focusedChart: null,
  soundEnabled: true,
  wsConnectedAt: null,
  lastWsEventTime: null
};
window.S = S;
var marketRetryTimer = null;
var chartRetryTimer = null;
var depthRenderTimer = null;
function scheduleDepthRender() {
  if (depthRenderTimer !== null) return;
  depthRenderTimer = window.setTimeout(() => {
    depthRenderTimer = null;
    if (S.tab === "depth") renderTab();
  }, 200);
}
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
async function requestJSON(url, options = {}) {
  const headers = { Accept: "application/json", ...options.body ? { "Content-Type": "application/json" } : {} };
  try {
    const devId = localStorage.getItem("scalper_device_id");
    if (devId) headers["X-Device-ID"] = devId;
    const sessToken = localStorage.getItem("scalper_serve_session");
    if (sessToken) headers["X-Serve-Session"] = sessToken;
  } catch (_) {
  }
  if (options.headers) Object.assign(headers, options.headers);
  const response = await fetch(url, {
    credentials: "include",
    ...options,
    headers
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
var getJSON = (url) => requestJSON(url);
var postJSON = (url, body) => requestJSON(url, { method: "POST", body: JSON.stringify(body) });
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
    confirm: true
  });
}
function normalizeStrike(value) {
  let strike = num(value);
  if (!strike) return 0;
  if (strike > 1e5) strike /= 100;
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
    changePct: prevClose ? (ltp - prevClose) / prevClose * 100 : 0,
    oi,
    volume: num(raw.volume),
    bid: num(raw.bid ?? raw.best_bid ?? raw.bestBid),
    ask: num(raw.ask ?? raw.best_ask ?? raw.bestAsk),
    bids,
    asks
  };
}
function normalizeChain(raw) {
  const rowsRaw = Array.isArray(raw) ? raw : raw?.rows || raw?.entries || raw?.chain || [];
  const byStrike = /* @__PURE__ */ new Map();
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
    direction
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
    below: S.chain.length > 0 && Boolean(page?.has_more_below ?? page?.hasMoreBelow)
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
    (S.chainLoading === "above" ? above : below).textContent = "Loading\u2026";
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
    const date2 = /* @__PURE__ */ new Date(value + "T00:00:00");
    return {
      date: value,
      label: Number.isNaN(date2.getTime()) ? value : date2.toLocaleDateString("en-GB", { day: "2-digit", month: "short" }).toUpperCase(),
      weekly: true,
      monthly: false,
      expired: false
    };
  }
  const date = value.date ?? value.expiry ?? value.value ?? "";
  const parsed = /* @__PURE__ */ new Date(date + "T00:00:00");
  return {
    date,
    label: value.label ?? (Number.isNaN(parsed.getTime()) ? date : parsed.toLocaleDateString("en-GB", { day: "2-digit", month: "short" }).toUpperCase()),
    weekly: value.weekly !== false,
    monthly: Boolean(value.monthly),
    expired: Boolean(value.expired)
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
      return Math.round(totalCost / totalOpenQty * 100) / 100;
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
      return Math.round(totalCost / totalOpenQty * 100) / 100;
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
    product: raw.product ?? raw.prod ?? "MIS"
  };
}
function comparePositions(a, b) {
  const aOpen = Math.abs(num(a.quantity)) > 0 ? 0 : 1;
  const bOpen = Math.abs(num(b.quantity)) > 0 ? 0 : 1;
  if (aOpen !== bOpen) return aOpen - bOpen;
  const active = S.activeIndex || "NIFTY";
  const aUnder = a.underlying || "";
  const bUnder = b.underlying || "";
  const aIsActive = aUnder === active ? 0 : 1;
  const bIsActive = bUnder === active ? 0 : 1;
  if (aIsActive !== bIsActive) return aIsActive - bIsActive;
  const underCmp = aUnder.localeCompare(bUnder);
  if (underCmp !== 0) return underCmp;
  const expCmp = String(a.expiry || "").localeCompare(String(b.expiry || ""));
  if (expCmp !== 0) return expCmp;
  const strikeDiff = num(a.strike) - num(b.strike);
  if (strikeDiff !== 0) return strikeDiff;
  const typeCmp = String(a.option_type || "").localeCompare(String(b.option_type || ""));
  if (typeCmp !== 0) return typeCmp;
  const symCmp = String(a.symbol || "").localeCompare(String(b.symbol || ""));
  if (symCmp !== 0) return symCmp;
  return String(a.instrument_key || "").localeCompare(String(b.instrument_key || ""));
}
function normalizeOrder(raw) {
  const rawSide = String(raw.side ?? raw.tt ?? raw.trdType ?? raw.trnsTp ?? raw.transactionType ?? raw.buy_sell ?? raw.buySell ?? "").trim().toUpperCase();
  const side = rawSide === "B" || rawSide === "BUY" ? "B" : rawSide === "S" || rawSide === "SELL" ? "S" : rawSide;
  return {
    id: raw.id ?? raw.client_order_id ?? raw.order_id ?? raw.broker_order_id ?? raw.nOrdNo ?? "\u2014",
    symbol: raw.symbol ?? raw.trading_symbol ?? raw.tradingSymbol ?? raw.trdSym ?? raw.sym ?? raw.instrument_key ?? "",
    instrument_key: raw.instrument_key ?? raw.instrumentKey ?? "",
    side,
    quantity: num(raw.quantity ?? raw.qty ?? raw.fldQty ?? raw.fillSz),
    price: num(raw.price ?? raw.prc ?? raw.avgPrc ?? raw.fillPrc),
    status: raw.status ?? raw.ordSt ?? "",
    reason: raw.reason ?? raw.reject_reason ?? raw.rejRsn ?? "",
    timestamp: raw.timestamp ?? raw.created_at ?? raw.updated_at ?? raw.ordDtTm ?? raw.hsUpTm ?? raw.ordDt ?? 0,
    product: raw.product ?? raw.prod ?? "MIS",
    mode: raw.mode ?? ""
  };
}
function normalizeTrade(raw) {
  const rawSide = String(raw.side ?? raw.tt ?? raw.trdType ?? raw.trnsTp ?? raw.transactionType ?? raw.buy_sell ?? raw.buySell ?? "").trim().toUpperCase();
  const side = rawSide === "B" || rawSide === "BUY" ? "B" : rawSide === "S" || rawSide === "SELL" ? "S" : rawSide;
  return {
    id: raw.id ?? raw.trade_id ?? raw.trdId ?? raw.order_id ?? raw.nOrdNo ?? raw.exOrdId ?? raw.exchOrdId ?? raw.flId ?? "\u2014",
    symbol: raw.symbol ?? raw.trading_symbol ?? raw.tradingSymbol ?? raw.trdSym ?? raw.sym ?? raw.instrument_key ?? "",
    side,
    quantity: num(raw.quantity ?? raw.qty ?? raw.fillSz ?? raw.fldQty ?? raw.flQty ?? raw.tradedQty),
    price: num(raw.price ?? raw.prc ?? raw.avgPrc ?? raw.fillPrc ?? raw.fldPrc ?? raw.flPrc ?? raw.tradePrice ?? raw.trdPrc),
    timestamp: raw.timestamp ?? raw.executed_at ?? raw.executedAt ?? raw.ordDtTm ?? raw.hsUpTm ?? raw.ordDt ?? raw.trdDt ?? 0
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
  const pnl = raw.pnl != null ? num(raw.pnl) : current_value - invested;
  const pnl_pct = invested > 0 ? (current_value - invested) / invested * 100 : 0;
  return {
    symbol,
    segment: raw.exchangeSegment || raw.exSeg || raw.segment || "nse_cm",
    quantity,
    average_price,
    ltp,
    invested,
    current_value,
    pnl,
    pnl_pct
  };
}
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
  const defaultUnit = S.boot.defaultUnit === "shares" ? "SHARES" : "LOTS";
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
  }, 1e3);
  window.setInterval(updateAIWinRateChip, 3e4);
  const serveAuthModal = $("serve-auth-modal");
  const isTerminalLocked = serveAuthModal && !serveAuthModal.hidden && serveAuthModal.style.display !== "none";
  updateBiometricButtons();
  const urlParams = new URLSearchParams(window.location.search);
  const pairToken = urlParams.get("pair_token");
  if (pairToken) {
    setTimeout(() => {
      toast("Pairing link detected. Registering biometric passkey...", "ok", 4e3);
      handleRegisterDevice("Mobile Device", pairToken);
    }, 300);
  } else if (isTerminalLocked) {
    tryAutoUnlockDevice();
  } else {
    startTerminalSession();
  }
}
var sessionRefreshTimer = null;
async function startTerminalSession() {
  checkVersionUpdate();
  await refreshSession();
  if (!S.session?.authenticated) {
    openLogin();
    return;
  }
  closeLogin();
  connectWebSocket();
  loadMarket();
  refreshBooksFromAPI(true).catch((error) => console.warn("account refresh failed", error));
  if (!sessionRefreshTimer) {
    sessionRefreshTimer = window.setInterval(refreshSession, 3e4);
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
  for (const chart of Object.values(S.charts)) chart.applyTheme();
  if (S.tab === "risk") renderTab();
  if (!persist) return;
  try {
    localStorage.setItem("scalper-theme", theme);
  } catch (error) {
    console.warn("Unable to save theme preference", error);
  }
}
var audioCtx = null;
var lastUiOrderTime = 0;
function getAudioContext() {
  if (!audioCtx) {
    const AudioContextClass = window.AudioContext || window.webkitAudioContext;
    if (AudioContextClass) {
      audioCtx = new AudioContextClass();
    }
  }
  if (audioCtx && audioCtx.state === "suspended") {
    audioCtx.resume().catch(() => {
    });
  }
  return audioCtx;
}
function initAudioOnFirstGesture() {
  const unlock = () => {
    const ctx = getAudioContext();
    if (ctx && ctx.state === "suspended") {
      ctx.resume().catch(() => {
      });
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
    gain.gain.setValueAtTime(1e-4, startTime);
    gain.gain.exponentialRampToValueAtTime(maxGain, startTime + 5e-3);
    gain.gain.exponentialRampToValueAtTime(1e-4, startTime + duration);
    osc.connect(gain);
    gain.connect(ctx.destination);
    osc.start(startTime);
    osc.stop(startTime + duration + 0.02);
  } catch (_) {
  }
}
function playTradeSound(side) {
  if (!S.soundEnabled) return;
  const ctx = getAudioContext();
  if (!ctx) return;
  if (ctx.state === "suspended") {
    ctx.resume().catch(() => {
    });
  }
  const now = ctx.currentTime;
  const action = String(side || "").toLowerCase();
  if (action === "buy" || action === "b") {
    playTone(ctx, 587.33, now, 0.08, 0.16, "sine");
    playTone(ctx, 880, now + 0.07, 0.18, 0.18, "sine");
  } else if (action === "sell" || action === "s") {
    playTone(ctx, 880, now, 0.08, 0.16, "sine");
    playTone(ctx, 523.25, now + 0.07, 0.18, 0.18, "sine");
  } else if (action === "target" || action === "tp") {
    playTone(ctx, 659.25, now, 0.07, 0.14, "sine");
    playTone(ctx, 987.77, now + 0.06, 0.09, 0.16, "sine");
    playTone(ctx, 1318.5, now + 0.14, 0.16, 0.2, "sine");
  } else if (action === "reject" || action === "error") {
    playTone(ctx, 220, now, 0.08, 0.14, "triangle");
    playTone(ctx, 175, now + 0.09, 0.12, 0.14, "triangle");
  }
}
function updateSoundButton() {
  const btn = $("sound-toggle");
  if (!btn) return;
  const icon = $("sound-icon");
  const label = $("sound-label");
  if (S.soundEnabled) {
    if (icon) icon.textContent = "\u{1F50A}";
    if (label) label.textContent = "Sound";
    btn.title = "Sound notifications enabled (Click to mute)";
    btn.setAttribute("aria-pressed", "true");
    btn.classList.remove("muted");
  } else {
    if (icon) icon.textContent = "\u{1F507}";
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
  } catch (_) {
  }
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
  } catch (_) {
  }
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
  const savedPatterns = localStorage.getItem("scalper-show-patterns") === "true";
  const savedTrades = localStorage.getItem("scalper-show-trades") !== "false";
  const savedMACE = localStorage.getItem("scalper-ma-ce") === "true";
  const savedMAUnderlying = (localStorage.getItem("scalper-ma-underlying") ?? localStorage.getItem("scalper-ma-spot")) === "true";
  const savedMAPE = localStorage.getItem("scalper-ma-pe") === "true";
  const savedDOM = localStorage.getItem("scalper-show-dom") !== "false";
  S.showTrades = savedTrades;
  S.charts.ce = new CandleChart($("chart-ce"), { showVolume: true, showSR: savedSR, showTrades: savedTrades, showAutoTrendlines: savedAutoTL, showPatterns: savedPatterns, showMA: savedMACE, showDepth: savedDOM });
  S.charts.spot = new CandleChart($("chart-spot"), { showVolume: true, showSR: savedSR, showTrades: savedTrades, showAutoTrendlines: savedAutoTL, showPatterns: savedPatterns, showMA: savedMAUnderlying, showDepth: savedDOM });
  S.charts.pe = new CandleChart($("chart-pe"), { showVolume: true, showSR: savedSR, showTrades: savedTrades, showAutoTrendlines: savedAutoTL, showPatterns: savedPatterns, showMA: savedMAPE, showDepth: savedDOM });
  Object.defineProperty(S.charts, "underlying", {
    get() {
      return S.charts.spot;
    },
    set(val) {
      S.charts.spot = val;
    },
    enumerable: false,
    configurable: true
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
    b.title = "Order Flow DOM \xB7 Fixed-interval Buyer & Seller volume ladder on chart";
  });
  $$('button[data-chart-action="ma"]').forEach((b) => {
    const pane = b.dataset.chart;
    const active = pane === "ce" && savedMACE || (pane === "spot" || pane === "underlying") && savedMAUnderlying || pane === "pe" && savedMAPE;
    b.classList.toggle("active", active);
    b.setAttribute("aria-pressed", active ? "true" : "false");
  });
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
        } catch (_) {
        }
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
  for (const button of $$(".chart-tools button[data-chart-action='ma']")) {
    const pane = button.dataset.chart;
    const active = pane === "ce" && savedMACE || (pane === "spot" || pane === "underlying") && savedMAUnderlying || pane === "pe" && savedMAPE;
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
    const isTarget = article.dataset.pane === S.focusedChart || S.focusedChart === "underlying" && (article.dataset.pane === "spot" || article.dataset.paneAlias === "spot") || S.focusedChart === "spot" && (article.dataset.pane === "underlying" || article.dataset.pane === "spot");
    article.classList.toggle("expanded", isTarget);
  }
  for (const button of $$(".chart-tools button[data-chart-action='expand']")) {
    const btnChart = button.dataset.chart;
    const active = btnChart === S.focusedChart || S.focusedChart === "underlying" && btnChart === "spot" || S.focusedChart === "spot" && btnChart === "underlying";
    button.classList.toggle("active", active);
    button.title = active ? "Restore chart layout (Esc)" : "Expand chart";
    button.setAttribute("aria-pressed", active ? "true" : "false");
  }
  requestAnimationFrame(() => {
    for (const chart of Object.values(S.charts)) chart?.schedule?.();
  });
}
function getVisiblePanes() {
  const visible = [];
  const panes = [
    { key: "ce", el: document.querySelector('.pane[data-pane="ce"]') },
    { key: "underlying", el: document.querySelector('.pane[data-pane="underlying"], .pane[data-pane-alias="underlying"], .pane[data-pane="spot"]') },
    { key: "pe", el: document.querySelector('.pane[data-pane="pe"]') }
  ];
  for (const { key, el } of panes) {
    if (el && !el.classList.contains("pane-hidden")) {
      visible.push(key);
    }
  }
  return visible;
}
function isPaneVisible(paneKey) {
  const normKey = paneKey === "spot" ? "underlying" : paneKey;
  const el = document.querySelector(`.pane[data-pane="${normKey}"], .pane[data-pane-alias="${normKey}"]${normKey === "underlying" ? ', .pane[data-pane="spot"]' : ""}`);
  return el ? !el.classList.contains("pane-hidden") : false;
}
function setPaneVisible(paneKey, visible) {
  const normKey = paneKey === "spot" ? "underlying" : paneKey;
  const currentVisible = getVisiblePanes();
  const isCurrentlyVis = currentVisible.includes(normKey);
  if (!visible && isCurrentlyVis && currentVisible.length <= 1) {
    toast("At least one chart must remain visible", "warn", 2e3);
    return false;
  }
  const el = document.querySelector(`.pane[data-pane="${normKey}"], .pane[data-pane-alias="${normKey}"]${normKey === "underlying" ? ', .pane[data-pane="spot"]' : ""}`);
  if (el) {
    el.classList.toggle("pane-hidden", !visible);
  }
  if (!visible && (S.focusedChart === normKey || normKey === "underlying" && S.focusedChart === "spot")) {
    setFocusedChart(null);
  }
  updateChartVisibilityUI();
  saveChartVisibility();
  requestAnimationFrame(() => {
    for (const [key, chart] of Object.entries(S.charts)) {
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
    const norm = pane === "spot" ? "underlying" : pane;
    const isVis = visible.includes(norm);
    btn.classList.toggle("active", isVis);
    btn.setAttribute("aria-pressed", isVis ? "true" : "false");
    const indicator = btn.querySelector(".vis-indicator");
    if (indicator) {
      indicator.textContent = isVis ? "\u2713" : "";
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
  } catch (_) {
  }
}
function initChartVisibility() {
  let visiblePanes = ["ce", "underlying", "pe"];
  try {
    const raw = localStorage.getItem("scalper-chart-visibility");
    if (raw) {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed) && parsed.length > 0) {
        visiblePanes = parsed.map((p) => p === "spot" ? "underlying" : p);
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
  } catch (_) {
  }
  if (!visiblePanes || visiblePanes.length === 0) {
    visiblePanes = ["ce", "underlying", "pe"];
  }
  const allPanes = ["ce", "underlying", "pe"];
  for (const pane of allPanes) {
    const el = document.querySelector(`.pane[data-pane="${pane}"], .pane[data-pane-alias="${pane}"]${pane === "underlying" ? ', .pane[data-pane="spot"]' : ""}`);
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
      rem = Math.max(0, Math.floor((expMs - Date.now()) / 1e3));
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
  const minutes = Math.floor(rem % 3600 / 60);
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
    toast(`Live market data unavailable: ${error.message}`, "error", 8e3);
    if (!marketRetryTimer) {
      marketRetryTimer = window.setTimeout(() => {
        marketRetryTimer = null;
        loadMarket();
      }, 3e4);
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
  const list = Array.isArray(indexRaw) ? indexRaw : indexRaw?.indices || [];
  const map = /* @__PURE__ */ new Map();
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
      spotKey: item.instrument_key ?? item.instrumentKey ?? `${item.index_segment ?? "nse_cm"}|${key}`
    });
  }
  for (const idx of [...S.boot.indices || [], ...S.boot.commodities || []]) {
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
        spotKey: idx.indexSegment ? `${idx.indexSegment}|${idx.key}` : ""
      });
    }
  }
  S.spots = map;
}
function normalizeExpiryList(raw) {
  const list = Array.isArray(raw) ? raw : raw?.expiries || raw?.expiry || [];
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
var booksInFlight = null;
async function loadBooksFromAPI(includePositions = true) {
  S.lastBooksRefresh = Date.now();
  const [positions, orders, trades, limits, brackets, holdings] = await Promise.allSettled([
    includePositions ? getJSON("/api/broker/positions") : Promise.resolve(null),
    getJSON("/api/broker/orders"),
    getJSON("/api/broker/trades"),
    getJSON("/api/broker/limits"),
    getJSON("/api/brackets"),
    getJSON("/api/broker/holdings")
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
    S.limits = { ...S.limits || {}, error: limits.reason?.message || "Account limits are unavailable." };
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
  const available = obj.available_margin ?? obj.availableMargin ?? obj.available ?? obj.Net ?? obj.net ?? value?.available_margin ?? value?.availableMargin ?? value?.available ?? value?.Net ?? value?.net ?? value?.data?.availableMargin ?? value?.data?.Net ?? value?.limits?.availableMargin ?? value?.limits?.Net;
  const used = obj.used_margin ?? obj.usedMargin ?? obj.used ?? obj.marginUsed ?? obj.MarginUsed ?? value?.used_margin ?? value?.usedMargin ?? value?.used ?? value?.marginUsed ?? value?.MarginUsed ?? value?.data?.MarginUsed;
  const total = obj.total_margin ?? obj.totalMargin ?? obj.total ?? obj.Net ?? obj.net ?? value?.total_margin ?? value?.totalMargin ?? value?.total ?? value?.Net ?? value?.net ?? value?.data?.Net;
  S.limits = {
    available: num(available),
    used: num(used),
    total: num(total),
    loaded: available != null,
    error: available == null ? "Account response did not include available margin." : ""
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
      toast(`Margin updated: \u20B9${fmt(num(S.limits.available), 0)}`, "info", 2e3);
    } else {
      toast("Margin refreshed.", "info", 2e3);
    }
  } catch (err) {
    toast(`Failed to update margin: ${err?.message || "network error"}`, "error", 4e3);
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
var liveTimer = null;
var underlyingTimer = null;
var booksRefreshTimer = null;
var chainPollCounter = 0;
function startLivePolling() {
  if (!S.session?.authenticated) return;
  if (!liveTimer) {
    liveTimer = window.setInterval(() => {
      if (document.hidden || !S.session?.authenticated) return;
      if (!S.marketOpen && Date.now() - (S.lastBooksRefresh || 0) < 6e4) return;
      refreshBooksFromAPI(true).catch((error) => console.warn("account refresh failed", error));
    }, 15e3);
  }
  if (!underlyingTimer) {
    underlyingTimer = window.setInterval(async () => {
      if (document.hidden || !S.session?.authenticated || !S.marketOpen) return;
      try {
        applyIndexQuotes(await getJSON("/api/indices"));
        updateFeedSubscriptions();
        renderHeader();
        chainPollCounter++;
        if (chainPollCounter >= 2 && S.activeIndex && S.expiry) {
          chainPollCounter = 0;
          const center = num(S.spots.get(S.activeIndex)?.ltp);
          const chainRaw = await getJSON(chainURL({ center }));
          acceptChainPage(chainRaw);
          renderChain(true);
          renderPaneHeaders();
          renderPCR();
        }
      } catch (error) {
        console.warn("underlying/chain quote refresh failed", error);
      }
    }, 3e4);
  }
}
function scheduleBooksRefresh(delay = 1e3) {
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
        }, 5e3);
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
  if (status) status.textContent = `Reconnecting\u2026 (attempt ${attempt})`;
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
    spot.changePct = num(
      data.change_pct,
      spot.prevClose ? (spot.ltp - spot.prevClose) / spot.prevClose * 100 : 0
    );
    if (!batch) {
      renderHeader();
      renderPaneHeaders();
      renderOrderPad();
      renderPCR();
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
    updateChartTrades();
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
    if (data.oi != null && Number(data.oi) >= 0 && Number(data.oi) <= 1e8) {
      opt.oi = num(data.oi, opt.oi);
    }
    if (data.bids || data.asks) {
      const merged = mergeDepthData({ bids: opt.bids, asks: opt.asks }, data.bids, data.asks);
      opt.bids = merged.bids;
      opt.asks = merged.asks;
    }
    opt.quoteAvailable = opt.ltp > 0;
    opt.change = opt.ltp - opt.prevClose;
    opt.changePct = opt.prevClose ? (opt.ltp - opt.prevClose) / opt.prevClose * 100 : 0;
    const type = row.call?.instrumentKey === data.key ? "CE" : "PE";
    const cell = $("chain-body")?.querySelector(`button[data-select="${type}"][data-strike="${row.strike}"]`);
    if (cell) {
      cell.innerHTML = `<span class="chain-ltp">${fmt(opt.ltp)}</span><span class="chain-volume">VOL ${fmtCompact(opt.volume)}</span>`;
      cell.title = `${fmt(opt.ltp)} \xB7 ${fmtSigned(opt.changePct)}% \xB7 Vol ${fmtCompact(opt.volume)}${opt.oi ? ` \xB7 OI ${fmtCompact(opt.oi)}` : ""}`;
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
        setChartHint(type === "CE" ? "ce" : "pe", hasCandles ? "" : S.feed === "live" ? "Waiting for live ticks\u2026" : "Waiting for authenticated live feed\u2026");
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
  if (event.type === "ai_call_outcome" || event.type === "ai_call_new") {
    handleAICallEvent(event.type, data);
    return;
  }
  if (event.type === "broker_order" || event.type === "broker_position") {
    if (event.type === "broker_order" && data) {
      const norm = normalizeOrder(data);
      if (norm.id && norm.id !== "\u2014") {
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
      updateChartTrades();
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
    const posKeys = /* @__PURE__ */ new Set();
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
      updateChartTrades();
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
    ...[...S.spots.values()].map((spot) => spot.spotKey)
  ].filter((k) => typeof k === "string" && k.includes("|") && !k.endsWith("|"));
  const desired = [...new Set(keys)];
  if (S.ws?.readyState !== WebSocket.OPEN) return;
  const remove = S.feedSubscriptions.filter((key) => !desired.includes(key));
  const add = desired.filter((key) => !S.feedSubscriptions.includes(key));
  if (remove.length) S.ws.send(JSON.stringify({ type: "unsubscribe", keys: remove }));
  if (add.length) S.ws.send(JSON.stringify({ type: "subscribe", keys: add }));
  S.feedSubscriptions = desired;
}
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
    totalPutVol
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
  let pcrOI = null;
  if (callOI > 0 && putOI > 0) {
    const ratio = putOI / callOI;
    if (Number.isFinite(ratio) && ratio >= 0.01 && ratio <= 50) {
      pcrOI = ratio;
    }
  } else if (putOI > 0 && callOI === 0) {
    pcrOI = 50;
  } else if (callOI > 0 && putOI === 0) {
    pcrOI = 0.01;
  }
  let pcrVol = null;
  if (callVol > 0 && putVol > 0) {
    const ratio = putVol / callVol;
    if (Number.isFinite(ratio) && ratio >= 0.01 && ratio <= 50) {
      pcrVol = ratio;
    }
  } else if (putVol > 0 && callVol === 0) {
    pcrVol = 50;
  } else if (callVol > 0 && putVol === 0) {
    pcrVol = 0.01;
  }
  return {
    strike: numStrike,
    pcrOI,
    pcrVol,
    callOI,
    putOI,
    callVol,
    putVol
  };
}
function renderPanePCR(pane, option, chainPCR, expiryLabel) {
  const chip = $(`${pane}-pcr-chip`);
  const valEl = $(`${pane}-pcr-val`);
  const subEl = $(`${pane}-sub`);
  if (!chip || !valEl) return;
  if (!option) {
    valEl.textContent = "\u2014";
    valEl.className = "";
    chip.title = "Select a strike to view PCR";
    if (subEl) subEl.textContent = "select a strike";
    return;
  }
  const strikePCR = computeStrikePCR(option.strike);
  const totalPCRVal = chainPCR?.pcrOI != null && Number.isFinite(chainPCR.pcrOI) && chainPCR.pcrOI <= 50 ? chainPCR.pcrOI : chainPCR?.pcrVol != null && Number.isFinite(chainPCR.pcrVol) && chainPCR.pcrVol <= 50 ? chainPCR.pcrVol : null;
  const strikePCRVal = strikePCR?.pcrOI != null && Number.isFinite(strikePCR.pcrOI) && strikePCR.pcrOI <= 50 ? strikePCR.pcrOI : strikePCR?.pcrVol != null && Number.isFinite(strikePCR.pcrVol) && strikePCR.pcrVol <= 50 ? strikePCR.pcrVol : null;
  const displayVal = strikePCRVal != null ? strikePCRVal : totalPCRVal;
  const isStrike = strikePCRVal != null;
  if (displayVal != null) {
    valEl.textContent = displayVal.toFixed(2);
    valEl.className = displayVal > 1.02 ? "up" : displayVal < 0.98 ? "down" : "";
    const sentiment = displayVal >= 1.3 ? "Strong Bullish / Put Support" : displayVal >= 1.05 ? "Mild Bullish" : displayVal >= 0.95 ? "Neutral" : displayVal >= 0.7 ? "Mild Bearish" : "Strong Bearish / Call Resistance";
    let tooltip = `${option.symbol || `${S.activeIndex} ${option.strike} ${option.type}`}
`;
    if (isStrike) {
      tooltip += `Strike ${fmtStrike(option.strike)} PCR: ${displayVal.toFixed(2)} [${sentiment}]
`;
      tooltip += `  Put OI: ${fmtCompact(strikePCR.putOI)} \xB7 Call OI: ${fmtCompact(strikePCR.callOI)}
`;
      if (strikePCR.putVol || strikePCR.callVol) {
        tooltip += `  Put Vol: ${fmtCompact(strikePCR.putVol)} \xB7 Call Vol: ${fmtCompact(strikePCR.callVol)}${strikePCR.pcrVol != null ? ` (Vol PCR: ${strikePCR.pcrVol.toFixed(2)})` : ""}
`;
      }
    }
    if (totalPCRVal != null) {
      tooltip += `Total Chain PCR: ${totalPCRVal.toFixed(2)}
`;
      tooltip += `  Total Put OI: ${fmtCompact(chainPCR.totalPutOI)} \xB7 Total Call OI: ${fmtCompact(chainPCR.totalCallOI)}`;
    }
    chip.title = tooltip.trim();
    if (subEl) {
      const strikePart = isStrike ? ` \xB7 Strike PCR ${displayVal.toFixed(2)}` : "";
      const totalPart = totalPCRVal != null ? ` \xB7 Total PCR ${totalPCRVal.toFixed(2)}` : "";
      subEl.textContent = `${expiryLabel} \xB7 ${S.timeframe}${strikePart}${totalPart}`;
    }
  } else {
    valEl.textContent = "\u2014";
    valEl.className = "";
    chip.title = "PCR quote data loading\u2026";
    if (subEl) subEl.textContent = `${expiryLabel} \xB7 ${S.timeframe}`;
  }
}
function renderPCR() {
  const pcrEl = $("pcr-value");
  const pcrChip = $("pcr-chip");
  const pcr = computePCR();
  if (pcrEl) {
    if (!pcr || pcr.pcrOI == null && pcr.pcrVol == null) {
      pcrEl.textContent = "\u2014";
      pcrEl.className = "";
      if (pcrChip) pcrChip.title = "Put/Call Ratio: loading chain data";
    } else {
      const primaryPCR = pcr.pcrOI != null ? pcr.pcrOI : pcr.pcrVol;
      const isOI = pcr.pcrOI != null;
      pcrEl.textContent = primaryPCR.toFixed(2);
      pcrEl.className = primaryPCR > 1.02 ? "up" : primaryPCR < 0.98 ? "down" : "";
      if (pcrChip) {
        const sentiment = primaryPCR >= 1.3 ? "Strong Bullish / Put Support" : primaryPCR >= 1.05 ? "Mild Bullish" : primaryPCR >= 0.95 ? "Neutral" : primaryPCR >= 0.7 ? "Mild Bearish" : "Strong Bearish / Call Resistance";
        const oiText = isOI ? `Put OI: ${fmtCompact(pcr.totalPutOI)} | Call OI: ${fmtCompact(pcr.totalCallOI)}` : "OI: unavailable";
        const volText = pcr.pcrVol != null ? `Vol PCR: ${pcr.pcrVol.toFixed(2)} (Put: ${fmtCompact(pcr.totalPutVol)} | Call: ${fmtCompact(pcr.totalCallVol)})` : "";
        pcrChip.title = `PCR (${isOI ? "Open Interest" : "Volume"}): ${primaryPCR.toFixed(2)} [${sentiment}]
${oiText}
${volText}`.trim();
      }
    }
  }
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
      if (ltpCell) ltpCell.textContent = `\u20B9${fmt(tgt.ltp)}`;
      const pnlCell = document.querySelector(`[data-bracket-pnl="${tgt.id}"]`);
      if (pnlCell && tgt.status === "IN_POSITION") {
        const pnl = (tgt.ltp - tgt.entryPrice) * tgt.shares * (tgt.action === "buy" ? 1 : -1);
        pnlCell.className = `${directionClass(pnl)} font-bold`;
        pnlCell.textContent = fmtSigned(pnl, 0);
      }
    }
  }
}
function selectDefaultStrike() {
  if (!S.chain.length) {
    S.selected = { type: "CE", CE: null, PE: null };
    return;
  }
  const atm = computeATM() ?? S.chain[Math.floor(S.chain.length / 2)].strike;
  const savedCE = num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-${S.expiry}-CE`)) || num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-CE`));
  const savedPE = num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-${S.expiry}-PE`)) || num(localStorage.getItem(`scalper-last-strike-${S.activeIndex}-PE`));
  const ceStrike = savedCE && S.chain.some((r) => r.strike === savedCE) ? savedCE : atm;
  const peStrike = savedPE && S.chain.some((r) => r.strike === savedPE) ? savedPE : atm;
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
    } catch (_) {
    }
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
  if (data.oi != null && Number(data.oi) >= 0 && Number(data.oi) <= 1e8) {
    option.oi = num(data.oi, option.oi);
  }
  if (data.bids || data.asks) {
    const merged = mergeDepthData({ bids: option.bids, asks: option.asks }, data.bids, data.asks);
    option.bids = merged.bids;
    option.asks = merged.asks;
  }
  option.quoteAvailable = option.ltp > 0;
  option.change = option.ltp - option.prevClose;
  option.changePct = option.prevClose ? option.change / option.prevClose * 100 : 0;
}
function lotSize(type = S.selected.type) {
  const option = selectedOption(type) || selectedOption(type === "CE" ? "PE" : "CE");
  return option?.lotSize || 0;
}
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
  const finishedExitIds = new Set(
    (S.autoTargets || []).filter((t) => t.status !== "IN_POSITION" && t.exitOrderId).map((t) => String(t.exitOrderId))
  );
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
        pnl: isolatedPnL
      };
      if (activeCEBracket.targetPrice > 0) {
        S.charts.ce.showTargetPreview?.(activeCEBracket.targetPrice, "TARGET");
      }
    } else if (!S.quickOrder || S.quickOrder.pane !== "ce") {
      S.charts.ce.clearTargetPreview?.();
    }
    const ceOpen = ce ? (S.orders || []).filter((o) => matchesContract(o, ce) && isOpenOrder(o) && !finishedExitIds.has(String(o.id))) : [];
    S.charts.ce.setTrades(ceTrades, { isSpot: false });
    S.charts.ce.setPosition(cePos);
    S.charts.ce.setOpenOrders(ceOpen);
  }
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
        pnl: isolatedPnL
      };
      if (activePEBracket.targetPrice > 0) {
        S.charts.pe.showTargetPreview?.(activePEBracket.targetPrice, "TARGET");
      }
    } else if (!S.quickOrder || S.quickOrder.pane !== "pe") {
      S.charts.pe.clearTargetPreview?.();
    }
    const peOpen = pe ? (S.orders || []).filter((o) => matchesContract(o, pe) && isOpenOrder(o) && !finishedExitIds.has(String(o.id))) : [];
    S.charts.pe.setTrades(peTrades, { isSpot: false });
    S.charts.pe.setPosition(pePos);
    S.charts.pe.setOpenOrders(peOpen);
  }
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
      const strike = strikeMatch ? strikeMatch[1] : t.strike ? String(t.strike) : "";
      const optType = isCE ? "CE" : isPE ? "PE" : "";
      const isBull = t.side === "B" && isCE || t.side === "S" && isPE;
      return {
        ...t,
        option_type: optType,
        strike,
        isBull
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
  } catch (_) {
  }
  for (const chart of Object.values(S.charts)) {
    chart?.toggleTrades(S.showTrades);
  }
  for (const button of $$(".chart-tools button[data-chart-action='trades']")) {
    button.classList.toggle("active", S.showTrades);
    button.setAttribute("aria-pressed", S.showTrades ? "true" : "false");
  }
  updateChartTrades();
  toast(S.showTrades ? "Orders & execution overlays enabled" : "Orders & execution overlays hidden", "ok", 2e3);
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
  const idx = [...S.boot.indices || [], ...S.boot.commodities || []].find((item) => item.key === S.activeIndex);
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
    timeZone: "Asia/Kolkata",
    year: "numeric",
    month: "2-digit",
    day: "2-digit"
  }).formatToParts(/* @__PURE__ */ new Date());
  const dateValues = Object.fromEntries(dateParts.map((part) => [part.type, part.value]));
  const sessionDate = `${dateValues.year}-${dateValues.month}-${dateValues.day}`;
  const paneTargets = [
    ["ce", chartInstrument("CE"), "Choose a strike to show chart data."],
    ["spot", activeSpotInstrument(), "Loading spot index data\u2026"],
    ["pe", chartInstrument("PE"), "Choose a strike to show chart data."]
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
      setChartHint(pane, S.marketOpen ? live.length ? "" : "Waiting for live ticks\u2026" : "Kotak does not provide historical MCX candles. Live candles appear during MCX trading hours.");
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
      setChartHint(pane, "Loading historical candles\u2026");
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
          const retryDelay = Number.isFinite(retryHeader) && retryHeader > 0 ? Math.min(3e4, retryHeader * 1e3) : 5e3;
          S.historicalCache.set(cacheKey, { retryAt: Date.now() + retryDelay, error: new Error(result.error) });
        } else {
          const rawCandles = (result.candles || []).map((c) => ({
            time: num(c.time ?? c.ts),
            open: num(c.open),
            high: num(c.high),
            low: num(c.low),
            close: num(c.close),
            volume: num(c.volume)
          }));
          const validCandles = rawCandles.map((c) => {
            if (c.low <= 0 && (c.open > 0 || c.close > 0 || c.high > 0)) {
              const pos = [c.open, c.close, c.high].filter((v) => v > 0);
              c.low = pos.length ? Math.min(...pos) : 0;
            }
            if (c.open <= 0) c.open = c.low > 0 ? c.low : c.close;
            if (c.close <= 0) c.close = c.open > 0 ? c.open : c.low;
            if (c.high <= 0 || c.high < Math.max(c.open, c.close, c.low)) {
              c.high = Math.max(c.open, c.close, c.low);
            }
            return c;
          }).filter((c) => c.time > 0 && c.open > 0 && c.high > 0 && c.low > 0 && c.close > 0 && c.high >= c.low);
          const parsed = {
            candles: validCandles,
            warning: result.warning || ""
          };
          S.historicalCache.set(cacheKey, parsed);
        }
      }
    } catch (error) {
      if (loadID !== S.chartLoadID) return;
      console.warn("candles batch request failed", error);
      const retryHeader = Number(error.retryAfter);
      const retryDelay = Number.isFinite(retryHeader) && retryHeader > 0 ? Math.min(3e4, retryHeader * 1e3) : 5e3;
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
    const currentExpectedKey = pane === "spot" ? activeSpotInstrument() : pane === "ce" ? chartInstrument("CE") : chartInstrument("PE");
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
      if (!S.candles[liveKey]?.length && entry.candles?.length) {
        S.candles[liveKey] = entry.candles.slice(-500).map((candle) => ({ ...candle }));
      }
      const dataToSet = S.candles[liveKey]?.length ? mergeChartCandles(entry.candles, S.candles[liveKey]) : entry.candles;
      chart.setData(dataToSet, { symbol: key });
      setChartHint(pane, entry.warning || (entry.candles.length ? "" : "Waiting for live ticks\u2026"));
    }
  }
  renderPaneHeaders();
  updateChartTrades();
}
function mergeChartCandles(historical, live) {
  const byTime = new Map(
    (historical || []).filter((c) => c && c.low > 0 && c.open > 0 && c.high > 0 && c.close > 0 && c.high >= c.low).map((candle) => [candle.time, candle])
  );
  for (const candle of live || []) {
    if (candle && candle.low > 0 && candle.open > 0 && candle.high > 0 && candle.close > 0 && candle.high >= candle.low) {
      byTime.set(candle.time, candle);
    }
  }
  return [...byTime.values()].sort((a, b) => a.time - b.time);
}
function setChartHint(pane, message) {
  const hint = $("hint-" + pane);
  hint.textContent = message;
  hint.hidden = !message;
}
function updateLiveCandle(key, price, timestamp, volume) {
  if (!Number.isFinite(Number(price)) || Number(price) <= 0) return;
  const timeMs = num(timestamp) > 1e12 ? num(timestamp) : num(timestamp) * 1e3;
  const time = Math.floor((timeMs > 0 ? timeMs : Date.now()) / 1e3);
  const interval = { "1m": 60, "3m": 180, "5m": 300, "15m": 900, "1h": 3600 }[S.timeframe];
  if (!interval) return;
  const sessionOpen = activeCommodity() ? "09:00" : S.boot.marketSession?.open || "09:15";
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
      dayVolume: cumVolume || 0
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
  $("ce-ltp").textContent = ce ? fmt(ce.ltp) : "\u2014";
  $("ce-change").textContent = ce ? `${fmtSigned(ce.change)} (${fmtSigned(ce.changePct)}%)` : "\u2014";
  $("ce-change").className = "pane-change " + directionClass(ce?.changePct);
  renderOptionStrikeSelect("CE", ce);
  renderPanePCR("ce", ce, chainPCR, expiryLabel);
  const spotTitle = $("spot-title");
  if (spotTitle) spotTitle.textContent = spot?.displayName || S.activeIndex || "UNDERLYING";
  const spotSub = $("spot-sub");
  if (spotSub) spotSub.textContent = `Spot \xB7 ${S.timeframe}`;
  const spotLtp = $("spot-ltp");
  if (spotLtp) spotLtp.textContent = spot?.ltp ? fmt(spot.ltp) : "\u2014";
  const spotChange = $("spot-change");
  if (spotChange) {
    spotChange.textContent = spot?.ltp ? `${fmtSigned(spot.change)} (${fmtSigned(spot.changePct)}%)` : "\u2014";
    spotChange.className = "pane-change " + directionClass(spot?.changePct);
  }
  $("pe-title").textContent = pe ? `${S.activeIndex} ${fmtStrike(pe.strike)} PE` : "PUT";
  $("pe-ltp").textContent = pe ? fmt(pe.ltp) : "\u2014";
  $("pe-change").textContent = pe ? `${fmtSigned(pe.change)} (${fmtSigned(pe.changePct)}%)` : "\u2014";
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
  const strikes = S.chain.filter((row) => row[field]).map((row) => row.strike);
  const options = strikes.map((strike) => `<option value="${strike}">${fmtStrike(strike)}</option>`);
  if (selected && !S.chain.some((row) => row.strike === selected.strike && row[field])) {
    options.push(`<option value="${selected.strike}">${fmtStrike(selected.strike)}</option>`);
    strikes.push(selected.strike);
  }
  const optionsKey = strikes.join(",");
  if (select.dataset.optionsKey !== optionsKey) {
    select.innerHTML = options.length ? options.join("") : '<option value="">\u2014</option>';
    select.dataset.optionsKey = optionsKey;
  }
  select.value = selected ? String(selected.strike) : "";
  select.disabled = options.length === 0;
  select.setAttribute("aria-label", `${type === "CE" ? "Call" : "Put"} strike`);
}
function expiryText(date) {
  if (!date) return "\u2014";
  const expiry = S.expiries.find((e) => e.date === date);
  return expiry?.label || date;
}
function renderAll(preserveChainScroll = false) {
  renderHeader();
  renderToolbar();
  renderChain(preserveChainScroll);
  renderOrderPad();
  renderTab();
  renderFooter();
}
function renderHeader() {
  const order = [...S.boot.indices || [], ...S.boot.commodities || []].map((i) => i.key);
  if (S.spots.has("INDIA VIX")) order.push("INDIA VIX");
  $("spot-strip").innerHTML = order.map((key) => {
    const spot = S.spots.get(key);
    if (!spot) {
      return `<span class="spot-chip${key === S.activeIndex ? " active" : ""}" data-index="${esc(key)}">
        <span class="spot-name">${esc(key)}</span><span class="spot-price">\u2014</span><span class="spot-change">\u2014</span></span>`;
    }
    const cls = directionClass(spot.changePct ?? spot.change);
    return `<span class="spot-chip${key === S.activeIndex ? " active" : ""}" data-index="${esc(key)}">
      <span class="spot-name">${esc(spot.displayName || key)}</span>
      <span class="spot-price">${fmt(spot.ltp)}</span>
      <span class="spot-change ${cls}">${fmtSigned(spot.change)} \xB7 ${fmtSigned(spot.changePct)}%</span>
    </span>`;
  }).join("");
  const available = num(S.limits?.available);
  $("margin-chip").textContent = S.limits?.loaded ? `\u20B9${fmt(available, 0)}` : S.limits?.error ? "N/A" : "\u2014";
  $("margin-chip").title = S.limits?.error || (S.limits?.loaded ? "Available account margin" : "Account margin not loaded");
  const totalPnl = (S.positions || []).reduce((sum, p) => sum + num(p.pnl), 0);
  const pnlChip = $("pnl-chip");
  if (S.session?.authenticated && (S.positionsLoaded || S.positions && S.positions.length > 0)) {
    pnlChip.textContent = `\u20B9${fmtSigned(totalPnl, 0)}`;
  } else {
    pnlChip.textContent = totalPnl ? `\u20B9${fmtSigned(totalPnl, 0)}` : "\u2014";
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
  renderPCR();
}
function expiryOptionsHTML() {
  if (!S.expiries.length) return `<option value="">no expiries</option>`;
  const weekly = S.expiries.filter((e) => !e.monthly && !e.expired);
  const monthly = S.expiries.filter((e) => e.monthly && !e.expired);
  let html = "";
  if (weekly.length) {
    html += `<optgroup label="Weekly">${weekly.map((e) => `<option value="${esc(e.date)}">${esc(e.label)} \xB7 W</option>`).join("")}</optgroup>`;
  }
  if (monthly.length) {
    html += `<optgroup label="Monthly">${monthly.map((e) => `<option value="${esc(e.date)}">${esc(e.label)} \xB7 M</option>`).join("")}</optgroup>`;
  }
  if (!html) {
    html = S.expiries.map((e) => `<option value="${esc(e.date)}">${esc(e.label)}</option>`).join("");
  }
  return html;
}
function renderChain(preserveScroll = false) {
  const body = $("chain-body");
  if (!S.chain.length) {
    body.innerHTML = `<div class="empty-state">Waiting for the option chain\u2026</div>`;
    $("strike-meta").textContent = "\u2014";
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
      <button type="button" class="cell-ce${callITM ? " itm" : ""}${callSelected ? " selected" : ""}${call ? ` ${directionClass(call.changePct)}` : ""}" data-select="CE" data-strike="${row.strike}" title="${call ? `${fmt(call.ltp)} \xB7 ${fmtSigned(call.changePct)}% \xB7 Vol ${fmtCompact(call.volume)}${call.oi ? ` \xB7 OI ${fmtCompact(call.oi)}` : ""}` : "Call quote unavailable"}">${call ? `<span class="chain-ltp">${fmt(call.ltp)}</span><span class="chain-volume">VOL ${fmtCompact(call.volume)}</span>` : "\u2014"}</button>
      <button type="button" class="cell-strike" data-select="${esc(S.selected.type)}" data-strike="${row.strike}">${isATM ? '<span class="atm-badge">ATM</span>' : ""}${fmtStrike(row.strike)}</button>
      <button type="button" class="cell-pe${putITM ? " itm" : ""}${putSelected ? " selected" : ""}${put ? ` ${directionClass(put.changePct)}` : ""}" data-select="PE" data-strike="${row.strike}" title="${put ? `${fmt(put.ltp)} \xB7 ${fmtSigned(put.changePct)}% \xB7 Vol ${fmtCompact(put.volume)}${put.oi ? ` \xB7 OI ${fmtCompact(put.oi)}` : ""}` : "Put quote unavailable"}">${put ? `<span class="chain-ltp">${fmt(put.ltp)}</span><span class="chain-volume">VOL ${fmtCompact(put.volume)}</span>` : "\u2014"}</button>
    </div>`;
  }).join("");
  const pcr = computePCR();
  const pcrSummary = pcr && pcr.pcrOI != null ? ` \xB7 PCR ${pcr.pcrOI.toFixed(2)}` : pcr && pcr.pcrVol != null ? ` \xB7 Vol PCR ${pcr.pcrVol.toFixed(2)}` : "";
  $("strike-meta").textContent = `${S.chain.length} strikes${pcrSummary} \xB7 ${expiryText(S.expiry)}`;
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
  const pcrSummary = pcr && pcr.pcrOI != null ? ` \xB7 PCR ${pcr.pcrOI.toFixed(2)}` : pcr && pcr.pcrVol != null ? ` \xB7 Vol PCR ${pcr.pcrVol.toFixed(2)}` : "";
  $("strike-meta").textContent = query ? `${visible} matching strike${visible === 1 ? "" : "s"} \xB7 ${S.chain.length} available` : `${S.chain.length} strikes${pcrSummary} \xB7 ${expiryText(S.expiry)}`;
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
        toast(`Strike ${fmtStrike(target)} is not available for ${index} ${expiry}.`, "warn", 5e3);
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
  const targetStrike = forceATM ? atm : selectedOption(S.selected.type)?.strike || atm;
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
var marginDebounceTimers = {};
var lastMarginReqKey = {};
function requestBrokerMargin(type, option, shares) {
  if (!option || !shares || !S.session?.authenticated) return;
  const prefix = type === "CE" ? "call" : "put";
  if (marginDebounceTimers[type]) {
    clearTimeout(marginDebounceTimers[type]);
  }
  marginDebounceTimers[type] = setTimeout(async () => {
    const token = option.pSymbol || (option.instrumentKey ? option.instrumentKey.split("|")[1] : "");
    const seg = option.exchangeSegment || "nse_fo";
    try {
      const res = await postJSON("/api/broker/margin/required", {
        exchange_segment: seg,
        instrument_token: token,
        trading_symbol: option.symbol,
        transaction_type: "B",
        order_type: "MKT",
        product: S.product,
        quantity: shares,
        price: option.ltp || 0
      });
      const data = res?.data || res;
      const ordMrgn = num(data?.ordMrgn ?? data?.order_margin);
      const insuf = num(data?.insufFund ?? data?.insufficient_funds);
      const buyEl = $(`leg-${prefix}-margin-buy`);
      if (buyEl && ordMrgn > 0) {
        if (insuf > 0) {
          buyEl.textContent = `B \u20B9${fmt(ordMrgn, 0)} (\u26A0\uFE0F Short \u20B9${fmt(insuf, 0)})`;
          buyEl.className = "margin-buy down";
        } else {
          buyEl.textContent = `B \u20B9${fmt(ordMrgn, 0)}`;
          buyEl.className = "margin-buy up";
        }
      }
    } catch (_) {
    }
    try {
      const res = await postJSON("/api/broker/margin/required", {
        exchange_segment: seg,
        instrument_token: token,
        trading_symbol: option.symbol,
        transaction_type: "S",
        order_type: "MKT",
        product: S.product,
        quantity: shares,
        price: option.ltp || 0
      });
      const data = res?.data || res;
      const ordMrgn = num(data?.ordMrgn ?? data?.order_margin);
      const insuf = num(data?.insufFund ?? data?.insufficient_funds);
      const sellEl = $(`leg-${prefix}-margin-sell`);
      if (sellEl && ordMrgn > 0) {
        if (insuf > 0) {
          sellEl.textContent = `S \u20B9${fmt(ordMrgn, 0)} (\u26A0\uFE0F Short \u20B9${fmt(insuf, 0)})`;
          sellEl.className = "margin-sell down";
        } else {
          sellEl.textContent = `S \u20B9${fmt(ordMrgn, 0)}`;
          sellEl.className = "margin-sell up";
        }
      }
    } catch (_) {
    }
  }, 300);
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
      toggleBtn.title = isShares ? "Quantity shown in Shares. Click to switch to Lots." : "Quantity shown in Lots. Click to switch to Shares.";
    }
    const valBtn = document.querySelector(`.validity-toggle[data-validity-toggle="${type}"]`);
    if (valBtn) {
      const v = S.orderValidity?.[type] || "DAY";
      valBtn.textContent = v;
      valBtn.classList.toggle("active", v === "IOC");
      valBtn.title = `Validity: ${v} (Click to toggle DAY/IOC)`;
    }
    const buyEl = $(`leg-${prefix}-margin-buy`);
    const sellEl = $(`leg-${prefix}-margin-sell`);
    const ltp = option ? num(option.ltp) : 0;
    if (!option || ltp <= 0 || !shares) {
      if (buyEl) {
        buyEl.textContent = "\u2014";
        buyEl.className = "margin-buy";
        buyEl.title = "Buy margin";
      }
      if (sellEl) {
        sellEl.textContent = "\u2014";
        sellEl.className = "margin-sell";
        sellEl.title = "Sell margin";
      }
    } else {
      const reqBuy = ltp * shares;
      const reqSell = requiredMargin(option, shares, "S");
      const sellFinite = Number.isFinite(reqSell);
      if (buyEl) {
        buyEl.textContent = `B \u20B9${fmt(reqBuy, 0)}`;
        if (hasLimits && available !== null) {
          buyEl.className = `margin-buy ${available >= reqBuy ? "up" : "down"}`;
          buyEl.title = `Buy: \u20B9${fmt(reqBuy, 2)} \xB7 Available: \u20B9${fmt(available, 0)} (${available >= reqBuy ? "Sufficient" : "Insufficient"})`;
        } else {
          buyEl.className = "margin-buy";
          buyEl.title = `Buy margin: \u20B9${fmt(reqBuy, 2)}`;
        }
      }
      if (sellEl) {
        sellEl.textContent = sellFinite ? `S \u20B9${fmt(reqSell, 0)}` : "S \u2014";
        if (sellFinite && hasLimits && available !== null) {
          sellEl.className = `margin-sell ${available >= reqSell ? "up" : "down"}`;
          sellEl.title = `Sell: \u20B9${fmt(reqSell, 2)} \xB7 Available: \u20B9${fmt(available, 0)} (${available >= reqSell ? "Sufficient" : "Insufficient"})`;
        } else {
          sellEl.className = "margin-sell";
          sellEl.title = sellFinite ? `Sell margin: \u20B9${fmt(reqSell, 2)}` : "Sell margin unavailable (no spot price)";
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
    pnl: clean.reduce((sum, leg) => sum + leg.qty * (intrinsic(leg.type, leg.strike, spot) - leg.avg), 0)
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
    if (a.pnl < 0 && b.pnl > 0 || a.pnl > 0 && b.pnl < 0) {
      const ratio = -a.pnl / (b.pnl - a.pnl);
      const be = a.spot + ratio * (b.spot - a.spot);
      if (!breakevens.some((v) => Math.abs(v - be) < 1e-3)) breakevens.push(be);
    }
  }
  return { points, maxProfit, maxLoss, breakevens, unlimitedProfit, unlimitedLoss };
}
function buildRiskGroups(positions) {
  const groups = /* @__PURE__ */ new Map();
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
      avg: num(position.average_price)
    });
  }
  return [...groups.values()].map((group) => {
    const payoff = computePayoff(group.legs);
    return { ...group, ...payoff, netPremium: group.legs.reduce((sum, leg) => sum - leg.qty * leg.avg, 0) };
  });
}
function switchTab(tabName) {
  S.tab = tabName;
  if (S.tab === "brackets") {
    fetchBrackets().catch(() => {
    });
  }
  renderTab();
}
function closeMobileDrawer() {
  const backdrop = $("mobile-drawer-backdrop");
  if (backdrop) backdrop.hidden = true;
}
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
        pnlEl.textContent = `${totalPnl >= 0 ? "+" : ""}\u20B9${fmt(totalPnl, 0)}`;
        pnlEl.className = `panic-pill-pnl ${totalPnl >= 0 ? "up" : "down"}`;
      }
    } else {
      mobilePanicPill.hidden = true;
    }
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
  else if (S.tab === "holdings") content.innerHTML = holdingsHTML();
  else if (S.tab === "depth") content.innerHTML = depthHTML();
}
function positionsHTML() {
  if (!S.positions.length) return `<div class="empty-state">No open positions. Sign in with Kotak to view your account or place an order.</div>`;
  const sorted = [...S.positions].sort(comparePositions);
  const groups = /* @__PURE__ */ new Map();
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
      avg: num(p.average_price)
    }))) : null;
    const groupMeta = payoff ? ` \u2014 group Max P ${payoff.maxProfit === Infinity ? "\u221E" : `\u20B9${fmt(payoff.maxProfit, 0)}`} \xB7 Max L ${payoff.maxLoss === -Infinity ? "\u221E" : `\u20B9${fmt(payoff.maxLoss, 0)}`}` : " \u2014 closed";
    html += `<tr class="group-row${hasOpenInGroup ? "" : " closed"}"><td colspan="8">${esc(key.replace("|", " \xB7 "))}${groupMeta}</td></tr>`;
    for (const p of list) {
      const risk = legRisk(p);
      const isOpen = Math.abs(num(p.quantity)) > 0;
      const openPnL = p.unrealised_pnl != null ? p.unrealised_pnl : p.pnl;
      html += `<tr class="${isOpen ? "" : "closed"}" data-pos-row="${esc(p.instrument_key)}">
        <td class="sym">${esc(p.symbol || `${p.underlying} ${fmtStrike(p.strike)} ${p.option_type}`)}</td>
        <td>${p.quantity > 0 ? "+" : ""}${p.quantity}</td>
        <td>${fmt(p.average_price)}</td>
        <td data-pos-ltp="${esc(p.instrument_key)}">${fmt(p.ltp)}</td>
        <td class="${directionClass(isOpen ? openPnL : p.pnl)}" data-pos-pnl="${esc(p.instrument_key)}">
          <span class="font-bold">${fmtSigned(isOpen ? openPnL : p.pnl, 0)}</span>
          ${isOpen && p.realised_pnl ? `<span class="muted" style="display:block;font-size:9.5px;" title="Realised earlier today">Day: ${fmtSigned(p.pnl, 0)}</span>` : ""}
        </td>
        <td>${risk.maxProfit === Infinity ? "\u221E" : `\u20B9${fmt(risk.maxProfit, 0)}`}</td>
        <td>${risk.maxLoss === -Infinity ? "\u221E" : `\u20B9${fmt(risk.maxLoss, 0)}`}</td>
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
    return long ? { maxProfit: Infinity, maxLoss: avg * qty } : { maxProfit: avg * qty, maxLoss: -Infinity };
  }
  return long ? { maxProfit: (strike - avg) * qty, maxLoss: avg * qty } : { maxProfit: avg * qty, maxLoss: (strike - avg) * qty };
}
function riskHTML() {
  const groups = S.risk?.length ? S.risk : buildRiskGroups(S.positions);
  if (!groups.length) {
    return `<div class="empty-state">No open legs. The at-expiry payoff appears here once a position is open.</div>`;
  }
  return `<div class="risk-cards">${groups.map((group, index) => {
    const maxP = group.maxProfit === Infinity ? "\u221E" : `\u20B9${fmt(num(group.maxProfit), 0)}`;
    const maxL = group.maxLoss === -Infinity ? "\u221E" : `\u20B9${fmt(num(group.maxLoss), 0)}`;
    const be = (group.breakevens || []).map((b) => fmt(num(b), 2)).join(", ") || "\u2014";
    const flag = group.unlimitedLoss ? `<span class="risk-flag bad">unlimited loss</span>` : group.unlimitedProfit ? `<span class="risk-flag good">unlimited profit</span>` : "";
    return `<article class="risk-card">
      <div class="risk-card-head">
        <div>
          <div class="risk-card-title">${esc(group.underlying || "\u2014")} \xB7 ${esc(expiryText(group.expiry) || "\u2014")}</div>
          <div class="risk-card-sub">at expiry \xB7 theoretical \xB7 gross of charges</div>
        </div>
        ${flag}
      </div>
      <div class="risk-metrics">
        <div class="risk-metric"><span>Net premium</span><b>\u20B9${fmt(num(group.netPremium), 0)}</b></div>
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
  const xOf = (x2) => pad + (x2 - minX) / Math.max(1, maxX - minX) * (width - pad * 2);
  const yOf = (y) => pad + (maxY - y) / Math.max(1, maxY - minY) * (height - pad * 2);
  const isLight = document.body.dataset.theme === "light";
  ctx.strokeStyle = isLight ? "#cbd5e1" : "#1a232c";
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(pad, yOf(0));
  ctx.lineTo(width - pad, yOf(0));
  ctx.stroke();
  ctx.beginPath();
  points.forEach((point, index) => {
    const x2 = xOf(point.spot);
    const y = yOf(point.pnl);
    if (index === 0) ctx.moveTo(x2, y);
    else ctx.lineTo(x2, y);
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
    const x2 = xOf(spot);
    ctx.strokeStyle = isLight ? "#0284c7" : "#58a6ff";
    ctx.setLineDash([2, 3]);
    ctx.beginPath();
    ctx.moveTo(x2, 2);
    ctx.lineTo(x2, height - 2);
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = isLight ? "#0284c7" : "#58a6ff";
    ctx.font = "8px ui-monospace, Menlo, monospace";
    ctx.fillText("spot", Math.min(width - 24, x2 + 3), 9);
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
    <td class="sym font-bold">${esc(o.symbol || "\u2014")}</td>
    <td class="${o.side === "B" ? "up" : "down"} font-bold">${o.side === "B" ? "BUY" : o.side === "S" ? "SELL" : esc(o.side)}</td>
    <td>${Math.max(0, o.quantity - (num(o.filled_quantity) || 0))} / ${o.quantity}</td>
    <td class="font-bold">\u20B9${o.price ? fmt(o.price) : "MKT"}</td>
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
    <td>${o.status}${o.reason ? `<span class="muted"> \xB7 ${esc(o.reason)}</span>` : ""}</td>
    <td>${fmtTime(o.timestamp || o.placed_at)}</td>
    <td>${isOpenOrder(o) ? `<button type="button" class="btn-cancel-order danger small ghost" data-order-id="${esc(o.id)}" title="Cancel order ${esc(o.id)}">Cancel</button>` : "\u2014"}</td>
  </tr>`).join("")}</tbody></table>`;
}
function cancelBrokerOrder(orderId) {
  return requestJSON(`/api/broker/orders/${encodeURIComponent(orderId)}`, {
    method: "DELETE",
    body: JSON.stringify({ confirm: true })
  });
}
async function onCancelOrder(orderId) {
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before managing orders.", "warn");
    return;
  }
  if (!orderId || orderId === "\u2014") {
    toast("Invalid order ID.", "error");
    return;
  }
  const target = (S.autoTargets || []).find((t) => t.orderId === orderId && t.status === "PENDING_ENTRY");
  if (target) {
    target.status = "CANCELLED";
    saveAutoTargets();
  }
  try {
    toast(`Cancelling order ${orderId}\u2026`, "info", 2e3);
    await cancelBrokerOrder(orderId);
    playTradeSound("reject");
    toast(`Order ${orderId} cancelled.`, "ok", 3e3);
    scheduleBooksRefresh(300);
  } catch (err) {
    toast(`Failed to cancel order ${orderId}: ${err.message}`, "error", 5e3);
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
    toast("No open orders to cancel.", "info", 2e3);
    return;
  }
  const ok = await confirmAction({
    title: "Cancel All Open Orders",
    body: `Are you sure you want to cancel all <b>${openOrders.length}</b> open limit order(s)?`,
    okLabel: "Cancel All",
    danger: true
  });
  if (!ok) return;
  toast(`Cancelling ${openOrders.length} open order(s)\u2026`, "info", 3e3);
  let cancelled = 0;
  for (const o of openOrders) {
    if (!o.id || o.id === "\u2014") continue;
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
  toast(`Cancelled ${cancelled} of ${openOrders.length} order(s).`, "ok", 4e3);
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
    <td>\u20B9${fmt(t.price * t.quantity, 0)}</td>
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
  const totalPnLPct = totalInvested > 0 ? totalPnL / totalInvested * 100 : 0;
  let html = `<div class="holdings-summary" style="display:flex; gap:20px; padding:10px 14px; margin-bottom:8px; background:rgba(255,255,255,0.03); border-radius:6px; font-size:12px;">
    <div><span class="muted">Total Invested:</span> <strong>\u20B9${fmt(totalInvested, 2)}</strong></div>
    <div><span class="muted">Current Value:</span> <strong>\u20B9${fmt(totalCurrent, 2)}</strong></div>
    <div><span class="muted">Overall P&amp;L:</span> <strong class="${directionClass(totalPnL)}">${fmtSigned(totalPnL, 2)} (${fmtSigned(totalPnLPct, 2)}%)</strong></div>
  </div>`;
  html += `<table class="book"><thead><tr>
    <th>Symbol</th><th>Qty</th><th>Avg Price</th><th>LTP</th><th>Invested</th><th>Current</th><th>P&amp;L</th><th>P&amp;L %</th>
  </tr></thead><tbody>`;
  for (const h of S.holdings) {
    html += `<tr>
      <td class="sym font-bold">${esc(h.symbol)} <span class="muted" style="font-size:10px;">${esc(h.segment)}</span></td>
      <td>${h.quantity}</td>
      <td>\u20B9${fmt(h.average_price)}</td>
      <td>\u20B9${fmt(h.ltp)}</td>
      <td>\u20B9${fmt(h.invested, 2)}</td>
      <td>\u20B9${fmt(h.current_value, 2)}</td>
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
  body.innerHTML = `<div class="empty-state">Loading history for order ${esc(orderId)}\u2026</div>`;
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
        <div>Symbol: <strong>${esc(first.trdSym || first.tradingSymbol || first.sym || "\u2014")}</strong></div>
        <div>Type: <strong>${esc(first.prcTp || first.order_type || "\u2014")}</strong></div>
        <div>Prod: <strong>${esc(first.prod || first.product || "\u2014")}</strong></div>
      `;
    }
    let html = `<table class="book" style="width:100%; font-size:12px;"><thead><tr>
      <th>Status</th><th>Filled / Total</th><th>Price</th><th>Avg Price</th><th>Reject Reason</th><th>Timestamp</th>
    </tr></thead><tbody>`;
    for (const h of historyList) {
      const st = (h.ordSt || h.status || "").toLowerCase();
      const statusClass = st === "complete" ? "up" : st === "rejected" || st === "cancelled" ? "down" : "warn";
      const rej = h.rejRsn && h.rejRsn !== "--" && h.rejRsn !== "" ? h.rejRsn : "\u2014";
      const tm = h.exchTmstp || h.timestamp || h.orderDateTime || h.time || "\u2014";
      const qty = h.qty || h.quantity || 0;
      const fldQty = h.fldQty ?? h.filledQuantity ?? 0;
      const prc = num(h.prc || h.price);
      const avgPrc = num(h.avgPrc || h.average_price);
      html += `<tr>
        <td class="${statusClass}"><strong>${esc(h.ordSt || h.status || "\u2014")}</strong></td>
        <td>${fldQty} / ${qty}</td>
        <td>${prc ? fmt(prc) : "MKT"}</td>
        <td>${avgPrc ? fmt(avgPrc) : "\u2014"}</td>
        <td class="${rej !== "\u2014" ? "down font-bold" : "muted"}">${esc(rej)}</td>
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
      <div class="depth-column-header"><h3>${type} \u2014 No strike selected</h3></div>
      <div class="empty-state">Select a ${type} strike from the chain</div>
    </div>`;
  }
  const cached = S.depths?.[option.instrumentKey] || {};
  let bids = cached.bids && cached.bids.length ? cached.bids : option.bids || [];
  let asks = cached.asks && cached.asks.length ? cached.asks : option.asks || [];
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
      <td class="col-orders">${b && b.orders ? b.orders : b ? "\u2014" : ""}</td>
      <td class="col-qty">${b && b.quantity ? b.quantity.toLocaleString("en-IN") : b ? "\u2014" : ""}</td>
      <td class="col-bid-px">${b && b.price ? fmt(b.price) : ""}</td>
      <td class="col-ask-px">${a && a.price ? fmt(a.price) : ""}</td>
      <td class="col-qty">${a && a.quantity ? a.quantity.toLocaleString("en-IN") : a ? "\u2014" : ""}</td>
      <td class="col-orders">${a && a.orders ? a.orders : a ? "\u2014" : ""}</td>
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
            <td colspan="2" class="total-bid-qty">${totalBidQty ? totalBidQty.toLocaleString("en-IN") : "\u2014"}</td>
            <td colspan="2" class="total-label">Total Quantity</td>
            <td colspan="2" class="total-ask-qty">${totalAskQty ? totalAskQty.toLocaleString("en-IN") : "\u2014"}</td>
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
  if (!option.quoteAvailable || option.ltp <= 0) {
    toast("No live quote is available for this contract; order was not submitted.", "error", 6e3);
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
    toast("A live underlying quote is required to calculate short-order margin.", "error", 6e3);
    return;
  }
  if (orderMode !== "live" && required > available) {
    toast(`Insufficient margin: required \u20B9${fmt(required, 0)}, available \u20B9${fmt(available, 0)}.`, "error", 5e3);
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
    lot_size: lot
  };
  try {
    const result = await postOrder(payload);
    lastUiOrderTime = Date.now();
    playTradeSound(action);
    toast(`Live order ${result?.status?.toLowerCase() || "submitted"}.`, "ok");
    scheduleBooksRefresh(400);
  } catch (err) {
    playTradeSound("reject");
    toast(`Order rejected: ${err.message}`, "error", 6e3);
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
    toast(`Insufficient margin: required \u20B9${fmt(required, 0)}, available \u20B9${fmt(available, 0)}.`, "error", 5e3);
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
    lot_size: lot
  };
  try {
    const result = await postOrder(payload);
    lastUiOrderTime = Date.now();
    playTradeSound(action);
    const orderId = String(result?.nOrdNo ?? result?.order_id ?? result?.id ?? "");
    toast(`Live ${action === "buy" ? "Buy" : "Sell"} Limit order placed: ${qty} shares @ \u20B9${fmt(price)}.`, "ok", 4e3);
    if (autoTargetPrice > 0) {
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
        shares: qty,
        filledQty: 0,
        exitOrderId: "",
        exitPrice: 0,
        ltp: price,
        status: "PENDING_ENTRY",
        createdAt: Date.now()
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
        stoploss_price: 0,
        trailing_stop: 0
      }).catch((err) => {
        console.warn("failed to register server bracket:", err);
      });
      toast(`\u{1F3AF} Auto-Target armed: Exit at \u20B9${autoTargetPrice.toFixed(2)} (+${Math.abs(autoTargetPrice - price).toFixed(2)} pts) once filled.`, "info", 4500);
      renderTab();
    }
    scheduleBooksRefresh(400);
    return result;
  } catch (err) {
    playTradeSound("reject");
    toast(`Order rejected: ${err.message}`, "error", 6e3);
  }
}
function loadAutoTargets() {
  try {
    const raw = localStorage.getItem("scalper-auto-targets");
    if (!raw) return [];
    const list = JSON.parse(raw);
    const now = Date.now();
    return Array.isArray(list) ? list.map(normalizeBracket).filter((t) => t && now - (t.createdAt || 0) < 864e5) : [];
  } catch (_) {
    return [];
  }
}
function saveAutoTargets() {
  try {
    localStorage.setItem("scalper-auto-targets", JSON.stringify(S.autoTargets || []));
  } catch (_) {
  }
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
  const exitPrice = num(raw.exit_price ?? raw.exitPrice ?? raw.ExitPrice);
  const shares = Math.abs(num(raw.shares ?? raw.quantity ?? raw.qty ?? raw.Shares));
  const filledQty = num(raw.filled_qty ?? raw.filledQty ?? raw.filled_quantity ?? raw.FilledQty);
  const ltp = num(raw.ltp ?? raw.last_price ?? raw.LTP ?? 0);
  const sym = String(raw.trading_symbol || raw.tradingSymbol || raw.symbol || raw.Symbol || "").trim();
  let strike = num(raw.strike ?? raw.Strike);
  let type = String(raw.option_type || raw.type || raw.Type || "").toUpperCase();
  if ((!strike || !type) && sym) {
    const match = /(?:CE|PE)?.*?(\d{4,6})\s*(CE|PE)$/i.exec(sym);
    if (match) {
      if (!strike) strike = Number(match[1]);
      if (!type) type = match[2].toUpperCase();
    }
  }
  return {
    id,
    orderId: String(raw.order_id ?? raw.orderId ?? raw.OrderId ?? ""),
    symbol: sym,
    instrumentKey: raw.instrument_key || raw.instrumentKey || raw.InstrumentKey || "",
    exchangeSegment: raw.exchange_segment || raw.exchangeSegment || raw.ExchangeSegment || "NSE_FO",
    underlying: raw.underlying || raw.Underlying || S.activeIndex || "",
    strike,
    type,
    action,
    entryPrice,
    targetPrice,
    shares,
    filledQty,
    exitOrderId: String(raw.exit_order_id ?? raw.exitOrderId ?? raw.ExitOrderId ?? ""),
    exitPrice,
    ltp: ltp > 0 ? ltp : entryPrice,
    status,
    createdAt: num(raw.created_at ?? raw.createdAt ?? raw.CreatedAt) || Date.now()
  };
}
function upsertBracket(bracket) {
  if (!bracket || !bracket.id) return;
  if (!S.autoTargets) S.autoTargets = [];
  const idx = S.autoTargets.findIndex((b) => b.id === bracket.id);
  if (idx !== -1) {
    const existing = S.autoTargets[idx];
    if (!bracket.ltp && existing.ltp) bracket.ltp = existing.ltp;
    const merged = {
      ...existing,
      ...bracket,
      symbol: bracket.symbol || existing.symbol || "",
      strike: bracket.strike || existing.strike || 0,
      type: bracket.type || existing.type || ""
    };
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
  const list = Array.isArray(data) ? data : data.brackets || data.data || [];
  if (!Array.isArray(list)) return;
  const normalized = list.map(normalizeBracket).filter(Boolean);
  if (!S.autoTargets) S.autoTargets = [];
  for (const b of normalized) {
    upsertBracket(b);
  }
  saveAutoTargets();
  renderTab();
  updateChartTrades();
}
async function fetchBrackets() {
  if (!S.session?.authenticated) return;
  try {
    const res = await getJSON("/api/brackets");
    if (res) applyServerBrackets(res);
  } catch (_) {
  }
}
function handleBracketUpdate(data) {
  if (!data) return;
  const items = Array.isArray(data) ? data : Array.isArray(data.brackets) ? data.brackets : [data.bracket || data];
  for (const item of items) {
    if (!item) continue;
    const norm = normalizeBracket(item);
    if (!norm || !norm.id) continue;
    upsertBracket(norm);
    if (norm.status === "TARGET_HIT") {
      playTradeSound("target");
      const pnl = (norm.exitPrice - norm.entryPrice) * norm.shares * (norm.action === "buy" ? 1 : -1);
      toast(`\u{1F3AF} Bracket Target Hit! Sold ${norm.shares} @ \u20B9${fmt(norm.exitPrice || norm.targetPrice)} (Profit: +\u20B9${fmt(pnl, 0)})`, "ok", 8e3);
      S.charts.ce?.clearTargetPreview?.();
      S.charts.pe?.clearTargetPreview?.();
    } else if (norm.status === "CANCELLED") {
      toast(`Bracket ${norm.symbol || norm.id} cancelled.`, "warn", 4e3);
    }
  }
  saveAutoTargets();
  renderTab();
  updateChartTrades();
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
    <th>Contract</th><th>Side</th><th>Qty</th><th>Entry Price</th><th>Target</th><th>Live LTP</th><th>Isolated P&amp;L</th><th>Status</th><th>Action</th>
  </tr></thead><tbody>`;
  for (const b of [...brackets].reverse()) {
    const ltp = getInstrumentLTP(b);
    let pnl = 0;
    let pnlClass = "";
    let pnlText = "\u2014";
    if (b.status === "IN_POSITION") {
      pnl = (ltp - b.entryPrice) * b.shares * (b.action === "buy" ? 1 : -1);
      pnlClass = directionClass(pnl);
      pnlText = fmtSigned(pnl, 0);
    } else if (b.status === "TARGET_HIT" || b.status === "EXITED_MANUAL" || b.status === "COMPLETED") {
      const exitP = b.exitPrice || b.targetPrice;
      pnl = (exitP - b.entryPrice) * b.shares * (b.action === "buy" ? 1 : -1);
      pnlClass = directionClass(pnl);
      pnlText = fmtSigned(pnl, 0) + (b.status === "TARGET_HIT" ? " \u{1F3AF}" : "");
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
    let actionBtn = "\u2014";
    if (b.status === "IN_POSITION") {
      actionBtn = `<button type="button" class="btn-exit-bracket danger small" data-exit-bracket="${esc(b.id)}" title="Cancel resting target and exit immediately at market">EXIT (MKT)</button>`;
    } else if (b.status === "PENDING_ENTRY") {
      actionBtn = `<button type="button" class="btn-cancel-bracket danger small ghost" data-cancel-bracket="${esc(b.id)}" title="Cancel pending entry limit order">Cancel</button>`;
    }
    const sideLabel = b.action === "buy" ? "BUY" : "SELL";
    const sideClass = b.action === "buy" ? "up" : "down";
    const targetDiff = (b.targetPrice - b.entryPrice) * (b.action === "buy" ? 1 : -1);
    const contractDisplay = b.strike && b.type ? `${b.underlying || S.activeIndex} ${fmtStrike(b.strike)} ${b.type}` : b.symbol || `${b.underlying || S.activeIndex}`;
    html += `<tr data-bracket-id="${esc(b.id)}">
      <td class="sym font-bold" title="${esc(b.symbol)}">${esc(contractDisplay)}</td>
      <td class="${sideClass} font-bold">${sideLabel}</td>
      <td>${b.shares}</td>
      <td class="font-bold">\u20B9${fmt(b.entryPrice)}</td>
      <td class="up font-bold">\u20B9${fmt(b.targetPrice)} <span class="muted" style="font-size:9.5px;">(+${targetDiff.toFixed(2)})</span></td>
      <td class="font-bold" data-bracket-ltp="${esc(b.id)}">\u20B9${fmt(ltp)}</td>
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
    danger: true
  });
  if (!ok) return;
  try {
    toast("Exiting bracket position @ Market\u2026", "info", 2e3);
    await postJSON(`/api/brackets/${encodeURIComponent(bracketId)}/exit`, { confirm: true });
    lastUiOrderTime = Date.now();
    playTradeSound(bracket.action === "buy" ? "sell" : "buy");
    bracket.status = "EXITED_MANUAL";
    bracket.exitPrice = getInstrumentLTP(bracket);
    saveAutoTargets();
    toast("Bracket exited at market.", "ok", 4e3);
    scheduleBooksRefresh(400);
    renderTab();
    updateChartTrades();
  } catch (err) {
    playTradeSound("reject");
    toast(`Market exit failed: ${err.message}`, "error", 6e3);
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
    toast("Cancelling bracket entry order\u2026", "info", 2e3);
    await postJSON(`/api/brackets/${encodeURIComponent(bracketId)}/cancel`, { confirm: true });
    bracket.status = "CANCELLED";
    saveAutoTargets();
    if (bracket.orderId && bracket.orderId !== "\u2014") {
      const oInState = (S.orders || []).find((o) => String(o.id) === String(bracket.orderId));
      if (oInState) {
        oInState.status = "CANCELLED";
      }
    }
    playTradeSound("reject");
    toast("Bracket entry order cancelled.", "ok", 3e3);
    scheduleBooksRefresh(400);
    renderTab();
    updateChartTrades();
  } catch (err) {
    playTradeSound("reject");
    toast(`Bracket cancel failed: ${err.message}`, "error", 6e3);
  }
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
        if (matchedOrder && matchedOrder.id && matchedOrder.id !== "\u2014") {
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
        toast(`Auto-Target cancelled: entry order ${matchedOrder.id} was ${status.toLowerCase()}.`, "warn", 4e3);
        renderTab();
        continue;
      }
      const currentFilled = Math.max(filledQty, isFilledStatus ? target.shares : 0);
      const previousFilled = target.filledQty || 0;
      if (currentFilled > previousFilled) {
        target.filledQty = currentFilled;
        const matchingTrade = (S.trades || []).find((t) => matchesSameContract(t, target) && (t.id === matchedOrder.id || Math.abs((num(t.timestamp) || 0) - (target.createdAt || 0)) < 3e5));
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
        const isExitFilled = ["TRAD", "COMPLETE", "EXECUTED", "FILLED"].includes(exitStatus) || num(matchedExit.filled_quantity) >= target.shares;
        if (isExitFilled) {
          target.status = "TARGET_HIT";
          target.exitPrice = num(matchedExit.price) || target.targetPrice;
          saveAutoTargets();
          playTradeSound("target");
          const pnl = (target.exitPrice - target.entryPrice) * target.shares * (target.action === "buy" ? 1 : -1);
          toast(`\u{1F3AF} Bracket Target Hit! Sold ${target.shares} @ \u20B9${fmt(target.exitPrice)} (Profit: +\u20B9${fmt(pnl, 0)})`, "ok", 8e3);
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
function initChartQuickOrder() {
  $("cqo-close")?.addEventListener("click", closeChartQuickOrder);
  $("cqo-price-dec")?.addEventListener("click", () => {
    if (S.quickOrder) updateQuickOrderPrice(S.quickOrder.price - 0.05);
  });
  $("cqo-price-inc")?.addEventListener("click", () => {
    if (S.quickOrder) updateQuickOrderPrice(S.quickOrder.price + 0.05);
  });
  $("cqo-price")?.addEventListener("focus", (e) => {
    e.target?.select?.();
  });
  $("cqo-price")?.addEventListener("input", (e) => {
    const val = num(e.target.value);
    if (Number.isFinite(val) && val > 0 && S.quickOrder) {
      S.quickOrder.price = val;
      $("cqo-buy-price").textContent = `@ \u20B9${val.toFixed(2)}`;
      $("cqo-sell-price").textContent = `@ \u20B9${val.toFixed(2)}`;
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
  $("cqo-target-enable")?.addEventListener("change", (e) => {
    if (!S.quickOrder) return;
    S.quickOrder.targetEnabled = e.target.checked;
    try {
      localStorage.setItem("scalper-target-enabled", e.target.checked ? "true" : "false");
    } catch (_) {
    }
    renderQuickOrderTarget();
    if (S.quickOrder.targetEnabled) {
      S.charts[S.quickOrder.pane]?.showTargetPreview(S.quickOrder.targetPrice, "TARGET");
    } else {
      S.charts[S.quickOrder.pane]?.clearTargetPreview();
    }
  });
  $("cqo-target-dec")?.addEventListener("click", () => {
    if (!S.quickOrder) return;
    const currentPts = S.quickOrder.targetPts || 0.5;
    const newPts = Math.max(0.05, Math.round((currentPts - 0.5) * 20) / 20);
    updateQuickOrderTargetPts(newPts);
  });
  $("cqo-target-inc")?.addEventListener("click", () => {
    if (!S.quickOrder) return;
    const currentPts = S.quickOrder.targetPts || 0.5;
    const newPts = Math.round((currentPts + 0.5) * 20) / 20;
    updateQuickOrderTargetPts(newPts);
  });
  $("cqo-target-price")?.addEventListener("focus", (e) => {
    e.target?.select?.();
  });
  $("cqo-target-price")?.addEventListener("input", (e) => {
    if (!S.quickOrder) return;
    const rawVal = e.target.value;
    const val = parseFloat(rawVal);
    if (!isNaN(val) && val > 0) {
      S.quickOrder.targetPrice = val;
      if (val > S.quickOrder.price) {
        const pts = Math.round((val - S.quickOrder.price) * 100) / 100;
        S.quickOrder.targetPts = Math.max(0.05, pts);
        try {
          localStorage.setItem("scalper-target-pts", String(S.quickOrder.targetPts));
        } catch (_) {
        }
        const shares = S.quickOrder.lotsCount * S.quickOrder.lot;
        const pnl = Math.round(pts * shares);
        const ptsEl = $("cqo-target-pts");
        if (ptsEl) ptsEl.textContent = `+${pts.toFixed(2)} pts`;
        const pnlEl = $("cqo-target-pnl");
        if (pnlEl) pnlEl.textContent = `(Est. +\u20B9${pnl.toLocaleString("en-IN")})`;
        if (S.quickOrder.targetEnabled) {
          S.charts[S.quickOrder.pane]?.showTargetPreview(val, "TARGET");
        }
      } else {
        const pts = Math.round((val - S.quickOrder.price) * 100) / 100;
        const ptsEl = $("cqo-target-pts");
        if (ptsEl) ptsEl.textContent = `${pts >= 0 ? "+" : ""}${pts.toFixed(2)} pts`;
        const pnlEl = $("cqo-target-pnl");
        if (pnlEl) pnlEl.textContent = "(Est. \u2014)";
      }
    }
  });
  $("cqo-target-price")?.addEventListener("change", (e) => {
    if (!S.quickOrder) return;
    const val = parseFloat(e.target.value);
    if (!isNaN(val) && val > S.quickOrder.price) {
      updateQuickOrderTargetPrice(val);
    } else {
      const fallbackPts = Math.max(0.5, S.quickOrder.targetPts || 0.5);
      updateQuickOrderTargetPts(fallbackPts);
    }
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
    widget.addEventListener("pointerdown", (e) => {
      e.stopPropagation();
      if (e.button !== 0 || isDragging) return;
      const target = e.target;
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
      } catch (_) {
      }
      e.preventDefault();
    });
    widget.addEventListener("pointermove", (e) => {
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
    const stopDrag = (e) => {
      if (!isDragging || e.pointerId !== void 0 && e.pointerId !== activePointerId) return;
      isDragging = false;
      activePointerId = -1;
      widget.classList.remove("is-dragging");
      try {
        if (widget.hasPointerCapture(e.pointerId)) {
          widget.releasePointerCapture(e.pointerId);
        }
      } catch (_) {
      }
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
    const isChartCanvas = event.target?.closest?.(".chart-canvas");
    if (!isChartCanvas) {
      closeChartQuickOrder();
    }
  });
}
function initChartTooltips() {
  const tooltip = $("chart-tooltip");
  if (!tooltip) return;
  const TOOLTIP_DATA = {
    trades: {
      title: "Trades & Positions (TX)",
      desc: "Toggle executed trades, open positions, and pending limit order lines directly on chart candles."
    },
    dom: {
      title: "Depth of Market (DOM)",
      desc: "Display live bid and ask order book volume bars beside prices on the chart canvas."
    },
    trendline: {
      title: "Draw Trendline (TL)",
      desc: "Click chart twice to place a manual trendline. Drag endpoints to adjust; right-click to delete."
    },
    "auto-tl": {
      title: "Auto Trendlines (ATL)",
      desc: "Automatically detects swing highs and swing lows to project dynamic support and resistance lines."
    },
    sr: {
      title: "Support & Resistance (SR)",
      desc: "Computes and plots horizontal key pivot levels and price reaction zones."
    },
    patterns: {
      title: "Pattern Recognition (PTN)",
      desc: "Detects candlestick patterns (Hammer, Engulfing, Doji, Star). Right-click for pattern settings."
    },
    ma: {
      title: "Moving Averages (MA)",
      desc: "Toggle Moving Average overlays (EMA 9, 21, 50, 200) on candles."
    },
    in: {
      title: "Zoom In (+)",
      desc: "Increase candlestick width to zoom in closer to recent price action."
    },
    out: {
      title: "Zoom Out (\u2212)",
      desc: "Decrease candlestick width to view more historical candles and wider price structure."
    },
    reset: {
      title: "Reset View (\u21BA)",
      desc: "Reset time and price zoom back to default and scroll to the latest live candle."
    },
    expand: {
      title: "Maximize / Restore (\u26F6)",
      desc: "Maximize this chart pane to full screen, or click again / press Esc to restore 3-chart layout."
    },
    "tools-toggle": {
      title: "Technical Tools (\u{1F4D0})",
      desc: "Toggle advanced chart indicators, overlays, and drawing tools panel."
    },
    "chart-hide": {
      title: "Hide Chart (\u2715)",
      desc: "Hide this chart pane to allocate more screen space to other charts."
    }
  };
  let activeBtn = null;
  let pendingBtn = null;
  let showTimer = null;
  const showTooltip = (btn) => {
    if (activeBtn && activeBtn !== btn) {
      if (activeBtn.dataset.originalTitle) {
        activeBtn.setAttribute("title", activeBtn.dataset.originalTitle);
        delete activeBtn.dataset.originalTitle;
      }
    }
    const action = btn.dataset.chartAction || (btn.classList.contains("pro-tools-toggle") ? "tools-toggle" : "") || (btn.classList.contains("pane-close-btn") ? "chart-hide" : "");
    const info = TOOLTIP_DATA[action] || {
      title: btn.dataset.tooltipTitle || btn.getAttribute("aria-label") || "Chart Action",
      desc: btn.dataset.tooltipDesc || ""
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
    const rect = btn.getBoundingClientRect();
    const tipWidth = tooltip.offsetWidth || 200;
    const tipHeight = tooltip.offsetHeight || 50;
    let left = rect.left + rect.width / 2 - tipWidth / 2;
    left = Math.max(8, Math.min(left, window.innerWidth - tipWidth - 8));
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
  targets.forEach((btn) => {
    let touchTimer = null;
    btn.addEventListener("pointerenter", (e) => {
      if (e.pointerType === "touch") return;
      pendingBtn = btn;
      clearTimeout(showTimer);
      showTimer = setTimeout(() => {
        if (pendingBtn === btn) showTooltip(btn);
      }, 80);
    });
    btn.addEventListener("pointerleave", (e) => {
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
  document.addEventListener("pointerdown", (e) => {
    if (!tooltip.hidden && !tooltip.contains(e.target) && activeBtn !== e.target) {
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
      if (pane === "spot") {
        toast("Click on the CALL (left) or PUT (right) chart to place a Limit order at that strike premium.", "info", 4e3);
        return;
      }
      const rect = container.getBoundingClientRect();
      const x2 = event.clientX - rect.left;
      const y = event.clientY - rect.top;
      const price = chart.getPriceFromPixel(y, x2);
      if (!price || price <= 0) return;
      openChartQuickOrder(pane, price, x2, y);
    });
  }
}
function openChartQuickOrder(pane, price, x2, y) {
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
  let defaultPts = 2;
  try {
    const saved = localStorage.getItem("scalper-target-pts");
    if (saved) defaultPts = num(saved, 2);
  } catch (_) {
  }
  if (defaultPts <= 0) defaultPts = 2;
  let targetEnabled = true;
  try {
    if (localStorage.getItem("scalper-target-enabled") === "false") targetEnabled = false;
  } catch (_) {
  }
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
    targetEnabled
  };
  const widget = $("chart-quick-order");
  if (!widget) return;
  $("cqo-title").textContent = `${S.activeIndex} ${fmtStrike(option.strike)} ${type}`;
  $("cqo-price").value = price.toFixed(2);
  $("cqo-buy-price").textContent = `@ \u20B9${price.toFixed(2)}`;
  $("cqo-sell-price").textContent = `@ \u20B9${price.toFixed(2)}`;
  renderQuickOrderQty();
  renderQuickOrderTarget();
  const frame = chart.container.closest(".chart-frame") || chart.container;
  frame.appendChild(widget);
  const frameRect = frame.getBoundingClientRect();
  const widgetWidth = 250;
  const widgetHeight = 160;
  let left = x2 - widgetWidth / 2;
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
  if (pnlEl) pnlEl.textContent = `(Est. ${pnl >= 0 ? "+" : "\u2212"}\u20B9${Math.abs(pnl).toLocaleString("en-IN")})`;
}
function updateQuickOrderTargetPts(newPts) {
  if (!S.quickOrder) return;
  const pts = Math.max(0.05, Math.round(num(newPts) * 20) / 20);
  S.quickOrder.targetPts = pts;
  S.quickOrder.targetPrice = Math.round((S.quickOrder.price + pts) * 20) / 20;
  try {
    localStorage.setItem("scalper-target-pts", String(pts));
  } catch (_) {
  }
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
  } catch (_) {
  }
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
  $("cqo-buy-price").textContent = `@ \u20B9${price.toFixed(2)}`;
  $("cqo-sell-price").textContent = `@ \u20B9${price.toFixed(2)}`;
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
  if (widget) {
    widget.classList.remove("is-dragging");
    widget.hidden = true;
  }
}
async function submitQuickOrder(action) {
  if (!S.quickOrder) return;
  const { type, lotsCount, lot, targetEnabled, targetPrice } = S.quickOrder;
  const inputPrice = num($("cqo-price")?.value);
  const price = Number.isFinite(inputPrice) && inputPrice > 0 ? inputPrice : S.quickOrder.price;
  const shares = lotsCount * lot;
  const inputTarget = num($("cqo-target-price")?.value);
  const tp = Number.isFinite(inputTarget) && inputTarget > 0 ? inputTarget : targetPrice;
  const autoTarget = targetEnabled && action === "buy" && tp > price ? tp : 0;
  closeChartQuickOrder();
  await onLimitTrade(action, type, price, shares, autoTarget);
  if (autoTarget > 0) {
    S.tab = "brackets";
    renderTab();
  }
}
async function onExit(instrumentKey) {
  const orderMode = tradingMode();
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before exiting a position.", "warn");
    return;
  }
  const position = S.positions.find((p) => p.instrument_key === instrumentKey);
  if (!position) return;
  const side = position.quantity > 0 ? "S" : "B";
  const qty = Math.abs(position.quantity);
  const ok = await confirmAction({
    title: "Exit position",
    body: `${side === "B" ? "Buy" : "Sell"} <b>${qty}</b> shares of <b>${esc(position.symbol)}</b> at market to close the position?`,
    okLabel: "Exit",
    danger: true
  });
  if (!ok) return;
  if (tradingMode() !== orderMode) {
    toast("Trading mode changed; exit was not sent. Review the selected account and try again.", "warn", 5e3);
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
      mode: orderMode
    });
    lastUiOrderTime = Date.now();
    playTradeSound(side === "B" ? "buy" : "sell");
    toast("Live exit order submitted.", "ok");
    scheduleBooksRefresh(400);
  } catch (err) {
    playTradeSound("reject");
    toast(`Exit failed: ${err.message}`, "error", 6e3);
  }
}
async function onSquareOffAll() {
  const orderMode = tradingMode();
  if (!S.session?.authenticated) {
    openLogin();
    toast("Sign in with Kotak before squaring off positions.", "warn");
    return;
  }
  const openPositions = S.positions.filter((p) => Math.abs(num(p.quantity)) > 0);
  if (!openPositions.length) {
    toast("No open positions to square off.", "warn");
    return;
  }
  const ok = await confirmAction({
    title: "Square off all",
    body: `Close <b>${openPositions.length}</b> open position${openPositions.length === 1 ? "" : "s"} at market? <span class="warn">This cannot be undone.</span>`,
    okLabel: "Square Off All",
    danger: true
  });
  if (!ok) return;
  if (tradingMode() !== orderMode) {
    toast("Trading mode changed; square-off was not sent. Review the selected account and try again.", "warn", 5e3);
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
    toast(`Square off failed: ${err.message}`, "error", 6e3);
  }
}
var confirmResolve = null;
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
var totpAccepted = false;
var loginBusy = false;
var versionCheckDone = false;
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
        headers: { Accept: "application/vnd.github.v3+json" }
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
          published_at: gh.published_at || ""
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
    submit.textContent = "Checking\u2026";
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
  submit.textContent = "Signing in\u2026";
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
var statusRefreshTimer = null;
var statusBusy = false;
function formatRelativeTime(dateOrIso) {
  if (!dateOrIso) return "\u2014";
  const time = typeof dateOrIso === "number" ? dateOrIso : new Date(dateOrIso).getTime();
  if (isNaN(time) || time <= 0) return "\u2014";
  const diffSec = Math.round((Date.now() - time) / 1e3);
  if (diffSec < 2) return "Just now";
  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.round(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHours = Math.round(diffMin / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  return new Date(time).toLocaleDateString();
}
function formatUptime(seconds) {
  if (seconds == null || isNaN(seconds) || seconds < 0) return "\u2014";
  const s = Math.floor(seconds % 60);
  const m = Math.floor(seconds / 60 % 60);
  const h = Math.floor(seconds / 3600 % 24);
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
  if (force && refreshBtn) refreshBtn.textContent = "\u21BB Refreshing\u2026";
  try {
    const res = await fetch("/api/health");
    let health = null;
    try {
      health = await res.json();
    } catch {
    }
    if (!health) {
      throw new Error(`HTTP ${res.status}`);
    }
    renderStatusModalData(health);
  } catch (err) {
    console.warn("failed to fetch /api/health", err);
    renderStatusModalOffline(err);
  } finally {
    statusBusy = false;
    if (refreshBtn) refreshBtn.textContent = "\u21BB Refresh Status";
  }
}
function renderStatusModalData(health) {
  const checks = health?.checks || {};
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
    const now = /* @__PURE__ */ new Date();
    $("status-last-checked").textContent = `Checked: ${now.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit", timeZone: "Asia/Kolkata" })} IST`;
  }
  const wsOpen = S.ws && S.ws.readyState === WebSocket.OPEN;
  const wsConnecting = S.ws && S.ws.readyState === WebSocket.CONNECTING;
  if (wsOpen) {
    setStatusPill($("status-card-ws-pill"), "ok", "LIVE");
    if ($("status-card-ws-state")) $("status-card-ws-state").textContent = "Connected (Open)";
  } else if (wsConnecting) {
    setStatusPill($("status-card-ws-pill"), "warn", "CONNECTING");
    if ($("status-card-ws-state")) $("status-card-ws-state").textContent = "Connecting\u2026";
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
  const marketEndpoint = mCheck.endpoint || (S.feed === "live" ? "wss://tradeapi.kotaksecurities.com/websocket" : "\u2014");
  if ($("status-card-market-endpoint")) {
    $("status-card-market-endpoint").textContent = marketEndpoint;
    $("status-card-market-endpoint").title = marketEndpoint;
  }
  if ($("status-card-market-event")) {
    $("status-card-market-event").textContent = mCheck.last_event ? formatRelativeTime(mCheck.last_event) : isMarketLive ? "Streaming live" : "\u2014";
  }
  const oCheck = checks.order_feed || {};
  const isOrderLive = S.orderFeed === "live" || oCheck.status === "connected";
  const isOrderConnecting = S.orderFeed === "connecting" || oCheck.status === "connecting";
  if (isOrderLive) {
    setStatusPill($("status-card-order-pill"), "ok", "CONNECTED");
    if ($("status-card-order-status")) $("status-card-order-status").textContent = "Connected (Kotak Neo)";
  } else if (isOrderConnecting) {
    setStatusPill($("status-card-order-pill"), "warn", "CONNECTING");
    if ($("status-card-order-status")) $("status-card-order-status").textContent = "Connecting to Order Stream\u2026";
  } else {
    setStatusPill($("status-card-order-pill"), "fail", "DISCONNECTED");
    if ($("status-card-order-status")) $("status-card-order-status").textContent = "Disconnected";
  }
  const orderEndpoint = oCheck.endpoint || (isOrderLive ? "wss://tradeapi.kotaksecurities.com/realtime" : "\u2014");
  if ($("status-card-order-endpoint")) {
    $("status-card-order-endpoint").textContent = orderEndpoint;
    $("status-card-order-endpoint").title = orderEndpoint;
  }
  if ($("status-card-order-event")) {
    $("status-card-order-event").textContent = oCheck.last_event ? formatRelativeTime(oCheck.last_event) : isOrderLive ? "Listening for events" : "\u2014";
  }
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
    $("status-card-broker-ucc").textContent = bCheck.ucc || S.session?.ucc || "\u2014";
  }
  if ($("status-card-broker-token")) {
    $("status-card-broker-token").textContent = tokenPresent ? "Configured" : "Missing";
  }
  if ($("status-card-broker-ratelimit")) {
    const rl = health?.rate_limits;
    if (rl && (rl.remaining > 0 || rl.limit > 0)) {
      $("status-card-broker-ratelimit").textContent = `${rl.remaining} / ${rl.limit || "\u2014"} (reset ${rl.reset || 0}s)`;
    } else {
      $("status-card-broker-ratelimit").textContent = "Normal / OK";
    }
  }
  const ipEl = $("status-card-broker-outbound-ip");
  if (ipEl && (ipEl.textContent === "\u2014" || ipEl.textContent === "")) {
    ipEl.textContent = "Checking\u2026";
    fetch("/api/diagnostics/ip").then((r) => r.json()).then((data) => {
      if (data?.ip) {
        ipEl.textContent = data.ip;
        ipEl.title = `Outbound IP (cached: ${data.cached})`;
      } else {
        ipEl.textContent = "Unavailable";
      }
    }).catch(() => {
      ipEl.textContent = "Unavailable";
    });
  }
  const dbCheck = checks.database || {};
  if (dbCheck.status === "ok") {
    setStatusPill($("status-card-db-pill"), "ok", "CONNECTED");
    if ($("status-card-db-status")) $("status-card-db-status").textContent = "Operational (Read/Write)";
  } else {
    setStatusPill($("status-card-db-pill"), "fail", "ERROR");
    if ($("status-card-db-status")) $("status-card-db-status").textContent = "Unreachable";
  }
  if ($("status-card-db-latency")) {
    $("status-card-db-latency").textContent = dbCheck.latency_ms != null ? `${dbCheck.latency_ms} ms` : "\u2014 ms";
  }
  if ($("status-card-db-detail")) {
    $("status-card-db-detail").textContent = dbCheck.detail || (dbCheck.status === "ok" ? "Healthy" : "Offline");
  }
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
    return !!(data.has_devices || data.count && data.count > 0);
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
      body: JSON.stringify({ device_id: savedDevId })
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
  } catch (_) {
  }
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
    body: JSON.stringify({ device_id: targetId })
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
  if (serveBtn) {
    serveBtn.disabled = true;
    serveBtn.textContent = "Authenticating...";
  }
  try {
    const savedDevId = localStorage.getItem("scalper_device_id") || "";
    let webAuthnError = null;
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
          const safeRpId = opts.rpId && !/^\d+\.\d+\.\d+\.\d+$/.test(opts.rpId) && opts.rpId !== "127.0.0.1" ? opts.rpId : void 0;
          const publicKey = {
            challenge: base64UrlToBuffer(opts.challenge),
            timeout: opts.timeout || 6e4,
            rpId: safeRpId,
            userVerification: opts.userVerification || "preferred",
            allowCredentials: (opts.allowCredentials || []).map((c) => ({
              type: c.type || "public-key",
              id: base64UrlToBuffer(c.id),
              transports: c.transports
            }))
          };
          const assertion = await navigator.credentials.get({ publicKey });
          if (assertion) {
            const finishBody = {
              id: assertion.id,
              rawId: bufferToBase64Url(assertion.rawId),
              type: assertion.type,
              response: {
                clientDataJSON: bufferToBase64Url(assertion.response.clientDataJSON),
                authenticatorData: bufferToBase64Url(assertion.response.authenticatorData),
                signature: bufferToBase64Url(assertion.response.signature),
                userHandle: assertion.response.userHandle ? bufferToBase64Url(assertion.response.userHandle) : null
              }
            };
            const finRes = await fetch("/api/auth/webauthn/login/finish", {
              method: "POST",
              credentials: "include",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify(finishBody)
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
        webAuthnError = waErr.name === "NotAllowedError" ? "Biometric authentication cancelled" : waErr.message || "Biometric failed";
      }
    }
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
      serveBtn.innerHTML = "<span>\u26A1 Biometric Unlock (Touch ID / Fingerprint / Face ID)</span>";
    }
  }
}
async function handleRegisterDevice(deviceName = "", pairToken = "") {
  const registerBtn = $("register-device-button");
  const pairEnrollBtn = $("pair-register-this-btn");
  if (registerBtn) {
    registerBtn.disabled = true;
    registerBtn.textContent = "Enrolling...";
  }
  if (pairEnrollBtn) {
    pairEnrollBtn.disabled = true;
    pairEnrollBtn.textContent = "Enrolling...";
  }
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
          pairToken: pairToken || ""
        })
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
      if (registerBtn) {
        registerBtn.disabled = false;
        registerBtn.textContent = "Register Device";
      }
      if (pairEnrollBtn) {
        pairEnrollBtn.disabled = false;
        pairEnrollBtn.textContent = "\u26A1 Register This Device";
      }
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
        displayName: opts.user.displayName
      },
      pubKeyCredParams: opts.pubKeyCredParams,
      authenticatorSelection: opts.authenticatorSelection || { userVerification: "preferred" },
      timeout: opts.timeout || 6e4,
      attestation: opts.attestation || "none"
    };
    const cred = await navigator.credentials.create({ publicKey });
    if (!cred) throw new Error("Registration was cancelled");
    const finishBody = {
      deviceName: deviceName || (window.navigator?.userAgentData?.platform || "My Device"),
      pairToken: pairToken || "",
      id: cred.id,
      rawId: bufferToBase64Url(cred.rawId),
      type: cred.type,
      response: {
        clientDataJSON: bufferToBase64Url(cred.response.clientDataJSON),
        attestationObject: bufferToBase64Url(cred.response.attestationObject)
      }
    };
    const finRes = await fetch("/api/auth/webauthn/register/finish", {
      method: "POST",
      credentials: "include",
      headers,
      body: JSON.stringify(finishBody)
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
    if (registerBtn) {
      registerBtn.disabled = false;
      registerBtn.textContent = "Register Device";
    }
    if (pairEnrollBtn) {
      pairEnrollBtn.disabled = false;
      pairEnrollBtn.textContent = "\u26A1 Register This Device";
    }
  }
}
async function openPairModal() {
  const modal = $("pair-modal");
  if (!modal) return;
  modal.hidden = false;
  modal.style.display = "grid";
  const qrContainer = $("pair-qr-svg");
  const urlInput = $("pair-url-input");
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
  } catch (e) {
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
async function onAskAI(type) {
  const option = selectedOption(type);
  if (!option) {
    toast(`Select a ${type} strike first.`, "warn");
    return;
  }
  if (!option.quoteAvailable || option.ltp <= 0) {
    toast(`No live quote available for ${type} option.`, "warn");
    return;
  }
  const askBtn = $(type === "CE" ? "btn-ask-ce" : "btn-ask-pe");
  if (askBtn) {
    askBtn.disabled = true;
    askBtn.textContent = "\u2026";
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
    const score = resp.score ?? "\u2014";
    const conf = resp.confidence ? `${Math.round(resp.confidence * 100)}%` : "\u2014";
    const tgtText = resp.target_price ? ` \xB7 Tgt \u20B9${fmt(resp.target_price, 2)}` : "";
    const slText = resp.stop_loss_price ? ` \xB7 SL \u20B9${fmt(resp.stop_loss_price, 2)}` : "";
    if (action.startsWith("BUY")) {
      toast(`\u{1F3AF} ${action} \xB7 Score ${score}/5 (${conf})${tgtText}${slText}`, "success", 4500);
    } else {
      toast(`\u270B ${action} \xB7 Score ${score}/5 (${conf})`, "info", 3500);
    }
    updateAIWinRateChip();
    if (isAISignalsModalOpen()) {
      refreshAISignalsModal();
    }
  } catch (err) {
    toast("AI Ask failed: " + (err?.message || String(err)), "error");
  } finally {
    if (askBtn) {
      askBtn.disabled = false;
      askBtn.textContent = "Ask";
    }
  }
}
function isAISignalsModalOpen() {
  const modal = $("ai-signals-modal");
  return Boolean(modal && !modal.hidden && modal.style.display !== "none");
}
function openAISignalsModal() {
  const modal = $("ai-signals-modal");
  if (!modal) return;
  modal.hidden = false;
  modal.style.display = "grid";
  refreshAISignalsModal();
}
function closeAISignalsModal() {
  const modal = $("ai-signals-modal");
  if (modal) {
    modal.hidden = true;
    modal.style.display = "none";
  }
}
function updateAIScannerStatus(scanner) {
  if (!scanner) return;
  S.aiScanner = scanner;
  renderAIScannerTimer();
  ensureAIScannerTicker();
}
function ensureAIScannerTicker() {
  if (S.aiScannerIntervalStarted) return;
  S.aiScannerIntervalStarted = true;
  window.setInterval(renderAIScannerTimer, 1e3);
}
function renderAIScannerTimer() {
  const headerEl = $("ai-header-timer");
  const modalValEl = $("ai-modal-timer-val");
  const modalBadgeEl = $("ai-modal-scanner-badge");
  if (!headerEl && !modalValEl) return;
  const scanner = S.aiScanner;
  if (!scanner || !scanner.enabled) {
    if (headerEl) {
      headerEl.textContent = "\u23F1 Off";
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
      headerEl.textContent = "\u23F1 Paused";
      headerEl.title = "AI background scanner is paused";
      headerEl.style.opacity = "0.7";
    }
    if (modalValEl) {
      modalValEl.textContent = "Paused";
      modalValEl.style.color = "var(--text-muted, #888)";
    }
    return;
  }
  const nextUnix = Number(scanner.next_scan_unix || 0);
  if (!nextUnix || nextUnix <= 0) {
    if (headerEl) {
      headerEl.textContent = "\u23F1 --:--";
      headerEl.style.opacity = "0.7";
    }
    if (modalValEl) modalValEl.textContent = "--:--";
    return;
  }
  const nowSec = Math.floor(Date.now() / 1e3);
  const remaining = Math.max(0, nextUnix - nowSec);
  let formatted = "";
  if (remaining <= 0) {
    formatted = "Scanning\u2026";
  } else {
    const mins = Math.floor(remaining / 60);
    const secs = remaining % 60;
    formatted = `${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
  }
  if (headerEl) {
    headerEl.textContent = remaining <= 0 ? "\u23F1 Scanning\u2026" : `\u23F1 ${formatted}`;
    headerEl.title = `Next AI background scan in ${remaining}s (interval: ${scanner.interval_minutes || 15}m)`;
    headerEl.style.opacity = "1";
    if (remaining <= 0) {
      headerEl.style.color = "var(--accent, #f59e0b)";
    } else {
      headerEl.style.color = "inherit";
    }
  }
  if (modalValEl) {
    modalValEl.textContent = formatted;
    if (remaining <= 0) {
      modalValEl.style.color = "var(--accent, #f59e0b)";
    } else {
      modalValEl.style.color = "var(--accent, #f59e0b)";
    }
  }
  if (remaining === 0 && !S.aiScanRefreshTriggered) {
    S.aiScanRefreshTriggered = true;
    setTimeout(() => {
      S.aiScanRefreshTriggered = false;
      updateAIWinRateChip();
      if (isAISignalsModalOpen()) {
        refreshAISignalsModal();
      }
    }, 4500);
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
    if (stats.total_calls > 0 && stats.target_hits + stats.stop_loss_hits > 0) {
      const winRate = Number(stats.win_rate_pct).toFixed(1);
      chip.textContent = `${winRate}%`;
      chip.style.color = stats.win_rate_pct >= 50 ? "var(--green, #22c55e)" : "var(--red, #ef4444)";
    } else if (stats.total_calls > 0) {
      chip.textContent = `${stats.pending_calls} active`;
      chip.style.color = "var(--accent, #f59e0b)";
    } else {
      chip.textContent = "--%";
      chip.style.color = "inherit";
    }
  } catch (_) {
  }
}
async function refreshAISignalsModal() {
  try {
    const [statsRes, evalsRes] = await Promise.allSettled([
      getJSON("/api/ai/evaluations/stats"),
      getJSON("/api/ai/evaluations?limit=50")
    ]);
    if (statsRes.status === "fulfilled" && statsRes.value?.stats) {
      const s = statsRes.value.stats;
      if (statsRes.value?.scanner) {
        updateAIScannerStatus(statsRes.value.scanner);
      }
      const winRateEl = $("ai-modal-winrate");
      if (winRateEl) {
        if (s.target_hits + s.stop_loss_hits > 0) {
          winRateEl.textContent = `${Number(s.win_rate_pct).toFixed(1)}%`;
          winRateEl.style.color = s.win_rate_pct >= 50 ? "var(--green, #22c55e)" : "var(--red, #ef4444)";
        } else {
          winRateEl.textContent = "--%";
          winRateEl.style.color = "var(--text-muted, #888)";
        }
      }
      if ($("ai-modal-target-hits")) $("ai-modal-target-hits").textContent = String(s.target_hits || 0);
      if ($("ai-modal-sl-hits")) $("ai-modal-sl-hits").textContent = String(s.stop_loss_hits || 0);
      if ($("ai-modal-active-calls")) $("ai-modal-active-calls").textContent = String(s.pending_calls || 0);
      if ($("ai-modal-total-calls")) $("ai-modal-total-calls").textContent = String(s.total_calls || 0);
      const chip = $("ai-winrate-chip");
      if (chip) {
        if (s.target_hits + s.stop_loss_hits > 0) {
          chip.textContent = `${Number(s.win_rate_pct).toFixed(1)}%`;
          chip.style.color = s.win_rate_pct >= 50 ? "var(--green, #22c55e)" : "var(--red, #ef4444)";
        } else if (s.total_calls > 0) {
          chip.textContent = `${s.pending_calls} active`;
        }
      }
    }
    if (evalsRes.status === "fulfilled") {
      const evals = evalsRes.value?.evaluations || [];
      const tbody = $("ai-calls-tbody");
      if (!tbody) return;
      if (!evals.length) {
        tbody.innerHTML = `<tr><td colspan="9" style="text-align:center; padding:24px; color:var(--text-muted,#888);">No AI evaluation calls recorded yet. Click "Scan Now" or use "Ask" on any strike.</td></tr>`;
        return;
      }
      tbody.innerHTML = evals.map((e) => {
        const timeStr = e.timestamp ? new Date(e.timestamp).toLocaleTimeString() : "\u2014";
        const strikeLabel = `${e.underlying || ""} ${e.instrument_key ? e.instrument_key.split("|")[4] || e.instrument_key : ""}`;
        let statusBadge = `<span class="ai-status-pill ai-status-pending">PENDING</span>`;
        if (e.status === "TARGET_HIT") {
          statusBadge = `<span class="ai-status-pill ai-status-target">TARGET HIT</span>`;
        } else if (e.status === "SL_HIT") {
          statusBadge = `<span class="ai-status-pill ai-status-sl">SL HIT</span>`;
        }
        const pnlStr = e.pnl_pct ? `<span style="font-weight:700; color:${e.pnl_pct >= 0 ? "var(--green, #22c55e)" : "var(--red, #ef4444)"}">${e.pnl_pct > 0 ? "+" : ""}${fmt(e.pnl_pct, 2)}%</span>` : "\u2014";
        return `<tr style="border-bottom:1px solid var(--border,#333);">
          <td style="padding:8px 10px; font-family:var(--mono);">${timeStr}</td>
          <td style="padding:8px 10px; font-weight:600;">${strikeLabel}</td>
          <td style="padding:8px 10px; font-weight:700; color:${e.action?.includes("PE") ? "var(--red,#ef4444)" : "var(--green,#22c55e)"};">${e.action || "\u2014"}</td>
          <td style="padding:8px 10px; font-family:var(--mono);">\u20B9${fmt(e.option_ltp, 2)}</td>
          <td style="padding:8px 10px; font-family:var(--mono); color:var(--green,#22c55e);">\u20B9${fmt(e.target_price, 2)}</td>
          <td style="padding:8px 10px; font-family:var(--mono); color:var(--red,#ef4444);">\u20B9${fmt(e.stop_loss_price, 2)}</td>
          <td style="padding:8px 10px; font-family:var(--mono);">${e.exit_price ? "\u20B9" + fmt(e.exit_price, 2) : "\u2014"}</td>
          <td style="padding:8px 10px; font-family:var(--mono);">${pnlStr}</td>
          <td style="padding:8px 10px; text-align:center;">${statusBadge}</td>
        </tr>`;
      }).join("");
    }
  } catch (err) {
    toast("Failed to refresh AI signals: " + (err?.message || String(err)), "error");
  }
}
function handleAICallEvent(eventType, data) {
  updateAIWinRateChip();
  if (isAISignalsModalOpen()) {
    refreshAISignalsModal();
  }
  if (eventType === "ai_call_outcome" && data) {
    if (data.status === "TARGET_HIT") {
      toast(`\u{1F3AF} AI Target Hit: ${data.instrument_key || data.underlying} +${fmt(data.pnl_pct, 1)}%!`, "success", 6e3);
    } else if (data.status === "SL_HIT") {
      toast(`\u{1F6D1} AI Stop-Loss Hit: ${data.instrument_key || data.underlying} ${fmt(data.pnl_pct, 1)}%`, "warn", 6e3);
    }
  } else if (eventType === "ai_call_new" && data) {
    toast(`\u{1F916} New Jev Signal: ${data.action} ${data.underlying} @ \u20B9${fmt(data.option_ltp, 2)} (Target: \u20B9${fmt(data.target_price, 2)})`, "info", 5e3);
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
    tbody.innerHTML = devices.map((d) => {
      const lastUsed = d.last_used_at ? new Date(d.last_used_at).toLocaleDateString("en-IN", {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit"
      }) : "\u2014";
      return `
        <tr>
          <td style="padding:6px 4px;">
            <div class="device-name-cell">
              <span>\u{1F4F1}</span>
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
    tbody.querySelectorAll(".btn-revoke-device").forEach((btn) => {
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
  } catch (err) {
    toast(err?.message || "Error revoking device", "warn");
  }
}
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
      const chartKey = button.dataset.chart;
      const chart = S.charts[chartKey] || (chartKey === "underlying" ? S.charts.spot : null);
      if (!chart) return;
      if (action === "trades") {
        toggleAllChartTrades();
        return;
      }
      if (action === "ma") {
        const nextState = chart.toggleMA();
        button.classList.toggle("active", nextState);
        button.setAttribute("aria-pressed", nextState ? "true" : "false");
        try {
          if (chartKey === "spot" || chartKey === "underlying") {
            localStorage.setItem("scalper-ma-underlying", String(nextState));
            localStorage.setItem("scalper-ma-spot", String(nextState));
          } else {
            localStorage.setItem(`scalper-ma-${chartKey}`, String(nextState));
          }
        } catch (_) {
        }
        const label = chartKey === "ce" ? "CALL" : chartKey === "pe" ? "PUT" : "UNDERLYING";
        toast(nextState ? `Moving Average enabled (${label})` : `Moving Average hidden (${label})`, "ok", 1500);
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
        } catch (_) {
        }
        const label = chartKey === "ce" ? "CALL" : chartKey === "pe" ? "PUT" : "UNDERLYING";
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
        }
        toast(nextState ? "Patterns enabled (Micro, Geometric, Harmonics & Setups)" : "Patterns hidden", "ok", 1500);
        return;
      }
      if (action === "trendline") {
        for (const [otherPane, otherChart] of Object.entries(S.charts)) {
          if (otherChart && otherChart !== chart) {
            otherChart.cancelTrendline?.();
          }
        }
        chart.toggleTrendline(button, {
          onStart: () => toast("Click chart to set trendline start point (Esc to cancel)", "ok", 4e3),
          onComplete: () => toast("Trendline placed (drag ends to adjust, right-click to delete)", "ok", 3500),
          onCancel: () => toast("Trendline cancelled", "warn", 2e3)
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
        toast(`Selected ${S.activeIndex} ${fmtStrike(strike)} ${targetType}`, "ok", 2e3);
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
  function closeMobileDrawer2() {
    const backdrop = $("mobile-drawer-backdrop");
    if (backdrop) backdrop.hidden = true;
  }
  $("mobile-menu-btn")?.addEventListener("click", openMobileDrawer);
  $("mobile-drawer-close")?.addEventListener("click", closeMobileDrawer2);
  $("mobile-drawer-backdrop")?.addEventListener("click", (e) => {
    if (e.target.id === "mobile-drawer-backdrop") closeMobileDrawer2();
  });
  $("drawer-status-btn")?.addEventListener("click", () => {
    closeMobileDrawer2();
    openStatusModal();
  });
  $("drawer-pair-btn")?.addEventListener("click", () => {
    closeMobileDrawer2();
    openPairModal();
  });
  $("drawer-register-btn")?.addEventListener("click", () => {
    closeMobileDrawer2();
    handleRegisterDevice();
  });
  $("drawer-theme-btn")?.addEventListener("click", () => {
    setTheme(document.body.dataset.theme === "dark" ? "light" : "dark");
    const themeLabel = $("drawer-theme-label");
    if (themeLabel) themeLabel.textContent = `Switch to ${document.body.dataset.theme === "light" ? "Dark" : "Light"} Theme`;
  });
  $("drawer-unpair-btn")?.addEventListener("click", () => {
    closeMobileDrawer2();
    try {
      localStorage.removeItem("scalper_server_url");
    } catch (_) {
    }
    window.location.href = window.Capacitor ? "capacitor://localhost/index.html" : "/static/index.html";
  });
  $("drawer-auth-btn")?.addEventListener("click", () => {
    closeMobileDrawer2();
    if (S.session?.authenticated) logout();
    else openLogin();
  });
  $("drawer-install-btn")?.addEventListener("click", () => {
    closeMobileDrawer2();
    if (deferredInstallPrompt) {
      deferredInstallPrompt.prompt();
      deferredInstallPrompt.userChoice.then(() => {
        deferredInstallPrompt = null;
      });
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
      fetchBrackets().catch(() => {
      });
    }
    renderTab();
  });
  $("tab-content").addEventListener("click", (event) => {
    const exit = event.target.closest("[data-exit]");
    if (exit) {
      onExit(exit.dataset.exit);
      return;
    }
    const exitBracket = event.target.closest("[data-exit-bracket]");
    if (exitBracket) {
      onExitBracket(exitBracket.dataset.exitBracket);
      return;
    }
    const cancelBracket = event.target.closest("[data-cancel-bracket]");
    if (cancelBracket) {
      onCancelBracket(cancelBracket.dataset.cancelBracket);
      return;
    }
    const refreshBrackets = event.target.closest("#btn-refresh-brackets");
    if (refreshBrackets) {
      toast("Refreshing brackets\u2026", "info", 1500);
      fetchBrackets().then(() => renderTab()).catch((err) => toast(`Refresh failed: ${err.message}`, "error"));
      return;
    }
    const clearBrackets = event.target.closest("#btn-clear-brackets");
    if (clearBrackets) {
      onClearFinishedBrackets();
      return;
    }
    const cancelBtn = event.target.closest(".btn-cancel-order");
    if (cancelBtn) {
      onCancelOrder(cancelBtn.dataset.orderId);
      return;
    }
    const cancelAllBtn = event.target.closest("#btn-cancel-all-open");
    if (cancelAllBtn) {
      onCancelAllOpenOrders();
      return;
    }
    const historyBtn = event.target.closest("[data-order-history]");
    if (historyBtn) {
      showOrderHistory(historyBtn.dataset.orderHistory);
      return;
    }
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
  $("ai-scan-now-btn")?.addEventListener("click", async () => {
    const btn = $("ai-scan-now-btn");
    if (btn) {
      btn.disabled = true;
      btn.textContent = "Scanning\u2026";
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
    } catch (err) {
      toast("Scan error: " + (err?.message || String(err)), "error");
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.textContent = "\u26A1 Scan Now (Jev)";
      }
    }
  });
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
  } catch {
  }
  const applyPatternConfigToAll = (config) => {
    for (const c of Object.values(S.charts)) {
      if (c && typeof c.setPatternConfig === "function") {
        c.setPatternConfig(config);
      }
    }
    try {
      localStorage.setItem("scalper-pattern-config", JSON.stringify(config));
    } catch {
    }
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
      liquidity: Boolean(ptnLiqCheck?.checked)
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
      if (!patternPopover.contains(e.target) && !e.target?.closest?.('[data-chart-action="patterns"]')) {
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
  document.addEventListener("keydown", (event) => {
    const target = event.target;
    const typing = target instanceof HTMLElement && (target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName));
    if (!typing && (event.key === "/" || event.key.toLowerCase() === "k" && (event.metaKey || event.ctrlKey))) {
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
      const norm = pane === "spot" ? "underlying" : pane;
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
  window.addEventListener("resize", () => {
    for (const chart of Object.values(S.charts)) {
      if (chart && typeof chart.resize === "function") chart.resize();
    }
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
  try {
    localStorage.setItem("scalper-last-index", key);
  } catch (_) {
  }
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
      toast(`No active expiries are available for ${key}.`, "warn", 6e3);
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
  try {
    localStorage.setItem(`scalper-last-expiry-${S.activeIndex}`, date);
  } catch (_) {
  }
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
  if (clock) {
    const text = (/* @__PURE__ */ new Date()).toLocaleTimeString("en-GB", {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
      timeZone: "Asia/Kolkata"
    });
    clock.textContent = `${text} IST`;
  }
  updateSessionCountdown();
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
    hourCycle: "h23"
  }).formatToParts(/* @__PURE__ */ new Date());
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  const date = `${values.year}-${values.month}-${values.day}`;
  if (values.weekday === "Sat" || values.weekday === "Sun" || (S.boot.marketSession?.holidays || []).includes(date)) return false;
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
      return 1 + (7 - weekday) % 7;
    };
    const secondSundayInMarch = firstSunday(3) + 7;
    const firstSundayInNovember = firstSunday(11);
    const dstStart = `${year}-03-${String(secondSundayInMarch).padStart(2, "0")}`;
    const dstEnd = `${year}-11-${String(firstSundayInNovember).padStart(2, "0")}`;
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
    S.historicalCache.clear();
    loadCharts();
  }
}
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
    for (const chart of Object.values(S.charts)) {
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
var deferredInstallPrompt = null;
function registerServiceWorker() {
  if (!("serviceWorker" in navigator)) return;
  const register = () => {
    navigator.serviceWorker.register("/sw.js", { scope: "/" }).then((reg) => {
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
    }).catch((err) => {
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
  const isStandalone = window.matchMedia("(display-mode: standalone)").matches || window.navigator.standalone === true;
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
      } catch (_) {
      }
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
  } catch (_) {
  }
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
    for (const chart of Object.values(S.charts)) {
      chart.schedule();
    }
  });
});
function handleAppBackButton() {
  const drawerBackdrop = $("mobile-drawer-backdrop");
  if (drawerBackdrop && !drawerBackdrop.hidden) {
    closeMobileDrawer();
    return true;
  }
  const patternPopover = $("pattern-popover");
  if (patternPopover && !patternPopover.hidden && patternPopover.style.display !== "none") {
    patternPopover.hidden = true;
    patternPopover.style.display = "none";
    return true;
  }
  const modalClosers = [
    { id: "strike-modal", fn: closeStrikeModal },
    { id: "confirm-modal", fn: () => closeConfirm(false) },
    { id: "pair-modal", fn: closePairModal },
    { id: "status-modal", fn: closeStatusModal },
    { id: "login-modal", fn: closeLogin }
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
  const orderDialog = $("order-dialog");
  if (orderDialog && !orderDialog.hidden && orderDialog.style.display !== "none") {
    orderDialog.hidden = true;
    orderDialog.style.display = "none";
    return true;
  }
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
  } catch (_) {
  }
}
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
//# sourceMappingURL=app.js.map
