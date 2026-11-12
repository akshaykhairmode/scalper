import { test } from "node:test";
import assert from "node:assert/strict";
import { calculateFibLevels } from "../src/chart/fibonacci.ts";

const near = (actual, expected, tolerance = 0.5) =>
  assert.ok(Math.abs(actual - expected) <= tolerance, `expected ${expected}, got ${actual}`);

function trendCandles(start, step, count = 30) {
  return Array.from({ length: count }, (_, i) => {
    const high = start + step * i;
    const low = high - 1;
    return {
      time: 1_792_000_000 + i * 300,
      open: low + 0.5,
      high,
      low,
      close: high - 0.5,
    };
  });
}

test("fallback swing builds retracement levels for an up leg", () => {
  const fib = calculateFibLevels(trendCandles(90, 1));
  assert.ok(fib);
  assert.equal(fib.direction, "up");
  assert.equal(fib.levels.length, 7);
  near(fib.levels[0].price, 119);
  near(fib.levels[6].price, 89);
  near(fib.levels[3].price, 104);
  assert.equal(fib.levels[0].label, "0%");
  assert.equal(fib.levels[6].label, "100%");
});

test("fallback swing builds retracement levels for a down leg", () => {
  const fib = calculateFibLevels(trendCandles(119, -1));
  assert.ok(fib);
  assert.equal(fib.direction, "down");
  near(fib.levels[0].price, 89);
  near(fib.levels[6].price, 119);
});

test("golden pocket levels are marked", () => {
  const fib = calculateFibLevels(trendCandles(90, 1));
  assert.ok(fib);
  const golden = fib.levels.filter((level) => level.golden).map((level) => level.ratio);
  assert.deepEqual(golden, [0.382, 0.5, 0.618]);
});

test("not enough candles returns no levels", () => {
  assert.equal(calculateFibLevels([]), null);
  assert.equal(calculateFibLevels(trendCandles(90, 1, 5)), null);
});
