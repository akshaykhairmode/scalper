/**
 * Deterministic, per-bar confluence signal engine for NIFTY/SENSEX index
 * option entries.
 *
 * `evaluateSignals` walks the CLOSED spot bars in order and emits one
 * {@link SignalResult} per bar. The engine is strictly causal: the result at
 * bar `i` is a pure function of the input data at or before bar `i`, so a
 * backtest can replay the identical code path on a growing prefix and get
 * bit-identical results (the no-lookahead property is enforced by tests).
 *
 * Design rules:
 * - A leg whose data source is missing (or still warming up) degrades to a
 *   flat vote with an explanatory note and is excluded from the score
 *   denominator, so a missing feed degrades confidence instead of silently
 *   punishing the score.
 * - Vetoes (session windows, cooldown, near OI walls) force `WAIT` with a
 *   `blockedBy` note regardless of score. Precedence when several apply:
 *   session start -> session end -> cooldown -> wall.
 * - `stop` / `target` / `strikeHint` only appear on confirmed `BUY_*` results.
 *
 * Pure functions only: no DOM, no clock, no network.
 */

// Sibling chart modules are imported with an explicit ".ts" extension so this
// file stays loadable under `node --test`'s type-stripping loader (which, like
// ESM in general, does not resolve extensionless specifiers). tsc runs in
// bundler mode without `allowImportingTsExtensions`, so a TS5097 expect-error
// directive sits on each import. esbuild and the app bundle resolve these
// either way, and the directives self-clean (they turn into "unused directive"
// errors) the day the tsconfig grows `allowImportingTsExtensions`.
// @ts-expect-error TS5097 - node --test needs the explicit .ts extension
import { atrSeries, emaSeries, rsiSeries, vwapSeries, type IndicatorCandle } from "./indicators.ts";
// @ts-expect-error TS5097 - node --test needs the explicit .ts extension
import { oiDeltaSeries, supertrendSeries } from "./intraday.ts";
// @ts-expect-error TS5097 - node --test needs the explicit .ts extension
import { calculateVolumeMA, detectCandlestickPatterns, detectLiquiditySetups, extractPivots } from "./patterns.ts";

// ==========================================
// Exported contract
// ==========================================

export type SignalLeg =
  | "supertrend"
  | "emaStructure"
  | "vwap"
  | "volumeConviction"
  | "futuresOI"
  | "rsi"
  | "patterns"
  | "premiumDOI"
  | "chainContext";

export type SignalAction = "BUY_CE" | "BUY_PE" | "ARMED_CE" | "ARMED_PE" | "WAIT";

export interface SignalVote {
  leg: SignalLeg;
  dir: "bull" | "bear" | "flat";
  /** Effective contribution of this vote (halved for short covering, for example). */
  weight: number;
  note?: string;
}

export interface SignalResult {
  /** Bar time in seconds, matching the input spot bar. */
  time: number;
  action: SignalAction;
  /** 0..100 direction-neutral magnitude: |net| / weight of non-missing legs * 100. */
  score: number;
  /** Sign of the net score. */
  direction: "bull" | "bear" | "flat";
  votes: SignalVote[];
  /** Spot-scale stop, only on BUY_CE / BUY_PE. */
  stop?: number;
  /** Spot-scale target, only on BUY_CE / BUY_PE. */
  target?: number;
  strikeHint?: { type: "CE" | "PE"; strike: number; reason: string };
  /** Human note when a veto forced WAIT. */
  blockedBy?: string;
}

export interface ChainSnapshotLike {
  /** Seconds; the engine uses the latest snapshot with time <= the bar time. */
  time: number;
  spot?: number;
  pcr?: number;
  maxPain?: number;
  /** Strike with the max CE open interest. */
  callOIWall?: number;
  /** Strike with the max PE open interest. */
  putOIWall?: number;
  atmStrike?: number;
}

export interface SignalSettings {
  /** Default 55 (of 100). */
  scoreThreshold: number;
  /** Default 12: score within [threshold-armBand, threshold) arms the signal. */
  armBand: number;
  /** Default 3: bars after a confirmed BUY that are forced WAIT. */
  cooldownBars: number;
  /** Default 14. */
  atrPeriod: number;
  /** Default 1.0. */
  atrStopMultiplier: number;
  /** Default 2.0 when no OI wall target exists. */
  fallbackTargetAtrX: number;
  /** Default 3 (15 min on 5m bars) blocked after the open. */
  skipFirstBars: number;
  /** Default 6 (last 30 min on 5m bars) blocked before the close. */
  noEntryLastBars: number;
  /** "09:15" IST. */
  sessionOpen: string;
  /** "15:30" IST. */
  sessionClose: string;
  /** Default 1.5 (futures bar volume vs its 20-bar MA). */
  volumeConvictionX: number;
  /** Default 0.25: a wall closer than this blocks the trade. */
  wallVetoAtrX: number;
  /** Default 1.2 (contrarian bull when PCR is at or above). */
  pcrBullAbove: number;
  /** Default 0.7 (contrarian bear when PCR is at or below). */
  pcrBearBelow: number;
  weights: Record<SignalLeg, number>;
}

export interface SignalInput {
  /** 5m CLOSED bars, ascending, IST epoch seconds. */
  spot: IndicatorCandle[];
  /** Same grid / timescale as the spot bars. */
  futures?: Array<IndicatorCandle & { oi?: number }>;
  premium?: {
    ce?: Array<IndicatorCandle & { oi?: number }>;
    pe?: Array<IndicatorCandle & { oi?: number }>;
  };
  /** Ascending by time. */
  chain?: ChainSnapshotLike[];
  settings?: Partial<SignalSettings>;
}

// ==========================================
// Constants
// ==========================================

const SIGNAL_LEGS: SignalLeg[] = [
  "supertrend",
  "emaStructure",
  "vwap",
  "volumeConviction",
  "futuresOI",
  "rsi",
  "patterns",
  "premiumDOI",
  "chainContext",
];

/** IST is a fixed +05:30 offset with no daylight saving. */
const IST_OFFSET_SECONDS = 19_800;
const SECONDS_PER_DAY = 86_400;
/** A chain snapshot older than 15 minutes is treated as missing data. */
const CHAIN_STALE_SECONDS = 900;

const SUPERTREND_PERIOD = 10;
const SUPERTREND_MULTIPLIER = 3;
const RSI_PERIOD = 14;
const EMA_FAST_PERIOD = 10;
const EMA_SLOW_PERIOD = 20;
const EMA_LONG_PERIOD = 200;
const VOLUME_MA_PERIOD = 20;

/** Minimum run (in ATRs) a wall must clear to be used as the target. */
const MIN_TARGET_RUN_ATR_X = 0.5;
/** NIFTY / SENSEX strike step. */
const STRIKE_STEP = 50;

const DEFAULT_BAR_SECONDS = 300;
const DEFAULT_OPEN_SECOND = 9 * 3600 + 15 * 60;
const DEFAULT_CLOSE_SECOND = 15 * 3600 + 30 * 60;

const CLOCK_RE = /^([01]?\d|2[0-3]):([0-5]\d)$/;

/** Default leg weights; they sum to 100. */
const DEFAULT_WEIGHTS: Record<SignalLeg, number> = {
  supertrend: 18,
  emaStructure: 14,
  vwap: 14,
  volumeConviction: 12,
  futuresOI: 10,
  rsi: 10,
  patterns: 8,
  premiumDOI: 8,
  chainContext: 6,
};

export function defaultSignalSettings(): SignalSettings {
  return {
    scoreThreshold: 55,
    armBand: 12,
    cooldownBars: 3,
    atrPeriod: 14,
    atrStopMultiplier: 1.0,
    fallbackTargetAtrX: 2.0,
    skipFirstBars: 3,
    noEntryLastBars: 6,
    sessionOpen: "09:15",
    sessionClose: "15:30",
    volumeConvictionX: 1.5,
    wallVetoAtrX: 0.25,
    pcrBullAbove: 1.2,
    pcrBearBelow: 0.7,
    weights: { ...DEFAULT_WEIGHTS },
  };
}

// ==========================================
// Internal types and helpers
// ==========================================

/** A leg's evaluation before it is folded into the score. */
interface LegEvaluation {
  leg: SignalLeg;
  dir: "bull" | "bear" | "flat";
  weight: number;
  note?: string;
  /**
   * False when the leg is flat because its data is missing or warming up:
   * such legs are excluded from the score denominator (degrade, don't punish).
   */
  available: boolean;
}

/** Loose shape of the pattern objects returned by patterns.ts. */
interface PatternLike {
  time?: number;
  bias?: string;
  name?: string;
}

/** Epoch seconds; tolerate millisecond inputs the way `session.ts` does. */
function toEpochSeconds(time: number): number {
  const value = Number(time);
  if (!Number.isFinite(value)) return NaN;
  return value > 1e11 ? Math.floor(value / 1000) : value;
}

/** Local IST session key: one trading day per key (fixed +05:30, no DST). */
function istDayKey(time: number): number {
  const seconds = toEpochSeconds(time);
  return Math.floor((seconds + IST_OFFSET_SECONDS) / SECONDS_PER_DAY);
}

/** Second of the IST day for a bar time (NaN for malformed times). */
function istSecondsOfDay(time: number): number {
  const seconds = toEpochSeconds(time);
  if (!Number.isFinite(seconds)) return NaN;
  return ((seconds + IST_OFFSET_SECONDS) % SECONDS_PER_DAY + SECONDS_PER_DAY) % SECONDS_PER_DAY;
}

/** "HH:MM" -> seconds of day, or the fallback when malformed. */
function parseClock(value: string, fallbackSeconds: number): number {
  if (typeof value !== "string") return fallbackSeconds;
  const match = CLOCK_RE.exec(value.trim());
  if (!match) return fallbackSeconds;
  return Number(match[1]) * 3600 + Number(match[2]) * 60;
}

/** Merge a partial override onto the defaults with light numeric hygiene. */
function resolveSettings(partial?: Partial<SignalSettings>): SignalSettings {
  const base = defaultSignalSettings();
  const raw = { ...base, ...(partial || {}) } as SignalSettings;
  const num = (value: unknown, fallback: number) => {
    const n = Number(value);
    return Number.isFinite(n) ? n : fallback;
  };
  const int = (value: unknown, fallback: number) => Math.max(0, Math.round(num(value, fallback)));

  const settings: SignalSettings = {
    scoreThreshold: num(raw.scoreThreshold, base.scoreThreshold),
    armBand: Math.max(0, num(raw.armBand, base.armBand)),
    cooldownBars: int(raw.cooldownBars, base.cooldownBars),
    atrPeriod: Math.max(1, Math.round(num(raw.atrPeriod, base.atrPeriod))),
    atrStopMultiplier: Math.max(0, num(raw.atrStopMultiplier, base.atrStopMultiplier)),
    fallbackTargetAtrX: Math.max(0, num(raw.fallbackTargetAtrX, base.fallbackTargetAtrX)),
    skipFirstBars: int(raw.skipFirstBars, base.skipFirstBars),
    noEntryLastBars: int(raw.noEntryLastBars, base.noEntryLastBars),
    sessionOpen:
      typeof raw.sessionOpen === "string" && CLOCK_RE.test(raw.sessionOpen.trim())
        ? raw.sessionOpen
        : base.sessionOpen,
    sessionClose:
      typeof raw.sessionClose === "string" && CLOCK_RE.test(raw.sessionClose.trim())
        ? raw.sessionClose
        : base.sessionClose,
    volumeConvictionX: Math.max(0, num(raw.volumeConvictionX, base.volumeConvictionX)),
    wallVetoAtrX: Math.max(0, num(raw.wallVetoAtrX, base.wallVetoAtrX)),
    pcrBullAbove: num(raw.pcrBullAbove, base.pcrBullAbove),
    pcrBearBelow: num(raw.pcrBearBelow, base.pcrBearBelow),
    weights: {} as Record<SignalLeg, number>,
  };

  for (const leg of SIGNAL_LEGS) {
    const weight = Number((partial as SignalSettings | undefined)?.weights?.[leg]);
    settings.weights[leg] = Number.isFinite(weight) && weight >= 0 ? weight : base.weights[leg];
  }
  return settings;
}

/** time -> index map (last index wins for duplicate times). */
function indexByTime(candles: Array<{ time?: number }>): Map<number, number> {
  const map = new Map<number, number>();
  if (!Array.isArray(candles)) return map;
  for (let index = 0; index < candles.length; index++) {
    const time = Number(candles[index]?.time);
    if (Number.isFinite(time)) map.set(time, index);
  }
  return map;
}

/** The bar's own direction: close vs open. */
function barDirection(candle: IndicatorCandle): "bull" | "bear" | "flat" {
  const open = Number(candle?.open);
  const close = Number(candle?.close);
  if (!Number.isFinite(open) || !Number.isFinite(close)) return "flat";
  if (close > open) return "bull";
  if (close < open) return "bear";
  return "flat";
}

/**
 * Session-anchored VWAP over the futures bars, sliced per IST day.
 *
 * `vwapSeries` breaks the line on the *last* bar of a session only once it
 * sees the first bar of the next one, so running it over a multi-day array
 * would make the value at the session's last bar depend on a future bar.
 * Slicing by session keeps every value causal and identical to what a
 * per-bar prefix recomputation would produce.
 */
function sessionSlicedVwap(futures: Array<IndicatorCandle & { oi?: number }>): number[] {
  const out = new Array<number>(futures.length).fill(NaN);
  if (!futures.length) return out;

  let start = 0;
  for (let j = 1; j <= futures.length; j++) {
    if (j < futures.length && istDayKey(Number(futures[j]?.time)) === istDayKey(Number(futures[start]?.time))) {
      continue;
    }
    const segment = futures.slice(start, j);
    const values = vwapSeries(segment, (time) => istDayKey(time));
    for (let k = 0; k < values.length; k++) out[start + k] = values[k];
    start = j;
  }
  return out;
}

/**
 * ATR at the bar: the Wilder value, scanning back to the first finite reading.
 * While the period is still warming up (bars before atrPeriod-1) this falls
 * back to the mean true range of the available prefix, which is still causal
 * and keeps the wall veto and stop/target usable on the earliest tradable
 * bars instead of silently skipping them.
 */
function atrAtBar(candles: IndicatorCandle[], series: number[], index: number): number {
  for (let j = index; j >= 0; j--) {
    const value = series[j];
    if (Number.isFinite(value) && value > 0) return value;
  }

  let sum = 0;
  let count = 0;
  for (let j = 0; j <= index; j++) {
    const high = Number(candles[j]?.high);
    const low = Number(candles[j]?.low);
    if (!Number.isFinite(high) || !Number.isFinite(low)) continue;
    const range = Math.max(0, high - low);
    const previousClose = j > 0 ? Number(candles[j - 1]?.close) : NaN;
    const tr = Number.isFinite(previousClose)
      ? Math.max(range, Math.abs(high - previousClose), Math.abs(low - previousClose))
      : range;
    if (Number.isFinite(tr)) {
      sum += tr;
      count++;
    }
  }
  return count > 0 ? sum / count : NaN;
}

/** Target for a BUY: the OI wall in the trade direction if it clears half an ATR, else the ATR fallback. */
function targetFor(
  close: number,
  atrHere: number,
  wall: number,
  bull: boolean,
  settings: SignalSettings,
): number {
  if (Number.isFinite(wall)) {
    const run = bull ? wall - close : close - wall;
    if (run >= MIN_TARGET_RUN_ATR_X * atrHere) return wall;
  }
  const fallback = settings.fallbackTargetAtrX * atrHere;
  return bull ? close + fallback : close - fallback;
}

/** Strike hint: fresh chain ATM when available, else spot rounded to the 50-point step. */
function strikeHintFor(
  bull: boolean,
  close: number,
  snapshot: ChainSnapshotLike | undefined,
): SignalResult["strikeHint"] {
  const chainStrike = snapshot === undefined ? NaN : Number(snapshot.atmStrike);
  const strike = Number.isFinite(chainStrike) ? chainStrike : roundToStep(close, STRIKE_STEP);
  if (!Number.isFinite(strike)) return undefined;
  return {
    type: bull ? "CE" : "PE",
    strike,
    reason: Number.isFinite(chainStrike)
      ? "ATM strike from the freshest chain snapshot"
      : "no chain data; spot rounded to the nearest 50 strike step",
  };
}

function roundToStep(value: number, step: number): number {
  if (!Number.isFinite(value) || !(step > 0)) return NaN;
  return Math.round(value / step) * step;
}

// ==========================================
// Engine
// ==========================================

export function evaluateSignals(input: SignalInput): SignalResult[] {
  const spot = Array.isArray(input?.spot) ? input.spot : [];
  if (spot.length < 2) return [];

  const settings = resolveSettings(input?.settings);
  const weights = settings.weights;
  const sessionOpenSecond = parseClock(settings.sessionOpen, DEFAULT_OPEN_SECOND);
  const sessionCloseSecond = parseClock(settings.sessionClose, DEFAULT_CLOSE_SECOND);
  // Bar length from the smallest positive gap so the session windows count
  // *bars*. A window whose head lands on a session break would otherwise
  // infer the overnight gap as the bar length and veto the whole evaluation.
  // 5m bars in practice, 300s as the fallback when no positive gap exists.
  let barSeconds = DEFAULT_BAR_SECONDS;
  for (let i = 1; i < spot.length; i++) {
    const delta = Number(spot[i].time) - Number(spot[i - 1].time);
    if (delta > 0 && delta < barSeconds) barSeconds = delta;
  }

  // Causal series, computed once over the full input. Every helper below is a
  // forward recurrence — its value at index i depends only on bars at or
  // before i — so one pass is bit-identical to recomputing each per-bar
  // prefix; the no-lookahead test enforces this. The two exceptions are
  // handled differently in the loop: pattern detectors below depend on the
  // *shape* of the window (lookback bounds, pivot confirmation), so they are
  // re-run on each prefix; and the VWAP is sliced per session above because
  // `vwapSeries` would otherwise write its session break into a bar using
  // the next session's first bar.
  const closes = spot.map((candle) => Number(candle?.close));
  const emaFastValues = emaSeries(closes, EMA_FAST_PERIOD);
  const emaSlowValues = emaSeries(closes, EMA_SLOW_PERIOD);
  const emaLongValues = emaSeries(closes, EMA_LONG_PERIOD);
  const rsiValues = rsiSeries(spot, RSI_PERIOD);
  const trendValues = supertrendSeries(spot, SUPERTREND_PERIOD, SUPERTREND_MULTIPLIER).trend;
  const atrValues = atrSeries(spot, settings.atrPeriod);

  const futures = Array.isArray(input?.futures) ? input.futures : [];
  const futuresIndexByTime = indexByTime(futures);
  const futuresOiDelta = futures.length ? oiDeltaSeries(futures) : [];
  const futuresVolumeMA = futures.length ? calculateVolumeMA(futures, VOLUME_MA_PERIOD) : [];
  const futuresVwap = sessionSlicedVwap(futures);

  const cePremium = Array.isArray(input?.premium?.ce) ? input.premium.ce : [];
  const pePremium = Array.isArray(input?.premium?.pe) ? input.premium.pe : [];
  const ceIndexByTime = indexByTime(cePremium);
  const peIndexByTime = indexByTime(pePremium);
  const ceOiDelta = cePremium.length ? oiDeltaSeries(cePremium) : [];
  const peOiDelta = pePremium.length ? oiDeltaSeries(pePremium) : [];

  const chain = (Array.isArray(input?.chain) ? input.chain : [])
    .filter((snapshot) => snapshot && Number.isFinite(Number(snapshot.time)))
    .sort((a, b) => Number(a.time) - Number(b.time));

  const results: SignalResult[] = [];
  let cooldownUntil = -1;
  let chainIndex = -1;

  for (let i = 0; i < spot.length; i++) {
    const bar = spot[i];
    const barTime = Number(bar?.time);
    const close = Number(bar?.close);

    // Latest chain snapshot at or before the bar; bars ascend, so a
    // two-pointer scan is enough and stays causal.
    while (chainIndex + 1 < chain.length && Number(chain[chainIndex + 1].time) <= barTime) {
      chainIndex++;
    }
    const snapshot = chainIndex >= 0 ? chain[chainIndex] : undefined;
    const snapshotFresh = !!snapshot && barTime - Number(snapshot.time) <= CHAIN_STALE_SECONDS;

    const futuresIndex = futuresIndexByTime.get(barTime);
    const ceIndex = ceIndexByTime.get(barTime);
    const peIndex = peIndexByTime.get(barTime);

    const legs: LegEvaluation[] = [];

    // 1. SuperTrend (10, 3): trend 1 bull, -1 bear, 0 still warming up.
    {
      const value = trendValues[i];
      legs.push({
        leg: "supertrend",
        dir: value === 1 ? "bull" : value === -1 ? "bear" : "flat",
        weight: weights.supertrend,
        note: value === 1 || value === -1 ? undefined : "warming up",
        available: value === 1 || value === -1,
      });
    }

    // 2. EMA structure: close vs EMA10 vs EMA20; EMA200 is informational and
    // never gates the vote while it warms up (documented in the note).
    {
      const fast = emaFastValues[i];
      const slow = emaSlowValues[i];
      const longEma = emaLongValues[i];
      let dir: "bull" | "bear" | "flat" = "flat";
      let note: string | undefined;
      let available = true;
      if (!Number.isFinite(close) || !Number.isFinite(fast) || !Number.isFinite(slow)) {
        available = false;
        note = "warming up";
      } else if (close > fast && fast > slow) {
        dir = "bull";
        note = Number.isFinite(longEma)
          ? close > longEma ? "above ema200" : "below ema200"
          : "ema200 warm-up; close vs ema20 structure";
      } else if (close < fast && fast < slow) {
        dir = "bear";
        note = Number.isFinite(longEma)
          ? close > longEma ? "above ema200" : "below ema200"
          : "ema200 warm-up; close vs ema20 structure";
      }
      legs.push({ leg: "emaStructure", dir, weight: weights.emaStructure, note, available });
    }

    // 3. Session VWAP of the futures bars. The vote compares the FUTURE's own
    // close against its own VWAP, never the spot close: the future trades at
    // spot plus carry, so a spot-vs-futures-VWAP comparison would carry a
    // permanent basis tilt instead of a bias read.
    {
      let dir: "bull" | "bear" | "flat" = "flat";
      let note: string | undefined;
      let available = true;
      if (futuresIndex === undefined) {
        available = false;
        note = "futures unavailable";
      } else {
        const vwap = futuresVwap[futuresIndex];
        const futuresClose = Number(futures[futuresIndex]?.close);
        if (!Number.isFinite(vwap)) {
          available = false;
          note = "vwap warming up";
        } else if (Number.isFinite(futuresClose) && futuresClose > vwap) {
          dir = "bull";
          note = `fut close > vwap ${vwap.toFixed(1)}`;
        } else if (Number.isFinite(futuresClose) && futuresClose < vwap) {
          dir = "bear";
          note = `fut close < vwap ${vwap.toFixed(1)}`;
        }
      }
      legs.push({ leg: "vwap", dir, weight: weights.vwap, note, available });
    }

    // 4. Volume conviction: the futures bar's volume clears x-times its
    // 20-bar MA and the vote follows the bar's own direction.
    {
      let dir: "bull" | "bear" | "flat" = "flat";
      let note: string | undefined;
      let available = true;
      if (futuresIndex === undefined) {
        available = false;
        note = "futures unavailable";
      } else {
        const volume = Number(futures[futuresIndex]?.volume);
        const volumeMA = futuresVolumeMA[futuresIndex];
        if (!Number.isFinite(volume) || !Number.isFinite(volumeMA) || volumeMA <= 0) {
          available = false;
          note = "volume unavailable";
        } else if (volume >= settings.volumeConvictionX * volumeMA) {
          dir = barDirection(futures[futuresIndex]);
        }
      }
      legs.push({ leg: "volumeConviction", dir, weight: weights.volumeConviction, note, available });
    }

    // 5. Futures price x OI quadrant. Short covering votes with the leg at
    // half weight; the denominator still counts the leg at full weight.
    {
      let dir: "bull" | "bear" | "flat" = "flat";
      let note: string | undefined;
      let available = true;
      let weight = weights.futuresOI;
      if (futuresIndex === undefined) {
        available = false;
        note = "futures unavailable";
      } else {
        const delta = futuresOiDelta[futuresIndex];
        if (!Number.isFinite(delta)) {
          available = false;
          note = "OI unavailable";
        } else {
          const price = barDirection(futures[futuresIndex]);
          if (price === "bull" && delta > 0) {
            dir = "bull";
            note = "long buildup";
          } else if (price === "bear" && delta < 0) {
            dir = "bear";
            note = "long unwinding";
          } else if (price === "bull" && delta < 0) {
            dir = "bull";
            weight = weights.futuresOI / 2;
            note = "short covering";
          } else if (price === "bear" && delta > 0) {
            dir = "bear";
            note = "short buildup";
          }
        }
      }
      legs.push({ leg: "futuresOI", dir, weight, note, available });
    }

    // 6. RSI(14): > 55 bull, < 45 bear.
    {
      const value = rsiValues[i];
      let dir: "bull" | "bear" | "flat" = "flat";
      let note: string | undefined;
      let available = true;
      if (!Number.isFinite(value)) {
        available = false;
        note = "warming up";
      } else {
        dir = value > 55 ? "bull" : value < 45 ? "bear" : "flat";
        note = `rsi ${value.toFixed(1)}`;
      }
      legs.push({ leg: "rsi", dir, weight: weights.rsi, note, available });
    }

    // 7. Fresh candlestick / liquidity patterns. A pattern confirms at its
    // own bar's close, so the detectors run on the prefix ending at bar i
    // and only the most recent pattern counts, provided it is at most two
    // bars old. A neutral freshest pattern (doji, spinning top) returns the
    // leg to flat; ties at the same time resolve to the later scan order.
    //
    // NOTE: detectGeometricPatterns / detectHarmonicPatterns are deliberately
    // NOT used for voting in v1 — extractPivots only confirms a pivot
    // windowSize bars later, so a shape detected over a prefix can silently
    // depend on future bars (lookahead). They stay display-only until
    // pivots can be confirmed causally.
    {
      let dir: "bull" | "bear" | "flat" = "flat";
      let note: string | undefined;
      let available = true;
      if (i < 2) {
        available = false;
        note = "warming up";
      } else {
        const prefix = spot.slice(0, i + 1);
        const found: PatternLike[] = [
          ...detectCandlestickPatterns(prefix),
          ...detectLiquiditySetups(prefix, extractPivots(prefix)),
        ];
        let latest: PatternLike | undefined;
        for (const pattern of found) {
          if (!latest || Number(pattern?.time) >= Number(latest.time)) latest = pattern;
        }
        const freshFrom = Number(spot[Math.max(0, i - 2)]?.time);
        if (
          latest &&
          Number.isFinite(Number(latest.time)) &&
          Number(latest.time) >= freshFrom &&
          (latest.bias === "bullish" || latest.bias === "bearish")
        ) {
          dir = latest.bias === "bullish" ? "bull" : "bear";
          note = String(latest.name ?? latest.bias);
        }
      }
      legs.push({ leg: "patterns", dir, weight: weights.patterns, note, available });
    }

    // 8. Premium ΔOI: rising CE premium with rising CE OI is a call buildup
    // (bull); the mirror on PE is a bear vote; both at once cancel out.
    {
      let dir: "bull" | "bear" | "flat" = "flat";
      let note: string | undefined;
      let available = true;
      if (ceIndex === undefined && peIndex === undefined) {
        available = false;
        note = "premium unavailable";
      } else {
        const callBuildup = ceIndex !== undefined &&
          ceIndex >= 1 &&
          ceOiDelta[ceIndex] > 0 &&
          Number(cePremium[ceIndex]?.close) > Number(cePremium[ceIndex - 1]?.close);
        const putBuildup = peIndex !== undefined &&
          peIndex >= 1 &&
          peOiDelta[peIndex] > 0 &&
          Number(pePremium[peIndex]?.close) > Number(pePremium[peIndex - 1]?.close);
        if (callBuildup && putBuildup) {
          note = "conflicting premium buildup";
        } else if (callBuildup) {
          dir = "bull";
          note = "call buildup";
        } else if (putBuildup) {
          dir = "bear";
          note = "put buildup";
        }
      }
      legs.push({ leg: "premiumDOI", dir, weight: weights.premiumDOI, note, available });
    }

    // 9. Contrarian PCR tilt from the freshest chain snapshot. This leg is a
    // light tilt only; the wall veto below is what actually blocks trades.
    {
      let dir: "bull" | "bear" | "flat" = "flat";
      let note: string | undefined;
      let available = true;
      if (!snapshot) {
        available = false;
        note = "chain unavailable";
      } else if (!snapshotFresh) {
        available = false;
        note = "chain stale";
      } else {
        const pcr = Number(snapshot.pcr);
        if (!Number.isFinite(pcr)) {
          available = false;
          note = "pcr unavailable";
        } else if (pcr >= settings.pcrBullAbove) {
          dir = "bull";
          note = `contrarian bull, pcr ${pcr.toFixed(2)}`;
        } else if (pcr <= settings.pcrBearBelow) {
          dir = "bear";
          note = `contrarian bear, pcr ${pcr.toFixed(2)}`;
        } else {
          note = `pcr ${pcr.toFixed(2)}`;
        }
      }
      legs.push({ leg: "chainContext", dir, weight: weights.chainContext, note, available });
    }

    // Fold the legs into the score. Missing legs are excluded from the
    // denominator; genuinely neutral legs stay in it.
    const votes: SignalVote[] = legs.map((leg) => {
      const vote: SignalVote = { leg: leg.leg, dir: leg.dir, weight: leg.weight };
      if (leg.note !== undefined) vote.note = leg.note;
      return vote;
    });

    let net = 0;
    let availableWeight = 0;
    for (const leg of legs) {
      if (leg.available) availableWeight += weights[leg.leg];
      if (leg.dir === "bull") net += leg.weight;
      else if (leg.dir === "bear") net -= leg.weight;
    }
    const score = availableWeight > 0 ? Math.round((Math.abs(net) / availableWeight) * 100) : 0;
    const direction: "bull" | "bear" | "flat" = net > 0 ? "bull" : net < 0 ? "bear" : "flat";

    const atrHere = atrAtBar(spot, atrValues, i);
    const secondsOfDay = istSecondsOfDay(barTime);

    // Vetoes, in precedence order: session windows, cooldown, near OI walls.
    let blockedBy: string | undefined;
    if (secondsOfDay < sessionOpenSecond + settings.skipFirstBars * barSeconds) {
      blockedBy = "session start";
    } else if (secondsOfDay >= sessionCloseSecond - settings.noEntryLastBars * barSeconds) {
      blockedBy = "session end";
    } else if (i <= cooldownUntil) {
      blockedBy = "cooldown";
    } else if (direction === "bull" && snapshotFresh && Number.isFinite(atrHere)) {
      const wall = Number(snapshot.callOIWall);
      if (Number.isFinite(wall) && Number.isFinite(close) && close < wall &&
        wall - close < settings.wallVetoAtrX * atrHere) {
        blockedBy = "CE wall overhead";
      }
    } else if (direction === "bear" && snapshotFresh && Number.isFinite(atrHere)) {
      const wall = Number(snapshot.putOIWall);
      if (Number.isFinite(wall) && Number.isFinite(close) && close > wall &&
        close - wall < settings.wallVetoAtrX * atrHere) {
        blockedBy = "PE wall below";
      }
    }

    let action: SignalAction = "WAIT";
    if (!blockedBy && direction !== "flat") {
      if (score >= settings.scoreThreshold) {
        action = direction === "bull" ? "BUY_CE" : "BUY_PE";
      } else if (score >= settings.scoreThreshold - settings.armBand) {
        action = direction === "bull" ? "ARMED_CE" : "ARMED_PE";
      }
    }

    const result: SignalResult = { time: barTime, action, score, direction, votes };
    if (blockedBy !== undefined) result.blockedBy = blockedBy;

    if (action === "BUY_CE" || action === "BUY_PE") {
      const bull = action === "BUY_CE";
      cooldownUntil = i + Math.max(0, Math.round(settings.cooldownBars));

      if (Number.isFinite(atrHere) && Number.isFinite(close)) {
        const stopDistance = settings.atrStopMultiplier * atrHere;
        result.stop = bull ? close - stopDistance : close + stopDistance;
        // The veto above blocks NEAR walls; the target takes the wall in the
        // trade direction when it clears the minimum run, else the ATR fallback.
        const wall = snapshotFresh
          ? Number(bull ? snapshot?.callOIWall : snapshot?.putOIWall)
          : NaN;
        result.target = targetFor(close, atrHere, wall, bull, settings);
      }

      const strikeHint = strikeHintFor(bull, close, snapshotFresh ? snapshot : undefined);
      if (strikeHint !== undefined) result.strikeHint = strikeHint;
    }

    results.push(result);
  }

  return results;
}
