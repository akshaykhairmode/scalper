import { test } from "node:test";
import assert from "node:assert/strict";
import { computePayoff, legRisk, payoffCurve, isOptionLeg, isPricedLeg } from "../src/utils/payoff.ts";

const CE = (strike, qty, avg) => ({ type: "CE", strike, qty, avg });
const PE = (strike, qty, avg) => ({ type: "PE", strike, qty, avg });
const near = (actual, expected, tolerance = 0.01) =>
  assert.ok(Math.abs(actual - expected) <= tolerance, `expected ${expected}, got ${actual}`);

test("long call: unlimited upside, loss capped at premium", () => {
  const p = computePayoff([CE(25000, 75, 100)]);
  assert.equal(p.maxProfit, Infinity);
  assert.equal(p.maxLoss, -7500);
  assert.equal(p.unlimitedProfit, true);
  assert.equal(p.unlimitedLoss, false);
  assert.equal(p.excluded, 0);
});

test("short call: premium capped, unlimited upside risk", () => {
  const p = computePayoff([CE(25000, -75, 100)]);
  assert.equal(p.maxProfit, 7500);
  assert.equal(p.maxLoss, -Infinity);
  assert.equal(p.unlimitedProfit, false);
  assert.equal(p.unlimitedLoss, true);
});

test("long put: profit capped at strike minus premium", () => {
  const p = computePayoff([PE(25000, 75, 100)]);
  assert.equal(p.maxProfit, 1867500);
  assert.equal(p.maxLoss, -7500);
});

test("short put: loss is negative and matches the group convention", () => {
  const p = computePayoff([PE(25000, -75, 100)]);
  assert.equal(p.maxProfit, 7500);
  assert.equal(p.maxLoss, -1867500);
});

test("bull call spread: defined risk on both sides", () => {
  const p = computePayoff([CE(25000, 75, 100), CE(26000, -75, 40)]);
  assert.equal(p.maxProfit, 70500);
  assert.equal(p.maxLoss, -4500);
  assert.equal(p.breakevens.length, 1);
  near(p.breakevens[0], 25060);
});

test("iron condor: finite max profit and max loss", () => {
  const p = computePayoff([CE(26000, -75, 100), CE(27000, 75, 40), PE(24000, -75, 100), PE(23000, 75, 40)]);
  assert.equal(p.maxProfit, 9000);
  assert.equal(p.maxLoss, -66000);
  assert.equal(p.unlimitedProfit, false);
  assert.equal(p.unlimitedLoss, false);
});

test("short strangle: unlimited loss, finite credit", () => {
  const p = computePayoff([CE(26000, -75, 100), PE(24000, -75, 100)]);
  assert.equal(p.maxProfit, 15000);
  assert.equal(p.maxLoss, -Infinity);
});

test("1x2 call ratio: unlimited loss despite a long leg", () => {
  const p = computePayoff([CE(25000, 75, 100), CE(26000, -150, 40)]);
  assert.equal(p.maxProfit, 73500);
  assert.equal(p.maxLoss, -Infinity);
});

test("unresolved contract metadata is excluded instead of corrupting the group", () => {
  // The Scrip Master miss leaves strike/avg empty; before the guard this
  // produced a fake short call at strike 0 and a negative max profit.
  const p = computePayoff([CE(25000, 75, 100), { type: "CE", strike: 0, qty: -75, avg: 0 }]);
  assert.equal(p.excluded, 1);
  assert.equal(p.maxProfit, Infinity);
  assert.equal(p.maxLoss, -7500);
  assert.ok(p.points.every((point) => Number.isFinite(point.spot) && point.spot >= 0));
});

test("a leg with no average price is excluded, not treated as free", () => {
  const p = computePayoff([CE(25000, 75, 0)]);
  assert.equal(p.excluded, 1);
  assert.equal(p.maxProfit, 0);
  assert.equal(p.maxLoss, 0);
  assert.equal(p.points.length, 0);
});

test("closed legs do not count as excluded", () => {
  const p = computePayoff([CE(25000, 0, 100), CE(25000, 75, 100)]);
  assert.equal(p.excluded, 0);
  assert.equal(p.maxLoss, -7500);
});

test("per-leg max loss is negative and matches the group for a single leg", () => {
  const group = computePayoff([CE(25000, 75, 100)]);
  const leg = legRisk({ quantity: 75, average_price: 100, strike: 25000, option_type: "CE" });
  assert.equal(leg.maxProfit, group.maxProfit);
  assert.equal(leg.maxLoss, group.maxLoss);
});

test("per-leg short put max loss is negative", () => {
  const leg = legRisk({ quantity: -75, average_price: 100, strike: 25000, option_type: "PE" });
  assert.equal(leg.maxProfit, 7500);
  assert.equal(leg.maxLoss, -1867500);
});

test("per-leg risk is unavailable when the leg is closed or unpriced", () => {
  assert.equal(legRisk({ quantity: 0, average_price: 100, strike: 25000, option_type: "CE" }), null);
  assert.equal(legRisk({ quantity: 75, average_price: 0, strike: 25000, option_type: "CE" }), null);
  assert.equal(legRisk({ quantity: 75, average_price: 100, strike: 0, option_type: "CE" }), null);
});

test("non-option legs use spot exposure instead of put math", () => {
  const long = legRisk({ quantity: 10, average_price: 93, strike: 0, option_type: "" });
  assert.equal(long.maxProfit, Infinity);
  assert.equal(long.maxLoss, -930);
  const short = legRisk({ quantity: -10, average_price: 93, strike: 0, option_type: "" });
  assert.equal(short.maxProfit, 930);
  assert.equal(short.maxLoss, -Infinity);
});

test("payoff curve is centred on spot and bounded by the configured range", () => {
  const curve = payoffCurve([CE(25000, 75, 100)], 25000, 10, 121);
  assert.equal(curve.length, 121);
  near(curve[0].spot, 22500);
  near(curve[curve.length - 1].spot, 27500);
  near(curve[60].spot, 25000);
  near(curve[0].pnl, -7500);
  near(curve[curve.length - 1].pnl, 75 * (2500 - 100));
});

test("payoff curve is empty without a spot", () => {
  assert.deepEqual(payoffCurve([CE(25000, 75, 100)], 0, 10, 121), []);
});

test("leg classification helpers", () => {
  assert.equal(isOptionLeg(CE(25000, 75, 100)), true);
  assert.equal(isOptionLeg({ type: "", strike: 0, qty: 10, avg: 93 }), false);
  assert.equal(isPricedLeg(CE(25000, 75, 0)), false);
  assert.equal(isPricedLeg(CE(25000, 75, 100)), true);
});
