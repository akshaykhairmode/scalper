import { test } from "node:test";
import assert from "node:assert/strict";
import { vwapFromBase, vwapSeries } from "../src/chart/indicators.ts";
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
  // The line breaks on the last bar of the old session, not on the first bar
  // of the new one, so the opening VWAP of a session is still visible.
  assert.ok(Number.isNaN(values[1]));
  near(values[2], 30);
  near(values[3], 35);
});

test("a zero-volume first bar of a new session stays NaN", () => {
  const sessionFor = (time) => (time < 100 ? "day-1" : "day-2");
  const values = vwapSeries([
    candle(1, 10, 100),
    candle(101, 30, 0),
    candle(102, 40, 100),
  ], sessionFor);
  // The old session's last bar carries the break instead of the new session's
  // first bar, so it is removed; the new session has no volume until t=102.
  assert.ok(Number.isNaN(values[0]));
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

test("vwapFromBase projects a 5m bar from the 1-minute series", () => {
  // Five 1m bars from 09:15 with a rising price; the 5m bar's own typical price
  // is deliberately different so a coarse recomputation cannot pass this test.
  const base = [
    candle(0, 100, 10),
    candle(60, 102, 10),
    candle(120, 104, 10),
    candle(180, 106, 10),
    candle(240, 108, 10),
  ];
  const display = [{ time: 0, open: 100, high: 109, low: 99, close: 108, volume: 50 }];
  const aligned = vwapFromBase(display, base, () => "session", 300);
  near(aligned.values[0], 104);
  assert.notEqual(aligned.values[0], (109 + 99 + 108) / 3);
  near(aligned.cumulativeVolume[0], 50);
  near(aligned.cumulativePv[0], 104 * 50);
  assert.equal(aligned.baseFirstTime, 0);
  assert.equal(aligned.baseLastTime, 240);
});

test("vwapFromBase is timeframe independent", () => {
  const base = [0, 60, 120, 180, 240, 300, 360, 420, 480, 540].map((time, index) =>
    candle(time, 100 + index, 10));
  const oneMinute = base.map((bar) => ({ ...bar }));
  const fiveMinute = [0, 300].map((time) => ({
    time,
    open: 100,
    high: 200,
    low: 50,
    close: 150,
    volume: 50,
  }));
  const fromOne = vwapFromBase(oneMinute, base, () => "session", 60);
  const fromFive = vwapFromBase(fiveMinute, base, () => "session", 300);
  // Both series describe the same session, so the last value must agree.
  near(fromOne.values[fromOne.values.length - 1], fromFive.values[1], 1e-9);
});

test("vwapFromBase prices only the uncovered volume of a partial bar", () => {
  // The base has seen the first three minutes of this 5m bar, so its 30 lots are
  // already inside the cumulative totals and must not be counted twice.
  const base = [candle(0, 100, 10), candle(60, 102, 10), candle(120, 104, 10)];
  const display = [{ time: 0, open: 100, high: 109, low: 99, close: 108, volume: 30 }];
  const aligned = vwapFromBase(display, base, () => "session", 300);
  near(aligned.values[0], (100 * 10 + 102 * 10 + 104 * 10) / 30);
  near(aligned.cumulativeVolume[0], 30);
});

test("vwapFromBase adds the remainder when the base lags the bar", () => {
  const base = [candle(0, 100, 10), candle(60, 100, 10)];
  // The 5m bar traded more volume than the two base minutes know about.
  const display = [{ time: 0, open: 100, high: 109, low: 99, close: 108, volume: 30 }];
  const aligned = vwapFromBase(display, base, () => "session", 300);
  const typical = (109 + 99 + 108) / 3;
  near(aligned.values[0], (100 * 20 + typical * 10) / 30);
  near(aligned.cumulativeVolume[0], 30);
});

test("vwapFromBase carries the cumulative totals across bars", () => {
  const base = [candle(0, 100, 10), candle(60, 100, 10), candle(120, 200, 10), candle(180, 200, 10)];
  const display = [
    { time: 0, open: 100, high: 101, low: 99, close: 100, volume: 20 },
    { time: 120, open: 200, high: 201, low: 199, close: 200, volume: 20 },
  ];
  const aligned = vwapFromBase(display, base, () => "session", 120);
  near(aligned.values[0], 100);
  near(aligned.values[1], 150);
  near(aligned.cumulativePv[1], 150 * 40);
  near(aligned.cumulativeVolume[1], 40);
});

test("vwapFromBase does not leak a previous session into the new one", () => {
  const sessionFor = (time) => (time < 1000 ? "day-1" : "day-2");
  const base = [
    candle(0, 100, 10),
    candle(60, 100, 10),
    candle(1000, 300, 10),
    candle(1060, 300, 10),
  ];
  const display = [
    { time: 0, open: 100, high: 101, low: 99, close: 100, volume: 20 },
    { time: 1000, open: 300, high: 301, low: 299, close: 300, volume: 20 },
  ];
  const aligned = vwapFromBase(display, base, sessionFor, 60);
  near(aligned.values[0], 100);
  near(aligned.values[1], 300);
});

test("vwapFromBase returns NaN before the base series starts", () => {
  const base = [candle(600, 100, 10)];
  const display = [{ time: 0, open: 100, high: 101, low: 99, close: 100, volume: 10 }];
  const aligned = vwapFromBase(display, base, () => "session", 300);
  assert.ok(Number.isNaN(aligned.values[0]));
});

test("vwapFromBase tolerates empty or malformed input", () => {
  assert.deepEqual(vwapFromBase([], [], () => "s", 300).values, []);
  const display = [{ time: 0, open: 100, high: 101, low: 99, close: 100, volume: 10 }];
  const aligned = vwapFromBase(display, null as any, () => "s", 300);
  assert.ok(Number.isNaN(aligned.values[0]));
  assert.equal(aligned.baseFirstTime, 0);
});

test("vwapFromBase matches the 1m VWAP on an aggregated session", () => {
  const start = 0;
  const base = Array.from({ length: 60 }, (_, i) => ({
    time: start + i * 60,
    open: 100 + i,
    high: 100 + i + 2,
    low: 100 + i - 2,
    close: 100 + i + 1,
    volume: 100 + ((i * 37) % 900),
  }));
  const aggregate = (seconds) => {
    const bars = [];
    for (const bar of base) {
      const bucket = start + Math.floor((bar.time - start) / seconds) * seconds;
      const last = bars[bars.length - 1];
      if (!last || last.time !== bucket) {
        bars.push({ time: bucket, open: bar.open, high: bar.high, low: bar.low, close: bar.close, volume: bar.volume });
      } else {
        last.high = Math.max(last.high, bar.high);
        last.low = Math.min(last.low, bar.low);
        last.close = bar.close;
        last.volume += bar.volume;
      }
    }
    return bars;
  };
  const truth = vwapSeries(base, () => "session");
  const expected = truth[truth.length - 1];
  for (const seconds of [300, 900, 3600]) {
    const display = aggregate(seconds);
    const projected = vwapFromBase(display, base, () => "session", seconds);
    near(projected.values[projected.values.length - 1], expected, 1e-6);
    // The coarse recomputation from the aggregated bars cannot be this exact.
    const coarse = vwapSeries(display, () => "session");
    assert.notEqual(coarse[coarse.length - 1], expected);
  }
});

test("vwapFromBase keeps cumulative totals advancing past the base", () => {
  // Base stops after the first 5m bar; later display bars must keep accumulating
  // instead of reusing the frozen base totals.
  const base = [candle(0, 100, 10), candle(60, 100, 10), candle(120, 100, 10)];
  const display = [
    { time: 0, open: 100, high: 101, low: 99, close: 100, volume: 30 },
    { time: 300, open: 200, high: 201, low: 199, close: 200, volume: 10 },
    { time: 600, open: 300, high: 301, low: 299, close: 300, volume: 10 },
  ];
  const aligned = vwapFromBase(display, base, () => "session", 300);
  near(aligned.values[0], 100);
  assert.ok(aligned.cumulativeVolume[1] > aligned.cumulativeVolume[0]);
  assert.ok(aligned.cumulativeVolume[2] > aligned.cumulativeVolume[1]);
  near(aligned.cumulativeVolume[2], 50);
  near(aligned.values[2], (100 * 30 + 200 * 10 + 300 * 10) / 50);
});

test("vwapFromBase reports the current session's first bar, not the oldest", () => {
  const sessionFor = (time) => (time < 1000 ? "day-1" : "day-2");
  const base = [
    candle(0, 100, 10),
    candle(60, 100, 10),
    candle(1000, 300, 10), // day 2 starts at 12:00, hours after the open
    candle(1060, 300, 10),
  ];
  const display = [
    { time: 0, open: 100, high: 101, low: 99, close: 100, volume: 20 },
    { time: 1000, open: 300, high: 301, low: 299, close: 300, volume: 20 },
  ];
  const aligned = vwapFromBase(display, base, sessionFor, 60);
  assert.equal(aligned.baseFirstTime, 0);
  assert.equal(aligned.lastSessionFirstTime, 1000);
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
