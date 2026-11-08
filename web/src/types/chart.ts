// Chart and KlineCharts type definitions

export interface Candle {
  timestamp: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume?: number;
  turnover?: number;
}

export interface TradeMarkerData {
  price: number;
  qty: number;
  side: "BUY" | "SELL" | "B" | "S";
  time: number;
  tradeId?: string;
  isSpot?: boolean;
}

export interface Trendline {
  p1: { x: number; y: number };
  p2: { x: number; y: number };
  slope: number;
  touches: number;
  score: number;
  type: "support" | "resistance";
}

declare global {
  interface Window {
    klinecharts?: any;
    currentChart?: any;
    optionChart?: any;
    appState?: any;
  }
}
