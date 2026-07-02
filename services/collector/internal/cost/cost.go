package cost

import (
	"github.com/agentmesh/agentmesh/shared/span"
	"github.com/shopspring/decimal"
)

// PricingModel defines the cost per 1,000 tokens for a specific LLM.
type PricingModel struct {
	CostPer1kInput  decimal.Decimal
	CostPer1kOutput decimal.Decimal
}

// DefaultPricing represents a static, hardcoded pricing table for Cost Engine v0.1.
// In later milestones, this will be loaded from a configuration file and refreshed periodically.
var DefaultPricing = map[string]PricingModel{
	"gpt-4o": {
		CostPer1kInput:  decimal.NewFromFloat(0.005),
		CostPer1kOutput: decimal.NewFromFloat(0.015),
	},
	"gpt-4o-mini": {
		CostPer1kInput:  decimal.NewFromFloat(0.00015),
		CostPer1kOutput: decimal.NewFromFloat(0.0006),
	},
	"claude-3-5-sonnet-20240620": {
		CostPer1kInput:  decimal.NewFromFloat(0.003),
		CostPer1kOutput: decimal.NewFromFloat(0.015),
	},
	"claude-3-haiku-20240307": {
		CostPer1kInput:  decimal.NewFromFloat(0.00025),
		CostPer1kOutput: decimal.NewFromFloat(0.00125),
	},
	// Fallback/test mock models
	"fake-model": {
		CostPer1kInput:  decimal.NewFromFloat(0.001),
		CostPer1kOutput: decimal.NewFromFloat(0.002),
	},
}

// Compute returns a cost_usd float64 for the given span if it's an llm.call
// and has token counts. It returns nil if the cost cannot be computed (e.g.,
// unknown model, no token counts, or not an LLM call).
// For tool.call, the SDK is expected to send the explicit cost_usd attribute,
// which the decoder handles directly. This engine currently only computes
// LLM costs.
func Compute(s span.Span) *float64 {
	if s.Kind != span.KindLLMCall {
		return nil
	}

	modelName := s.Name
	// Check if the exact model name is in our pricing table
	pricing, ok := DefaultPricing[modelName]
	if !ok {
		return nil
	}

	if s.TokenInput == nil && s.TokenOutput == nil {
		return nil
	}

	var totalCost decimal.Decimal

	if s.TokenInput != nil {
		inputTokens := decimal.NewFromInt(int64(*s.TokenInput))
		inputCost := inputTokens.Div(decimal.NewFromInt(1000)).Mul(pricing.CostPer1kInput)
		totalCost = totalCost.Add(inputCost)
	}

	if s.TokenOutput != nil {
		outputTokens := decimal.NewFromInt(int64(*s.TokenOutput))
		outputCost := outputTokens.Div(decimal.NewFromInt(1000)).Mul(pricing.CostPer1kOutput)
		totalCost = totalCost.Add(outputCost)
	}

	// ClickHouse decimal(12,6) precision
	totalCost = totalCost.RoundBank(6)
	f, _ := totalCost.Float64()
	return &f
}
