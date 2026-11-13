import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildVolumeProfile,
  formatPrice,
  formatRange,
  suggestVolumeRanges,
} from "../src/chart/volumeProfile.ts";

// Dense shelf at 104-106 (30 bars x 1000) plus a thin tail up to 120 (10 bars
// x 100). Total volume is 31000 and the profile spans 104..120, so with 16
// buckets each bucket is exactly one price point wide.
function shelfCandles() {
  const candles = [];
  for (let i = 0; i < 30; i++) {
    candles.push({ time: 1_792_000_000 + i * 300, open: 105, high: 106, low: 104, close: 105.5, volume: 1000 });
  }
  for (let i = 0; i < 10; i++) {
    candles.push({ time: 1_792_000_000 + (30 + i) * 300, open: 110 + i, high: 111 + i, low: 109 + i, close: 110.5 + i, volume: 100 });
  }
  return candles;
}

const near = (actual, expected, tolerance = 1e-6) =>
  assert.ok(Math.abs(actual - expected) <= tolerance, `expected ${expected}, got ${actual}`);

test("returns null when there is nothing to profile", () => {
  assert.equal(buildVolumeProfile(null), null);
  assert.equal(buildVolumeProfile([]), null);
  assert.equal(buildVolumeProfile([{ time: 1, open: 1, high: 2, low: 1, close: 2, volume: 0 }]), null);
  const noVolume = shelfCandles().map((c) => ({ ...c, volume: 0 }));
  assert.equal(buildVolumeProfile(noVolume), null);
});

test("returns null for a zero-height range", () => {
  const flat = Array.from({ length: 10 }, (_, i) => ({
    time: 1_792_000_000 + i * 300, open: 100, high: 100, low: 100, close: 100, volume: 50,
  }));
  assert.equal(buildVolumeProfile(flat), null);
});

test("drops non-finite volume, non-finite open, and non-positive prices", () => {
  const base = shelfCandles();
  const dirty = [
    ...base,
    { time: 1, open: 100, high: 101, low: 100, close: 101, volume: Number.POSITIVE_INFINITY },
    { time: 2, open: Number.NaN, high: 101, low: 100, close: 101, volume: 500 },
    { time: 3, open: 0, high: 1, low: 0, close: 1, volume: 500 },
    { time: 4, open: 100, high: 99, low: 100, close: 99, volume: 500 },
  ];
  const profile = buildVolumeProfile(dirty, { buckets: 16 });
  assert.ok(profile);
  near(profile.total, 31000);
});

test("accounts for every unit of candle volume", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16 });
  assert.ok(profile);
  near(profile.total, 31000);
  // The shelf candles split evenly across the two buckets they span.
  near(profile.nodes[0].volume, 15000, 1e-3);
  near(profile.nodes[1].volume, 15000, 1e-3);
  const summed = profile.nodes.reduce((sum, node) => sum + node.volume, 0);
  near(summed, 31000);
});

test("splits buy and sell volume by candle direction", () => {
  const rising = buildVolumeProfile(shelfCandles(), { buckets: 16 });
  assert.ok(rising);
  near(rising.nodes[0].sell, 0);

  const falling = shelfCandles().map((c) => ({ ...c, close: c.open - 1, open: c.open }));
  const profile = buildVolumeProfile(falling, { buckets: 16 });
  assert.ok(profile);
  assert.ok(profile.nodes[0].sell > 0);
  near(profile.nodes[0].buy, 0);
  for (const node of profile.nodes) near(node.volume, node.buy + node.sell);
});

test("POC lands on the dense shelf and the value area brackets it", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16 });
  assert.ok(profile);
  assert.ok(profile.poc > 103 && profile.poc < 107, `POC was ${profile.poc}`);
  assert.ok(profile.val <= profile.poc);
  assert.ok(profile.vah >= profile.poc);
  assert.ok(profile.val >= profile.low);
  assert.ok(profile.vah <= profile.high);
});

test("value area covers at least the requested share of volume", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16, valueArea: 0.7 });
  assert.ok(profile);
  const inside = profile.nodes
    .filter((node) => node.high > profile.val && node.low < profile.vah)
    .reduce((sum, node) => sum + node.volume, 0);
  assert.ok(inside / profile.total >= 0.69, `value area held ${inside / profile.total}`);
});

test("flags high and low volume nodes", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16 });
  assert.ok(profile);
  assert.ok(profile.hvn.length > 0);
  assert.ok(profile.lvn.length > 0);
  assert.ok(profile.hvn.some((price) => price > 103 && price < 107));
  assert.ok(!profile.hvn.some((price) => price > 115));
});

test("clamps bucket count and value-area share", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 2 });
  assert.ok(profile);
  assert.equal(profile.nodes.length, 8);

  const wide = buildVolumeProfile(shelfCandles(), { buckets: 16, valueArea: 0.99 });
  assert.ok(wide);
  // valueArea is clamped to 0.95, so a 99% request still only guarantees 95%.
  const covered = wide.nodes
    .filter((node) => node.high > wide.val && node.low < wide.vah)
    .reduce((sum, node) => sum + node.volume, 0);
  assert.ok(covered / wide.total >= 0.95);
});

test("lookback keeps the most recent traded bars", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16, lookback: 10 });
  assert.ok(profile);
  assert.equal(profile.candles, 10);
  assert.ok(profile.low >= 109);
});

test("suggested zones sit either side of the POC", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16 });
  const suggestion = suggestVolumeRanges(profile, 112);
  assert.ok(suggestion);
  assert.equal(suggestion.buy.high, profile.poc);
  assert.equal(suggestion.buy.low, profile.val);
  assert.equal(suggestion.sell.low, profile.poc);
  assert.equal(suggestion.sell.high, profile.vah);
});

test("bias follows the POC and over-extension disables it", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16 });
  assert.ok(profile);
  assert.equal(suggestVolumeRanges(profile, profile.poc + 1)?.bias, "long");
  assert.equal(suggestVolumeRanges(profile, profile.poc - 1)?.bias, "short");

  const height = Math.max(profile.vah - profile.val, profile.bucketSize * 3);
  // The over-extension test is a strict inequality.
  assert.equal(suggestVolumeRanges(profile, profile.vah + height * 1.5)?.bias, "long");
  assert.equal(suggestVolumeRanges(profile, profile.vah + height * 1.5 + 0.01)?.bias, "none");
  assert.equal(suggestVolumeRanges(profile, profile.val - height * 1.5 - 0.01)?.bias, "none");
});

test("stop and target straddle the value area", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16 });
  assert.ok(profile);
  const long = suggestVolumeRanges(profile, profile.poc + 1);
  assert.ok(long);
  assert.ok(long.stop < profile.val);
  assert.equal(long.target, profile.vah);

  const short = suggestVolumeRanges(profile, profile.poc - 1);
  assert.ok(short);
  assert.ok(short.stop > profile.vah);
  assert.equal(short.target, profile.val);
});

test("suggest returns null without a usable profile", () => {
  assert.equal(suggestVolumeRanges(null, 100), null);
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16 });
  assert.equal(suggestVolumeRanges(profile, Number.NaN), null);
  assert.equal(suggestVolumeRanges(profile, 0), null);
});

test("formatRange renders low-high with fixed decimals", () => {
  assert.equal(formatRange({ low: 12.4, high: 14.2 }), "12.40–14.20");
  assert.equal(formatRange({ low: 14.2, high: 12.4 }), "12.40–14.20");
  assert.equal(formatRange({ low: 12.4, high: 14.2 }, 1), "12.4–14.2");
  assert.equal(formatRange({ low: 12.4, high: 14.2 }, -1), "12.40–14.20");
  assert.equal(formatRange(null), "—");
  assert.equal(formatRange({ low: Number.NaN, high: 1 }), "—");
});

test("formatPrice renders a single level without a range", () => {
  assert.equal(formatPrice(103.5), "103.50");
  assert.equal(formatPrice(103.5, 1), "103.5");
  assert.equal(formatPrice(undefined), "—");
  assert.equal(formatPrice(Number.POSITIVE_INFINITY), "—");
});
