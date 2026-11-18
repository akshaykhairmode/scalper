/**
 * Backtest harness for the per-bar signal engine.
 *
 * The engine is deterministic and no-lookahead: the same bars always produce
 * the same per-bar results. `runBacktest` therefore runs the engine exactly
 * ONCE over the full history and then replays each confirmed BUY_CE / BUY_PE
 * signal forward. The simulator is no-lookahead as well: a signal's outcome is
 * resolved only from bars strictly AFTER the signal bar — the signal bar's own
 * premium close is the entry price, and nothing past the exit bar is read.
 *
 * Stop/target are checked on the spot scale (that is what the engine's levels
 * mean); P&L is computed on the ATM premium series of the bought side,
 * mirroring how the live terminal would manage the position.
 *
 * Report terminology:
 * - "decided" trade — price levels resolved it: exitReason "stop" or "target".
 * - "settled" trade — it has a realized pnlPerLot: decided trades plus
 *   "timeout" closes.
 * - "open" and "no-premium" trades never enter the P&L statistics.
 *
 * Pure functions only: bars in, report out. No DOM, no network, no clock.
 */

// ---------------------------------------------------------------------------
// Structural contract of the signal engine.
//
// These mirror the shapes the real engine (`signal.ts`, built separately)
// produces. They are declared locally on purpose so this module compiles and
// tests in isolation; integration injects the concrete engine as a
// `SignalEngineFn` and nothing here imports from `signal.ts`.
// ---------------------------------------------------------------------------

export type SignalAction = "BUY_CE" | "BUY_PE" | "ARMED_CE" | "ARMED_PE" | "WAIT";

export interface SignalVoteLike {
  leg: string;
  dir: "bull" | "bear" | "flat";
  weight: number;
  note?: string;
}

export interface SignalResultLike {
  /** Bar time in seconds. */
  time: number;
  action: SignalAction;
  score: number;
  direction: "bull" | "bear" | "flat";
  votes: SignalVoteLike[];
  /** Stop level on the spot scale. */
  stop?: number;
  /** Target level on the spot scale. */
  target?: number;
  strikeHint?: { type: "CE" | "PE"; strike: number; reason: string };
  blockedBy?: string;
}

export interface IndicatorCandleLike {
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume?: number;
  oi?: number;
}

// ---------------------------------------------------------------------------
// Public backtest API
// ---------------------------------------------------------------------------

export interface BacktestInput {
  /** Closed 5-minute spot bars, ascending by time. */
  spot: IndicatorCandleLike[];
  /** Futures bars on the same grid; consumed by the engine, not the simulator. */
  futures?: IndicatorCandleLike[];
  /** ATM premium bars for the session, same timescale as `spot`. */
  premium?: { ce?: IndicatorCandleLike[]; pe?: IndicatorCandleLike[] };
  /**
   * Chain snapshots, ascending by time. Declared loosely on purpose: the
   * simulator never reads them, but the injected engine receives the whole
   * input object and consumes the chain context per bar.
   */
  chain?: unknown[];
  /** Passed through to the engine untouched. */
  settings?: unknown;
}

/** The engine, injected by the caller; `runBacktest` invokes it exactly once. */
export type SignalEngineFn = (input: BacktestInput) => SignalResultLike[];

export interface BacktestOptions {
  /** Options lot size. Default 75. */
  lotSize?: number;
  /** Hold window in bars. Default 12 (1 hour on the 5m grid). */
  maxHoldBars?: number;
  /** Reserved for tick-level exit pessimism; unused in v1. */
  stopTickBps?: number;
}

export interface BacktestSignalRow {
  time: number;
  action: "BUY_CE" | "BUY_PE";
  score: number;
  votes: SignalVoteLike[];
  /** Spot-scale levels the outcome was resolved against. */
  stop: number;
  target: number;
  strikeHint?: { type: "CE" | "PE"; strike: number; reason: string };
  /** Premium close at the signal bar; null when premium data is missing. */
  entryPremium: number | null;
  exitTime: number | null;
  exitPremium: number | null;
  exitReason: "stop" | "target" | "timeout" | "open" | "no-premium";
  /** (exit − entry) × lotSize for the bought option; null when unmeasurable. */
  pnlPerLot: number | null;
  /** Best premium close minus entry over the hold window (signed). */
  mfePremium: number | null;
  /** Worst premium close minus entry over the hold window (signed). */
  maePremium: number | null;
}

export interface LegAttribution {
  leg: string;
  /** Directional vote counts across ALL engine results (WAIT/ARMED included). */
  bullVotes: number;
  bearVotes: number;
  flatVotes: number;
  /** Confirmed signals where this leg voted bull or bear. */
  signalsWithLeg: number;
  /** Of those, target-hit outcomes. */
  winsWithLeg: number;
}

export interface BacktestReport {
  signals: BacktestSignalRow[];
  totalSignals: number;
  buyCE: number;
  buyPE: number;
  targetHits: number;
  stopHits: number;
  timeouts: number;
  openTrades: number;
  noPremiumTrades: number;
  /** targetHits / decided (target + stop); null when nothing was decided. */
  hitRate: number | null;
  /** Mean pnlPerLot over settled trades (stop/target/timeout); null when none. */
  avgPnlPerLot: number | null;
  /** Mean pnlPerLot over decided trades (stop/target); null when none. */
  expectancyPerSignal: number | null;
  /** Sum of realized pnlPerLot over settled trades; null when none. */
  cumulativePnl: number | null;
  /**
   * Minimum of the running cumulative P&L over settled trades, seeded at 0
   * before the first trade — 0 when the curve never dips below its start.
   * Null when no trade settled.
   */
  maxDrawdown: number | null;
  /** Sorted by directional vote volume (bull + bear), then by leg name. */
  perLeg: LegAttribution[];
}

/** Default lot size (NIFTY options). */
const DEFAULT_LOT_SIZE = 75;
/** Default hold window: 12 bars × 5m = 1 hour. */
const DEFAULT_MAX_HOLD_BARS = 12;
/** The 5-minute grid. */
const DEFAULT_BAR_SECONDS = 300;
/** 2026-01-05 09:15 IST as epoch seconds — the synthetic session open. */
const IST_SESSION_OPEN = 1767584700;

function finiteOr(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

/** A bar the simulator can trade against: finite levels on a finite time. */
function isSimulableBar(bar: IndicatorCandleLike): boolean {
  return Boolean(
    bar
    && Number.isFinite(bar.time)
    && Number.isFinite(bar.high)
    && Number.isFinite(bar.low)
    && Number.isFinite(bar.close),
  );
}

/** Bars the simulator can use, ascending by time (input never mutated). */
function sortedBars(bars?: IndicatorCandleLike[]): IndicatorCandleLike[] {
  const list = (Array.isArray(bars) ? bars : []).filter(isSimulableBar);
  return list.slice().sort((a, b) => a.time - b.time);
}

/** time -> close for a premium series; only the close is ever consumed. */
function premiumCloseMap(bars?: IndicatorCandleLike[]): Map<number, number> {
  const map = new Map<number, number>();
  for (const bar of Array.isArray(bars) ? bars : []) {
    if (bar && Number.isFinite(bar.time) && Number.isFinite(bar.close)) map.set(bar.time, bar.close);
  }
  return map;
}

/** Smallest positive gap between consecutive spot bars (fallback: 5m). */
function inferBarSeconds(spot: IndicatorCandleLike[]): number {
  let barSeconds = Infinity;
  for (let i = 1; i < spot.length; i++) {
    const delta = spot[i].time - spot[i - 1].time;
    if (delta > 0 && delta < barSeconds) barSeconds = delta;
  }
  return Number.isFinite(barSeconds) ? barSeconds : DEFAULT_BAR_SECONDS;
}

/**
 * Premium close for a bar time, tolerating a one-bar misalignment of the
 * premium grid. Preference order: the exact bar, then the PREVIOUS bar (the
 * stale price is known at decision time — never a lookahead). The next bar is
 * deliberately NOT consulted: pricing a decision off a bar that forms after
 * it would leak the future into the entry. Null when nothing precedes the bar
 * within one step.
 */
function premiumCloseAt(premium: Map<number, number>, time: number, barSeconds: number): number | null {
  for (const key of [time, time - barSeconds]) {
    const close = premium.get(key);
    if (close !== undefined) return close;
  }
  return null;
}

/**
 * Replay a deterministic signal engine over a closed bar history and simulate
 * every confirmed BUY_CE / BUY_PE signal on the premium scale.
 *
 * The engine is invoked exactly once with the full `input` (settings included,
 * untouched). ARMED/WAIT results are advisory and skipped. A signal whose bar
 * is missing from the spot series, or whose BUY lacks usable stop/target
 * levels, cannot be replayed and is dropped. A signal landing on a bar
 * occupied by an open simulated position (the exit bar itself counts as
 * occupied) is likewise dropped — see the loop for the exact rule.
 */
export function runBacktest(
  input: BacktestInput,
  engine: SignalEngineFn,
  options: BacktestOptions = {},
): BacktestReport {
  const spot = sortedBars(input && input.spot);
  const rawLot = options ? options.lotSize : undefined;
  const lotSize = typeof rawLot === "number" && Number.isFinite(rawLot) && rawLot > 0 ? rawLot : DEFAULT_LOT_SIZE;
  const rawHold = options ? options.maxHoldBars : undefined;
  const maxHoldBars =
    typeof rawHold === "number" && Number.isFinite(rawHold) && rawHold > 0 ? Math.floor(rawHold) : DEFAULT_MAX_HOLD_BARS;

  const barSeconds = inferBarSeconds(spot);
  const spotIndexByTime = new Map<number, number>();
  for (let i = 0; i < spot.length; i++) spotIndexByTime.set(spot[i].time, i);

  const cePremium = premiumCloseMap(input && input.premium && input.premium.ce);
  const pePremium = premiumCloseMap(input && input.premium && input.premium.pe);
  // The engine runs exactly once over the full history; everything below is
  // pure replay over its per-bar results.
  const rawResults = typeof engine === "function" ? engine(input) : [];
  const ordered = (Array.isArray(rawResults) ? rawResults : [])
    .filter((result) => result && Number.isFinite(result.time))
    .slice()
    .sort((a, b) => a.time - b.time);

  const legs = new Map<string, LegAttribution>();
  const legOf = (name: string): LegAttribution => {
    let tally = legs.get(name);
    if (!tally) {
      tally = { leg: name, bullVotes: 0, bearVotes: 0, flatVotes: 0, signalsWithLeg: 0, winsWithLeg: 0 };
      legs.set(name, tally);
    }
    return tally;
  };

  // Directional vote counts across ALL engine results — WAIT and ARMED bars
  // included, not just confirmed signals.
  for (const result of ordered) {
    for (const vote of Array.isArray(result.votes) ? result.votes : []) {
      if (!vote || typeof vote.leg !== "string") continue;
      const tally = legOf(vote.leg);
      if (vote.dir === "bull") tally.bullVotes++;
      else if (vote.dir === "bear") tally.bearVotes++;
      else tally.flatVotes++;
    }
  }

  // Joins one confirmed signal's directional votes with its outcome. A leg
  // counts once per signal even if it somehow cast two votes, and a flat vote
  // never marks participation.
  const countSignalLegs = (result: SignalResultLike, won: boolean): void => {
    const seen = new Set<string>();
    for (const vote of Array.isArray(result.votes) ? result.votes : []) {
      if (!vote || typeof vote.leg !== "string") continue;
      if (vote.dir !== "bull" && vote.dir !== "bear") continue;
      if (seen.has(vote.leg)) continue;
      seen.add(vote.leg);
      const tally = legOf(vote.leg);
      tally.signalsWithLeg++;
      if (won) tally.winsWithLeg++;
    }
  };

  const signals: BacktestSignalRow[] = [];
  let buyCE = 0;
  let buyPE = 0;
  let targetHits = 0;
  let stopHits = 0;
  let timeouts = 0;
  let openTrades = 0;
  let noPremiumTrades = 0;
  // First spot index at which a new simulated position may start. The exit
  // bar itself counts as occupied by the closing position (never optimistic).
  let nextFreeIndex = 0;

  for (const result of ordered) {
    if (result.action !== "BUY_CE" && result.action !== "BUY_PE") continue; // ARMED/WAIT are advisory only
    const entryIndex = spotIndexByTime.get(result.time);
    if (entryIndex === undefined) continue; // signal bar missing from the spot series: not replayable
    // While a simulated position is open no new simulated position may start.
    // Signals landing on an occupied bar are simply EXCLUDED from `signals`
    // and counted nowhere (not in totals, not in attribution) — the engine's
    // own cooldown is the primary density control, this only keeps the
    // replay honest.
    if (entryIndex < nextFreeIndex) continue;
    const stop = Number(result.stop);
    const target = Number(result.target);
    // A BUY without usable levels cannot be represented in a signal row
    // (stop/target are non-optional there), so it is dropped the same way.
    if (!Number.isFinite(stop) || !Number.isFinite(target)) continue;

    const isCE = result.action === "BUY_CE";
    if (isCE) buyCE++;
    else buyPE++;

    const score = Number.isFinite(result.score) ? result.score : 0;
    const votes = Array.isArray(result.votes) ? result.votes.slice() : [];
    const premium = isCE ? cePremium : pePremium;
    const entryPremium = premiumCloseAt(premium, result.time, barSeconds);

    if (entryPremium === null) {
      // No tradable entry price: still a signal for the report and for
      // attribution, but never a trade — excluded from hitRate, expectancy
      // and every cumulative figure.
      noPremiumTrades++;
      countSignalLegs(result, false);
      signals.push({
        time: result.time,
        action: result.action,
        score,
        votes,
        stop,
        target,
        entryPremium: null,
        exitTime: null,
        exitPremium: null,
        exitReason: "no-premium",
        pnlPerLot: null,
        mfePremium: null,
        maePremium: null,
        ...(result.strikeHint ? { strikeHint: result.strikeHint } : {}),
      });
      continue; // no position was opened, so nothing becomes occupied
    }

    // Forward walk (no lookahead: strictly after the signal bar). The first
    // bar where either level holds decides the exit; when both hold inside
    // one bar the STOP wins (pessimistic).
    const lastWalkIndex = Math.min(entryIndex + maxHoldBars, spot.length - 1);
    let exitIndex = -1;
    let exitReason: BacktestSignalRow["exitReason"] = "open";
    for (let i = entryIndex + 1; i <= lastWalkIndex; i++) {
      const bar = spot[i];
      const stopHit = isCE ? bar.low <= stop : bar.high >= stop;
      if (stopHit) {
        exitIndex = i;
        exitReason = "stop";
        break;
      }
      const targetHit = isCE ? bar.high >= target : bar.low <= target;
      if (targetHit) {
        exitIndex = i;
        exitReason = "target";
        break;
      }
    }
    if (exitIndex < 0 && entryIndex + maxHoldBars <= spot.length - 1) {
      // Nothing hit within the window: leave at the close of the
      // maxHoldBars-th bar after entry.
      exitIndex = entryIndex + maxHoldBars;
      exitReason = "timeout";
    }

    // Premium excursions over the hold window: bars strictly after entry up
    // to the exit bar (or the series end while still open).
    const windowEndIndex = exitIndex >= 0 ? exitIndex : spot.length - 1;
    let mfePremium: number | null = null;
    let maePremium: number | null = null;
    for (let i = entryIndex + 1; i <= windowEndIndex; i++) {
      const close = premiumCloseAt(premium, spot[i].time, barSeconds);
      if (close === null) continue;
      const delta = close - entryPremium;
      if (mfePremium === null || delta > mfePremium) mfePremium = delta;
      if (maePremium === null || delta < maePremium) maePremium = delta;
    }

    const exitTime = exitIndex >= 0 ? spot[exitIndex].time : null;
    const exitPremium = exitTime !== null ? premiumCloseAt(premium, exitTime, barSeconds) : null;
    const pnlPerLot = exitPremium !== null ? (exitPremium - entryPremium) * lotSize : null;

    if (exitReason === "target") targetHits++;
    else if (exitReason === "stop") stopHits++;
    else if (exitReason === "timeout") timeouts++;
    else openTrades++;

    if (exitIndex >= 0) nextFreeIndex = exitIndex + 1;
    else nextFreeIndex = spot.length; // still open at data end: everything after is occupied

    countSignalLegs(result, exitReason === "target");
    signals.push({
      time: result.time,
      action: result.action,
      score,
      votes,
      stop,
      target,
      entryPremium,
      exitTime,
      exitPremium,
      exitReason,
      pnlPerLot,
      mfePremium,
      maePremium,
      ...(result.strikeHint ? { strikeHint: result.strikeHint } : {}),
    });
  }

  const decidedPnls: number[] = [];
  const settledPnls: number[] = [];
  for (const row of signals) {
    if (row.pnlPerLot === null) continue;
    settledPnls.push(row.pnlPerLot);
    if (row.exitReason === "stop" || row.exitReason === "target") decidedPnls.push(row.pnlPerLot);
  }

  const decidedCount = targetHits + stopHits;
  const sum = (values: number[]): number => values.reduce((acc, value) => acc + value, 0);

  let maxDrawdown: number | null = null;
  if (settledPnls.length) {
    let running = 0;
    maxDrawdown = 0;
    for (const pnl of settledPnls) {
      running += pnl;
      if (running < maxDrawdown) maxDrawdown = running;
    }
  }

  const perLeg = Array.from(legs.values()).sort(
    (a, b) => (b.bullVotes + b.bearVotes) - (a.bullVotes + a.bearVotes) || a.leg.localeCompare(b.leg),
  );

  return {
    signals,
    totalSignals: signals.length,
    buyCE,
    buyPE,
    targetHits,
    stopHits,
    timeouts,
    openTrades,
    noPremiumTrades,
    hitRate: decidedCount > 0 ? targetHits / decidedCount : null,
    avgPnlPerLot: settledPnls.length ? sum(settledPnls) / settledPnls.length : null,
    expectancyPerSignal: decidedPnls.length ? sum(decidedPnls) / decidedPnls.length : null,
    cumulativePnl: settledPnls.length ? sum(settledPnls) : null,
    maxDrawdown,
    perLeg,
  };
}

// ---------------------------------------------------------------------------
// Synthetic fixtures — usable by tests here and by later integration tests.
// ---------------------------------------------------------------------------

export interface FixtureOptions {
  /** Starting close (and range center) on the spot scale. Default 25000. */
  start?: number;
  /** Epoch seconds of the first bar. Default 2026-01-05 09:15 IST. */
  startTime?: number;
  /** Bar spacing in seconds. Default 300 (the 5-minute grid). */
  barSeconds?: number;
  /** Wicks added above and below each bar body. Default 2. */
  volatility?: number;
  /** Constant futures offset from the spot close. Default 25. */
  basis?: number;
}

/** Synthetic spot + futures bars on one shared time grid. */
export interface FixtureBars {
  spot: IndicatorCandleLike[];
  futures: IndicatorCandleLike[];
}

/**
 * A steady drift of `step` points per bar on a 5m IST grid opening 09:15.
 * Futures track the spot at a constant basis with volume and open interest
 * rising every bar (participation builds with the trend).
 */
export function buildTrendFixture(count: number, step: number, opts: FixtureOptions = {}): FixtureBars {
  const start = finiteOr(opts.start, 25000);
  const startTime = finiteOr(opts.startTime, IST_SESSION_OPEN);
  const barSeconds = finiteOr(opts.barSeconds, DEFAULT_BAR_SECONDS);
  const volatility = finiteOr(opts.volatility, 2);
  const basis = finiteOr(opts.basis, 25);
  const n = Math.max(0, Math.floor(count) || 0);

  const spot: IndicatorCandleLike[] = [];
  const futures: IndicatorCandleLike[] = [];
  for (let i = 0; i < n; i++) {
    const time = startTime + barSeconds * i;
    const open = i === 0 ? start : start + step * (i - 1);
    const close = start + step * i;
    spot.push({
      time,
      open,
      high: Math.max(open, close) + volatility,
      low: Math.min(open, close) - volatility,
      close,
    });
    const fOpen = open + basis;
    const fClose = close + basis;
    futures.push({
      time,
      open: fOpen,
      high: Math.max(fOpen, fClose) + volatility,
      low: Math.min(fOpen, fClose) - volatility,
      close: fClose,
      volume: 1000 + 100 * i,
      oi: 50000 + 500 * i,
    });
  }
  return { spot, futures };
}

/**
 * A flat range of `spread` points around `start`: every bar spans exactly
 * [center − spread, center + spread] with alternating bodies. Futures track
 * the spot at a constant basis with flat participation (a range neither
 * builds nor unwinds positioning).
 */
export function buildRangeFixture(count: number, spread: number, opts: FixtureOptions = {}): FixtureBars {
  const center = finiteOr(opts.start, 25000);
  const startTime = finiteOr(opts.startTime, IST_SESSION_OPEN);
  const barSeconds = finiteOr(opts.barSeconds, DEFAULT_BAR_SECONDS);
  const basis = finiteOr(opts.basis, 25);
  const half = spread / 2;
  const n = Math.max(0, Math.floor(count) || 0);

  const spot: IndicatorCandleLike[] = [];
  const futures: IndicatorCandleLike[] = [];
  for (let i = 0; i < n; i++) {
    const time = startTime + barSeconds * i;
    const up = i % 2 === 0;
    const open = up ? center - half : center + half;
    const close = up ? center + half : center - half;
    spot.push({ time, open, high: center + spread, low: center - spread, close });
    futures.push({
      time,
      open: open + basis,
      high: center + spread + basis,
      low: center - spread + basis,
      close: close + basis,
      volume: 1000,
      oi: 50000,
    });
  }
  return { spot, futures };
}

// ---------------------------------------------------------------------------
// Reporting
// ---------------------------------------------------------------------------

/**
 * Compact human-readable summary of a backtest report: signal mix, outcome
 * counts, hit rate, expectancy and the top legs by winning contribution.
 * Intended for the UI's backtest panel and quick console sanity checks.
 */
export function summarizeReport(report: BacktestReport): string {
  if (!report) return "Backtest: no report";
  const signed = (value: number | null): string =>
    value === null || !Number.isFinite(value) ? "n/a" : (value >= 0 ? "+" : "") + value.toFixed(1);
  const pct = (value: number | null): string =>
    value === null || !Number.isFinite(value) ? "n/a" : (value * 100).toFixed(1) + "%";

  const lines = [
    `Backtest: ${report.totalSignals} signals (${report.buyCE} CE / ${report.buyPE} PE)`,
    `Outcomes: ${report.targetHits} target, ${report.stopHits} stop, ${report.timeouts} timeout, `
      + `${report.openTrades} open, ${report.noPremiumTrades} no-premium`,
    `Hit rate: ${pct(report.hitRate)} | Expectancy/signal: ${signed(report.expectancyPerSignal)} `
      + `| Avg P&L/lot: ${signed(report.avgPnlPerLot)}`,
    `Cumulative P&L/lot: ${signed(report.cumulativePnl)} | Max drawdown: ${signed(report.maxDrawdown)}`,
  ];

  const top = (Array.isArray(report.perLeg) ? report.perLeg : [])
    .filter((leg) => leg && (leg.signalsWithLeg > 0 || leg.winsWithLeg > 0))
    .sort(
      (a, b) =>
        b.winsWithLeg - a.winsWithLeg
        || b.signalsWithLeg - a.signalsWithLeg
        || a.leg.localeCompare(b.leg),
    )
    .slice(0, 5);
  if (top.length) {
    lines.push(`Top legs: ${top.map((leg) => `${leg.leg} ${leg.winsWithLeg}W/${leg.signalsWithLeg}`).join(", ")}`);
  }
  return lines.join("\n");
}
