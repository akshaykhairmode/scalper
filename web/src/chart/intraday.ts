/**
 * Intraday index-option indicators that klinecharts does not ship:
 * ATR, SuperTrend and the open-interest walls derived from the option chain.
 *
 * Everything here is pure so it can be unit-tested without a DOM.
 */

export type CandleLike = {
  open?: number;
  high?: number;
  low?: number;
  close?: number;
  time?: number;
};

export type SupertrendResult = {
  /** Final lower band while the trend is up, null otherwise. */
  up: Array<number | null>;
  /** Final upper band while the trend is down, null otherwise. */
  down: Array<number | null>;
  /** 1 = up, -1 = down, 0 = not enough bars yet. */
  trend: number[];
};

export type OIWall = {
  strike: number;
  side: "CE" | "PE" | "BOTH";
  metric: "oi" | "doi";
  /** Open interest, or the intraday ΔOI, in contracts. */
  value: number;
  /** ΔOI as a percentage of the session baseline (metric "doi" only). */
  pct?: number;
};

const numOrNaN = (value) => {
  const n = Number(value);
  return Number.isFinite(n) ? n : NaN;
};

/** True range for one bar; the first bar uses high-low. */
export function trueRange(candle: CandleLike, previousClose = NaN) {
  const high = numOrNaN(candle?.high);
  const low = numOrNaN(candle?.low);
  if (!Number.isFinite(high) || !Number.isFinite(low) || high <= 0 || low <= 0) return NaN;
  const range = Math.max(0, high - low);
  if (!Number.isFinite(previousClose) || previousClose <= 0) return range;
  return Math.max(range, Math.abs(high - previousClose), Math.abs(low - previousClose));
}

/**
 * Wilder's ATR. Values before the first full period are NaN so callers can
 * treat them as "warming up" instead of drawing a partial average.
 */
export function atrSeries(candles: CandleLike[], period = 14): number[] {
  const list = Array.isArray(candles) ? candles : [];
  const length = Math.max(1, Math.round(Number(period) || 14));
  const out = new Array<number>(list.length).fill(NaN);
  if (!list.length) return out;

  let sum = 0;
  for (let i = 0; i < list.length; i++) {
    const previousClose = i > 0 ? numOrNaN(list[i - 1]?.close) : NaN;
    const tr = trueRange(list[i], previousClose);
    if (i < length) {
      if (Number.isFinite(tr)) sum += tr;
      if (i === length - 1) {
        out[i] = Number.isFinite(sum) ? sum / length : NaN;
      }
      continue;
    }
    const previous = out[i - 1];
    if (!Number.isFinite(previous) || !Number.isFinite(tr)) {
      out[i] = Number.isFinite(tr) ? tr : previous;
      continue;
    }
    out[i] = (previous * (length - 1) + tr) / length;
  }
  return out;
}

/** Latest ATR value, or 0 while the series is warming up. */
export function atrValue(candles: CandleLike[], period = 14): number {
  const series = atrSeries(candles, period);
  for (let i = series.length - 1; i >= 0; i--) {
    if (Number.isFinite(series[i]) && series[i] > 0) return series[i];
  }
  return 0;
}

/**
 * Per-bar open-interest change. Bars without a valid OI reading are NaN and do
 * not advance the comparison base, so a gap in the feed does not look like a
 * liquidation on the next bar.
 */
export function oiDeltaSeries(candles: Array<CandleLike & { oi?: number }>): number[] {
  const list = Array.isArray(candles) ? candles : [];
  const out = new Array<number>(list.length).fill(NaN);
  let previous = 0;
  for (let i = 0; i < list.length; i++) {
    const oi = Number(list[i]?.oi);
    if (!Number.isFinite(oi) || oi <= 0) continue;
    if (previous > 0) out[i] = oi - previous;
    // The first reading of a series has no comparable base.
    previous = oi;
  }
  return out;
}

export type OITickState = { oi: number; oiChange: number; oiChangePct: number };

/**
 * Fold a live tick into the chain's session ΔOI. The server owns the day
 * baseline, so a tick can only *shift* the change by the observed OI delta. A
 * contract whose previous OI was missing/zero must not fabricate a change: the
 * server's value is kept until the next chain snapshot provides a real base.
 */
export function applyOITick(current: OITickState, nextOI: number): OITickState {
  const previous = Number(current?.oi);
  const change = Number(current?.oiChange) || 0;
  const pct = Number(current?.oiChangePct) || 0;
  const next = Number(nextOI);
  if (!Number.isFinite(next) || next <= 0) {
    return { oi: Number.isFinite(previous) && previous > 0 ? previous : 0, oiChange: change, oiChangePct: pct };
  }
  if (!Number.isFinite(previous) || previous <= 0) {
    const baseline = next - change;
    return { oi: next, oiChange: change, oiChangePct: baseline > 0 ? (change / baseline) * 100 : 0 };
  }
  const delta = next - previous;
  if (delta === 0) {
    return { oi: next, oiChange: change, oiChangePct: pct };
  }
  const nextChange = change + delta;
  const baseline = next - nextChange;
  return { oi: next, oiChange: nextChange, oiChangePct: baseline > 0 ? (nextChange / baseline) * 100 : 0 };
}

/**
 * SuperTrend (ATR bands + trend flip). The up band trails price in an uptrend,
 * the down band trails it in a downtrend; a close through the active band flips
 * the trend. This is the classic intraday 10/3 configuration.
 */
export function supertrendSeries(
  candles: CandleLike[],
  period = 10,
  multiplier = 3,
): SupertrendResult {
  const list = Array.isArray(candles) ? candles : [];
  const length = list.length;
  const up = new Array<number | null>(length).fill(null);
  const down = new Array<number | null>(length).fill(null);
  const trend = new Array<number>(length).fill(0);
  if (!length) return { up, down, trend };

  const atr = atrSeries(list, period);
  const factor = Number.isFinite(Number(multiplier)) && Number(multiplier) > 0 ? Number(multiplier) : 3;

  let previousUpper = NaN;
  let previousLower = NaN;
  let previousTrend = 0;

  for (let i = 0; i < length; i++) {
    const candle = list[i] || {};
    const high = numOrNaN(candle.high);
    const low = numOrNaN(candle.low);
    const close = numOrNaN(candle.close);
    const currentAtr = atr[i];
    if (!Number.isFinite(high) || !Number.isFinite(low) || !Number.isFinite(close) || !Number.isFinite(currentAtr)) {
      trend[i] = previousTrend;
      continue;
    }

    const midpoint = (high + low) / 2;
    const basicUpper = midpoint + factor * currentAtr;
    const basicLower = midpoint - factor * currentAtr;
    const previousClose = i > 0 ? numOrNaN(list[i - 1]?.close) : close;

    const finalUpper = !Number.isFinite(previousUpper) || basicUpper < previousUpper || previousClose > previousUpper
      ? basicUpper
      : previousUpper;
    const finalLower = !Number.isFinite(previousLower) || basicLower > previousLower || previousClose < previousLower
      ? basicLower
      : previousLower;

    let currentTrend = previousTrend;
    if (previousTrend === 1) {
      if (close < finalLower) currentTrend = -1;
    } else if (previousTrend === -1) {
      if (close > finalUpper) currentTrend = 1;
    } else {
      // Seed the first trend from the closing side of the midpoint band.
      currentTrend = close >= finalLower ? 1 : -1;
    }

    trend[i] = currentTrend;
    if (currentTrend === 1) up[i] = finalLower;
    else down[i] = finalUpper;

    previousUpper = finalUpper;
    previousLower = finalLower;
    previousTrend = currentTrend;
  }

  return { up, down, trend };
}

const chainInt = (value) => {
  const n = Number(value);
  return Number.isFinite(n) ? n : 0;
};

/**
 * Pick the option-chain walls that matter for intraday index trading:
 * the strikes with the largest CE/PE open interest (resistance/support) and,
 * when it exists, the strikes with the largest positive intraday ΔOI build-up.
 * A strike that tops both CE and PE open interest collapses into one line.
 */
export function computeOIWalls(
  chain: any[],
  { includeDelta = true }: { includeDelta?: boolean } = {},
): OIWall[] {
  const rows = Array.isArray(chain) ? chain : [];
  let maxCallOI = 0;
  let maxPutOI = 0;
  let callOIStrike = 0;
  let putOIStrike = 0;
  let maxCallDOI = 0;
  let maxPutDOI = 0;
  let callDOIStrike = 0;
  let putDOIStrike = 0;
  let callDOIPct = 0;
  let putDOIPct = 0;

  for (const row of rows) {
    const strike = Number(row?.strike);
    if (!Number.isFinite(strike) || strike <= 0) continue;
    const call = row?.call;
    const put = row?.put;

    const callOI = chainInt(call?.oi);
    if (callOI > maxCallOI) {
      maxCallOI = callOI;
      callOIStrike = strike;
    }
    const putOI = chainInt(put?.oi);
    if (putOI > maxPutOI) {
      maxPutOI = putOI;
      putOIStrike = strike;
    }

    const callDOI = chainInt(call?.oiChange);
    if (callDOI > maxCallDOI) {
      maxCallDOI = callDOI;
      callDOIStrike = strike;
      callDOIPct = Number(call?.oiChangePct) || 0;
    }
    const putDOI = chainInt(put?.oiChange);
    if (putDOI > maxPutDOI) {
      maxPutDOI = putDOI;
      putDOIStrike = strike;
      putDOIPct = Number(put?.oiChangePct) || 0;
    }
  }

  const walls: OIWall[] = [];
  if (maxCallOI > 0 && maxPutOI > 0 && callOIStrike === putOIStrike) {
    walls.push({ strike: callOIStrike, side: "BOTH", metric: "oi", value: Math.max(maxCallOI, maxPutOI) });
  } else {
    if (maxCallOI > 0) walls.push({ strike: callOIStrike, side: "CE", metric: "oi", value: maxCallOI });
    if (maxPutOI > 0) walls.push({ strike: putOIStrike, side: "PE", metric: "oi", value: maxPutOI });
  }

  if (includeDelta) {
    if (maxCallDOI > 0) walls.push({ strike: callDOIStrike, side: "CE", metric: "doi", value: maxCallDOI, pct: callDOIPct });
    if (maxPutDOI > 0) walls.push({ strike: putDOIStrike, side: "PE", metric: "doi", value: maxPutDOI, pct: putDOIPct });
  }

  return walls.sort((a, b) => (a.strike - b.strike) || a.metric.localeCompare(b.metric));
}
