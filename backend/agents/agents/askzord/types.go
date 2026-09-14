package askzord

const (
	IntentRecord         = "RECORD"
	IntentAggregate      = "AGGREGATE"
	IntentExplanation    = "EXPLANATION"
	IntentKnowledge      = "KNOWLEDGE"
	IntentCashPosition   = "CASH_POSITION"
	IntentInvestigation  = "INVESTIGATION"
	IntentReconciliation = "RECONCILIATION"
)

type EntityRef struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id,omitempty"`
}

type QueryPlan struct {
	Intent             string            `json:"intent"`
	Entity             EntityRef         `json:"entity"`
	RequiredSources    []string          `json:"required_sources"`
	Metrics            []string          `json:"metrics,omitempty"`
	Filters            map[string]string `json:"filters,omitempty"`
	LossQuestion       bool              `json:"-"`
	BankCauseQuestion  bool              `json:"-"`
	SettledAllQuestion bool              `json:"-"`
}

type Fact struct {
	Field    string `json:"field"`
	Value    any    `json:"value"`
	Currency string `json:"currency,omitempty"`
}

type Calculation struct {
	Formula string `json:"formula"`
	Output  int64  `json:"output"`
}

type KnowledgeHit struct {
	Title   string `json:"title"`
	Version string `json:"version"`
	Text    string `json:"text"`
}

type FinanceContext struct {
	Plan           QueryPlan
	Facts          []Fact
	Calculations   []Calculation
	Evidence       []string
	Knowledge      []KnowledgeHit
	Limitations    []string
	Exceptions     []map[string]any
	Primary        map[string]any
	Summary        map[string]any
	MissingPrimary bool
	BankProven     bool
	Integrity      string
}

type Response struct {
	Answer       string        `json:"answer"`
	Intent       string        `json:"intent"`
	Facts        []Fact        `json:"facts"`
	Calculations []Calculation `json:"calculations"`
	Evidence     []string      `json:"evidence"`
	Sources      []string      `json:"sources"`
	Confidence   float64       `json:"confidence"`
	Limitations  []string      `json:"limitations"`
	AuditID      string        `json:"audit_id,omitempty"`
}

type QueryRequest struct {
	Question       string    `json:"question"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Inherit        EntityRef `json:"-"`
}
