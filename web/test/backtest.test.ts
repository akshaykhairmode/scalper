import { test } from "node:test";
import assert from "node:assert/strict";
import { buildRangeFixture, buildTrendFixture, runBacktest, summarizeReport } from "../src/chart/backtest.ts";

// 2026-01-05 09:15 IST — the same anchor the fixture builders use by default.
const T0 = 1767584700;
const BAR = 300;
const t = (i) => T0 + BAR * i;

/** One spot bar at grid index `i`. */
function bar(i, open, high, low, close) {
  return { time: t(i), open, high, low, close };
}

/** Flat bars: close `close`, high +pad, low −pad — never touching far levels. */
function flatBars(count, close = 100, pad = 1) {
  return Array.from({ length: count }, (_, i) => bar(i, close, close + pad, close - pad, close));
}

/** Premium candles keyed by grid index. */
function prem(pairs) {
  return pairs.map(([i, close]) => ({ time: t(i), open: close, high: close, low: close, close }));
}

function vote(leg, dir, weight = 1) {
  return { leg, dir, weight };
}

function signal(i, action, stop, target, votes = []) {
  return { time: t(i), action, score: 1, direction: action === "BUY_PE" ? "bear" : "bull", votes, stop, target };
}

/**
 * Deterministic fake engine: replays the given results in the order supplied
 * (deliberately unsorted — runBacktest must order them itself) and never looks
 * at the input bars.
 */
function engineOf(signals) {
  return () => signals.slice();
}

test("target exit resolves on the first bar touching the target with exact P&L math", () => {
  const spot = flatBars(8);
  spot[3] = bar(3, 100, 108, 99, 107); // near-miss: high 108 < target
  spot[4] = bar(4, 107, 112, 100, 110); // target bar: high 112 >= 110, low 100 > stop
  const settings = { fast: 9, slow: 21 };
  let calls = 0;
  let seen = null;
  const report = runBacktest(
    { spot, premium: { ce: prem([[2, 100], [3, 105], [4, 120]]) }, settings },
    (input) => {
      calls++;
      seen = input;
      return [signal(2, "BUY_CE", 95, 110)];
    },
  );

  assert.equal(calls, 1, "the engine must run exactly once over the full history");
  assert.ok(seen, "the engine receives the full input");
  assert.equal(seen.settings, settings, "settings are passed through untouched");
  assert.equal(seen.spot, spot, "the engine sees the caller's bars");

  assert.equal(report.totalSignals, 1);
  assert.equal(report.buyCE, 1);
  assert.equal(report.buyPE, 0);

  const row = report.signals[0];
  assert.equal(row.exitReason, "target");
  assert.equal(row.entryPremium, 100);
  assert.equal(row.exitTime, t(4));
  assert.equal(row.exitPremium, 120);
  assert.equal(row.pnlPerLot, (120 - 100) * 75);
  assert.equal(row.mfePremium, 20, "premium closes in the hold window: 105, 120");
  assert.equal(row.maePremium, 5);

  assert.equal(report.targetHits, 1);
  assert.equal(report.stopHits, 0);
  assert.equal(report.hitRate, 1);
  assert.equal(report.avgPnlPerLot, 1500);
  assert.equal(report.expectancyPerSignal, 1500);
  assert.equal(report.cumulativePnl, 1500);
  assert.equal(report.maxDrawdown, 0, "an all-win curve never dips below its start");
  assert.deepEqual(report.perLeg, [], "no votes were cast");
});

test("stop exit resolves before a later target bar could rescue the trade", () => {
  const spot = flatBars(8);
  spot[3] = bar(3, 100, 105, 94, 96); // stop bar: low 94 <= 95
  spot[4] = bar(4, 96, 112, 90, 111); // would have been the target bar
  const report = runBacktest(
    { spot, premium: { ce: prem([[2, 100], [3, 80], [4, 60]]) } },
    engineOf([signal(2, "BUY_CE", 95, 110)]),
  );
  const row = report.signals[0];
  assert.equal(row.exitReason, "stop");
  assert.equal(row.exitTime, t(3), "the walk stops at the first deciding bar");
  assert.equal(row.exitPremium, 80);
  assert.equal(row.pnlPerLot, (80 - 100) * 75);
  assert.equal(row.mfePremium, -20);
  assert.equal(row.maePremium, -20);
  assert.equal(report.stopHits, 1);
  assert.equal(report.targetHits, 0);
  assert.equal(report.hitRate, 0, "a decided loss is a 0% hit rate, not null");
  assert.equal(report.expectancyPerSignal, -1500);
  assert.equal(report.cumulativePnl, -1500);
  assert.equal(report.maxDrawdown, -1500);
});

test("a bar touching both stop and target resolves as a stop (pessimistic)", () => {
  const spot = flatBars(6);
  spot[3] = bar(3, 100, 112, 94, 96); // low 94 <= 95 AND high 112 >= 110 in one bar
  const report = runBacktest(
    { spot, premium: { ce: prem([[2, 100], [3, 70]]) } },
    engineOf([signal(2, "BUY_CE", 95, 110)]),
  );
  const row = report.signals[0];
  assert.equal(row.exitReason, "stop");
  assert.equal(row.exitTime, t(3));
  assert.equal(row.exitPremium, 70);
  assert.equal(row.pnlPerLot, (70 - 100) * 75);
  assert.equal(report.stopHits, 1);
  assert.equal(report.targetHits, 0);
});

test("no level hit within maxHoldBars times out at the Nth bar close", () => {
  const spot = flatBars(10);
  const report = runBacktest(
    { spot, premium: { ce: prem([[2, 100], [3, 101], [4, 100.5], [5, 99]]) } },
    engineOf([signal(2, "BUY_CE", 90, 130)]),
    { maxHoldBars: 3 },
  );
  const row = report.signals[0];
  assert.equal(row.exitReason, "timeout");
  assert.equal(row.exitTime, t(5), "exit at the close of the 3rd bar after entry");
  assert.equal(row.exitPremium, 99);
  assert.equal(row.pnlPerLot, (99 - 100) * 75);
  assert.equal(row.mfePremium, 1);
  assert.equal(row.maePremium, -1);
  assert.equal(report.timeouts, 1);
  assert.equal(report.hitRate, null, "a timeout decides nothing on the hit-rate scale");
  assert.equal(report.expectancyPerSignal, null, "timeouts are settled, not decided");
  assert.equal(report.avgPnlPerLot, -75, "the settled average includes the timeout close");
  assert.equal(report.cumulativePnl, -75);
  assert.equal(report.maxDrawdown, -75);
});

test("a signal that outlives the series stays open with null exit fields", () => {
  const spot = flatBars(10);
  const report = runBacktest(
    { spot, premium: { ce: prem([[8, 100], [9, 104]]) } },
    engineOf([signal(8, "BUY_CE", 95, 110)]),
  );
  assert.equal(report.openTrades, 1);
  assert.deepEqual(report.signals[0], {
    time: t(8),
    action: "BUY_CE",
    score: 1,
    votes: [],
    stop: 95,
    target: 110,
    entryPremium: 100,
    exitTime: null,
    exitPremium: null,
    exitReason: "open",
    pnlPerLot: null,
    mfePremium: 4, // one premium close observed during the truncated hold
    maePremium: 4,
  });
  assert.equal(report.hitRate, null);
  assert.equal(report.expectancyPerSignal, null);
  assert.equal(report.avgPnlPerLot, null);
  assert.equal(report.cumulativePnl, null);
  assert.equal(report.maxDrawdown, null);
});

test("missing premium marks the signal no-premium and keeps it out of P&L stats", () => {
  const spot = flatBars(6);
  const report = runBacktest(
    { spot }, // no premium series at all
    engineOf([signal(2, "BUY_CE", 95, 110, [vote("momentum", "bull", 2)])]),
  );
  assert.equal(report.totalSignals, 1);
  assert.equal(report.noPremiumTrades, 1);
  assert.deepEqual(report.signals[0], {
    time: t(2),
    action: "BUY_CE",
    score: 1,
    votes: [vote("momentum", "bull", 2)],
    stop: 95,
    target: 110,
    entryPremium: null,
    exitTime: null,
    exitPremium: null,
    exitReason: "no-premium",
    pnlPerLot: null,
    mfePremium: null,
    maePremium: null,
  });
  assert.equal(report.hitRate, null);
  assert.equal(report.expectancyPerSignal, null);
  assert.equal(report.avgPnlPerLot, null);
  assert.equal(report.cumulativePnl, null);
  assert.equal(report.maxDrawdown, null);
  const momentum = report.perLeg.find((leg) => leg.leg === "momentum");
  assert.equal(momentum.signalsWithLeg, 1, "the signal still counts for attribution");
  assert.equal(momentum.winsWithLeg, 0);
  assert.equal(momentum.bullVotes, 1);
});

test("signals landing inside an open position are excluded from the report entirely", () => {
  const spot = flatBars(12);
  spot[4] = bar(4, 100, 112, 100, 110); // first trade reaches its target on bar 4
  const ce = prem([
    [2, 100], [3, 105], [4, 120], [5, 100], [6, 100], [7, 100],
    [8, 100], [9, 100], [10, 100], [11, 100],
  ]);
  // Deliberately scrambled: runBacktest must order engine results itself.
  const report = runBacktest(
    { spot, premium: { ce } },
    engineOf([
      signal(5, "BUY_CE", 95, 110), // after the exit bar: simulable
      signal(4, "BUY_CE", 95, 110), // the exit bar itself is still occupied
      signal(3, "BUY_CE", 95, 110), // inside the open position
      signal(2, "BUY_CE", 95, 110), // the opening signal
    ]),
  );
  assert.equal(report.totalSignals, 2);
  assert.deepEqual(report.signals.map((row) => row.time), [t(2), t(5)]);
  assert.equal(report.targetHits, 1); // the bar-2 signal
  assert.equal(report.openTrades, 1); // the bar-5 signal outlives the series
  assert.equal(report.buyCE, 2);
  assert.equal(report.hitRate, 1);
  assert.equal(report.cumulativePnl, (120 - 100) * 75, "only the settled trade contributes");
});

test("per-leg attribution joins votes across all results with signal outcomes", () => {
  const spot = flatBars(20);
  spot[4] = bar(4, 100, 112, 99, 110); // CE target for the bar-2 signal
  spot[11] = bar(11, 100, 106, 99, 105); // PE stop for the bar-10 signal
  const results = [
    signal(2, "BUY_CE", 95, 110, [vote("momentum", "bull", 2), vote("oiWalls", "bull", 1)]),
    { time: t(6), action: "WAIT", score: 0, direction: "flat", votes: [vote("momentum", "flat"), vote("oiWalls", "bear")] },
    signal(10, "BUY_PE", 105, 90, [vote("momentum", "bear", 2), vote("theta", "flat", 1)]),
    { time: t(14), action: "ARMED_CE", score: 1, direction: "bull", votes: [vote("momentum", "bull", 1)] },
  ];
  const premium = { ce: prem([[2, 100], [3, 105], [4, 120]]), pe: prem([[10, 100], [11, 80]]) };
  const report = runBacktest({ spot, premium }, engineOf(results));

  assert.equal(report.totalSignals, 2, "WAIT and ARMED results are advisory only");
  assert.equal(report.buyCE, 1);
  assert.equal(report.buyPE, 1);
  assert.equal(report.signals[0].exitReason, "target");
  assert.equal(report.signals[0].pnlPerLot, (120 - 100) * 75);
  assert.equal(report.signals[0].mfePremium, 20);
  assert.equal(report.signals[0].maePremium, 5);
  assert.equal(report.signals[1].exitReason, "stop");
  assert.equal(report.signals[1].pnlPerLot, (80 - 100) * 75);
  assert.equal(report.hitRate, 0.5);
  assert.equal(report.expectancyPerSignal, 0);
  assert.equal(report.cumulativePnl, 0);

  assert.deepEqual(report.perLeg.map((leg) => leg.leg), ["momentum", "oiWalls", "theta"]);
  assert.deepEqual(report.perLeg.find((leg) => leg.leg === "momentum"), {
    leg: "momentum",
    bullVotes: 2, // BUY_CE + ARMED_CE
    bearVotes: 1, // BUY_PE
    flatVotes: 1, // WAIT
    signalsWithLeg: 2, // the two confirmed signals
    winsWithLeg: 1, // only the target hit
  });
  assert.deepEqual(report.perLeg.find((leg) => leg.leg === "oiWalls"), {
    leg: "oiWalls",
    bullVotes: 1,
    bearVotes: 1, // the WAIT result still counts directionally
    flatVotes: 0,
    signalsWithLeg: 1,
    winsWithLeg: 1,
  });
  assert.deepEqual(report.perLeg.find((leg) => leg.leg === "theta"), {
    leg: "theta",
    bullVotes: 0,
    bearVotes: 0,
    flatVotes: 1,
    signalsWithLeg: 0, // flat votes never mark participation
    winsWithLeg: 0,
  });
});

test("fixture builders emit ascending 5m IST bars; trend futures volume and oi rise", () => {
  const trend = buildTrendFixture(12, 5);
  assert.equal(trend.spot.length, 12);
  assert.equal(trend.futures.length, 12);
  assert.equal(trend.spot[0].time, 1767584700); // 2026-01-05 09:15 IST
  assert.equal(trend.spot[0].close, 25000);
  for (let i = 0; i < 11; i++) {
    assert.equal(trend.spot[i + 1].time - trend.spot[i].time, 300);
    assert.equal(trend.futures[i + 1].time - trend.futures[i].time, 300);
    assert.ok(trend.futures[i + 1].volume > trend.futures[i].volume, `futures volume must rise at bar ${i + 1}`);
    assert.ok(trend.futures[i + 1].oi > trend.futures[i].oi, `futures oi must rise at bar ${i + 1}`);
    assert.equal(trend.spot[i + 1].close - trend.spot[i].close, 5);
  }
  for (const series of [trend.spot, trend.futures]) {
    for (const b of series) {
      assert.ok(b.high >= Math.max(b.open, b.close));
      assert.ok(b.low <= Math.min(b.open, b.close));
    }
  }
  assert.equal(trend.futures[3].close - trend.spot[3].close, 25, "futures trade at the default basis");

  const range = buildRangeFixture(10, 8);
  assert.equal(range.spot.length, 10);
  assert.equal(range.futures.length, 10);
  for (let i = 0; i < 9; i++) {
    assert.ok(range.spot[i + 1].time > range.spot[i].time, "range times must ascend");
    assert.equal(range.spot[i + 1].time - range.spot[i].time, 300);
  }
  for (const b of range.spot) {
    assert.equal(b.high - b.low, 16, "each range bar spans exactly 2 × spread");
    assert.ok(b.close >= 24996 && b.close <= 25004, "closes alternate inside the range");
  }
});

test("premium lookup tolerates a one-bar misalignment, preferring the earlier bar", () => {
  const spot = flatBars(8);
  spot[3] = bar(3, 100, 112, 99, 110); // target on the first walk bar
  const sig = signal(2, "BUY_CE", 95, 110);

  // The premium grid has no bar at t(2); the previous bar exists.
  const shifted = runBacktest(
    { spot, premium: { ce: prem([[1, 90], [3, 130], [4, 140]]) } },
    engineOf([sig]),
  );
  const row = shifted.signals[0];
  assert.equal(row.entryPremium, 90, "falls back to the earlier bar");
  assert.equal(row.exitReason, "target");
  assert.equal(row.exitPremium, 130);
  assert.equal(row.pnlPerLot, (130 - 90) * 75);
  assert.equal(row.mfePremium, 40);
  assert.equal(row.maePremium, 40);

  // A gap where only a LATER premium bar exists must not be filled from the
  // future: the entry prices off a bar that has not formed yet, so the signal
  // stays unmeasurable rather than lookahead-priced.
  const futureOnly = runBacktest(
    { spot, premium: { ce: prem([[3, 130], [4, 140]]) } },
    engineOf([sig]),
  );
  assert.equal(futureOnly.signals[0].exitReason, "no-premium");
  assert.equal(futureOnly.signals[0].entryPremium, null);

  // Two bars ahead is beyond tolerance either way: no entry, no trade.
  const absent = runBacktest(
    { spot, premium: { ce: prem([[4, 140], [5, 150]]) } },
    engineOf([sig]),
  );
  assert.equal(absent.signals[0].exitReason, "no-premium");
  assert.equal(absent.signals[0].entryPremium, null);
  assert.equal(absent.noPremiumTrades, 1);
});

test("summarizeReport renders counts, hit rate and the top legs", () => {
  const spot = flatBars(20);
  spot[4] = bar(4, 100, 112, 99, 110);
  spot[11] = bar(11, 100, 106, 99, 105);
  const results = [
    signal(2, "BUY_CE", 95, 110, [vote("momentum", "bull", 2), vote("oiWalls", "bull", 1)]),
    signal(10, "BUY_PE", 105, 90, [vote("momentum", "bear", 2)]),
  ];
  const premium = { ce: prem([[2, 100], [3, 105], [4, 120]]), pe: prem([[10, 100], [11, 80]]) };
  const report = runBacktest({ spot, premium }, engineOf(results));
  const text = summarizeReport(report);

  assert.ok(text.includes("2 signals (1 CE / 1 PE)"), text);
  assert.ok(text.includes("1 target, 1 stop, 0 timeout, 0 open, 0 no-premium"), text);
  assert.ok(text.includes("Hit rate: 50.0%"), text);
  assert.ok(text.includes("Expectancy/signal: +0.0"), text);
  assert.ok(text.includes("Cumulative P&L/lot: +0.0"), text);
  assert.ok(text.includes("momentum 1W/2"), text);
  assert.ok(text.includes("oiWalls 1W/1"), text);
  assert.ok(!text.includes("theta"), "legs with no participation are omitted");
});
