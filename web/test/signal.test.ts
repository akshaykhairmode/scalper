import { test } from "node:test";
import assert from "node:assert/strict";
import { atrSeries } from "../src/chart/indicators.ts";
import {
  defaultSignalSettings,
  evaluateSignals,
  type SignalInput,
  type SignalResult,
} from "../src/chart/signal.ts";

/**
 * Fixtures are one or two full IST sessions of 5m bars (09:15..15:25, 75
 * bars). Trend bars use a fixed 4/7 body ratio with uniform colour, which is
 * strong enough that every momentum leg agrees yet too small to trigger any
 * candlestick pattern — so the pattern leg stays flat and the scores below
 * are hand-verifiable.
 */

const BAR_SECONDS = 300;
const BARS_PER_DAY = 75;
const DAY_SECONDS = 86_400;
/** 2026-11-16T09:15:00 IST in epoch seconds (a Monday). */
const T0 = Date.parse("2026-11-16T03:45:00Z") / 1000;

const barTime = (day: number, barOfDay: number) => T0 + day * DAY_SECONDS + barOfDay * BAR_SECONDS;

type Bar = { time: number; open: number; high: number; low: number; close: number };
type FuturesBar = Bar & { volume: number; oi: number };
type ChainBar = {
  time: number;
  spot: number;
  pcr: number;
  maxPain: number;
  callOIWall: number;
  putOIWall: number;
  atmStrike: number;
};

interface TrendDayOptions {
  startClose?: number;
  /** Futures OI change per bar; default +50 (short covering test uses -50). */
  oiStep?: number;
  /** PE premium also builds up, so the premium leg sees both sides. */
  bothPremiumRising?: boolean;
  /** Chain wall hugs each bar's close, putting it inside the veto distance. */
  nearWall?: boolean;
}

function trendDay(day: number, falling: boolean, options: TrendDayOptions = {}) {
  const { startClose = 25000, oiStep = 50, bothPremiumRising = false, nearWall = false } = options;
  const spot: Bar[] = [];
  const futures: FuturesBar[] = [];
  const ce: FuturesBar[] = [];
  const pe: FuturesBar[] = [];
  const chain: ChainBar[] = [];

  for (let k = 0; k < BARS_PER_DAY; k++) {
    const time = barTime(day, k);
    const close = startClose + (falling ? -8 : 8) * k;
    const bar: Bar = falling
      ? { time, open: close + 4, high: close + 5, low: close - 2, close }
      : { time, open: close - 4, high: close + 2, low: close - 5, close };
    spot.push(bar);
    // Volume steps up at bar 60 so the conviction leg only fires late in the day.
    futures.push({ ...bar, volume: k < 60 ? 100 : 1000, oi: 100000 + oiStep * k });

    // CE premium gains with the market, PE drains; both carry building OI so
    // the premium leg mirrors the trend.
    const ceClose = falling ? 200 - k : 200 + 2 * k;
    ce.push({
      time,
      open: ceClose - 1,
      high: ceClose + 1,
      low: ceClose - 2,
      close: ceClose,
      oi: falling ? 5000 - 10 * k : 5000 + 25 * k,
    });

    const peRises = falling || bothPremiumRising;
    const peClose = peRises ? 300 + 2 * k : 300 - k;
    pe.push({
      time,
      open: peClose - 1,
      high: peClose + 1,
      low: peClose - 2,
      close: peClose,
      oi: peRises ? 3000 + 25 * k : 3000 - 10 * k,
    });

    const callOIWall = !falling && nearWall ? close + 1 : startClose + 1000;
    const putOIWall = falling && nearWall ? close - 1 : startClose - 1000;
    chain.push({
      time,
      spot: close,
      pcr: falling ? 0.6 : 1.3,
      maxPain: startClose + 100,
      callOIWall,
      putOIWall,
      atmStrike: falling ? startClose - 500 : startClose + 600,
    });
  }

  return { spot, futures, ce, pe, chain };
}

function trendInput(
  day: number,
  falling: boolean,
  options: TrendDayOptions & { includeFutures?: boolean; includePremium?: boolean; includeChain?: boolean } = {},
): SignalInput {
  const {
    includeFutures = true,
    includePremium = true,
    includeChain = true,
    ...dayOptions
  } = options;
  const parts = trendDay(day, falling, dayOptions);
  return {
    spot: parts.spot,
    futures: includeFutures ? parts.futures : undefined,
    premium: includePremium ? { ce: parts.ce, pe: parts.pe } : undefined,
    chain: includeChain ? parts.chain : undefined,
  };
}

function flatInput(day = 0): SignalInput {
  const spot: Bar[] = [];
  const futures: FuturesBar[] = [];
  const ce: FuturesBar[] = [];
  const pe: FuturesBar[] = [];
  const chain: ChainBar[] = [];
  for (let k = 0; k < BARS_PER_DAY; k++) {
    const time = barTime(day, k);
    const bar: Bar = { time, open: 25000, high: 25002, low: 24998, close: 25000 };
    spot.push(bar);
    futures.push({ ...bar, volume: 100, oi: 100000 });
    ce.push({ time, open: 200, high: 201, low: 199, close: 200, oi: 5000 });
    pe.push({ time, open: 300, high: 301, low: 299, close: 300, oi: 3000 });
    chain.push({
      time,
      spot: 25000,
      pcr: 1.0,
      maxPain: 25000,
      callOIWall: 26000,
      putOIWall: 24000,
      atmStrike: 25000,
    });
  }
  return { spot: spot as Bar[], futures, premium: { ce, pe }, chain };
}

function voteFor(result: SignalResult, leg: string) {
  const vote = result.votes.find((v) => v.leg === leg);
  assert.ok(vote, `missing vote for leg ${leg}`);
  return vote;
}

const isBuy = (result: SignalResult) => result.action === "BUY_CE" || result.action === "BUY_PE";

test("defaultSignalSettings matches the documented defaults", () => {
  const defaults = defaultSignalSettings();
  assert.deepEqual(defaults, {
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
    weights: {
      supertrend: 18,
      emaStructure: 14,
      vwap: 14,
      volumeConviction: 12,
      futuresOI: 10,
      rsi: 10,
      patterns: 8,
      premiumDOI: 8,
      chainContext: 6,
    },
  });
  const total = Object.values(defaults.weights).reduce((sum, weight) => sum + weight, 0);
  assert.equal(total, 100);
});

test("synthetic uptrend confirms BUY_CE with stop, target and strike hint", () => {
  const input = trendInput(0, false);
  const results = evaluateSignals(input);
  assert.equal(results.length, input.spot.length);
  for (let i = 0; i < results.length; i++) {
    assert.equal(results[i].time, input.spot[i].time);
    assert.equal(results[i].votes.length, 9);
  }

  const t = results.findIndex((r) => r.action === "BUY_CE");
  assert.equal(t, 3, "first BUY fires right after the session-start window");
  assert.equal(results[t].blockedBy, undefined);
  // ATR(14) is still warming at bar 3; the stop falls back to the prefix mean true range.
  assert.ok(Number.isFinite(results[t].stop) && results[t].stop < input.spot[t].close);
  assert.equal(results[t].target, 26000, "the CE OI wall above spot is the target");
  assert.deepEqual(results[t].strikeHint, {
    type: "CE",
    strike: 25600,
    reason: "ATM strike from the freshest chain snapshot",
  });

  // Mid-day, with Wilder ATR(14) live: the stop is exactly close - 1xATR.
  const i = 23;
  const close = input.spot[i].close;
  const atr = atrSeries(input.spot, 14)[i];
  assert.ok(Number.isFinite(atr));
  assert.equal(results[i].action, "BUY_CE");
  assert.equal(results[i].score, 80); // 80 of the 100 available weight voting bull
  assert.ok(Math.abs(results[i].stop - (close - atr)) < 1e-9);
  assert.equal(results[i].target, 26000);
});

test("synthetic downtrend confirms BUY_PE with a put-wall target", () => {
  const input = trendInput(0, true);
  const results = evaluateSignals(input);
  const t = results.findIndex((r) => r.action === "BUY_PE");
  assert.equal(t, 3);
  assert.equal(results[t].blockedBy, undefined);

  // Deep in the trend, with Wilder ATR(14) live: the stop is exactly close + 1xATR.
  // (SuperTrend flips bear at bar 13, so the downtrend BUY cadence is 3,7,13,17,21,25...)
  const i = 25;
  const close = input.spot[i].close;
  const atr = atrSeries(input.spot, 14)[i];
  assert.ok(Number.isFinite(atr));
  assert.equal(results[i].action, "BUY_PE");
  assert.equal(results[i].score, 80);
  assert.ok(Math.abs(results[i].stop - (close + atr)) < 1e-9);
  assert.equal(results[i].target, 24000, "the PE OI wall below spot is the target");
  assert.deepEqual(results[i].strikeHint, {
    type: "PE",
    strike: 24500,
    reason: "ATM strike from the freshest chain snapshot",
  });
});

test("flat range stays WAIT", () => {
  const results = evaluateSignals(flatInput());
  assert.equal(results.length, BARS_PER_DAY);
  assert.ok(results.every((r) => r.action === "WAIT"));
  // Only SuperTrend's seeded up-trend tilts the book; 18/100 is far below the arm band.
  assert.equal(results[30].score, 18);
  assert.equal(results[30].direction, "bull");
  assert.equal(results[30].blockedBy, undefined);
});

test("a confirmed BUY forces a cooldown of WAIT bars", () => {
  const results = evaluateSignals(trendInput(0, false));
  const buys = results.map((r, i) => (r.action === "BUY_CE" ? i : -1)).filter((i) => i >= 0);
  assert.ok(buys.length >= 5, `expected several BUYs, got ${buys.length}`);
  for (const t of buys) {
    for (let j = 1; j <= 3 && t + j < 69; j++) {
      assert.equal(results[t + j].action, "WAIT", `bar ${t + j} after BUY at ${t}`);
      assert.equal(results[t + j].blockedBy, "cooldown", `bar ${t + j} after BUY at ${t}`);
    }
    if (t + 4 < 69) {
      assert.equal(results[t + 4].action, "BUY_CE", `cooldown from BUY at ${t} must expire at ${t + 4}`);
    }
  }
});

test("CE wall overhead within 0.25 ATR vetoes bull entries", () => {
  const results = evaluateSignals(trendInput(0, false, { nearWall: true }));
  assert.ok(results.every((r) => r.action !== "BUY_CE" && r.action !== "ARMED_CE"));
  for (let i = 3; i <= 68; i++) {
    assert.equal(results[i].action, "WAIT");
    assert.equal(results[i].blockedBy, "CE wall overhead", `bar ${i}`);
  }
  assert.equal(results[0].blockedBy, "session start"); // session windows keep precedence
  assert.equal(results[69].blockedBy, "session end");
});

test("PE wall below within 0.25 ATR vetoes bear entries", () => {
  const results = evaluateSignals(trendInput(0, true, { nearWall: true }));
  assert.ok(results.every((r) => r.action !== "BUY_PE" && r.action !== "ARMED_PE"));
  for (let i = 3; i <= 68; i++) {
    assert.equal(results[i].action, "WAIT");
    assert.equal(results[i].blockedBy, "PE wall below", `bar ${i}`);
  }
});

test("session start and session end windows are blocked", () => {
  const results = evaluateSignals(trendInput(0, false));
  for (const i of [0, 1, 2]) {
    assert.equal(results[i].action, "WAIT");
    assert.equal(results[i].blockedBy, "session start");
  }
  assert.equal(results[3].blockedBy, undefined);
  assert.equal(results[3].action, "BUY_CE"); // first bar past the open window can trade
  for (let i = 69; i <= 74; i++) assert.equal(results[i].blockedBy, "session end");
  assert.notEqual(results[68].blockedBy, "session end"); // cooldown there, not the session clock
});

test("a window whose head sits on a session gap keeps the bar grid intact", () => {
  // The live rolling window's head slides one bar at a time; once per day it
  // starts exactly on the previous session's last bar, so the FIRST gap in
  // the window is the overnight break (~86400s). Bar length must come from
  // the smallest positive gap (300s), not the first gap, or every bar of the
  // new day lands inside a bogus "session start" window and the day's
  // signals silently vanish.
  const day0 = trendDay(0, false);
  const day1 = trendDay(1, false);
  const input: SignalInput = {
    spot: [...day0.spot.slice(-1), ...day1.spot],
    futures: [...day0.futures.slice(-1), ...day1.futures],
    premium: { ce: [...day0.ce.slice(-1), ...day1.ce], pe: [...day0.pe.slice(-1), ...day1.pe] },
    chain: [...day0.chain.slice(-1), ...day1.chain],
  };
  const results = evaluateSignals(input);
  assert.equal(results.length, input.spot.length);
  // Index 0 is day 0's closing bar (session end); day 1 opens at index 1.
  assert.equal(results[0].blockedBy, "session end");
  for (const i of [1, 2, 3]) {
    assert.equal(results[i].blockedBy, "session start", `day-1 open bar ${i}`);
  }
  // A mid-day bar must be free of BOTH session blocks: with the overnight
  // gap misread as the bar length, every bar of day 1 would read as
  // "session start". Cooldown blocks are fine here — they are the engine
  // doing its job on a confirmed BUY, not a clock error.
  for (let i = 40; i <= 45; i++) {
    assert.notEqual(results[i].blockedBy, "session start", `bar ${i} must not read as session start`);
    assert.notEqual(results[i].blockedBy, "session end", `bar ${i} must not read as session end`);
    assert.ok(results[i].score > 0, `bar ${i} is scored`);
  }
});

test("omitted feeds degrade instead of punishing the score", () => {
  const day = trendDay(0, false);
  const full = evaluateSignals({ spot: day.spot, futures: day.futures, premium: { ce: day.ce, pe: day.pe }, chain: day.chain });
  const spotOnly = evaluateSignals({ spot: day.spot });

  // Missing legs vote flat with an explanatory note...
  for (const [leg, note] of [
    ["vwap", "futures unavailable"],
    ["volumeConviction", "futures unavailable"],
    ["futuresOI", "futures unavailable"],
    ["premiumDOI", "premium unavailable"],
    ["chainContext", "chain unavailable"],
  ]) {
    const vote = voteFor(spotOnly[25], leg);
    assert.equal(vote.dir, "flat");
    assert.equal(vote.note, note);
  }

  // ...and are excluded from the denominator: with only supertrend + ema +
  // rsi + patterns alive, 42/50 scores HIGHER than the full book's 80/100.
  assert.equal(spotOnly[25].action, "BUY_CE");
  assert.equal(spotOnly[25].score, 84);
  assert.equal(full[23].action, "BUY_CE");
  assert.equal(full[23].score, 80);
  assert.ok(spotOnly[25].score > full[25].score, "degradation must not silently punish the score");

  // The first spot-only BUY lands once SuperTrend warms (bar 9: 18/26 -> 69);
  // ATR(14) is still warming, so the stop falls back to the prefix mean true range.
  assert.equal(spotOnly[9].action, "BUY_CE");
  assert.equal(spotOnly[9].score, 69);
  assert.equal(spotOnly[9].strikeHint.strike, 25050); // 25072 rounded to the 50 step
  assert.ok(Number.isFinite(spotOnly[9].stop) && spotOnly[9].stop < day.spot[9].close);
  assert.ok(Number.isFinite(spotOnly[9].target) && spotOnly[9].target > day.spot[9].close);

  // From bar 13 Wilder ATR(14) is live: exact ATR fallback target, no chain wall.
  const close = day.spot[13].close;
  const atr = atrSeries(day.spot, 14)[13];
  assert.ok(Number.isFinite(atr));
  assert.equal(spotOnly[13].action, "BUY_CE");
  assert.equal(spotOnly[13].strikeHint.strike, 25100);
  assert.match(spotOnly[13].strikeHint.reason, /50/);
  assert.ok(Math.abs(spotOnly[13].stop - (close - atr)) < 1e-9);
  assert.ok(Math.abs(spotOnly[13].target - (close + 2 * atr)) < 1e-9);
});

test("no lookahead: replaying a truncated prefix reproduces every probed bar exactly", () => {
  const day0 = trendDay(0, false, { startClose: 25000 });
  const day1 = trendDay(1, false, { startClose: 25600 });
  const input: SignalInput = {
    spot: [...day0.spot, ...day1.spot],
    futures: [...day0.futures, ...day1.futures],
    premium: { ce: [...day0.ce, ...day1.ce], pe: [...day0.pe, ...day1.pe] },
    chain: [...day0.chain, ...day1.chain],
  };
  const full = evaluateSignals(input);
  assert.equal(full.length, 2 * BARS_PER_DAY);

  const firstBuy = full.findIndex((r) => isBuy(r));
  const indices = new Set<number>([
    1, 2, 3, 4, 6, 7, 9, 13, 14, 19, 23, 24, 25, 30, 59, 60, 63, 67, 68, 69, 74,
    75, 76, 77, 78, 79, 100, 125, 149,
  ]);
  // Around the first signal and its cooldown boundary, plus the day rollover
  // (74/75) where a naive multi-session VWAP would leak the next session.
  for (const offset of [-1, 0, 1, 3, 4]) indices.add(firstBuy + offset);

  for (const i of indices) {
    if (i < 1 || i >= full.length) continue;
    const cutoff = input.spot[i].time;
    const replay = evaluateSignals({
      spot: input.spot.slice(0, i + 1),
      futures: input.futures.filter((c) => c.time <= cutoff),
      premium: {
        ce: input.premium.ce.filter((c) => c.time <= cutoff),
        pe: input.premium.pe.filter((c) => c.time <= cutoff),
      },
      chain: input.chain.filter((c) => c.time <= cutoff),
    });
    assert.equal(replay.length, i + 1);
    assert.deepEqual(replay[i], full[i], `bar ${i} diverged between the full run and the prefix replay`);
  }
});

test("degenerate inputs never throw", () => {
  assert.deepEqual(evaluateSignals({} as any), []);
  assert.deepEqual(evaluateSignals({ spot: [] }), []);
  assert.deepEqual(
    evaluateSignals({ spot: [{ time: barTime(0, 0), open: 25000, high: 25002, low: 24998, close: 25000 }] }),
    [],
  );

  const junk = evaluateSignals({
    spot: Array.from({ length: 5 }, (_, k) => ({
      time: barTime(0, k),
      open: NaN,
      high: NaN,
      low: NaN,
      close: NaN,
    })),
    futures: [],
    premium: { ce: [], pe: [] },
    chain: [{ time: NaN, pcr: 1.3 }, null],
  } as any);
  assert.equal(junk.length, 5);
  assert.ok(junk.every((r) => r.action === "WAIT"));

  const short = evaluateSignals({
    spot: [
      { time: barTime(0, 0), open: 25000, high: 25002, low: 24998, close: 25000 },
      { time: barTime(0, 1), open: 25004, high: 25006, low: 25002, close: 25004 },
    ],
  });
  assert.equal(short.length, 2);
  assert.ok(short.every((r) => r.action === "WAIT"));
});

test("score threshold and cooldown settings are honoured", () => {
  const input = trendInput(0, false);

  const strict = evaluateSignals({ ...input, settings: { scoreThreshold: 101 } });
  assert.ok(strict.every((r) => !isBuy(r)), "an unreachable threshold must never confirm");
  assert.ok(strict.some((r) => r.action === "ARMED_CE"), "92-score bars land in the 89+ arm band");

  const noCooldown = evaluateSignals({ ...input, settings: { cooldownBars: 0 } });
  let adjacent = 0;
  for (let i = 0; i + 1 < noCooldown.length; i++) {
    if (isBuy(noCooldown[i]) && isBuy(noCooldown[i + 1])) adjacent++;
  }
  assert.ok(adjacent >= 1, "zero cooldown must allow back-to-back BUYs");
});

test("falling futures OI with a rising price votes short covering at half weight", () => {
  const results = evaluateSignals(trendInput(0, false, { oiStep: -50 }));
  const vote = voteFor(results[30], "futuresOI");
  assert.equal(vote.dir, "bull");
  assert.equal(vote.weight, 5); // half of the 10-point leg
  assert.equal(vote.note, "short covering");
});

test("simultaneous CE and PE buildups keep the premium leg flat", () => {
  const results = evaluateSignals(trendInput(0, false, { bothPremiumRising: true }));
  const vote = voteFor(results[30], "premiumDOI");
  assert.equal(vote.dir, "flat");
  assert.equal(vote.note, "conflicting premium buildup");
});
