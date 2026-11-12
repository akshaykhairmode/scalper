// Automatic Fibonacci retracement levels for the most recent meaningful swing.
//
// The chart already has automatic support/resistance and trendlines, so this
// follows the same model-free approach: find the dominant recent pivot pair
// and project the standard retracement ratios between its high and low.

export interface FibCandle {
  time: number;
  open?: number;
  high: number;
  low: number;
  close: number;
}

export interface FibLevel {
  ratio: number;
  price: number;
  label: string;
  golden: boolean;
  color: string;
}

export interface FibResult {
  high: number;
  low: number;
  direction: "up" | "down";
  startTime: number;
  endTime: number;
  levels: FibLevel[];
}

const RATIOS = [0, 0.236, 0.382, 0.5, 0.618, 0.786, 1];
const BASE_COLOR = "#2dd4bf";
const GOLDEN_COLOR = "#fbbf24";

function formatRatio(ratio: number): string {
  if (ratio === 0) return "0%";
  if (ratio === 1) return "100%";
  return `${(ratio * 100).toFixed(1)}%`;
}

function extractPivots(candles: FibCandle[], { windowSize = 2, maxPivots = 50 } = {}) {
  if (!candles || candles.length < windowSize * 2 + 1) return [];
  const rawPivots = [];
  for (let i = windowSize; i < candles.length - windowSize; i++) {
    const candle = candles[i];
    let isHigh = true;
    let isLow = true;
    for (let j = i - windowSize; j <= i + windowSize; j++) {
      if (j === i) continue;
      if (candles[j].high > candle.high) isHigh = false;
      if (candles[j].low < candle.low) isLow = false;
    }
    if (isHigh && !isLow) {
      rawPivots.push({ index: i, time: candle.time, price: candle.high, type: "HIGH" });
    } else if (isLow && !isHigh) {
      rawPivots.push({ index: i, time: candle.time, price: candle.low, type: "LOW" });
    } else if (isHigh && isLow) {
      const closedUp = Number(candle.close) >= Number(candle.open);
      rawPivots.push({
        index: i,
        time: candle.time,
        price: closedUp ? candle.high : candle.low,
        type: closedUp ? "HIGH" : "LOW",
      });
    }
  }

  const alternating: any[] = [];
  for (const pivot of rawPivots) {
    const last = alternating[alternating.length - 1];
    if (!last) {
      alternating.push(pivot);
    } else if (last.type === pivot.type) {
      if (pivot.type === "HIGH" && pivot.price > last.price) alternating[alternating.length - 1] = pivot;
      else if (pivot.type === "LOW" && pivot.price < last.price) alternating[alternating.length - 1] = pivot;
    } else {
      alternating.push(pivot);
    }
  }
  return alternating.slice(-maxPivots);
}

export function calculateFibLevels(
  candles: FibCandle[],
  { lookback = 160, maxPivots = 20 } = {},
): FibResult | null {
  if (!Array.isArray(candles) || candles.length < 10) return null;
  const slice = candles.length > lookback ? candles.slice(-lookback) : candles;
  const pivots = extractPivots(slice, { windowSize: 2, maxPivots });
  const recent = pivots.slice(-6);

  let high = 0;
  let low = 0;
  let direction: "up" | "down" = "up";
  let startTime = 0;
  let endTime = 0;
  let best: { a: any; b: any; score: number } | null = null;

  for (let i = 0; i < recent.length - 1; i++) {
    const a = recent[i];
    const b = recent[i + 1];
    if (!a || !b || a.type === b.type) continue;
    const diff = Math.abs(Number(b.price) - Number(a.price));
    if (!(diff > 0)) continue;
    // Favour larger swings but give the newest pairs a modest boost so a
    // stale, much older range does not dominate the current leg.
    const recency = (i + 1) / Math.max(1, recent.length - 1);
    const score = diff * (0.7 + 0.3 * recency);
    if (!best || score > best.score) best = { a, b, score };
  }

  if (best) {
    high = Math.max(Number(best.a.price), Number(best.b.price));
    low = Math.min(Number(best.a.price), Number(best.b.price));
    direction = Number(best.a.price) < Number(best.b.price) ? "up" : "down";
    startTime = best.a.time;
    endTime = best.b.time;
  } else {
    // Fallback when the pivot extractor does not find a clean alternating
    // swing: use the highest high and lowest low in the lookback window.
    let highIndex = -1;
    let lowIndex = -1;
    for (let i = 0; i < slice.length; i++) {
      const candle = slice[i];
      const candleHigh = Number(candle.high);
      const candleLow = Number(candle.low);
      if (!Number.isFinite(candleHigh) || !Number.isFinite(candleLow)) continue;
      if (highIndex < 0 || candleHigh > high) {
        high = candleHigh;
        highIndex = i;
      }
      if (lowIndex < 0 || candleLow < low) {
        low = candleLow;
        lowIndex = i;
      }
    }
    if (highIndex < 0 || lowIndex < 0 || !(high > low)) return null;
    const highFirst = highIndex < lowIndex;
    direction = highFirst ? "down" : "up";
    startTime = slice[highFirst ? highIndex : lowIndex].time;
    endTime = slice[highFirst ? lowIndex : highIndex].time;
  }

  const range = high - low;
  if (!(range > 0)) return null;
  const levels = RATIOS.map((ratio) => {
    const golden = ratio >= 0.382 && ratio <= 0.618;
    return {
      ratio,
      price: direction === "up" ? high - range * ratio : low + range * ratio,
      label: formatRatio(ratio),
      golden,
      color: golden ? GOLDEN_COLOR : BASE_COLOR,
    };
  });

  return { high, low, direction, startTime, endTime, levels };
}
