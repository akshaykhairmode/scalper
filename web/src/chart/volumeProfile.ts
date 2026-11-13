// Volume profile + suggested entry zones for the option charts.
//
// Mirrors the "fixed range volume profile" workflow: bucket traded volume by
// price, find the point of control (POC) and the 70% value area, then propose
// a buy zone on the acceptance shelf below the POC and a sell zone above it.
//
// Pure functions only: candles in, levels out. The chart wrapper in
// `charts.ts` draws the result and `app.ts` renders the badge text.

export interface ProfileCandle {
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume?: number;
}

export interface ProfileNode {
  /** Bucket midpoint, used for labels and POC. */
  price: number;
  low: number;
  high: number;
  volume: number;
  buy: number;
  sell: number;
}

export interface VolumeProfile {
  low: number;
  high: number;
  bucketSize: number;
  nodes: ProfileNode[];
  total: number;
  /** Price with the most traded volume. */
  poc: number;
  /** Value area high / low (70% of volume by default). */
  vah: number;
  val: number;
  mean: number;
  /** High volume nodes: prices the market accepted. */
  hvn: number[];
  /** Low volume nodes: prices the market rejected quickly. */
  lvn: number[];
  candles: number;
}

export interface PriceRange {
  low: number;
  high: number;
}

export interface VolumeSuggestion {
  bias: "long" | "short" | "none";
  buy: PriceRange;
  sell: PriceRange;
  stop: number;
  target: number;
  note: string;
}

export interface ProfileOptions {
  buckets?: number;
  lookback?: number;
  valueArea?: number;
  hvnFactor?: number;
  lvnFactor?: number;
}

const DEFAULTS = {
  buckets: 48,
  lookback: 300,
  valueArea: 0.7,
  hvnFactor: 1.3,
  lvnFactor: 0.4,
};

const clampIndex = (value: number, max: number) =>
  value < 0 ? 0 : value > max ? max : value;

/**
 * Build a price-bucketed volume profile from candles.
 *
 * Each candle's volume is spread across the price buckets its high-low range
 * overlaps, proportional to the overlap. That is the standard approximation
 * used by volume-profile indicators when only one timeframe is available.
 *
 * Returns null when there is nothing meaningful to draw (no volume, fewer than
 * five candles, or a zero-height range).
 */
export function buildVolumeProfile(
  candles: ProfileCandle[] | null | undefined,
  options: ProfileOptions = {},
): VolumeProfile | null {
  const buckets = Math.max(8, Math.floor(options.buckets ?? DEFAULTS.buckets));
  const lookback = Math.max(8, Math.floor(options.lookback ?? DEFAULTS.lookback));
  const valueArea = Math.min(0.95, Math.max(0.3, options.valueArea ?? DEFAULTS.valueArea));
  const hvnFactor = options.hvnFactor ?? DEFAULTS.hvnFactor;
  const lvnFactor = options.lvnFactor ?? DEFAULTS.lvnFactor;

  if (!Array.isArray(candles)) return null;

  const source = candles
    .filter((candle) =>
      candle &&
      Number.isFinite(candle.open) &&
      Number.isFinite(candle.high) &&
      Number.isFinite(candle.low) &&
      Number.isFinite(candle.close) &&
      candle.high >= candle.low &&
      candle.low > 0 &&
      Number.isFinite(Number(candle.volume)) &&
      Number(candle.volume) > 0)
    .slice(-lookback);
  if (source.length < 5) return null;

  let low = Infinity;
  let high = -Infinity;
  for (const candle of source) {
    if (candle.low < low) low = candle.low;
    if (candle.high > high) high = candle.high;
  }
  if (!Number.isFinite(low) || !Number.isFinite(high) || !(high > low)) return null;

  const bucketSize = (high - low) / buckets;
  const nodes: ProfileNode[] = Array.from({ length: buckets }, (_, i) => ({
    low: low + i * bucketSize,
    high: low + (i + 1) * bucketSize,
    price: low + (i + 0.5) * bucketSize,
    volume: 0,
    buy: 0,
    sell: 0,
  }));

  for (const candle of source) {
    const volume = Number(candle.volume);
    const span = candle.high - candle.low;
    const rising = candle.close >= candle.open;
    if (!(span > 0)) {
      const index = clampIndex(Math.floor((candle.close - low) / bucketSize), buckets - 1);
      nodes[index].volume += volume;
      if (rising) nodes[index].buy += volume;
      else nodes[index].sell += volume;
      continue;
    }
    const first = clampIndex(Math.floor((candle.low - low) / bucketSize), buckets - 1);
    const last = clampIndex(Math.floor((candle.high - low) / bucketSize), buckets - 1);
    for (let i = first; i <= last; i++) {
      const node = nodes[i];
      const overlap = Math.min(candle.high, node.high) - Math.max(candle.low, node.low);
      if (overlap <= 0) continue;
      const share = volume * (overlap / span);
      node.volume += share;
      if (rising) node.buy += share;
      else node.sell += share;
    }
  }

  const total = nodes.reduce((sum, node) => sum + node.volume, 0);
  if (!(total > 0)) return null;

  let pocIndex = 0;
  for (let i = 1; i < buckets; i++) {
    if (nodes[i].volume > nodes[pocIndex].volume) pocIndex = i;
  }

  // Grow the value area outward from the POC, always taking the richer
  // neighbouring bucket, until it holds the target share of volume.
  let lower = pocIndex;
  let upper = pocIndex;
  let accumulated = nodes[pocIndex].volume;
  const wanted = total * valueArea;
  while (accumulated < wanted && (lower > 0 || upper < buckets - 1)) {
    const below = lower > 0 ? nodes[lower - 1].volume : -1;
    const above = upper < buckets - 1 ? nodes[upper + 1].volume : -1;
    if (below < 0 && above < 0) break;
    if (above >= below) {
      upper += 1;
      accumulated += nodes[upper].volume;
    } else {
      lower -= 1;
      accumulated += nodes[lower].volume;
    }
  }

  const mean = total / buckets;
  return {
    low,
    high,
    bucketSize,
    nodes,
    total,
    poc: nodes[pocIndex].price,
    vah: nodes[upper].high,
    val: nodes[lower].low,
    mean,
    hvn: nodes.filter((node) => node.volume >= mean * hvnFactor).map((node) => node.price),
    lvn: nodes.filter((node) => node.volume <= mean * lvnFactor).map((node) => node.price),
    candles: source.length,
  };
}

/**
 * Turn a profile into a buy zone, a sell zone, a stop and a target.
 *
 * The rule is the one the profile implies: trade the retest of the acceptance
 * shelf (POC) in the direction price is already leaning, keep the stop just
 * outside the value area, and target the opposite value-area edge.
 */
export function suggestVolumeRanges(
  profile: VolumeProfile | null,
  lastPrice: number,
): VolumeSuggestion | null {
  if (!profile || !Number.isFinite(lastPrice) || lastPrice <= 0) return null;
  const { poc, vah, val, bucketSize } = profile;
  if (!(poc > 0) || !(vah > val)) return null;

  const height = Math.max(vah - val, bucketSize * 3);
  const buy: PriceRange = { low: val, high: poc };
  const sell: PriceRange = { low: poc, high: vah };
  const buffer = height * 0.25;

  if (lastPrice > vah + height * 1.5) {
    return {
      bias: "none",
      buy,
      sell,
      stop: val - buffer,
      target: vah,
      note: "Overextended above value — wait for a pullback to the POC",
    };
  }
  if (lastPrice < val - height * 1.5) {
    return {
      bias: "none",
      buy,
      sell,
      stop: vah + buffer,
      target: val,
      note: "Overextended below value — wait for a bounce to the POC",
    };
  }
  if (lastPrice >= poc) {
    return {
      bias: "long",
      buy,
      sell,
      stop: val - buffer,
      target: vah,
      note: "Above the POC — buy the retest of the value shelf",
    };
  }
  return {
    bias: "short",
    buy,
    sell,
    stop: vah + buffer,
    target: val,
    note: "Below the POC — sell the retest of the value shelf",
  };
}

/** Compact "12.40–14.20" label used by the pane badge. */
export function formatRange(range: PriceRange | null | undefined, decimals = 2): string {
  if (!range || !Number.isFinite(range.low) || !Number.isFinite(range.high)) return "—";
  const digits = Number.isInteger(decimals) && decimals >= 0 ? Math.min(decimals, 6) : 2;
  const lo = Math.min(range.low, range.high);
  const hi = Math.max(range.low, range.high);
  return `${lo.toFixed(digits)}–${hi.toFixed(digits)}`;
}

/** Single-price label ("12.40") — avoids the "12.40–12.40" range for levels. */
export function formatPrice(value: number | null | undefined, decimals = 2): string {
  if (!Number.isFinite(Number(value))) return "—";
  const digits = Number.isInteger(decimals) && decimals >= 0 ? Math.min(decimals, 6) : 2;
  return Number(value).toFixed(digits);
}
