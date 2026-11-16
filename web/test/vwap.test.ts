import { test } from "node:test";
import assert from "node:assert/strict";
import { vwapSeries } from "../src/chart/indicators.ts";
import { DAY_MS, IST_OFFSET_MS, istSessionKey } from "../src/chart/session.ts";

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
  // The first bar of the new session breaks the line, but still contributes
  // its volume to the next bar's VWAP.
  assert.ok(Number.isNaN(values[2]));
  near(values[3], 35);
});

test("a zero-volume first bar of a new session stays NaN", () => {
  const sessionFor = (time) => (time < 100 ? "day-1" : "day-2");
  const values = vwapSeries([
    candle(1, 10, 100),
    candle(101, 30, 0),
    candle(102, 40, 100),
  ], sessionFor);
  near(values[0], 10);
  assert.ok(Number.isNaN(values[1]));
  near(values[2], 40);
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

test("coerces string volume and ignores negative volume", () => {
  const values = vwapSeries([
    { time: 1, open: 10, high: 11, low: 9, close: 10, volume: "100" },
    { time: 2, open: 20, high: 21, low: 19, close: 20, volume: -50 },
    { time: 3, open: 30, high: 31, low: 29, close: 30, volume: 100 },
  ], () => "session");
  near(values[0], 10);
  near(values[1], 10);
  near(values[2], 20);
});

test("skips malformed OHLC instead of producing a bogus VWAP", () => {
  const values = vwapSeries([
    { time: 1, open: 10, high: NaN, low: 9, close: 10, volume: 100 },
    { time: 2, open: 0, high: 0, low: 0, close: 0, volume: 100 },
    { time: 3, open: 20, high: 21, low: 19, close: 20, volume: 100 },
  ], () => "session");
  assert.ok(Number.isNaN(values[0]));
  assert.ok(Number.isNaN(values[1]));
  near(values[2], 20);
});

test("handles empty and malformed input", () => {
  assert.deepEqual(vwapSeries([], () => "session"), []);
  assert.deepEqual(vwapSeries(null as any, () => "session"), []);
  const values = vwapSeries([
    { time: NaN, open: 1, high: 2, low: 0, close: 1, volume: 100 },
    candle(2, 10, 100),
  ], () => "session");
  assert.ok(Number.isNaN(values[0]));
  near(values[1], 10);
});

test("IST session key rolls over at midnight IST", () => {
  const beforeMidnight = Date.parse("2026-11-16T18:29:59Z"); // 23:59:59 IST
  const afterMidnight = Date.parse("2026-11-16T18:30:00Z"); // 00:00 IST next day
  assert.notEqual(istSessionKey(beforeMidnight), istSessionKey(afterMidnight));
  assert.equal(istSessionKey(afterMidnight), istSessionKey(Date.parse("2026-11-17T03:45:00Z")));
});

test("IST session key keeps an NSE day on one session", () => {
  const open = Date.parse("2026-11-16T03:45:00Z"); // 09:15 IST
  const close = Date.parse("2026-11-16T10:00:00Z"); // 15:30 IST
  assert.equal(istSessionKey(open), istSessionKey(close));
});

test("IST session constants stay aligned", () => {
  assert.equal(IST_OFFSET_MS, 19_800_000);
  assert.equal(DAY_MS, 86_400_000);
  assert.equal(istSessionKey(0), Math.floor(IST_OFFSET_MS / DAY_MS));
});
