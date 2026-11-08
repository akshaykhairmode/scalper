// Market and Option Chain Type Definitions

export interface UnderlierInfo {
  key: string;
  label: string;
  symbol: string;
  token?: string;
  segment?: string;
  lotSize?: number;
}

export interface IndexQuote {
  key: string;
  label: string;
  lastPrice: number;
  change: number;
  changePct: number;
  open?: number;
  high?: number;
  low?: number;
  prevClose?: number;
  updatedAt?: number;
}

export interface OptionLegGreeks {
  delta: number;
  gamma: number;
  theta: number;
  vega: number;
  rho?: number;
  iv?: number;
}

export interface OptionLeg {
  token: string;
  tradingSymbol: string;
  exchangeSegment: string;
  lastPrice: number;
  change: number;
  changePct: number;
  bidPrice: number;
  bidQty: number;
  askPrice: number;
  askQty: number;
  openInterest: number;
  oiChange: number;
  volume: number;
  iv: number;
  greeks?: OptionLegGreeks;
  lotSize: number;
  strikePrice: number;
  optionType: "CE" | "PE";
  expiry: string;
}

export interface OptionRow {
  strikePrice: number;
  call: OptionLeg | null;
  put: OptionLeg | null;
  atm?: boolean;
  straddleLtp?: number;
}

export interface ChainData {
  underlying: string;
  spotPrice: number;
  expiry: string;
  expiries: string[];
  strikes: OptionRow[];
  lotSize: number;
  totalCallOi: number;
  totalPutOi: number;
  pcr: number;
  updatedAt: number;
}
