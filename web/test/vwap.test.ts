import { test } from "node:test";
import assert from "node:assert/strict";
import { vwapSeries } from "../src/chart/indicators.ts";

const near = (actual, expected, tolerance = 1e-9) =>
  assert.ok(Math.abs(actual - expected) <= tolerance, `expected ${expected}, got ${actual}`);

function candle(time, typical, volume) {
  // High/low/close symmetric around `typical`, so typical price is exact.
  return { time, open: typical, high: typical + 1, low: typical - 1, close: typical, volume };
}

test("accumulates typical price weighted by volume", () => {
  const values = vwapSeries([
    candle(1, 10, 100),
    candle(2, 20, 300),
  ], () => "session");
  near(values[0], 10);
  near(values[1], (10 * 100 + 20 * 300) / 400);
});

test("resets the cumulative totals when the session changes", () => {
  const sessionFor = (time) => (time < 100 ? "day-1" : "day-2");
  const values = vwapSeries([
    candle(1, 10, 100),
    candle(2, 20, 100),
    candle(101, 30, 100),
    candle(102, 40, 100),
  ], sessionFor);
  near(values[0], 10);
  near(values[1], 15);
  near(values[2], 30);
  near(values[3], 35);
});

test("skips bars without volume but keeps the last accumulated VWAP", () => {
  const values = vwapSeries([
    candle(1, 10, 100),
    candle(2, 20, 0),
    candle(3, 30, 100),
  ], () => "session");
  near(values[0], 10);
  near(values[1], 10);
  near(values[2], 20);
});

test("returns NaN when the session has no traded volume", () => {
  const values = vwapSeries([
    candle(1, 10, 0),
    candle(2, 20, 0),
  ], () => "session");
  assert.ok(Number.isNaN(values[0]));
  assert.ok(Number.isNaN(values[1]));
});

test("handles empty and malformed input", () => {
  assert.deepEqual(vwapSeries([]), []);
  assert.deepEqual(vwapSeries(null), []);
  const values = vwapSeries([
    { time: NaN, open: 1, high: 2, low: 0, close: 1, volume: 100 },
    candle(2, 10, 100),
  ], () => "session");
  assert.ok(Number.isNaN(values[0]));
  near(values[1], 10);
});
