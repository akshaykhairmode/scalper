// Black-Scholes time-decay estimation for the chart's theta pane.
//
// Kotak's market-data feed does not expose Greeks or implied volatility, so
// this module backs IV out of the option premium and derives theta locally.
// The result is a model estimate, not an exchange-provided Greek.

export interface CandleLike {
  time: number;
  close: number;
}

export interface ThetaPoint {
  ce: number | null;
  pe: number | null;
  ceTheta: number | null;
  peTheta: number | null;
  ceIv: number | null;
  peIv: number | null;
}

export interface ThetaMeta {
  ceTheta: number | null;
  peTheta: number | null;
  ceIv: number | null;
  peIv: number | null;
  ceDecay: number | null;
  peDecay: number | null;
  asOf: number;
}

export interface ThetaResult {
  byTime: Map<number, ThetaPoint>;
  meta: ThetaMeta | null;
}

export interface ThetaInput {
  spotCandles: CandleLike[];
  ceCandles: CandleLike[];
  peCandles: CandleLike[];
  strike: number;
  expiry: string;
  ceLotSize?: number;
  peLotSize?: number;
  riskFreeRate?: number;
  barSeconds?: number;
  sessionSeconds?: number;
  sessionCloseSeconds?: number;
  expiryCloseSeconds?: number;
}

const DAYS_PER_YEAR = 365;
const DEFAULT_RISK_FREE = 0.065;
const DEFAULT_SESSION_SECONDS = 6.25 * 60 * 60;
const MIN_IV = 0.001;
const MAX_IV = 3;

function toMillis(time: number): number {
  return time > 1e11 ? time : time * 1000;
}

function normPdf(x: number): number {
  return Math.exp(-0.5 * x * x) / Math.sqrt(2 * Math.PI);
}

function normCdf(x: number): number {
  const t = 1 / (1 + 0.2316419 * Math.abs(x));
  const d = 0.3989423 * Math.exp(-0.5 * x * x);
  let p = d * t * (0.3193815 + t * (-0.3565638 + t * (1.781478 + t * (-1.821256 + t * 1.330274))));
  return x > 0 ? 1 - p : p;
}

function optionPrice(type: string, spot: number, strike: number, years: number, rate: number, vol: number): number {
  if (spot <= 0 || strike <= 0 || years <= 0 || vol <= 0) return 0;
  const sqrtT = Math.sqrt(years);
  const d1 = (Math.log(spot / strike) + (rate + 0.5 * vol * vol) * years) / (vol * sqrtT);
  const d2 = d1 - vol * sqrtT;
  if (type === "PE") {
    return strike * Math.exp(-rate * years) * normCdf(-d2) - spot * normCdf(-d1);
  }
  return spot * normCdf(d1) - strike * Math.exp(-rate * years) * normCdf(d2);
}

function impliedVolatility(
  type: string,
  price: number,
  spot: number,
  strike: number,
  years: number,
  rate: number,
): number | null {
  if (!(price > 0) || spot <= 0 || strike <= 0 || years <= 0) return null;
  const discountedStrike = strike * Math.exp(-rate * years);
  const intrinsic = type === "PE"
    ? Math.max(0, discountedStrike - spot)
    : Math.max(0, spot - discountedStrike);
  const upper = type === "PE" ? discountedStrike : spot;
  if (price <= intrinsic + 1e-8 || price >= upper) return null;

  if (optionPrice(type, spot, strike, years, rate, MAX_IV) < price) return null;

  let low = MIN_IV;
  let high = MAX_IV;
  for (let i = 0; i < 70; i++) {
    const mid = (low + high) / 2;
    if (optionPrice(type, spot, strike, years, rate, mid) < price) low = mid;
    else high = mid;
  }
  const iv = (low + high) / 2;
  return Number.isFinite(iv) ? iv : null;
}

function thetaPerDay(
  type: string,
  spot: number,
  strike: number,
  years: number,
  rate: number,
  vol: number,
): number {
  if (spot <= 0 || strike <= 0 || years <= 0 || vol <= 0) return 0;
  const sqrtT = Math.sqrt(years);
  const d1 = (Math.log(spot / strike) + (rate + 0.5 * vol * vol) * years) / (vol * sqrtT);
  const d2 = d1 - vol * sqrtT;
  const first = -(spot * normPdf(d1) * vol) / (2 * sqrtT);
  const carry = rate * strike * Math.exp(-rate * years);
  const annual = type === "PE"
    ? first + carry * normCdf(-d2)
    : first - carry * normCdf(d2);
  return annual / DAYS_PER_YEAR;
}

function expiryMillis(expiry: string, closeSeconds = 15 * 3600 + 30 * 60): number {
  if (!expiry) return 0;
  const hours = Math.floor(closeSeconds / 3600);
  const minutes = Math.floor((closeSeconds % 3600) / 60);
  const clock = `${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}:00`;
  const parsed = new Date(`${expiry.trim()}T${clock}+05:30`).getTime();
  return Number.isFinite(parsed) ? parsed : 0;
}

function optionMap(candles: CandleLike[]): Map<number, number> {
  const map = new Map<number, number>();
  for (const candle of candles || []) {
    const price = Number(candle?.close);
    if (Number.isFinite(candle?.time) && Number.isFinite(price) && price > 0) {
      map.set(candle.time, price);
    }
  }
  return map;
}

export function buildThetaSeries(input: ThetaInput): ThetaResult {
  const {
    spotCandles,
    ceCandles,
    peCandles,
    strike,
    expiry,
    ceLotSize = 1,
    peLotSize = 1,
    riskFreeRate = DEFAULT_RISK_FREE,
    barSeconds = 300,
    sessionSeconds = DEFAULT_SESSION_SECONDS,
    sessionCloseSeconds = 15 * 3600 + 30 * 60,
    expiryCloseSeconds = 15 * 3600 + 30 * 60,
  } = input;

  const byTime = new Map<number, ThetaPoint>();
  const expiryAt = expiryMillis(expiry, expiryCloseSeconds);
  if (!expiryAt || !Array.isArray(spotCandles) || !spotCandles.length || !(strike > 0)) {
    return { byTime, meta: null };
  }

  const cePrices = optionMap(ceCandles);
  const pePrices = optionMap(peCandles);
  const session = Math.max(1, sessionSeconds);

  let lastCeIv: number | null = null;
  let lastPeIv: number | null = null;
  let lastCePrice: number | null = null;
  let lastPePrice: number | null = null;
  let ceDecay = 0;
  let peDecay = 0;
  let currentDay = Number.NaN;
  let last: ThetaPoint | null = null;
  let lastTime = 0;

  for (const spotCandle of spotCandles) {
    if (!Number.isFinite(spotCandle?.time) || !Number.isFinite(spotCandle?.close) || spotCandle.close <= 0) {
      continue;
    }
    const time = spotCandle.time;
    const day = Math.floor((toMillis(time) / 1000 + 5 * 3600 + 30 * 60) / 86400);
    if (day !== currentDay) {
      // Decay is cumulative within one trading session, not across the whole
      // retained history shown on the chart.
      currentDay = day;
      ceDecay = 0;
      peDecay = 0;
    }
    const localSeconds = (((toMillis(time) / 1000 + 5 * 3600 + 30 * 60) % 86400) + 86400) % 86400;
    const remainingSession = sessionCloseSeconds - localSeconds;
    const effectiveBarSeconds = remainingSession > 0
      ? Math.min(Math.max(1, barSeconds), remainingSession)
      : Math.max(1, barSeconds);
    const barFraction = effectiveBarSeconds / session;
    const years = (expiryAt - toMillis(time)) / (DAYS_PER_YEAR * 24 * 60 * 60 * 1000);
    if (years <= 0) {
      ceDecay = 0;
      peDecay = 0;
      lastCeIv = null;
      lastPeIv = null;
      last = { ce: null, pe: null, ceTheta: null, peTheta: null, ceIv: null, peIv: null };
      byTime.set(time, last);
      continue;
    }

    const spot = spotCandle.close;
    const cePrice = cePrices.get(time) ?? lastCePrice;
    const pePrice = pePrices.get(time) ?? lastPePrice;
    if (cePrices.has(time)) lastCePrice = cePrices.get(time) || lastCePrice;
    if (pePrices.has(time)) lastPePrice = pePrices.get(time) || lastPePrice;

    if (cePrice && cePrice > 0) {
      const iv = impliedVolatility("CE", cePrice, spot, strike, years, riskFreeRate);
      if (iv != null) lastCeIv = iv;
    }
    if (pePrice && pePrice > 0) {
      const iv = impliedVolatility("PE", pePrice, spot, strike, years, riskFreeRate);
      if (iv != null) lastPeIv = iv;
    }

    const ceTheta = lastCeIv == null
      ? null
      : thetaPerDay("CE", spot, strike, years, riskFreeRate, lastCeIv) * ceLotSize;
    const peTheta = lastPeIv == null
      ? null
      : thetaPerDay("PE", spot, strike, years, riskFreeRate, lastPeIv) * peLotSize;

    if (ceTheta != null && Number.isFinite(ceTheta)) ceDecay += ceTheta * barFraction;
    if (peTheta != null && Number.isFinite(peTheta)) peDecay += peTheta * barFraction;

    last = {
      ce: ceTheta == null ? null : ceDecay,
      pe: peTheta == null ? null : peDecay,
      ceTheta,
      peTheta,
      ceIv: lastCeIv,
      peIv: lastPeIv,
    };
    lastTime = time;
    byTime.set(time, last);
  }

  if (!last) return { byTime, meta: null };
  return {
    byTime,
    meta: {
      ceTheta: last.ceTheta,
      peTheta: last.peTheta,
      ceIv: last.ceIv,
      peIv: last.peIv,
      ceDecay: last.ce,
      peDecay: last.pe,
      asOf: lastTime,
    },
  };
}
