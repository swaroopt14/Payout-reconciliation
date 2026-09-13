package recon

import "sort"

type BankContinuityFact struct {
	ID           string
	IdentityHash string
	Seq          int64
	CreditMinor  int64
	DebitMinor   int64
	BalanceMinor int64
	HasBalance   bool
}

type ContinuityReport struct {
	OK         bool
	Duplicates []string
	Gaps       []string
}

// AssertBankContinuity checks statement running-balance math and duplicate hashes.
// Rows should already be in statement order. When HasBalance is false, only
// duplicates are asserted.
func AssertBankContinuity(rows []BankContinuityFact) ContinuityReport {
	rep := ContinuityReport{OK: true}
	seen := map[string]int{}
	var running int64
	var haveRunning bool
	for i, r := range rows {
		if r.IdentityHash != "" {
			if n := seen[r.IdentityHash]; n > 0 {
				rep.Duplicates = append(rep.Duplicates, r.IdentityHash)
				rep.OK = false
			}
			seen[r.IdentityHash]++
		}
		if !r.HasBalance {
			continue
		}
		if !haveRunning {
			running = r.BalanceMinor
			haveRunning = true
			continue
		}
		expect := running + r.CreditMinor - r.DebitMinor
		if expect != r.BalanceMinor {
			id := r.ID
			if id == "" {
				id = r.IdentityHash
			}
			rep.Gaps = append(rep.Gaps, id)
			rep.OK = false
		}
		running = r.BalanceMinor
		_ = i
	}
	return rep
}

// SortBankFactsBySeq orders imported statement rows for continuity.
func SortBankFactsBySeq(rows []BankContinuityFact) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Seq < rows[j].Seq })
}
