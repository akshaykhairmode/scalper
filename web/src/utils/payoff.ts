// At-expiry payoff math for the Positions and Risk tabs.
//
// Quantities are shares (Kotak's net position quantity) and premiums are per
// share. A short leg carries a negative quantity, so a single
// `qty * (intrinsic - avg)` term covers both sides.
//
// The maximum and minimum of an option payoff can only occur at a strike, at
// spot = 0, or at infinity. Sampling those kinks is exact; the unbounded tails
// are decided by the net call quantity.

export type PayoffLeg = { type: string; strike: number; qty: number; avg: number };
export type PayoffPoint = { spot: number; pnl: number };
export type PayoffResult = {
  points: PayoffPoint[];
  maxProfit: number;
  maxLoss: number;
  breakevens: number[];
  unlimitedProfit: boolean;
  unlimitedLoss: boolean;
  /** Open legs dropped because strike or average price was unavailable. */
  excluded: number;
};
export type LegRisk = { maxProfit: number; maxLoss: number } | null;

const num = (value: unknown): number => {
  const n = Number(value);
  return Number.isFinite(n) ? n : 0;
};

function normalizeLeg(leg: PayoffLeg): PayoffLeg {
  return {
    type: String(leg?.type ?? "").toUpperCase(),
    strike: num(leg?.strike),
    qty: num(leg?.qty),
    avg: num(leg?.avg),
  };
}

export function intrinsic(type: string, strike: number, spot: number): number {
  return String(type).toUpperCase() === "CE"
    ? Math.max(0, spot - strike)
    : Math.max(0, strike - spot);
}

export function isOptionLeg(leg: PayoffLeg): boolean {
  const type = String(leg?.type ?? "").toUpperCase();
  return (type === "CE" || type === "PE") && num(leg?.strike) > 0;
}

// A leg can only be projected when the contract metadata resolved: the Scrip
// Master lookup supplies the strike and the broker supplies the average price.
export function isPricedLeg(leg: PayoffLeg): boolean {
  return isOptionLeg(leg) && num(leg?.avg) > 0;
}

export function computePayoff(legs: PayoffLeg[]): PayoffResult {
  const open = (legs || []).map(normalizeLeg).filter((leg) => leg.qty !== 0);
  const clean = open.filter(isPricedLeg);
  const excluded = open.length - clean.length;
  if (!clean.length) {
    return { points: [], maxProfit: 0, maxLoss: 0, breakevens: [], unlimitedProfit: false, unlimitedLoss: false, excluded };
  }

  const strikes = [...new Set(clean.map((leg) => leg.strike))].sort((a, b) => a - b);
  const maxStrike = strikes[strikes.length - 1];
  const map = [...new Set([0, ...strikes, maxStrike * 2])];
  const points = map.map((spot) => ({
    spot,
    pnl: clean.reduce((sum, leg) => sum + leg.qty * (intrinsic(leg.type, leg.strike, spot) - leg.avg), 0),
  }));

  const netCalls = clean.filter((leg) => leg.type === "CE").reduce((sum, leg) => sum + leg.qty, 0);
  const unlimitedProfit = netCalls > 0;
  const unlimitedLoss = netCalls < 0;

  let maxProfit = -Infinity;
  let maxLoss = Infinity;
  for (const point of points) {
    maxProfit = Math.max(maxProfit, point.pnl);
    maxLoss = Math.min(maxLoss, point.pnl);
  }
  if (unlimitedProfit) maxProfit = Infinity;
  if (unlimitedLoss) maxLoss = -Infinity;

  const breakevens = [];
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1];
    const b = points[i];
    if (a.pnl === 0) {
      if (!breakevens.includes(a.spot)) breakevens.push(a.spot);
    }
    if ((a.pnl < 0 && b.pnl > 0) || (a.pnl > 0 && b.pnl < 0)) {
      const ratio = -a.pnl / (b.pnl - a.pnl);
      const be = a.spot + ratio * (b.spot - a.spot);
      if (!breakevens.some((v) => Math.abs(v - be) < 0.001)) breakevens.push(be);
    }
  }
  const last = points[points.length - 1];
  if (last.pnl === 0 && !breakevens.includes(last.spot)) breakevens.push(last.spot);

  return { points, maxProfit, maxLoss, breakevens, unlimitedProfit, unlimitedLoss, excluded };
}

// Dense series for the payoff chart, centred on the live spot. The chart must
// not start at spot 0: for a Nifty option that compresses every kink into a
// few pixels on the right edge.
export function payoffCurve(legs: PayoffLeg[], center: number, rangePct: number, count: number): PayoffPoint[] {
  const clean = (legs || []).map(normalizeLeg).filter((leg) => leg.qty !== 0 && isPricedLeg(leg));
  const mid = num(center);
  if (!clean.length || mid <= 0) return [];
  const pct = Math.min(100, Math.max(1, num(rangePct) || 10));
  const steps = Math.min(1000, Math.max(2, Math.round(num(count)) || 121));
  const span = (mid * pct) / 100;
  const lo = Math.max(0, mid - span);
  const hi = mid + span;
  const out: PayoffPoint[] = [];
  for (let i = 0; i < steps; i++) {
    const spot = lo + ((hi - lo) * i) / (steps - 1);
    out.push({
      spot,
      pnl: clean.reduce((sum, leg) => sum + leg.qty * (intrinsic(leg.type, leg.strike, spot) - leg.avg), 0),
    });
  }
  return out;
}

// Per-leg max profit/loss if that leg alone ran to expiry. Losses are
// negative, matching the group-level figures.
export function legRisk(position: any): LegRisk {
  const quantity = num(position?.quantity);
  const qty = Math.abs(quantity);
  const avg = num(position?.average_price);
  const strike = num(position?.strike);
  const type = String(position?.option_type ?? "").toUpperCase();
  if (qty <= 0 || avg <= 0) return null;

  const long = quantity > 0;
  if (type === "CE" || type === "PE") {
    if (strike <= 0) return null;
    if (type === "CE") {
      return long
        ? { maxProfit: Infinity, maxLoss: -avg * qty }
        : { maxProfit: avg * qty, maxLoss: -Infinity };
    }
    return long
      ? { maxProfit: (strike - avg) * qty, maxLoss: -avg * qty }
      : { maxProfit: avg * qty, maxLoss: -(strike - avg) * qty };
  }

  // Equity/ETF/futures leg: a long position risks its cost and can run
  // indefinitely; a short position is the mirror image.
  return long
    ? { maxProfit: Infinity, maxLoss: -avg * qty }
    : { maxProfit: avg * qty, maxLoss: -Infinity };
}
