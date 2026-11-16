/**
 * Market-session helpers shared by the chart indicator and candle code.
 *
 * IST is a fixed +05:30 offset with no daylight saving, so a numeric day index
 * is both exact and cheap enough to run inside per-tick indicator recalculations.
 */
export const IST_OFFSET_MS = 19_800_000;
export const DAY_MS = 86_400_000;

const toMillis = (time: number) => (time > 1e11 ? time : time * 1000);

/** Numeric IST calendar-day index. */
export const istSessionKey = (time: number) => Math.floor((toMillis(time) + IST_OFFSET_MS) / DAY_MS);
