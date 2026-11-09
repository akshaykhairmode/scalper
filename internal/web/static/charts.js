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
          const x = isFromZero ? 0 : bounding.width;
          const align = isFromZero ? "left" : "right";
          const lastBar = dataList[lastIdx];
          const isUp = lastBar && lastBar.close >= lastBar.open;
          let bgColor = fig.key === "volume" ? isUp ? styles.bars?.[0]?.upColor || "#35c46a" : styles.bars?.[0]?.downColor || "#ff5d5d" : styles.lines?.[figIdx]?.color || ind.styles?.lines?.[figIdx]?.color || "#2196f3";
          if (typeof bgColor === "string" && bgColor.startsWith("#") && bgColor.length === 9) {
            bgColor = bgColor.slice(0, 7);
          }
          const figure = this.createFigure({
            name: "text",
            attrs: { x, y, text, align, baseline: "middle" },
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
export {
  CandleChart,
  calculateAutoTrendlines,
  calculateOrderFlowBuckets,
  computeDOMInterval,
  formatDOMQty,
  pairTrades,
  parseTimestamp
};
//# sourceMappingURL=charts.js.map
