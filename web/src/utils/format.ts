// Value formatting helpers for Scalper terminal

export const num = (value: unknown, fallback = 0): number => {
  const n = Number(value);
  return Number.isFinite(n) ? n : fallback;
};

export function fmt(value: number, digits = 2): string {
  if (!Number.isFinite(value)) return "—";
  return value.toLocaleString("en-IN", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

export function fmtSigned(value: number, digits = 2): string {
  if (!Number.isFinite(value)) return "—";
  const sign = value > 0 ? "+" : value < 0 ? "−" : "";
  return sign + fmt(Math.abs(value), digits);
}

export function fmtCompact(value: number): string {
  if (!Number.isFinite(value)) return "—";
  if (Math.abs(value) >= 1e7) return `${fmt(value / 1e7, 2)}Cr`;
  if (Math.abs(value) >= 1e5) return `${fmt(value / 1e5, 2)}L`;
  if (Math.abs(value) >= 1e3) return `${fmt(value / 1e3, 1)}K`;
  return fmt(value, 0);
}

export function fmtStrike(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return Number.isInteger(value) ? String(value) : fmt(value, 2);
}

export function directionClass(value: number): string {
  return value > 0 ? "up" : value < 0 ? "down" : "";
}

export function fmtTime(ts: unknown): string {
  if (!ts) return "—";
  const date = typeof ts === "number" ? new Date(ts < 1e12 ? ts * 1000 : ts) : new Date(String(ts));
  return Number.isNaN(date.getTime())
    ? "—"
    : date.toLocaleTimeString("en-IN", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
}

export function formatExpiryDisplay(expiryStr: string): string {
  if (!expiryStr) return "—";
  const clean = expiryStr.trim();
  const parts = clean.split("-");
  if (parts.length === 3) {
    const year = parseInt(parts[0], 10);
    const month = parseInt(parts[1], 10) - 1;
    const day = parseInt(parts[2], 10);
    const d = new Date(year, month, day);
    if (!Number.isNaN(d.getTime())) {
      const dayNum = String(d.getDate()).padStart(2, "0");
      const monthShort = d.toLocaleString("en-US", { month: "short" }).toUpperCase();
      return `${dayNum} ${monthShort}`;
    }
  }
  return clean;
}
