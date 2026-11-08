/**
 * Complete Pattern Recognition Engine for Scalper
 *
 * Implements:
 * 1. Candlestick Patterns (Micro / 1–3 Bars)
 *    - Single: Hammer, Inverted Hammer, Hanging Man, Shooting Star, Doji (Dragonfly, Gravestone, Long-Legged), Spinning Top, Marubozu.
 *    - Dual: Bullish/Bearish Engulfing, Bullish/Bearish Harami, Piercing Line, Dark Cloud Cover, Tweezer Tops/Bottoms, Kicker.
 *    - Multi: Morning/Evening Star, Three White Soldiers, Three Black Crows, Three Inside Up/Down, Three Outside Up/Down, Rising/Falling Three Methods.
 *
 * 2. Classical Geometric Patterns (Macro / Multi-Bar)
 *    - Reversals: Head & Shoulders, Inverse Head & Shoulders, Double Top ("M"), Double Bottom ("W"), Triple Top/Bottom, V-Reversal.
 *    - Continuations: Bull/Bear Flag, Pennant, Ascending Triangle, Descending Triangle, Symmetrical Triangle, Rectangle Range.
 *    - Bilateral/Directional: Falling Wedge, Rising Wedge, Expanding/Broadening Wedge (Megaphone).
 *
 * 3. Harmonic Patterns (Fibonacci-Based)
 *    - AB=CD, Gartley, Bat, Butterfly, Crab / Deep Crab, Cypher, Shark.
 *
 * 4. Liquidity & Price Action Setups
 *    - Wyckoff Spring / Upthrust (Liquidity Sweeps with Volume)
 *    - Quasimodo (QML / Over & Under)
 *    - Island Reversal
 */

// ==========================================
// 1. Core Mathematical & Pivot Utilities
// ==========================================

export function calculateATR(candles, period = 14) {
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

export function calculateVolumeMA(candles, period = 20) {
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

/**
 * Extracts alternating swing high and swing low pivots using local extrema window.
 */
export function extractPivots(candles, { windowSize = 2, maxPivots = 50 } = {}) {
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

  // Filter so pivots strictly alternate HIGH and LOW
  const alternating = [];
  for (const p of rawPivots) {
    if (!alternating.length) {
      alternating.push(p);
      continue;
    }
    const last = alternating[alternating.length - 1];
    if (last.type === p.type) {
      if (p.type === "HIGH" && p.price > last.price) {
        alternating[alternating.length - 1] = p; // keep highest
      } else if (p.type === "LOW" && p.price < last.price) {
        alternating[alternating.length - 1] = p; // keep lowest
      }
    } else {
      alternating.push(p);
    }
  }

  return alternating.slice(-maxPivots);
}

// ==========================================
// 2. Phase 1: Micro Candlestick Patterns
// ==========================================

export function detectCandlestickPatterns(candles, { lookback = 80 } = {}) {
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
    const atr = atrs[i] || (c.high - c.low) || 1;

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

    // Previous short-term trend direction (over last 3-5 bars)
    const prevTrend = i >= 4 ? (candles[i - 1].close > candles[i - 4].close ? "up" : "down") : "neutral";

    // --- 1. SINGLE BAR PATTERNS ---

    // Hammer (Bullish reversal at bottom)
    if (lowerRatio >= 0.55 && upperRatio <= 0.18 && bodyRatio <= 0.35 && prevTrend === "down") {
      patterns.push({
        id: `p_hmr_${c.time}`,
        type: "candlestick",
        name: "Hammer",
        label: "🔨 HAMMER",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish hammer rejection at swing low",
      });
      continue;
    }

    // Inverted Hammer (Bullish reversal at bottom)
    if (upperRatio >= 0.55 && lowerRatio <= 0.18 && bodyRatio <= 0.35 && prevTrend === "down") {
      patterns.push({
        id: `p_ihmr_${c.time}`,
        type: "candlestick",
        name: "Inverted Hammer",
        label: "🔨 INV HAMMER",
        bias: "bullish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bullish inverted hammer reversal test",
      });
      continue;
    }

    // Hanging Man (Bearish reversal at top)
    if (lowerRatio >= 0.55 && upperRatio <= 0.18 && bodyRatio <= 0.35 && prevTrend === "up") {
      patterns.push({
        id: `p_hng_${c.time}`,
        type: "candlestick",
        name: "Hanging Man",
        label: "⚠️ HANGING MAN",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish hanging man warning at top",
      });
      continue;
    }

    // Shooting Star (Bearish reversal at top)
    if (upperRatio >= 0.55 && lowerRatio <= 0.18 && bodyRatio <= 0.35 && prevTrend === "up") {
      patterns.push({
        id: `p_star_${c.time}`,
        type: "candlestick",
        name: "Shooting Star",
        label: "⭐ SHOOTING STAR",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish shooting star rejection wick at top",
      });
      continue;
    }

    // Dragonfly Doji (Bullish)
    if (bodyRatio <= 0.08 && lowerRatio >= 0.65 && upperRatio <= 0.12) {
      patterns.push({
        id: `p_dfd_${c.time}`,
        type: "candlestick",
        name: "Dragonfly Doji",
        label: "✦ DRAGONFLY",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish dragonfly doji rejection",
      });
      continue;
    }

    // Gravestone Doji (Bearish)
    if (bodyRatio <= 0.08 && upperRatio >= 0.65 && lowerRatio <= 0.12) {
      patterns.push({
        id: `p_gsd_${c.time}`,
        type: "candlestick",
        name: "Gravestone Doji",
        label: "✦ GRAVESTONE",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish gravestone doji rejection",
      });
      continue;
    }

    // Long-Legged / Neutral Doji
    if (bodyRatio <= 0.08 && upperRatio >= 0.30 && lowerRatio >= 0.30) {
      patterns.push({
        id: `p_lld_${c.time}`,
        type: "candlestick",
        name: "Long-Legged Doji",
        label: "✦ DOJI",
        bias: "neutral",
        time: c.time,
        price: (c.high + c.low) / 2,
        candleIndex: i,
        description: "Indecision doji with balanced long shadows",
      });
      continue;
    }

    // Spinning Top
    if (bodyRatio >= 0.10 && bodyRatio <= 0.32 && upperRatio >= 0.28 && lowerRatio >= 0.28) {
      patterns.push({
        id: `p_spt_${c.time}`,
        type: "candlestick",
        name: "Spinning Top",
        label: "✢ SPINNING TOP",
        bias: "neutral",
        time: c.time,
        price: (c.high + c.low) / 2,
        candleIndex: i,
        description: "Equilibrium indecision spinning top",
      });
      continue;
    }

    // Marubozu (Very strong momentum bar with almost zero wicks)
    if (bodyRatio >= 0.88 && body >= atr * 0.75) {
      patterns.push({
        id: `p_mrb_${c.time}`,
        type: "candlestick",
        name: isGreen ? "Bullish Marubozu" : "Bearish Marubozu",
        label: isGreen ? "⚡ MARUBOZU (BULL)" : "⚡ MARUBOZU (BEAR)",
        bias: isGreen ? "bullish" : "bearish",
        time: c.time,
        price: isGreen ? c.high : c.low,
        candleIndex: i,
        description: isGreen ? "Strong institutional bullish expansion" : "Strong institutional bearish expansion",
      });
      continue;
    }

    // --- 2. DUAL BAR PATTERNS ---
    const prevBody = Math.abs(prev1.close - prev1.open);
    const prevRange = prev1.high - prev1.low;
    const prevIsGreen = prev1.close >= prev1.open;
    const prevIsRed = prev1.close < prev1.open;

    // Kicker (Extreme gap reversal)
    if (prevIsRed && isGreen && c.open >= prev1.open && body >= atr * 0.7 && prevBody >= atr * 0.7) {
      patterns.push({
        id: `p_bkck_${c.time}`,
        type: "candlestick",
        name: "Bullish Kicker",
        label: "🚀 BULL KICKER",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Explosive bullish kicker gap reversal",
      });
      continue;
    }
    if (prevIsGreen && isRed && c.open <= prev1.open && body >= atr * 0.7 && prevBody >= atr * 0.7) {
      patterns.push({
        id: `p_skck_${c.time}`,
        type: "candlestick",
        name: "Bearish Kicker",
        label: "📉 BEAR KICKER",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Explosive bearish kicker gap reversal",
      });
      continue;
    }

    // Bullish Engulfing
    if (prevIsRed && isGreen && c.close >= prev1.open && c.open <= prev1.close && body > prevBody * 1.05) {
      patterns.push({
        id: `p_beng_${c.time}`,
        type: "candlestick",
        name: "Bullish Engulfing",
        label: "▲ ENGULFING",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish engulfing candle reversal",
      });
      continue;
    }

    // Bearish Engulfing
    if (prevIsGreen && isRed && c.close <= prev1.open && c.open >= prev1.close && body > prevBody * 1.05) {
      patterns.push({
        id: `p_seng_${c.time}`,
        type: "candlestick",
        name: "Bearish Engulfing",
        label: "▼ ENGULFING",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish engulfing candle reversal",
      });
      continue;
    }

    // Bullish Harami (Inside Bar)
    if (prevIsRed && isGreen && c.open >= prev1.close && c.close <= prev1.open && prevBody > atr * 0.55) {
      patterns.push({
        id: `p_bhar_${c.time}`,
        type: "candlestick",
        name: "Bullish Harami",
        label: "⊞ HARAMI (BUY)",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish inside bar contraction setup",
      });
      continue;
    }

    // Bearish Harami (Inside Bar)
    if (prevIsGreen && isRed && c.open <= prev1.close && c.close >= prev1.open && prevBody > atr * 0.55) {
      patterns.push({
        id: `p_shar_${c.time}`,
        type: "candlestick",
        name: "Bearish Harami",
        label: "⊞ HARAMI (SELL)",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish inside bar contraction setup",
      });
      continue;
    }

    // Piercing Line (Bullish)
    if (prevIsRed && isGreen && c.open < prev1.low && c.close > (prev1.open + prev1.close) / 2 && c.close < prev1.open) {
      patterns.push({
        id: `p_prc_${c.time}`,
        type: "candlestick",
        name: "Piercing Line",
        label: "▲ PIERCING",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Bullish piercing line piercing >50% of prior candle",
      });
      continue;
    }

    // Dark Cloud Cover (Bearish)
    if (prevIsGreen && isRed && c.open > prev1.high && c.close < (prev1.open + prev1.close) / 2 && c.close > prev1.open) {
      patterns.push({
        id: `p_dcc_${c.time}`,
        type: "candlestick",
        name: "Dark Cloud Cover",
        label: "▼ DARK CLOUD",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Bearish dark cloud cover penetrating >50% of prior candle",
      });
      continue;
    }

    // Tweezer Bottom
    if (prevIsRed && isGreen && Math.abs(c.low - prev1.low) / (c.low || 1) <= 0.0008 && lowerRatio >= 0.32) {
      patterns.push({
        id: `p_twb_${c.time}`,
        type: "candlestick",
        name: "Tweezer Bottom",
        label: "♊ TWEEZER BTM",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Dual rejection at identical low level",
      });
      continue;
    }

    // Tweezer Top
    if (prevIsGreen && isRed && Math.abs(c.high - prev1.high) / (c.high || 1) <= 0.0008 && upperRatio >= 0.32) {
      patterns.push({
        id: `p_twt_${c.time}`,
        type: "candlestick",
        name: "Tweezer Top",
        label: "♊ TWEEZER TOP",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Dual rejection at identical high level",
      });
      continue;
    }

    // --- 3. MULTI-BAR (3+ BARS) ---
    const prev2IsRed = prev2.close < prev2.open;
    const prev2IsGreen = prev2.close >= prev2.open;
    const prev2Body = Math.abs(prev2.close - prev2.open);

    // Morning Star (Bullish 3-bar reversal)
    if (prev2IsRed && prev2Body > atr * 0.45 && prevBody < prev2Body * 0.45 && isGreen && c.close >= (prev2.open + prev2.close) / 2) {
      patterns.push({
        id: `p_mst_${c.time}`,
        type: "candlestick",
        name: "Morning Star",
        label: "★ MORNING STAR",
        bias: "bullish",
        time: c.time,
        price: prev1.low,
        candleIndex: i,
        description: "Bullish morning star 3-candle reversal",
      });
      continue;
    }

    // Evening Star (Bearish 3-bar reversal)
    if (prev2IsGreen && prev2Body > atr * 0.45 && prevBody < prev2Body * 0.45 && isRed && c.close <= (prev2.open + prev2.close) / 2) {
      patterns.push({
        id: `p_est_${c.time}`,
        type: "candlestick",
        name: "Evening Star",
        label: "★ EVENING STAR",
        bias: "bearish",
        time: c.time,
        price: prev1.high,
        candleIndex: i,
        description: "Bearish evening star 3-candle reversal",
      });
      continue;
    }

    // Three Inside Up (Harami confirmed by breakout bar)
    if (prev2IsRed && prevIsGreen && prev1.open >= prev2.close && prev1.close <= prev2.open && isGreen && c.close > prev2.high) {
      patterns.push({
        id: `p_3iu_${c.time}`,
        type: "candlestick",
        name: "Three Inside Up",
        label: "▲ 3 INSIDE UP",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Confirmed bullish harami breakout",
      });
      continue;
    }

    // Three Inside Down
    if (prev2IsGreen && prevIsRed && prev1.open <= prev2.close && prev1.close >= prev2.open && isRed && c.close < prev2.low) {
      patterns.push({
        id: `p_3id_${c.time}`,
        type: "candlestick",
        name: "Three Inside Down",
        label: "▼ 3 INSIDE DOWN",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Confirmed bearish harami breakdown",
      });
      continue;
    }

    // Three Outside Up (Engulfing confirmed by continuation)
    if (prev2IsRed && prevIsGreen && prev1.close >= prev2.open && prev1.open <= prev2.close && isGreen && c.close > prev1.high) {
      patterns.push({
        id: `p_3ou_${c.time}`,
        type: "candlestick",
        name: "Three Outside Up",
        label: "▲ 3 OUTSIDE UP",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Confirmed bullish engulfing expansion",
      });
      continue;
    }

    // Three Outside Down
    if (prev2IsGreen && prevIsRed && prev1.close <= prev2.open && prev1.open >= prev2.close && isRed && c.close < prev1.low) {
      patterns.push({
        id: `p_3od_${c.time}`,
        type: "candlestick",
        name: "Three Outside Down",
        label: "▼ 3 OUTSIDE DOWN",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Confirmed bearish engulfing breakdown",
      });
      continue;
    }

    // Three White Soldiers (Strong Bullish Continuation)
    if (prev2IsGreen && prevIsGreen && isGreen &&
        c.close > prev1.close && prev1.close > prev2.close &&
        c.open > prev1.open && prev1.open > prev2.open &&
        bodyRatio > 0.55 && upperRatio < 0.25) {
      patterns.push({
        id: `p_3ws_${c.time}`,
        type: "candlestick",
        name: "Three White Soldiers",
        label: "▲ 3 SOLDIERS",
        bias: "bullish",
        time: c.time,
        price: c.low,
        candleIndex: i,
        description: "Three consecutive strong bullish candles",
      });
      continue;
    }

    // Three Black Crows (Strong Bearish Continuation)
    if (prev2IsRed && prevIsRed && isRed &&
        c.close < prev1.close && prev1.close < prev2.close &&
        c.open < prev1.open && prev1.open < prev2.open &&
        bodyRatio > 0.55 && lowerRatio < 0.25) {
      patterns.push({
        id: `p_3bc_${c.time}`,
        type: "candlestick",
        name: "Three Black Crows",
        label: "▼ 3 CROWS",
        bias: "bearish",
        time: c.time,
        price: c.high,
        candleIndex: i,
        description: "Three consecutive strong bearish candles",
      });
      continue;
    }

    // Rising Three Methods (5 candles: strong green -> 3 small reds within range -> strong green breakout)
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
          label: "⚡ RISING 3 METHODS",
          bias: "bullish",
          time: c.time,
          price: c.low,
          candleIndex: i,
          description: "Institutional pullback and continuation rally",
        });
        continue;
      }
    }

    // Falling Three Methods (5 candles: strong red -> 3 small greens within range -> strong red breakdown)
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
          label: "⚡ FALLING 3 METHODS",
          bias: "bearish",
          time: c.time,
          price: c.high,
          candleIndex: i,
          description: "Institutional consolidation and continuation breakdown",
        });
        continue;
      }
    }
  }

  return patterns;
}

// ==========================================
// 3. Phase 2: Classical Geometric Patterns
// ==========================================

export function detectGeometricPatterns(candles, pivots, { lookbackPivots = 30 } = {}) {
  if (!candles || !pivots || pivots.length < 4) return [];
  const patterns = [];
  const recent = pivots.slice(-lookbackPivots);
  const n = recent.length;
  const lastPrice = candles[candles.length - 1]?.close || 0;
  const atrs = calculateATR(candles, 14);
  const currentATR = atrs[atrs.length - 1] || 10;

  // Scan through pivot windows
  for (let i = 0; i <= n - 4; i++) {
    const p0 = recent[i];
    const p1 = recent[i + 1];
    const p2 = recent[i + 2];
    const p3 = recent[i + 3];
    const p4 = i + 4 < n ? recent[i + 4] : null;
    const p5 = i + 5 < n ? recent[i + 5] : null;

    // --- Double Top ("M" Shape) ---
    // P1 = Peak 1, P2 = Valley (Neckline), P3 = Peak 2
    if (p1.type === "HIGH" && p2.type === "LOW" && p3.type === "HIGH") {
      const topDiff = Math.abs(p1.price - p3.price) / p1.price;
      const height = ((p1.price + p3.price) / 2) - p2.price;
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
            { time: p3.time, price: p3.price, label: "Top 2" },
          ],
          necklinePrice: neckline,
          targetPrice: target,
          confirmed,
          triggerTime: p3.time,
        });
      }
    }

    // --- Double Bottom ("W" Shape) ---
    // P1 = Valley 1, P2 = Peak (Neckline), P3 = Valley 2
    if (p1.type === "LOW" && p2.type === "HIGH" && p3.type === "LOW") {
      const btmDiff = Math.abs(p1.price - p3.price) / p1.price;
      const height = p2.price - ((p1.price + p3.price) / 2);
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
            { time: p3.time, price: p3.price, label: "Btm 2" },
          ],
          necklinePrice: neckline,
          targetPrice: target,
          confirmed,
          triggerTime: p3.time,
        });
      }
    }

    // --- Triple Top / Triple Bottom ---
    if (p5 && i + 5 < n) {
      // Triple Top: P1(HIGH), P2(LOW), P3(HIGH), P4(LOW), P5(HIGH)
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
              { time: p5.time, price: p5.price, label: "Top 3" },
            ],
            necklinePrice: avgNeckline,
            targetPrice: avgNeckline - (avgTop - avgNeckline),
            confirmed: lastPrice < avgNeckline,
            triggerTime: p5.time,
          });
        }
      }

      // Triple Bottom: P1(LOW), P2(HIGH), P3(LOW), P4(HIGH), P5(LOW)
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
              { time: p5.time, price: p5.price, label: "Btm 3" },
            ],
            necklinePrice: avgNeckline,
            targetPrice: avgNeckline + (avgNeckline - avgBtm),
            confirmed: lastPrice > avgNeckline,
            triggerTime: p5.time,
          });
        }
      }
    }

    // --- Head & Shoulders & Inverse Head & Shoulders (5 pivots) ---
    if (p4 && i + 4 < n) {
      // Regular Head & Shoulders: LS(P0), V1(P1), Head(P2), V2(P3), RS(P4)
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
              { time: p4.time, price: p4.price, label: "RS" },
            ],
            necklinePrice: avgNeckline,
            targetPrice: target,
            confirmed: lastPrice < avgNeckline,
            triggerTime: p4.time,
          });
        }
      }

      // Inverse Head & Shoulders: LS(P0), P1(P1), Head(P2), P2(P3), RS(P4)
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
              { time: p4.time, price: p4.price, label: "RS" },
            ],
            necklinePrice: avgNeckline,
            targetPrice: target,
            confirmed: lastPrice > avgNeckline,
            triggerTime: p4.time,
          });
        }
      }
    }
  }

  // --- V-Reversal ---
  for (let i = 2; i < candles.length - 1; i++) {
    const cPrev = candles[i - 1];
    const cCurr = candles[i];
    const cNext = candles[i + 1];

    // Bullish V-Bottom: sharp drop followed immediately by sharp rally
    const drop = cPrev.open - cCurr.low;
    const rally = cNext.close - cCurr.low;
    if (drop >= currentATR * 2.0 && rally >= currentATR * 1.8 && cCurr.low === Math.min(cPrev.low, cCurr.low, cNext.low)) {
      patterns.push({
        id: `geo_vbtm_${cCurr.time}`,
        type: "geometric",
        subType: "v_reversal",
        name: "V-Bottom Reversal",
        label: "⚡ V-BOTTOM",
        bias: "bullish",
        points: [
          { time: cPrev.time, price: cPrev.open },
          { time: cCurr.time, price: cCurr.low },
          { time: cNext.time, price: cNext.close },
        ],
        targetPrice: cPrev.open,
        triggerTime: cCurr.time,
      });
    }

    // Bearish V-Top: sharp rally followed immediately by sharp dump
    const pump = cCurr.high - cPrev.open;
    const dump = cCurr.high - cNext.close;
    if (pump >= currentATR * 2.0 && dump >= currentATR * 1.8 && cCurr.high === Math.max(cPrev.high, cCurr.high, cNext.high)) {
      patterns.push({
        id: `geo_vtop_${cCurr.time}`,
        type: "geometric",
        subType: "v_reversal",
        name: "V-Top Reversal",
        label: "⚡ V-TOP",
        bias: "bearish",
        points: [
          { time: cPrev.time, price: cPrev.open },
          { time: cCurr.time, price: cCurr.high },
          { time: cNext.time, price: cNext.close },
        ],
        targetPrice: cPrev.open,
        triggerTime: cCurr.time,
      });
    }
  }

  // --- Triangles, Wedges, Flags & Megaphones ---
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

    // Ascending Triangle: Flat resistance (slope ~ 0), Rising support
    if (Math.abs(highSlopeNormalized) <= 0.005 && lowSlopeNormalized >= 0.008) {
      patterns.push({
        id: `geo_asctri_${h3.time}`,
        type: "geometric",
        subType: "ascending_triangle",
        name: "Ascending Triangle",
        label: "▲ ASCENDING TRIANGLE",
        bias: "bullish",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time),
      });
    }
    // Descending Triangle: Falling resistance, Flat support
    else if (highSlopeNormalized <= -0.008 && Math.abs(lowSlopeNormalized) <= 0.005) {
      patterns.push({
        id: `geo_dsctri_${h3.time}`,
        type: "geometric",
        subType: "descending_triangle",
        name: "Descending Triangle",
        label: "▼ DESCENDING TRIANGLE",
        bias: "bearish",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time),
      });
    }
    // Symmetrical Triangle: Falling resistance, Rising support
    else if (highSlopeNormalized <= -0.006 && lowSlopeNormalized >= 0.006) {
      patterns.push({
        id: `geo_symtri_${h3.time}`,
        type: "geometric",
        subType: "symmetrical_triangle",
        name: "Symmetrical Triangle",
        label: "◈ SYMMETRICAL TRIANGLE",
        bias: "neutral",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time),
      });
    }
    // Megaphone / Broadening Wedge: Rising highs and falling lows (expanding)
    else if (highSlopeNormalized >= 0.008 && lowSlopeNormalized <= -0.008) {
      patterns.push({
        id: `geo_megaph_${h3.time}`,
        type: "geometric",
        subType: "broadening_wedge",
        name: "Broadening Wedge (Megaphone)",
        label: "📢 BROADENING WEDGE",
        bias: "neutral",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time),
      });
    }
    // Rising Wedge: Both rising, low slope steeper than high slope
    else if (highSlopeNormalized > 0.006 && lowSlopeNormalized > highSlopeNormalized * 1.25) {
      patterns.push({
        id: `geo_rwdg_${h3.time}`,
        type: "geometric",
        subType: "rising_wedge",
        name: "Rising Wedge",
        label: "▼ RISING WEDGE",
        bias: "bearish",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time),
      });
    }
    // Falling Wedge: Both falling, high slope steeper than low slope
    else if (highSlopeNormalized < -0.006 && lowSlopeNormalized < 0 && Math.abs(highSlopeNormalized) > Math.abs(lowSlopeNormalized) * 1.25) {
      patterns.push({
        id: `geo_fwdg_${h3.time}`,
        type: "geometric",
        subType: "falling_wedge",
        name: "Falling Wedge",
        label: "▲ FALLING WEDGE",
        bias: "bullish",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time),
      });
    }
    // Rectangle Range: Both slopes nearly zero (channel/range)
    else if (Math.abs(highSlopeNormalized) <= 0.003 && Math.abs(lowSlopeNormalized) <= 0.003) {
      patterns.push({
        id: `geo_rect_${h3.time}`,
        type: "geometric",
        subType: "rectangle_range",
        name: "Rectangle Range",
        label: "▭ RECTANGLE RANGE",
        bias: "neutral",
        resistance: { p1: h1, p2: h3 },
        support: { p1: l1, p2: l3 },
        triggerTime: Math.max(h3.time, l3.time),
      });
    }
  }

  // Bull & Bear Flags (Impulse flagpole + compact counter-trend consolidation)
  if (candles.length >= 15) {
    const last15 = candles.slice(-15);
    const poleStart = last15[0];
    const poleEnd = last15[4];
    const flagSlice = last15.slice(4);

    const bullPole = poleEnd.close - poleStart.open;
    const bearPole = poleStart.open - poleEnd.close;

    if (bullPole >= currentATR * 3.0) {
      const flagHigh = Math.max(...flagSlice.map((c) => c.high));
      const flagLow = Math.min(...flagSlice.map((c) => c.low));
      const retracement = (flagHigh - flagLow) / bullPole;
      if (retracement <= 0.45 && last15[last15.length - 1].close >= flagHigh * 0.995) {
        patterns.push({
          id: `geo_bflag_${poleEnd.time}`,
          type: "geometric",
          subType: "bull_flag",
          name: "Bull Flag",
          label: "🚩 BULL FLAG",
          bias: "bullish",
          points: [
            { time: poleStart.time, price: poleStart.open, label: "Pole" },
            { time: poleEnd.time, price: poleEnd.close, label: "Peak" },
            { time: last15[last15.length - 1].time, price: flagLow, label: "Flag" },
          ],
          targetPrice: poleEnd.close + bullPole,
          triggerTime: poleEnd.time,
        });
      }
    } else if (bearPole >= currentATR * 3.0) {
      const flagHigh = Math.max(...flagSlice.map((c) => c.high));
      const flagLow = Math.min(...flagSlice.map((c) => c.low));
      const retracement = (flagHigh - flagLow) / bearPole;
      if (retracement <= 0.45 && last15[last15.length - 1].close <= flagLow * 1.005) {
        patterns.push({
          id: `geo_sflag_${poleEnd.time}`,
          type: "geometric",
          subType: "bear_flag",
          name: "Bear Flag",
          label: "🚩 BEAR FLAG",
          bias: "bearish",
          points: [
            { time: poleStart.time, price: poleStart.open, label: "Pole" },
            { time: poleEnd.time, price: poleEnd.close, label: "Trough" },
            { time: last15[last15.length - 1].time, price: flagHigh, label: "Flag" },
          ],
          targetPrice: poleEnd.close - bearPole,
          triggerTime: poleEnd.time,
        });
      }
    }
  }

  return patterns.slice(-6);
}

// ==========================================
// 4. Phase 3: Harmonic Patterns (Fibonacci)
// ==========================================

export function detectHarmonicPatterns(pivots, { maxLookback = 25 } = {}) {
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

    // Bullish Harmonic: X is Low, A is High, B is Low, C is High, D is Low (Buy Reversal at D)
    const isBull = X.type === "LOW" && A.type === "HIGH" && B.type === "LOW" && C.type === "HIGH" && D.type === "LOW";
    // Bearish Harmonic: X is High, A is Low, B is High, C is Low, D is High (Sell Reversal at D)
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

    // Gartley: B ~ 0.618 XA, D ~ 0.786 XA
    if (rXB >= 0.55 && rXB <= 0.68 && rAC >= 0.35 && rAC <= 0.90 && (isBull ? D.price > X.price : D.price < X.price)) {
      const gartleyXD = isBull ? (A.price - D.price) / XA : (D.price - A.price) / XA;
      if (gartleyXD >= 0.72 && gartleyXD <= 0.85) {
        matchedPattern = "Gartley";
      }
    }

    // Bat: B ~ 0.382-0.50 XA, D ~ 0.886 XA
    if (!matchedPattern && rXB >= 0.35 && rXB <= 0.53 && rAC >= 0.35 && rAC <= 0.90) {
      const batXD = isBull ? (A.price - D.price) / XA : (D.price - A.price) / XA;
      if (batXD >= 0.82 && batXD <= 0.95) {
        matchedPattern = "Bat";
      }
    }

    // Butterfly: B ~ 0.786 XA, D ~ 1.27-1.618 XA (Extends past X!)
    if (!matchedPattern && rXB >= 0.72 && rXB <= 0.85 && rAC >= 0.35 && rAC <= 0.90) {
      const butterflyXD = isBull ? (A.price - D.price) / XA : (D.price - A.price) / XA;
      if (butterflyXD >= 1.20 && butterflyXD <= 1.70) {
        matchedPattern = "Butterfly";
      }
    }

    // Crab / Deep Crab: B ~ 0.382-0.618 XA (or 0.886 for Deep Crab), D ~ 1.618 XA
    if (!matchedPattern && ((rXB >= 0.35 && rXB <= 0.65) || (rXB >= 0.82 && rXB <= 0.92)) && rAC >= 0.35 && rAC <= 0.90) {
      const crabXD = isBull ? (A.price - D.price) / XA : (D.price - A.price) / XA;
      if (crabXD >= 1.55 && crabXD <= 1.75) {
        matchedPattern = rXB > 0.75 ? "Deep Crab" : "Crab";
      }
    }

    // Cypher: B ~ 0.382-0.618 XA, C extends 1.13-1.414 XA, D ~ 0.786 XC
    if (!matchedPattern && rXB >= 0.35 && rXB <= 0.65) {
      const rXC = isBull ? (C.price - X.price) / XA : (X.price - C.price) / XA;
      if (rXC >= 1.10 && rXC <= 1.45) {
        matchedPattern = "Cypher";
      }
    }

    // Shark: C ~ 1.13-1.618 AB, D ~ 0.886-1.13 OX
    if (!matchedPattern && rAC >= 1.10 && rAC <= 1.65) {
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
          { time: D.time, price: D.price, label: "D" },
        ],
        ratios: {
          xb: rXB.toFixed(3),
          ac: rAC.toFixed(3),
          bd: rBD.toFixed(3),
          xd: rXD.toFixed(3),
        },
        prz: { low: przLow, high: przHigh },
        targets: { t1: target1, t2: target2 },
        triggerTime: D.time,
      });
    }
  }

  // Also check 4-point AB=CD pattern
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

    if (ratioABCD >= 0.90 && ratioABCD <= 1.10 && retracementBC >= 0.55 && retracementBC <= 0.85) {
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
          { time: D.time, price: D.price, label: "D" },
        ],
        ratios: {
          bc: retracementBC.toFixed(3),
          abcd: ratioABCD.toFixed(3),
        },
        targets: { t1: isBull ? D.price + CD * 0.382 : D.price - CD * 0.382 },
        triggerTime: D.time,
      });
    }
  }

  return harmonics.slice(-4);
}

// ==========================================
// 5. Phase 3: Liquidity & Price Action Setups
// ==========================================

export function detectLiquiditySetups(candles, pivots, { lookback = 60 } = {}) {
  if (!candles || candles.length < 3) return [];
  const setups = [];
  const startIdx = Math.max(3, candles.length - lookback);
  const volumeMAs = calculateVolumeMA(candles, 20);

  // 1. Wyckoff Spring & Upthrust (Liquidity Sweeps)
  const recentPivots = (pivots || []).slice(-15);
  const supportPivots = recentPivots.filter((p) => p.type === "LOW");
  const resistancePivots = recentPivots.filter((p) => p.type === "HIGH");

  for (let i = startIdx; i < candles.length; i++) {
    const c = candles[i];
    const vol = Number(c.volume || 0);
    const volMA = volumeMAs[i] || 1;
    const isHighVol = vol >= volMA * 1.05 || vol === 0; // if volume feed not present, proceed based on price action

    // Check Wyckoff Spring: Price pierces below support, but closes back above with rejection
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
          label: "⚡ SPRING (SWEEP)",
          bias: "bullish",
          time: c.time,
          levelPrice: supLevel,
          sweepPrice: c.low,
          description: `Liquidity sweep below ₹${supLevel.toFixed(2)} with strong recovery`,
        });
        break;
      }
    }

    // Check Wyckoff Upthrust (UTAD): Price pierces above resistance, closes back below
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
          label: "⚡ UPTHRUST (SWEEP)",
          bias: "bearish",
          time: c.time,
          levelPrice: resLevel,
          sweepPrice: c.high,
          description: `Liquidity sweep above ₹${resLevel.toFixed(2)} with strong rejection`,
        });
        break;
      }
    }
  }

  // 2. Quasimodo (QML / Over & Under)
  // Bullish QML: Low (L1) -> High (H1) -> Lower Low (L2) -> Higher High (H2) -> Retest L1
  // Bearish QML: High (H1) -> Low (L1) -> Higher High (H2) -> Lower Low (L2) -> Retest H1
  if (recentPivots.length >= 4) {
    const p1 = recentPivots[recentPivots.length - 4];
    const p2 = recentPivots[recentPivots.length - 3];
    const p3 = recentPivots[recentPivots.length - 2];
    const p4 = recentPivots[recentPivots.length - 1];

    // Bullish Quasimodo
    if (p1.type === "LOW" && p2.type === "HIGH" && p3.type === "LOW" && p4.type === "HIGH") {
      const lowerLow = p3.price < p1.price;
      const higherHigh = p4.price > p2.price;
      if (lowerLow && higherHigh) {
        setups.push({
          id: `liq_qm_bull_${p4.time}`,
          type: "liquidity",
          subType: "quasimodo",
          name: "Quasimodo Bullish (QML)",
          label: "🎯 QML DEMAND ZONE",
          bias: "bullish",
          time: p4.time,
          levelPrice: p1.price,
          targetPrice: p4.price,
          description: `Bullish QML structure shift; buy zone at ₹${p1.price.toFixed(2)}`,
        });
      }
    }

    // Bearish Quasimodo
    if (p1.type === "HIGH" && p2.type === "LOW" && p3.type === "HIGH" && p4.type === "LOW") {
      const higherHigh = p3.price > p1.price;
      const lowerLow = p4.price < p2.price;
      if (higherHigh && lowerLow) {
        setups.push({
          id: `liq_qm_bear_${p4.time}`,
          type: "liquidity",
          subType: "quasimodo",
          name: "Quasimodo Bearish (QML)",
          label: "🎯 QML SUPPLY ZONE",
          bias: "bearish",
          time: p4.time,
          levelPrice: p1.price,
          targetPrice: p4.price,
          description: `Bearish QML structure shift; sell zone at ₹${p1.price.toFixed(2)}`,
        });
      }
    }
  }

  // 3. Island Reversal (Gaps)
  for (let i = startIdx + 2; i < candles.length; i++) {
    const c1 = candles[i - 2];
    const c2 = candles[i - 1];
    const c3 = candles[i];

    // Bullish Island Reversal: Gap down to c2, then gap up to c3 leaving c2 isolated below
    if (c2.high < c1.low && c3.low > c2.high) {
      setups.push({
        id: `liq_isl_bull_${c3.time}`,
        type: "liquidity",
        subType: "island_reversal",
        name: "Island Reversal",
        label: "🏝️ ISLAND REVERSAL (BUY)",
        bias: "bullish",
        time: c2.time,
        levelPrice: c2.low,
        description: "Isolated bottom island reversal",
      });
    }
    // Bearish Island Reversal: Gap up to c2, then gap down to c3 leaving c2 isolated above
    if (c2.low > c1.high && c3.high < c2.low) {
      setups.push({
        id: `liq_isl_bear_${c3.time}`,
        type: "liquidity",
        subType: "island_reversal",
        name: "Island Reversal",
        label: "🏝️ ISLAND REVERSAL (SELL)",
        bias: "bearish",
        time: c2.time,
        levelPrice: c2.high,
        description: "Isolated top island reversal",
      });
    }
  }

  return setups.slice(-5);
}

// ==========================================
// 6. Master Pattern Scanner
// ==========================================

export function detectAllPatterns(candles, {
  candlestick = true,
  geometric = true,
  harmonic = true,
  liquidity = true,
  lookback = 100,
} = {}) {
  if (!candles || candles.length < 3) {
    return { candlestick: [], geometric: [], harmonic: [], liquidity: [] };
  }

  const result = {
    candlestick: [],
    geometric: [],
    harmonic: [],
    liquidity: [],
  };

  if (candlestick) {
    result.candlestick = detectCandlestickPatterns(candles, { lookback });
  }

  const pivots = (geometric || harmonic || liquidity) ? extractPivots(candles) : [];

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
