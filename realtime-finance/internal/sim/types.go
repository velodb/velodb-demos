// Package sim is the in-memory simulator: price engine, book, trader,
// calibrator and seeding. It never reads Doris while running; it only emits rows.
package sim

import (
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

// DorisTime marshals as "2006-01-02 15:04:05.000" in UTC, which Doris DATETIME(3) accepts.
type DorisTime time.Time

func (t DorisTime) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Time(t).UTC().Format("2006-01-02 15:04:05.000") + `"`), nil
}

func (t DorisTime) String() string { return time.Time(t).UTC().Format("2006-01-02 15:04:05.000") }

// Emitter receives every row the simulator produces, keyed by table name.
type Emitter interface {
	Emit(table string, row Row)
}

const (
	TablePrices        = "prices"
	TableLatestPrice   = "latest_price"
	TableTrades        = "trades"
	TableBookSnapshots = "book_snapshots"
	TableCalibrations  = "calibrations"
	TableCashouts      = "cashouts"
)

const (
	SideBuy  = "BUY"
	SideSell = "SELL"
)

type PriceRow struct {
	TS           DorisTime       `json:"ts"`
	Symbol       string          `json:"symbol"`
	Price        decimal.Decimal `json:"price"`
	FairPrice    decimal.Decimal `json:"fair_price"`
	RealPos      float64         `json:"real_pos"`
	PerceivedPos float64         `json:"perceived_pos"`
	Dir          int             `json:"dir"`
}

// CashoutRow is cash transferred out of the book (a dividend / capital return).
type CashoutRow struct {
	TS         DorisTime       `json:"ts"`
	CashoutID  int64           `json:"cashout_id"`
	Amount     decimal.Decimal `json:"amount"`
	CashBefore decimal.Decimal `json:"cash_before"`
	NavBefore  decimal.Decimal `json:"nav_before"`
}

func (r CashoutRow) Fields() []string {
	return []string{r.TS.String(), i2s(r.CashoutID), r.Amount.String(), r.CashBefore.String(), r.NavBefore.String()}
}

type LatestPriceRow struct {
	Symbol    string          `json:"symbol"`
	TS        DorisTime       `json:"ts"`
	Price     decimal.Decimal `json:"price"`
	FairPrice decimal.Decimal `json:"fair_price"`
	Name      string          `json:"name"`
	Sector    string          `json:"sector"`
	Weight    float64         `json:"weight"`
}

type TradeRow struct {
	TS            DorisTime       `json:"ts"`
	TradeID       int64           `json:"trade_id"`
	BlockID       *int64          `json:"block_id"`
	SliceNo       *int            `json:"slice_no"`
	Symbol        string          `json:"symbol"`
	Side          string          `json:"side"`
	Qty           int64           `json:"qty"`
	Price         decimal.Decimal `json:"price"`
	Notional      decimal.Decimal `json:"notional"`
	TradeCost     decimal.Decimal `json:"trade_cost"`
	RealizedPnl   decimal.Decimal `json:"realized_pnl"`
	AvgCostAfter  decimal.Decimal `json:"avg_cost_after"`
	DecisionPrice decimal.Decimal `json:"decision_price"`
	PerceivedPos  float64         `json:"perceived_pos"`
	CashAfter     decimal.Decimal `json:"cash_after"`
	BuyChance     float64         `json:"buy_chance"`
	IsSeed        bool            `json:"is_seed"`
}

type SnapshotRow struct {
	TS             DorisTime       `json:"ts"`
	Cash           decimal.Decimal `json:"cash"`
	Equity         decimal.Decimal `json:"equity"`
	Nav            decimal.Decimal `json:"nav"`
	Pnl            decimal.Decimal `json:"pnl"`
	TradeCostTotal decimal.Decimal `json:"trade_cost_total"`
	RealizedTotal  decimal.Decimal `json:"realized_pnl_total"`
	CashRatio      float64         `json:"cash_ratio"`
	BuyChance      float64         `json:"buy_chance"`
	Calibrated     bool            `json:"calibrated"`
}

type CalibrationRow struct {
	TS              DorisTime       `json:"ts"`
	Cash            decimal.Decimal `json:"cash"`
	Equity          decimal.Decimal `json:"equity"`
	CashRatio       float64         `json:"cash_ratio"`
	TargetCashRatio float64         `json:"target_cash_ratio"`
	BuyChanceBefore float64         `json:"buy_chance_before"`
	BuyChanceAfter  float64         `json:"buy_chance_after"`
}

// Columns per table, in the order the JSON keys are declared. Used for Stream Load headers.
var TableColumns = map[string][]string{
	TablePrices:        {"ts", "symbol", "price", "fair_price", "real_pos", "perceived_pos", "dir"},
	TableLatestPrice:   {"symbol", "ts", "price", "fair_price", "name", "sector", "weight"},
	TableTrades:        {"ts", "trade_id", "block_id", "slice_no", "symbol", "side", "qty", "price", "notional", "trade_cost", "realized_pnl", "avg_cost_after", "decision_price", "perceived_pos", "cash_after", "buy_chance", "is_seed"},
	TableBookSnapshots: {"ts", "cash", "equity", "nav", "pnl", "trade_cost_total", "realized_pnl_total", "cash_ratio", "buy_chance", "calibrated"},
	TableCalibrations:  {"ts", "cash", "equity", "cash_ratio", "target_cash_ratio", "buy_chance_before", "buy_chance_after"},
	TableCashouts:      {"ts", "cashout_id", "amount", "cash_before", "nav_before"},
}

// Row is anything the sink can serialise as one CSV line, in TableColumns order.
type Row interface {
	Fields() []string
}

const csvNull = `\N`

func b2s(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func f2s(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
func i2s(i int64) string   { return strconv.FormatInt(i, 10) }

func (r PriceRow) Fields() []string {
	return []string{r.TS.String(), r.Symbol, r.Price.String(), r.FairPrice.String(), f2s(r.RealPos), f2s(r.PerceivedPos), i2s(int64(r.Dir))}
}

func (r LatestPriceRow) Fields() []string {
	return []string{r.Symbol, r.TS.String(), r.Price.String(), r.FairPrice.String(), r.Name, r.Sector, f2s(r.Weight)}
}

func (r TradeRow) Fields() []string {
	blockID, sliceNo := csvNull, csvNull
	if r.BlockID != nil {
		blockID = i2s(*r.BlockID)
	}
	if r.SliceNo != nil {
		sliceNo = i2s(int64(*r.SliceNo))
	}
	return []string{r.TS.String(), i2s(r.TradeID), blockID, sliceNo, r.Symbol, r.Side, i2s(r.Qty),
		r.Price.String(), r.Notional.String(), r.TradeCost.String(), r.RealizedPnl.String(), r.AvgCostAfter.String(),
		r.DecisionPrice.String(), f2s(r.PerceivedPos), r.CashAfter.String(), f2s(r.BuyChance), b2s(r.IsSeed)}
}

func (r SnapshotRow) Fields() []string {
	return []string{r.TS.String(), r.Cash.String(), r.Equity.String(), r.Nav.String(), r.Pnl.String(),
		r.TradeCostTotal.String(), r.RealizedTotal.String(), f2s(r.CashRatio), f2s(r.BuyChance), b2s(r.Calibrated)}
}

func (r CalibrationRow) Fields() []string {
	return []string{r.TS.String(), r.Cash.String(), r.Equity.String(), f2s(r.CashRatio), f2s(r.TargetCashRatio),
		f2s(r.BuyChanceBefore), f2s(r.BuyChanceAfter)}
}
