package recon

// PickLatestObservations keeps the highest FactVersion per kind+entity.
// Earlier restatements of the same settlement or bank fact are dropped.
func PickLatestObservations(obs []SourceObservation) []SourceObservation {
	best := map[string]SourceObservation{}
	order := make([]string, 0, len(obs))
	for _, o := range obs {
		k := o.Kind + "|" + firstNonEmpty(o.EntityID, o.ID)
		if o.Kind == SourceKindTax {
			k = o.Kind + "|" + firstNonEmpty(o.ID, o.EntityID+"|"+o.Status)
		}
		if _, seen := best[k]; !seen {
			order = append(order, k)
		}
		prev, ok := best[k]
		if !ok || o.FactVersion >= prev.FactVersion {
			best[k] = o
		}
	}
	out := make([]SourceObservation, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}
