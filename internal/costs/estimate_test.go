package costs_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/OcheOps/platform-IDP/internal/costs"
)

func TestCalculateSeparatesSharedServiceAndPreviewCosts(t *testing.T) {
	request := json.RawMessage(`{"spec":{"capacity":{"cpu":"250m","memory":"256Mi","minReplicas":1,"maxReplicas":3},"database":{"engine":"postgres"},"preview":{"enabled":true,"ttlHours":72}}}`)
	estimate, err := costs.Calculate(costs.Input{Region: "eu-west-1", Request: request, CalculatedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC), Prices: costs.FallbackPrices()})
	if err != nil {
		t.Fatal(err)
	}
	if estimate.SharedPlatform.AmountCents != 50000 || estimate.IncrementalService.AmountCents <= 0 || estimate.PreviewScenario.AmountCents <= 0 {
		t.Fatalf("unexpected estimate: %#v", estimate)
	}
	if estimate.Digest == "" || estimate.PriceSource != "versioned-fallback" || estimate.Currency != "USD" {
		t.Fatalf("missing provenance: %#v", estimate)
	}
}

func TestDigestChangesWhenReviewedCapacityChanges(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	first, _ := costs.Calculate(costs.Input{Region: "eu-west-1", Request: json.RawMessage(`{"spec":{"capacity":{"cpu":"250m","memory":"256Mi","minReplicas":1,"maxReplicas":3},"database":{"engine":"none"}}}`), CalculatedAt: now, Prices: costs.FallbackPrices()})
	second, _ := costs.Calculate(costs.Input{Region: "eu-west-1", Request: json.RawMessage(`{"spec":{"capacity":{"cpu":"500m","memory":"256Mi","minReplicas":1,"maxReplicas":3},"database":{"engine":"none"}}}`), CalculatedAt: now, Prices: costs.FallbackPrices()})
	if first.Digest == second.Digest {
		t.Fatal("capacity change did not invalidate estimate digest")
	}
}

func TestDigestIgnoresJSONFormatting(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	compact := json.RawMessage(`{"spec":{"capacity":{"cpu":"250m","memory":"256Mi","minReplicas":1,"maxReplicas":3},"database":{"engine":"none"}}}`)
	formatted := json.RawMessage("{\n  \"spec\": {\"database\": {\"engine\": \"none\"}, \"capacity\": {\"maxReplicas\": 3, \"minReplicas\": 1, \"memory\": \"256Mi\", \"cpu\": \"250m\"}}\n}")
	first, _ := costs.Calculate(costs.Input{Region: "eu-west-1", Request: compact, CalculatedAt: now, Prices: costs.FallbackPrices()})
	second, _ := costs.Calculate(costs.Input{Region: "eu-west-1", Request: formatted, CalculatedAt: now, Prices: costs.FallbackPrices()})
	if first.Digest != second.Digest {
		t.Fatal("format-only JSON change invalidated estimate digest")
	}
}

func TestCalculateAcceptsEveryContractQuantityForm(t *testing.T) {
	request := json.RawMessage(`{"spec":{"capacity":{"cpu":"1","memory":"2Gi","minReplicas":1,"maxReplicas":3},"database":{"engine":"none"}}}`)
	if _, err := costs.Calculate(costs.Input{Region: "eu-west-1", Request: request, CalculatedAt: time.Now(), Prices: costs.FallbackPrices()}); err != nil {
		t.Fatal(err)
	}
}
