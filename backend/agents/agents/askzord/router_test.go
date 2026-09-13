package askzord

import "testing"

func TestRouterIntents(t *testing.T) {
	cases := []struct {
		q      string
		intent string
	}{
		{"What happened to payment pay_123?", IntentRecord},
		{"What happened to payout pout_123?", IntentRecord},
		{"Why is pay_123 unresolved?", IntentExplanation},
		{"How much money is currently unresolved?", IntentAggregate},
		{"Why is the reconciliation rate only 94%?", IntentReconciliation},
		{"How much cash is expected versus actually received?", IntentCashPosition},
		{"When does in-flight cash hit the bank?", IntentCashPosition},
		{"What are the biggest unresolved financial issues?", IntentInvestigation},
		{"Show me all failed payments where money moved", IntentInvestigation},
		{"What is the difference between settlement and bank credit?", IntentKnowledge},
		{"How much money did we lose from failed payments?", IntentAggregate},
		{"Please review this workspace", IntentKnowledge},
		{"Can you look at our numbers in general?", IntentKnowledge},
	}
	for _, tc := range cases {
		p := Plan(tc.q, EntityRef{})
		if p.Intent != tc.intent {
			t.Fatalf("%q: got %s want %s", tc.q, p.Intent, tc.intent)
		}
	}
}

func TestRouterInheritsEntityForFollowup(t *testing.T) {
	p := Plan("And what about the refund?", EntityRef{Type: "payment", ID: "pay_123"})
	if p.Entity.ID != "pay_123" || p.Intent != IntentRecord {
		t.Fatalf("%+v", p)
	}
}
