package billing

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/server"

	"github.com/tokenmp/cli/internal/clientauth"
)

// int64Ptr 造可空字段的指针，与 records 的测试同一写法。
func int64Ptr(value int64) *int64 { return &value }

// testNow 是补时间戳用的固定时刻。
var testNow = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

// usageOutcome 是一条成功调用：1000 个提示词 token 里有 200 个缓存读、100 个缓存写，
// 计费输入因此是 700。定义在一处，避免每个用例各写一遍容易写错。
func usageOutcome() server.Outcome {
	return server.Outcome{
		RequestID: "req_1",
		Status:    200,
		Duration:  2 * time.Second,
		Model:     "gpt-4o",
		Channel:   "openai",
		Usage: &server.Usage{
			PromptTokens:     1000,
			CompletionTokens: 500,
			TotalTokens:      1500,
			CachedTokens:     200,
			CacheWriteTokens: 100,
		},
	}
}

// tokenCost 是一条按 token 计价的渠道单价。四个单价故意取不同的值，能把哪一档算错
// 区分开。
func tokenCost() Config {
	return Config{
		Channels: map[string]ChannelCost{
			"openai": {
				CostInputPerMillion:      1,
				CostOutputPerMillion:     2,
				CostCacheReadPerMillion:  0.1,
				CostCacheWritePerMillion: 0.5,
			},
		},
	}
}

// tokenIdentity 是 markup 模式的计价口径。
func tokenIdentity() clientauth.Identity {
	return clientauth.Identity{
		UserID:   263,
		APIKeyID: 42,
		Pricing: clientauth.Pricing{
			PlanID:           int64Ptr(3),
			Mode:             clientauth.PricingModeMarkup,
			MarkupMultiplier: 1.15,
		},
	}
}

// closeTo 比较两个金额，容忍浮点末尾的误差。
func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

// TestRowAmounts 逐条核对金额算式。带缓存读写的用例把输入价、缓存读价、缓存写价
// 分开取不同值：如果没把缓存量从输入里扣掉，成本会明显偏大，断言就过不去。
func TestRowAmounts(t *testing.T) {
	tablePrice := func(price clientauth.Price) clientauth.Pricing {
		return clientauth.Pricing{
			PlanID: int64Ptr(7),
			Mode:   clientauth.PricingModeTable,
			Prices: map[string]clientauth.Price{"gpt-4o": price},
		}
	}

	cases := []struct {
		name      string
		outcome   server.Outcome
		identity  clientauth.Identity
		cost      Config
		models    []config.Model
		wantErr   error
		wantCost  float64
		wantRev   float64
		wantCostM float64
		wantRevM  float64
	}{
		{
			name:     "markup 模式：成本先算，收入乘倍率",
			outcome:  usageOutcome(),
			identity: tokenIdentity(),
			cost:     tokenCost(),
			// 成本 = 700×1 + 500×2 + 200×0.1 + 100×0.5，都按每百万 token 折算。
			wantCost:  0.00177,
			wantRev:   0.0020355,
			wantCostM: 1,
			wantRevM:  1.15,
		},
		{
			name:    "table 模式按 token 价：与成本单价无关",
			outcome: usageOutcome(),
			identity: clientauth.Identity{UserID: 263, APIKeyID: 42, Pricing: tablePrice(clientauth.Price{
				CostMode:             CostModeToken,
				InputPerMillion:      3,
				OutputPerMillion:     6,
				CacheReadPerMillion:  0.3,
				CacheWritePerMillion: 1.5,
			})},
			cost: tokenCost(),
			// 收入 = 700×3 + 500×6 + 200×0.3 + 100×1.5。
			wantCost:  0.00177,
			wantRev:   0.00531,
			wantCostM: 1,
			wantRevM:  1,
		},
		{
			name:    "table 模式的模型按次计费，不看 token",
			outcome: usageOutcome(),
			identity: clientauth.Identity{UserID: 263, APIKeyID: 42, Pricing: tablePrice(clientauth.Price{
				CostMode:      CostModeRequest,
				PerRequest:    0.05,
				RequestWeight: 2,
			})},
			cost:      tokenCost(),
			wantRev:   0.1,
			wantCost:  0.00177,
			wantCostM: 1,
			wantRevM:  2,
		},
		{
			name:     "成本侧按次计费",
			outcome:  usageOutcome(),
			identity: tokenIdentity(),
			cost: Config{Channels: map[string]ChannelCost{
				"openai": {CostMode: CostModeRequest, CostPerRequest: 0.02},
			}},
			wantCost:  0.02,
			wantRev:   0.023,
			wantCostM: 1,
			wantRevM:  1.15,
		},
		{
			name: "table 模式按上游名查不到时用模型规则的 match 回退",
			outcome: func() server.Outcome {
				outcome := usageOutcome()
				outcome.Model = "openai/gpt-4o"
				return outcome
			}(),
			identity: clientauth.Identity{UserID: 263, APIKeyID: 42, Pricing: tablePrice(clientauth.Price{
				CostMode:         CostModeToken,
				InputPerMillion:  3,
				OutputPerMillion: 6,
			})},
			cost: tokenCost(),
			// cli 的模型规则把用户看到的 gpt-4o 发成上游的 openai/gpt-4o；价目表按
			// 用户看到的模型名建，因此要反查回 gpt-4o。
			models:    []config.Model{{Match: "gpt-4o", Channel: "openai", Upstream: "openai/gpt-4o"}},
			wantCost:  0.00177,
			wantRev:   700*3/1e6 + 500*6/1e6,
			wantCostM: 1,
			wantRevM:  1,
		},
		{
			name:     "没有套餐算不出来",
			outcome:  usageOutcome(),
			identity: clientauth.Identity{UserID: 263, APIKeyID: 42},
			cost:     tokenCost(),
			wantErr:  errors.New("没有计价口径"),
		},
		{
			name: "table 模式价目表里没有这个模型，也回退不到",
			outcome: func() server.Outcome {
				outcome := usageOutcome()
				outcome.Model = "unknown-model"
				return outcome
			}(),
			identity: clientauth.Identity{UserID: 263, APIKeyID: 42, Pricing: tablePrice(clientauth.Price{CostMode: CostModeToken, InputPerMillion: 3})},
			cost:     tokenCost(),
			wantErr:  errors.New("价目表里没有模型"),
		},
		{
			name:     "cost 段没配这条渠道",
			outcome:  usageOutcome(),
			identity: tokenIdentity(),
			cost:     Config{Channels: map[string]ChannelCost{"other": {CostInputPerMillion: 1}}},
			wantErr:  errors.New("cost 段里没有渠道"),
		},
		{
			name: "调用失败不计费",
			outcome: func() server.Outcome {
				outcome := usageOutcome()
				outcome.Status = 500
				return outcome
			}(),
			identity: tokenIdentity(),
			cost:     tokenCost(),
			wantErr:  ErrNotBillable,
		},
		{
			name:    "收入为 0 不计费",
			outcome: usageOutcome(),
			identity: clientauth.Identity{UserID: 263, APIKeyID: 42, Pricing: clientauth.Pricing{
				Mode:             clientauth.PricingModeMarkup,
				MarkupMultiplier: 0,
			}},
			cost:    tokenCost(),
			wantErr: ErrNotBillable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calc := New(tc.cost, tc.models)
			row, err := calc.Row(tc.outcome, tc.identity, testNow)

			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("期望报错 %v，实际算出了 %+v", tc.wantErr, row)
				}
				if errors.Is(tc.wantErr, ErrNotBillable) {
					if !errors.Is(err, ErrNotBillable) {
						t.Fatalf("期望 ErrNotBillable，实际 %v", err)
					}
					return
				}
				if errors.Is(err, ErrNotBillable) {
					t.Fatalf("期望算不出来的错误，实际是 ErrNotBillable")
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if !closeTo(row.CostAmount, tc.wantCost) {
				t.Errorf("cost_amount 是 %v，期望 %v", row.CostAmount, tc.wantCost)
			}
			if !closeTo(row.RevenueAmount, tc.wantRev) {
				t.Errorf("revenue_amount 是 %v，期望 %v", row.RevenueAmount, tc.wantRev)
			}
			if !closeTo(row.CostMultiplier, tc.wantCostM) {
				t.Errorf("cost_multiplier 是 %v，期望 %v", row.CostMultiplier, tc.wantCostM)
			}
			if !closeTo(row.RevenueMultiplier, tc.wantRevM) {
				t.Errorf("revenue_multiplier 是 %v，期望 %v", row.RevenueMultiplier, tc.wantRevM)
			}
			if len(row.CostMultiplierRules) == 0 || len(row.RevenueMultiplierRules) == 0 {
				t.Fatalf("两列倍率规则都要非空: %+v", row)
			}
			if row.RequestID != tc.outcome.RequestID || row.Model != tc.outcome.Model {
				t.Errorf("request_id/model 是 %q/%q", row.RequestID, row.Model)
			}
			if row.UserID == nil || *row.UserID != 263 || row.APIKeyID == nil || *row.APIKeyID != 42 {
				t.Errorf("user_id/api_key_id 是 %v/%v", row.UserID, row.APIKeyID)
			}
			// 五个 token 列原样带上，缓存量不从总数里减：扣的是计价用的输入，不是记录。
			if row.PromptTokens == nil || *row.PromptTokens != 1000 {
				t.Errorf("prompt_tokens 是 %v，期望 1000", row.PromptTokens)
			}
			if row.CacheReadTokens == nil || *row.CacheReadTokens != 200 {
				t.Errorf("cache_read_tokens 是 %v，期望 200", row.CacheReadTokens)
			}
			if row.CacheWriteTokens == nil || *row.CacheWriteTokens != 100 {
				t.Errorf("cache_write_tokens 是 %v，期望 100", row.CacheWriteTokens)
			}
			if !row.CreatedAt.Equal(testNow.Add(-tc.outcome.Duration)) {
				t.Errorf("created_at 是 %v，期望 %v", row.CreatedAt, testNow.Add(-tc.outcome.Duration))
			}
			if row.Status != "settled" {
				t.Errorf("status 是 %q，期望 settled", row.Status)
			}
			if row.Currency != DefaultCurrency {
				t.Errorf("currency 是 %q，期望 %q", row.Currency, DefaultCurrency)
			}
		})
	}
}

// TestRowCacheTokensNotChargedAsInput 单独盯住缓存扣减：同一份用量，把缓存读写成 0
// 之后输入档的金额必须变大，两个金额的差正好是缓存量按输入价与缓存价之差。
func TestRowCacheTokensNotChargedAsInput(t *testing.T) {
	calc := New(tokenCost(), nil)
	identity := tokenIdentity()

	withCache := usageOutcome()
	withoutCache := usageOutcome()
	withoutCache.Usage.CachedTokens = 0
	withoutCache.Usage.CacheWriteTokens = 0

	cached, err := calc.Row(withCache, identity, testNow)
	if err != nil {
		t.Fatalf("带缓存的用例报错: %v", err)
	}
	plain, err := calc.Row(withoutCache, identity, testNow)
	if err != nil {
		t.Fatalf("不带缓存的用例报错: %v", err)
	}
	// 带缓存时省下 200×(1-0.1)/1e6 + 100×(1-0.5)/1e6，成本因此更小。
	wantSaved := 200*(1-0.1)/1e6 + 100*(1-0.5)/1e6
	if !closeTo(plain.CostAmount-cached.CostAmount, wantSaved) {
		t.Errorf("两次成本差是 %v，期望 %v", plain.CostAmount-cached.CostAmount, wantSaved)
	}
}

// TestPriceForFallbackSkipsPlaceholder 占位符规则翻译不出固定名字，不参与回退。
func TestPriceForFallbackSkipsPlaceholder(t *testing.T) {
	pricing := clientauth.Pricing{
		Mode:   clientauth.PricingModeTable,
		Prices: map[string]clientauth.Price{"gpt-4o": {CostMode: CostModeToken, InputPerMillion: 3}},
	}
	_, _, ok := priceFor(pricing, "renamed", []config.Model{{Match: "gpt-4o", Upstream: "${model}"}})
	if ok {
		t.Error("占位符规则不该被当成字面上游名")
	}
}
