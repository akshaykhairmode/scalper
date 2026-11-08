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
export {
  calculateATR,
  calculateVolumeMA,
  detectAllPatterns,
  detectCandlestickPatterns,
  detectGeometricPatterns,
  detectHarmonicPatterns,
  detectLiquiditySetups,
  extractPivots
};
//# sourceMappingURL=patterns.js.map
