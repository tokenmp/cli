package billing

import "time"

// Row 是 billing_records 的一行，字段名与 web 的对接文档一致。
//
// 可空列用指针，与 records.Row 同一规矩：nil 是不给，0 与空串是给了具体的值。
// ledger 不在这里：它由 web 在摄入时按用户当期的订阅余量判定，本机不填。
type Row struct {
	RequestID string `json:"request_id"`
	UserID    *int64 `json:"user_id,omitempty"`
	APIKeyID  *int64 `json:"api_key_id,omitempty"`
	Model     string `json:"model,omitempty"`

	PromptTokens     *int `json:"prompt_tokens,omitempty"`
	CompletionTokens *int `json:"completion_tokens,omitempty"`
	CacheReadTokens  *int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int `json:"cache_write_tokens,omitempty"`
	TotalTokens      *int `json:"total_tokens,omitempty"`

	// CostAmount 是付给上游的钱，RevenueAmount 是向用户收的钱。两者分开记，谁也不拿
	// 谁算。倍率列回答「这笔为什么是这个倍率」。
	CostAmount             float64 `json:"cost_amount"`
	RevenueAmount          float64 `json:"revenue_amount"`
	CostMultiplier         float64 `json:"cost_multiplier"`
	RevenueMultiplier      float64 `json:"revenue_multiplier"`
	CostMultiplierRules    []Rule  `json:"cost_multiplier_rules,omitempty"`
	RevenueMultiplierRules []Rule  `json:"revenue_multiplier_rules,omitempty"`

	Currency    string    `json:"currency,omitempty"`
	BillingMode string    `json:"billing_mode,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// Rule 是倍率规则里的一条，原样写进 *_multiplier_rules 列。字段按命中来源填：
// 成本侧命中的是渠道（Scope 为 channel），收入侧命中的是套餐（Scope 为 plan）。
// 倍率为 1 时也写一条，这样两列恒非空，对账时能看出这一笔按什么口径算的。
type Rule struct {
	// Scope 取 channel 或 plan。
	Scope string `json:"scope"`
	// Multiplier 是这条规则给出的倍率。
	Multiplier float64 `json:"multiplier"`
	// Channel 与 Model 是成本侧命中的渠道与模型。
	Channel string `json:"channel,omitempty"`
	Model   string `json:"model,omitempty"`
	// PlanID、Mode、CostMode 是收入侧命中的套餐与计价模式。
	PlanID   *int64 `json:"plan_id,omitempty"`
	Mode     string `json:"mode,omitempty"`
	CostMode string `json:"cost_mode,omitempty"`
}
