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

/** Typical price used for VWAP weighting. */
export function typicalPrice(candle: IndicatorCandle): number {
  const high = Number(candle?.high);
  const low = Number(candle?.low);
  const close = Number(candle?.close);
  if (!Number.isFinite(high) || !Number.isFinite(low) || !Number.isFinite(close)) return NaN;
  const value = (high + low + close) / 3;
  return Number.isFinite(value) && value > 0 ? value : NaN;
}

/**
 * Session-anchored Volume Weighted Average Price of the *given* series.
 *
 * Uses typical price ((high + low + close) / 3) and resets the cumulative
 * totals whenever `sessionKey` changes. Bars without traded volume are not
 * accumulated; the previous VWAP is carried forward.
 *
 * This is only exact when `candles` is a 1-minute (or tick) series: a 5m/15m/1h
 * bar contributes a single typical price instead of the volume-weighted average
 * of the trades inside it. Charts should therefore prefer {@link vwapFromBase}
 * with a 1-minute base series, and use this function as the fallback when no
 * base series is available.
 *
 * The line is broken on the *last* bar of a session rather than the first bar
 * of the next one, so the new session's opening VWAP is still drawn.
 */
export function vwapSeries(
  candles: IndicatorCandle[],
  sessionKey: (time: number) => string | number,
): number[] {
  const n = Array.isArray(candles) ? candles.length : 0;
  const out = new Array(n).fill(NaN);
  let currentSession: string | number = "";
  let cumulativePV = 0;
  let cumulativeVolume = 0;
  let sessionStarted = false;

  for (let i = 0; i < n; i++) {
    const candle = candles[i];
    if (!candle || !Number.isFinite(candle.time)) continue;

    const key = sessionKey(candle.time);
    if (key !== currentSession) {
      if (sessionStarted && i > 0) out[i - 1] = NaN;
      currentSession = key;
      sessionStarted = true;
      cumulativePV = 0;
      cumulativeVolume = 0;
    }

    const typical = typicalPrice(candle);
    if (!Number.isFinite(typical)) continue;

    const volume = Number(candle.volume);
    if (Number.isFinite(volume) && volume > 0) {
      cumulativePV += typical * volume;
      cumulativeVolume += volume;
    }
    if (cumulativeVolume > 0) {
      out[i] = cumulativePV / cumulativeVolume;
    }
  }
  return out;
}

export interface AlignedVwap {
  /** VWAP per display bar; NaN where nothing can be computed. */
  values: number[];
  /** Cumulative Σ(typical × volume) through each display bar (session scoped). */
  cumulativePv: number[];
  /** Cumulative Σ(volume) through each display bar (session scoped). */
  cumulativeVolume: number[];
  /** First timestamp present in the base series (0 when empty). */
  baseFirstTime: number;
  /** Last timestamp present in the base series (0 when empty). */
  baseLastTime: number;
  /**
   * First base timestamp of the session that contains the last display bar
   * (0 when that session has no base coverage). Compared with the exchange
   * open this tells the caller whether the VWAP is session-complete.
   */
  lastSessionFirstTime: number;
}

function lastIndexAtOrBefore(times: number[], value: number): number {
  let low = 0;
  let high = times.length - 1;
  let answer = -1;
  while (low <= high) {
    const mid = (low + high) >> 1;
    if (times[mid] <= value) {
      answer = mid;
      low = mid + 1;
    } else {
      high = mid - 1;
    }
  }
  return answer;
}

/**
 * Project a session VWAP computed from a fine-grained base series (1-minute
 * bars, ideally) onto a coarser display series.
 *
 * For a display bar fully covered by the base, the value is the base's
 * cumulative VWAP at that bar's close — i.e. true session VWAP, independent of
 * the chart timeframe. For a bar the base has only partly seen (typically the
 * forming bar) only the minutes it has not accounted for are priced with the
 * bar's own typical price, so the bar's volume is never counted twice.
 *
 * The cumulative outputs always advance monotonically along the display series,
 * even when the base stops before the last bar, so a caller can keep extending
 * the line from the previous bar between reloads.
 *
 * Both series must be sorted ascending by time and expressed in the same unit
 * (seconds, matching the chart candles).
 */
export function vwapFromBase(
  display: IndicatorCandle[],
  base: IndicatorCandle[],
  sessionKey: (time: number) => string | number,
  barSeconds: number,
): AlignedVwap {
  const bars = Array.isArray(display) ? display : [];
  const n = bars.length;
  const result: AlignedVwap = {
    values: new Array(n).fill(NaN),
    cumulativePv: new Array(n).fill(0),
    cumulativeVolume: new Array(n).fill(0),
    baseFirstTime: 0,
    baseLastTime: 0,
    lastSessionFirstTime: 0,
  };
  const source = (Array.isArray(base) ? base : []).filter(
    (candle) => candle && Number.isFinite(candle.time) && Number.isFinite(typicalPrice(candle)),
  );
  if (!n || !source.length || !(barSeconds > 0)) return result;

  const times: number[] = [];
  const keys: (string | number)[] = [];
  const cumulativePv: number[] = [];
  const cumulativeVolume: number[] = [];
  let currentSession: string | number | null = null;
  let runningPv = 0;
  let runningVolume = 0;

  for (const candle of source) {
    const key = sessionKey(candle.time);
    if (key !== currentSession) {
      currentSession = key;
      runningPv = 0;
      runningVolume = 0;
    }
    const typical = typicalPrice(candle);
    const volume = Number(candle.volume);
    if (Number.isFinite(volume) && volume > 0) {
      runningPv += typical * volume;
      runningVolume += volume;
    }
    times.push(candle.time);
    keys.push(key);
    cumulativePv.push(runningPv);
    cumulativeVolume.push(runningVolume);
  }
  result.baseFirstTime = times[0] || 0;
  result.baseLastTime = times[times.length - 1] || 0;

  const indexFor = (limit: number, key: string | number): number => {
    let index = lastIndexAtOrBefore(times, limit);
    while (index >= 0 && keys[index] !== key) index--;
    return index;
  };

  for (let i = 0; i < n; i++) {
    const bar = bars[i];
    if (!bar || !Number.isFinite(bar.time)) continue;
    const key = sessionKey(bar.time);
    const barEnd = bar.time + barSeconds;
    const covered = indexFor(barEnd - 1, key);

    // Volume already priced for this bar: either the base's cumulative totals
    // (which may cover a prefix of the bar) or the previous display bar's
    // totals when the base lags behind the chart.
    const before = indexFor(bar.time - 1, key);
    const basePv = covered >= 0 ? cumulativePv[covered] : 0;
    const baseVolume = covered >= 0 ? cumulativeVolume[covered] : 0;
    const baseVolumeInBar = covered >= 0
      ? baseVolume - (before >= 0 ? cumulativeVolume[before] : 0)
      : 0;
    const sameSession = i > 0
      && Number.isFinite(bars[i - 1]?.time)
      && sessionKey(bars[i - 1].time) === key;
    const prevPv = sameSession ? result.cumulativePv[i - 1] : 0;
    const prevVolume = sameSession ? result.cumulativeVolume[i - 1] : 0;

    if (covered < 0 && !sameSession) continue;

    const useBase = baseVolume >= prevVolume;
    const anchorPv = useBase ? basePv : prevPv;
    const anchorVolume = useBase ? baseVolume : prevVolume;
    const alreadyPriced = useBase ? baseVolumeInBar : 0;

    const typical = typicalPrice(bar);
    const volume = Number(bar.volume);
    const barVolume = Number.isFinite(volume) && volume > 0 ? volume : 0;
    const remaining = Math.max(0, barVolume - alreadyPriced);
    const totalPv = anchorPv + (Number.isFinite(typical) ? typical * remaining : 0);
    const totalVolume = anchorVolume + (Number.isFinite(typical) ? remaining : 0);
    if (totalVolume > 0) result.values[i] = totalPv / totalVolume;
    result.cumulativePv[i] = totalPv;
    result.cumulativeVolume[i] = totalVolume;
  }

  if (n > 0 && Number.isFinite(bars[n - 1]?.time)) {
    const lastKey = sessionKey(bars[n - 1].time);
    const first = keys.indexOf(lastKey);
    result.lastSessionFirstTime = first >= 0 ? times[first] : 0;
  }
  return result;
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
