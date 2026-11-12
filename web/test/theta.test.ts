import { test } from "node:test";
import assert from "node:assert/strict";
import { buildThetaSeries } from "../src/chart/theta.ts";

const near = (actual, expected, tolerance = 0.01) =>
  assert.ok(Math.abs(actual - expected) <= tolerance, `expected ${expected}, got ${actual}`);

function normCdf(x) {
  const t = 1 / (1 + 0.2316419 * Math.abs(x));
  const d = 0.3989423 * Math.exp(-0.5 * x * x);
  const p = d * t * (0.3193815 + t * (-0.3565638 + t * (1.781478 + t * (-1.821256 + t * 1.330274))));
  return x > 0 ? 1 - p : p;
}

function bsPrice(type, spot, strike, years, rate, vol) {
  const sqrtT = Math.sqrt(years);
  const d1 = (Math.log(spot / strike) + (rate + 0.5 * vol * vol) * years) / (vol * sqrtT);
  const d2 = d1 - vol * sqrtT;
  if (type === "PE") return strike * Math.exp(-rate * years) * normCdf(-d2) - spot * normCdf(-d1);
  return spot * normCdf(d1) - strike * Math.exp(-rate * years) * normCdf(d2);
}

const expiry = "2026-11-12";
const expiryMs = new Date(`${expiry}T15:30:00+05:30`).getTime();
const spot = 25000;
const strike = 25000;
const rate = 0.065;
const vol = 0.18;

function candleAt(secondsBeforeExpiry) {
  const time = Math.round((expiryMs - secondsBeforeExpiry * 1000) / 1000);
  const years = (expiryMs - time * 1000) / (365 * 24 * 60 * 60 * 1000);
  return {
    time,
    years,
    ce: bsPrice("CE", spot, strike, years, rate, vol),
    pe: bsPrice("PE", spot, strike, years, rate, vol),
  };
}

test("backs out IV and produces negative cumulative decay for CE and PE", () => {
  const first = candleAt(2 * 24 * 60 * 60);
  const second = candleAt(2 * 24 * 60 * 60 - 300);
  const result = buildThetaSeries({
    spotCandles: [{ time: first.time, close: spot }, { time: second.time, close: spot }],
    ceCandles: [{ time: first.time, close: first.ce }, { time: second.time, close: second.ce }],
    peCandles: [{ time: first.time, close: first.pe }, { time: second.time, close: second.pe }],
    strike,
    expiry,
    ceLotSize: 75,
    peLotSize: 75,
    riskFreeRate: rate,
    barSeconds: 300,
    sessionSeconds: 6.25 * 60 * 60,
  });

  assert.ok(result.meta);
  near(result.meta.ceIv, vol, 0.005);
  near(result.meta.peIv, vol, 0.005);
  assert.ok(result.meta.ceTheta < 0, `CE theta should be negative, got ${result.meta.ceTheta}`);
  assert.ok(result.meta.peTheta < 0, `PE theta should be negative, got ${result.meta.peTheta}`);
  assert.ok(result.meta.ceDecay < 0, `CE cumulative decay should be negative, got ${result.meta.ceDecay}`);
  assert.ok(result.meta.peDecay < 0, `PE cumulative decay should be negative, got ${result.meta.peDecay}`);
});

test("stale option price keeps using the last valid IV", () => {
  const first = candleAt(3 * 24 * 60 * 60);
  const second = candleAt(3 * 24 * 60 * 60 - 300);
  const result = buildThetaSeries({
    spotCandles: [{ time: first.time, close: spot }, { time: second.time, close: spot }],
    ceCandles: [{ time: first.time, close: first.ce }],
    peCandles: [{ time: first.time, close: first.pe }],
    strike,
    expiry,
    ceLotSize: 75,
    peLotSize: 75,
    riskFreeRate: rate,
    barSeconds: 300,
    sessionSeconds: 6.25 * 60 * 60,
  });
  const point = result.byTime.get(second.time);
  assert.ok(point);
  assert.ok(Number.isFinite(point.ceTheta));
  assert.ok(Number.isFinite(point.peTheta));
});

test("prices below intrinsic do not produce a theta estimate", () => {
  const at = candleAt(24 * 60 * 60);
  const result = buildThetaSeries({
    spotCandles: [{ time: at.time, close: 27000 }],
    ceCandles: [{ time: at.time, close: 1 }],
    peCandles: [{ time: at.time, close: at.pe }],
    strike: 25000,
    expiry,
    ceLotSize: 75,
    peLotSize: 75,
    riskFreeRate: rate,
    barSeconds: 300,
    sessionSeconds: 6.25 * 60 * 60,
  });
  const point = result.byTime.get(at.time);
  assert.ok(point);
  assert.equal(point.ceTheta, null);
  assert.equal(point.ce, null);
});

test("expired contracts are skipped", () => {
  const result = buildThetaSeries({
    spotCandles: [{ time: Math.round(expiryMs / 1000) + 60, close: spot }],
    ceCandles: [],
    peCandles: [],
    strike,
    expiry,
    riskFreeRate: rate,
    barSeconds: 300,
    sessionSeconds: 6.25 * 60 * 60,
  });
  const point = [...result.byTime.values()][0];
  assert.ok(point);
  assert.equal(point.ce, null);
  assert.equal(point.pe, null);
});

test("missing expiry or strike returns an empty series", () => {
  const result = buildThetaSeries({
    spotCandles: [{ time: 1793000000, close: spot }],
    ceCandles: [],
    peCandles: [],
    strike: 0,
    expiry: "",
  });
  assert.equal(result.byTime.size, 0);
  assert.equal(result.meta, null);
});
