// Order, Trade, Position, and Margin Limits Type Definitions

export type OrderSide = "BUY" | "SELL" | "B" | "S";
export type OrderType = "LIMIT" | "MARKET" | "SL" | "SL-M";
export type ProductType = "MIS" | "NRML" | "CNC";

export interface OrderItem {
  orderId: string;
  nOrdNo?: string;
  tradingSymbol: string;
  token: string;
  side: OrderSide;
  product: ProductType;
  orderType: OrderType;
  qty: number;
  filledQty: number;
  remainingQty?: number;
  price: number;
  triggerPrice?: number;
  avgPrice?: number;
  status: "OPEN" | "COMPLETE" | "CANCELLED" | "REJECTED" | "TRIGGER_PENDING";
  statusMessage?: string;
  placedTime: string;
  updateTime?: string;
}

export interface TradeItem {
  tradeId: string;
  orderId: string;
  tradingSymbol: string;
  token: string;
  side: OrderSide;
  qty: number;
  price: number;
  tradeTime: string;
  exchangeSegment?: string;
}

export interface PositionItem {
  tradingSymbol: string;
  token: string;
  product: ProductType;
  segment?: string;
  netQty: number;
  buyQty: number;
  buyAvgPrice: number;
  sellQty: number;
  sellAvgPrice: number;
  lastPrice: number;
  pnl: number;
  realizedPnl: number;
  unrealizedPnl: number;
  lotSize?: number;
}

export interface MarginLimits {
  cash: number;
  collateral: number;
  marginUtilized: number;
  availableMargin: number;
  netMargin: number;
}
