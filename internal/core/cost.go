package core

// Cost returns the cost of a row in USD. A source-reported cost wins (it is 0
// for free models); otherwise the cost is estimated from the price book.
// priced is false when the model has no price: the row is then unpriced, never
// free. Reasoning tokens are already inside output tokens for Claude, so they
// are not charged again.
func Cost(r UsageRow, pb PriceBook) (usd float64, priced bool) {
	if r.CostReportedUSD != nil {
		return *r.CostReportedUSD, true
	}
	p, ok := pb.Lookup(r.Model, r.LongContext)
	if !ok {
		return 0, false
	}
	usd = (float64(r.Input)*p.Input + float64(r.Output)*p.Output +
		float64(r.CacheWrite5m)*p.CacheWrite5m + float64(r.CacheWrite1h)*p.CacheWrite1h +
		float64(r.CacheRead)*p.CacheRead) / 1_000_000
	return usd, true
}
