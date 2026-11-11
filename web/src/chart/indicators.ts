/**
 * Chart indicator math shared by the terminal charts.
 *
 * Pure functions only: candles in, series/signals out. The chart wrapper in
 * `charts.ts` is responsible for drawing the results.
 */

export interface IndicatorCandle {
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume?: number;
}

export interface Divergence {
  kind: "bull" | "bear";
  from: { time: number; price: number; rsi: number };
  to: { time: number; price: number; rsi: number };
}

export interface MomentumCandle {
  time: number;
  price: number;
  kind: "bull" | "bear";
  body: number;
  atr: number;
}

function rsiFromAverages(avgGain: number, avgLoss: number): number {
  if (avgLoss === 0) return avgGain === 0 ? 50 : 100;
  const rs = avgGain / avgLoss;
  return 100 - 100 / (1 + rs);
}

/** Wilder-smoothed RSI. Values before the first full period are NaN. */
export function rsiSeries(candles: IndicatorCandle[], period = 14): number[] {
  const n = Array.isArray(candles) ? candles.length : 0;
  const out = new Array(n).fill(NaN);
  if (n <= period) return out;

  let gain = 0;
  let loss = 0;
  for (let i = 1; i <= period; i++) {
    const change = candles[i].close - candles[i - 1].close;
    if (change >= 0) gain += change;
    else loss -= change;
  }
  let avgGain = gain / period;
  let avgLoss = loss / period;
  out[period] = rsiFromAverages(avgGain, avgLoss);

  for (let i = period + 1; i < n; i++) {
    const change = candles[i].close - candles[i - 1].close;
    const up = change > 0 ? change : 0;
    const down = change < 0 ? -change : 0;
    avgGain = (avgGain * (period - 1) + up) / period;
    avgLoss = (avgLoss * (period - 1) + down) / period;
    out[i] = rsiFromAverages(avgGain, avgLoss);
  }
  return out;
}

/** Exponential moving average seeded with the SMA of the first period. */
export function emaSeries(values: number[], period: number): number[] {
  const n = Array.isArray(values) ? values.length : 0;
  const out = new Array(n).fill(NaN);
  if (n === 0 || period <= 0) return out;

  const k = 2 / (period + 1);
  let prev = NaN;
  for (let i = 0; i < n; i++) {
    const value = values[i];
    if (!Number.isFinite(value)) continue;
    if (!Number.isFinite(prev)) {
      if (i + 1 < period) continue;
      let sum = 0;
      for (let j = i + 1 - period; j <= i; j++) sum += values[j];
      prev = sum / period;
    } else {
      prev = value * k + prev * (1 - k);
    }
    out[i] = prev;
  }
  return out;
}

export interface EmaCross {
  time: number;
  price: number;
  kind: "bull" | "bear";
}

/** Crossovers between a fast and a slow EMA series. */
export function detectEmaCrosses(
  candles: IndicatorCandle[],
  fast: number[],
  slow: number[],
  { lookback = 200, max = 20 }: { lookback?: number; max?: number } = {}
): EmaCross[] {
  const n = Array.isArray(candles) ? candles.length : 0;
  if (n < 2 || !Array.isArray(fast) || !Array.isArray(slow)) return [];
  if (fast.length !== n || slow.length !== n) return [];

  const start = Math.max(1, n - lookback);
  const out: EmaCross[] = [];
  for (let i = start; i < n; i++) {
    if (!Number.isFinite(fast[i]) || !Number.isFinite(slow[i])) continue;
    if (!Number.isFinite(fast[i - 1]) || !Number.isFinite(slow[i - 1])) continue;
    const prevDiff = fast[i - 1] - slow[i - 1];
    const diff = fast[i] - slow[i];
    if (prevDiff <= 0 && diff > 0) {
      out.push({ time: candles[i].time, price: candles[i].low, kind: "bull" });
    } else if (prevDiff >= 0 && diff < 0) {
      out.push({ time: candles[i].time, price: candles[i].high, kind: "bear" });
    }
  }
  return out.slice(-max);
}

/** Wilder-smoothed Average True Range. Values before the first period are NaN. */
export function atrSeries(candles: IndicatorCandle[], period = 14): number[] {
  const n = Array.isArray(candles) ? candles.length : 0;
  const out = new Array(n).fill(NaN);
  if (n < period) return out;

  const trueRanges = new Array(n).fill(0);
  for (let i = 0; i < n; i++) {
    const candle = candles[i];
    const prev = i > 0 ? candles[i - 1] : null;
    trueRanges[i] = prev
      ? Math.max(candle.high - candle.low, Math.abs(candle.high - prev.close), Math.abs(candle.low - prev.close))
      : candle.high - candle.low;
  }

  let sum = 0;
  for (let i = 0; i < period; i++) sum += trueRanges[i];
  let atr = sum / period;
  out[period - 1] = atr;
  for (let i = period; i < n; i++) {
    atr = (atr * (period - 1) + trueRanges[i]) / period;
    out[i] = atr;
  }
  return out;
}

interface DivergenceOptions {
  lookback?: number;
  span?: number;
  minRsiDelta?: number;
  max?: number;
}

/**
 * Regular (classic) RSI divergence between the two most recent matching pivots:
 * bullish = lower price low with a higher RSI low, bearish = higher price high
 * with a lower RSI high.
 */
export function detectDivergences(
  candles: IndicatorCandle[],
  rsi: number[],
  { lookback = 150, span = 3, minRsiDelta = 1, max = 4 }: DivergenceOptions = {}
): Divergence[] {
  const n = Array.isArray(candles) ? candles.length : 0;
  if (n < span * 2 + 3 || !Array.isArray(rsi) || rsi.length !== n) return [];

  const start = Math.max(span, n - lookback);
  const lows: number[] = [];
  const highs: number[] = [];

  for (let i = start; i < n - span; i++) {
    if (!Number.isFinite(rsi[i])) continue;
    let isLow = true;
    let isHigh = true;
    for (let k = 1; k <= span; k++) {
      if (candles[i].low > candles[i - k].low || candles[i].low > candles[i + k].low) isLow = false;
      if (candles[i].high < candles[i - k].high || candles[i].high < candles[i + k].high) isHigh = false;
      if (!isLow && !isHigh) break;
    }
    if (isLow) lows.push(i);
    if (isHigh) highs.push(i);
  }

  const found: Divergence[] = [];

  for (let j = 1; j < lows.length; j++) {
    const a = lows[j - 1];
    const b = lows[j];
    if (candles[b].low < candles[a].low && rsi[b] > rsi[a] + minRsiDelta) {
      found.push({
        kind: "bull",
        from: { time: candles[a].time, price: candles[a].low, rsi: rsi[a] },
        to: { time: candles[b].time, price: candles[b].low, rsi: rsi[b] },
      });
    }
  }

  for (let j = 1; j < highs.length; j++) {
    const a = highs[j - 1];
    const b = highs[j];
    if (candles[b].high > candles[a].high && rsi[b] < rsi[a] - minRsiDelta) {
      found.push({
        kind: "bear",
        from: { time: candles[a].time, price: candles[a].high, rsi: rsi[a] },
        to: { time: candles[b].time, price: candles[b].high, rsi: rsi[b] },
      });
    }
  }

  return found.slice(-max);
}

interface MomentumOptions {
  lookback?: number;
  multiplier?: number;
  maxBodyRatio?: number;
  max?: number;
}

/**
 * "Momentum candles" (TradingLab-style): a wide-body bar whose body is at least
 * `multiplier` x ATR and that closes near its extreme (small wicks).
 */
export function detectMomentumCandles(
  candles: IndicatorCandle[],
  atr: number[],
  { lookback = 150, multiplier = 1.5, maxBodyRatio = 0.6, max = 40 }: MomentumOptions = {}
): MomentumCandle[] {
  const n = Array.isArray(candles) ? candles.length : 0;
  if (n < 2 || !Array.isArray(atr) || atr.length !== n) return [];

  const start = Math.max(0, n - lookback);
  const out: MomentumCandle[] = [];

  for (let i = start; i < n; i++) {
    const candle = candles[i];
    const range = candle.high - candle.low;
    const body = Math.abs(candle.close - candle.open);
    const average = atr[i];
    if (range <= 0 || !Number.isFinite(average) || average <= 0) continue;
    if (body < average * multiplier) continue;
    if (body / range < maxBodyRatio) continue;
    const bull = candle.close >= candle.open;
    out.push({
      time: candle.time,
      price: bull ? candle.low : candle.high,
      kind: bull ? "bull" : "bear",
      body,
      atr: average,
    });
  }

  return out.slice(-max);
}
