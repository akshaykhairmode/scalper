import { test } from "node:test";
import assert from "node:assert/strict";
import { applyOITick, atrSeries, atrValue, computeOIWalls, oiDeltaSeries, supertrendSeries, trueRange } from "../src/chart/intraday.ts";

const near = (actual, expected, tolerance = 1e-6) =>
  assert.ok(Math.abs(actual - expected) <= tolerance, `expected ${expected}, got ${actual}`);

/** Flat-range candles: every true range is exactly 2. */
function flatRangeCandles(count, start = 100) {
  return Array.from({ length: count }, (_, i) => ({
    open: start + i,
    high: start + i + 1,
    low: start + i - 1,
    close: start + i,
  }));
}

function trendingCandles(count, step, start = 100) {
  return Array.from({ length: count }, (_, i) => {
    const close = start + step * i;
    return { open: close - step / 2, high: close + 1, low: close - 1, close };
  });
}

test("trueRange falls back to high-low on the first bar", () => {
  near(trueRange({ high: 105, low: 100 }, NaN), 5);
  near(trueRange({ high: 105, low: 100 }, 90), 15);
  assert.ok(Number.isNaN(trueRange({ high: 0, low: 0 })));
});

test("ATR warms up for a full period then smooths the true range", () => {
  const series = atrSeries(flatRangeCandles(20), 14);
  for (let i = 0; i < 13; i++) assert.ok(Number.isNaN(series[i]), `index ${i} should warm up`);
  near(series[13], 2);
  near(series[19], 2);
  near(atrValue(flatRangeCandles(20), 14), 2);
  assert.equal(atrValue(flatRangeCandles(5), 14), 0);
});

test("SuperTrend trails below price in an uptrend", () => {
  const candles = trendingCandles(40, 2);
  const { up, down, trend } = supertrendSeries(candles, 10, 3);
  assert.equal(trend.length, candles.length);
  assert.equal(trend[trend.length - 1], 1);
  assert.ok(Number.isFinite(up[up.length - 1]));
  assert.equal(down[down.length - 1], null);
  // A trailing band must sit below the close in an uptrend.
  assert.ok(up[up.length - 1] < candles[candles.length - 1].close);
});

test("SuperTrend flips after a close through the trailing band", () => {
  const rising = trendingCandles(40, 2);
  const top = rising[rising.length - 1].close;
  const falling = Array.from({ length: 12 }, (_, i) => {
    const close = top - (i + 1) * 8;
    return { open: close + 4, high: close + 1, low: close - 1, close };
  });
  const series = supertrendSeries([...rising, ...falling], 10, 3);
  assert.equal(series.trend[series.trend.length - 1], -1);
  assert.equal(series.up[series.up.length - 1], null);
  assert.ok(Number.isFinite(series.down[series.down.length - 1]));
});

test("SuperTrend returns empty series for empty input", () => {
  const result = supertrendSeries([], 10, 3);
  assert.deepEqual(result.up, []);
  assert.deepEqual(result.down, []);
  assert.deepEqual(result.trend, []);
});

test("computeOIWalls picks max-OI strikes and merges a shared strike", () => {
  const chain = [
    { strike: 24800, call: { oi: 1000, oiChange: 50 }, put: { oi: 9000, oiChange: 400 } },
    { strike: 24900, call: { oi: 5000, oiChange: 300 }, put: { oi: 4000, oiChange: -200 } },
    { strike: 25000, call: { oi: 12000, oiChange: 900 }, put: { oi: 3000, oiChange: 100 } },
  ];
  const walls = computeOIWalls(chain);
  const oiWalls = walls.filter((wall) => wall.metric === "oi");
  assert.deepEqual(oiWalls.map((wall) => [wall.side, wall.strike]), [["PE", 24800], ["CE", 25000]]);
  const ceDelta = walls.find((wall) => wall.metric === "doi" && wall.side === "CE");
  const peDelta = walls.find((wall) => wall.metric === "doi" && wall.side === "PE");
  assert.equal(ceDelta.strike, 25000);
  assert.equal(ceDelta.value, 900);
  assert.equal(peDelta.strike, 24800);
  assert.equal(peDelta.value, 400);
});

test("computeOIWalls collapses CE and PE max OI on the same strike", () => {
  const chain = [
    { strike: 25000, call: { oi: 12000 }, put: { oi: 15000 } },
    { strike: 24900, call: { oi: 1000 }, put: { oi: 1000 } },
  ];
  const walls = computeOIWalls(chain, { includeDelta: false });
  assert.equal(walls.length, 1);
  assert.equal(walls[0].side, "BOTH");
  assert.equal(walls[0].strike, 25000);
  assert.equal(walls[0].value, 15000);
});

test("computeOIWalls tolerates an empty or malformed chain", () => {
  assert.deepEqual(computeOIWalls([]), []);
  assert.deepEqual(computeOIWalls(null), []);
  assert.deepEqual(computeOIWalls([{ strike: 0, call: { oi: 5 } }, { strike: 100 }]), []);
});

test("oiDeltaSeries compares against the previous valid reading", () => {
  const deltas = oiDeltaSeries([
    { oi: 100 },
    { oi: 140 },
    { oi: 0 },
    { oi: 120 },
    { oi: 150 },
  ]);
  assert.equal(deltas.length, 5);
  assert.ok(Number.isNaN(deltas[0]), "first bar has no comparable base");
  assert.equal(deltas[1], 40);
  assert.ok(Number.isNaN(deltas[2]), "missing OI stays a gap");
  assert.equal(deltas[3], -20, "gap does not advance the comparison base");
  assert.equal(deltas[4], 30);
});

test("oiDeltaSeries returns all gaps when no OI is present", () => {
  const deltas = oiDeltaSeries([{ oi: 0 }, {}, { oi: -5 }]);
  assert.equal(deltas.length, 3);
  assert.ok(deltas.every((value) => Number.isNaN(value)));
});

test("applyOITick shifts the server day ΔOI by the observed delta", () => {
  const next = applyOITick({ oi: 1000, oiChange: 200, oiChangePct: 25 }, 1100);
  assert.equal(next.oi, 1100);
  assert.equal(next.oiChange, 300);
  near(next.oiChangePct, 37.5);
  const down = applyOITick({ oi: 1100, oiChange: 300, oiChangePct: 37.5 }, 900);
  assert.equal(down.oiChange, 100);
  near(down.oiChangePct, 12.5);
});

test("applyOITick never fabricates a change from a zero/absent previous OI", () => {
  const first = applyOITick({ oi: 0, oiChange: 0, oiChangePct: 0 }, 1200);
  assert.equal(first.oi, 1200);
  assert.equal(first.oiChange, 0, "a zero-OI snapshot must not create a fake build-up");
  assert.equal(first.oiChangePct, 0);

  const serverChange = applyOITick({ oi: 0, oiChange: 150, oiChangePct: 15 }, 1150);
  assert.equal(serverChange.oiChange, 150, "the server value is kept, not overwritten");
});

test("applyOITick ignores zero/missing OI ticks", () => {
  const zero = applyOITick({ oi: 1000, oiChange: 200, oiChangePct: 25 }, 0);
  assert.deepEqual(zero, { oi: 1000, oiChange: 200, oiChangePct: 25 });
  const missing = applyOITick({ oi: 1000, oiChange: 200, oiChangePct: 25 }, NaN);
  assert.deepEqual(missing, { oi: 1000, oiChange: 200, oiChangePct: 25 });
});
