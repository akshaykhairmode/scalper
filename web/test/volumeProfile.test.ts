import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildVolumeProfile,
  formatRange,
  suggestVolumeRanges,
} from "../src/chart/volumeProfile.ts";

// Dense shelf around 105, thin tail up to 120.
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

test("buckets preserve total volume and split buy from sell", () => {
  const profile = buildVolumeProfile(shelfCandles(), { buckets: 16 });
  assert.ok(profile);
  const total = profile.nodes.reduce((sum, node) => sum + node.volume, 0);
  assert.ok(Math.abs(total - profile.total) < 1e-6);
  for (const node of profile.nodes) {
    assert.ok(Math.abs(node.volume - (node.buy + node.sell)) < 1e-6);
  }
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
  const farAbove = suggestVolumeRanges(profile, profile.vah + height * 2);
  assert.equal(farAbove?.bias, "none");
  const farBelow = suggestVolumeRanges(profile, profile.val - height * 2);
  assert.equal(farBelow?.bias, "none");
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
  assert.equal(formatRange(null), "—");
});
