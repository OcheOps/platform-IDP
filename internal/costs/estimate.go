package costs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const ModelVersion = "v1alpha1-fallback-2026-08"

type Prices struct {
	Version                    string `json:"version"`
	SharedPlatformMonthlyCents int64  `json:"sharedPlatformMonthlyCents"`
	VCPUHourMilliCents         int64  `json:"vCPUHourMilliCents"`
	GiBHourMilliCents          int64  `json:"giBHourMilliCents"`
	PostgresMonthlyCents       int64  `json:"postgresMonthlyCents"`
	SecretMonthlyCents         int64  `json:"secretMonthlyCents"`
}

type Input struct {
	Region       string
	Request      json.RawMessage
	CalculatedAt time.Time
	Prices       Prices
}

type Estimate struct {
	ModelVersion       string        `json:"modelVersion"`
	Digest             string        `json:"digest"`
	Region             string        `json:"region"`
	Currency           string        `json:"currency"`
	CalculatedAt       time.Time     `json:"calculatedAt"`
	PriceSource        string        `json:"priceSource"`
	PriceVersion       string        `json:"priceVersion"`
	SharedPlatform     MonthlyAmount `json:"sharedPlatform"`
	IncrementalService MonthlyAmount `json:"incrementalService"`
	PreviewScenario    MonthlyAmount `json:"previewScenario"`
	Assumptions        []string      `json:"assumptions"`
	Flags              []string      `json:"flags"`
}

type MonthlyAmount struct {
	AmountCents int64  `json:"amountCents"`
	Display     string `json:"display"`
}

func FallbackPrices() Prices {
	return Prices{Version: "fallback-2026-08", SharedPlatformMonthlyCents: 50000, VCPUHourMilliCents: 4200, GiBHourMilliCents: 500, PostgresMonthlyCents: 2500, SecretMonthlyCents: 40}
}

func Calculate(input Input) (Estimate, error) {
	if strings.TrimSpace(input.Region) == "" || len(input.Request) == 0 || input.CalculatedAt.IsZero() {
		return Estimate{}, errors.New("region, request, and calculation time are required")
	}
	if input.Prices.Version == "" {
		return Estimate{}, errors.New("price version is required")
	}
	if input.Prices.SharedPlatformMonthlyCents < 0 || input.Prices.VCPUHourMilliCents < 0 || input.Prices.GiBHourMilliCents < 0 || input.Prices.PostgresMonthlyCents < 0 || input.Prices.SecretMonthlyCents < 0 {
		return Estimate{}, errors.New("prices cannot be negative")
	}
	var normalizedRequest any
	if err := json.Unmarshal(input.Request, &normalizedRequest); err != nil {
		return Estimate{}, fmt.Errorf("decode service request: %w", err)
	}
	canonicalRequest, err := json.Marshal(normalizedRequest)
	if err != nil {
		return Estimate{}, fmt.Errorf("normalize service request: %w", err)
	}
	var request struct {
		Spec struct {
			Capacity struct {
				CPU         string `json:"cpu"`
				Memory      string `json:"memory"`
				MinReplicas int64  `json:"minReplicas"`
				MaxReplicas int64  `json:"maxReplicas"`
			} `json:"capacity"`
			Database struct {
				Engine string `json:"engine"`
			} `json:"database"`
			Preview struct {
				Enabled  bool  `json:"enabled"`
				TTLHours int64 `json:"ttlHours"`
			} `json:"preview"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(canonicalRequest, &request); err != nil {
		return Estimate{}, fmt.Errorf("decode service request: %w", err)
	}
	milliCPU, err := parseCPU(request.Spec.Capacity.CPU)
	if err != nil {
		return Estimate{}, err
	}
	memoryMiB, err := parseMemory(request.Spec.Capacity.Memory)
	if err != nil {
		return Estimate{}, err
	}
	if request.Spec.Capacity.MinReplicas <= 0 || request.Spec.Capacity.MaxReplicas < request.Spec.Capacity.MinReplicas {
		return Estimate{}, errors.New("replica range is invalid")
	}
	computeMonthly := resourceCost(milliCPU, memoryMiB, request.Spec.Capacity.MinReplicas, 730, input.Prices)
	databaseMonthly, secretMonthly := int64(0), int64(0)
	if request.Spec.Database.Engine == "postgres" {
		databaseMonthly, secretMonthly = input.Prices.PostgresMonthlyCents, input.Prices.SecretMonthlyCents
	}
	incremental := computeMonthly + databaseMonthly + secretMonthly
	preview := int64(0)
	if request.Spec.Preview.Enabled {
		ttl := request.Spec.Preview.TTLHours
		if ttl == 0 {
			ttl = 72
		}
		preview = resourceCost(milliCPU, memoryMiB, 1, ttl, input.Prices) + prorate(databaseMonthly+secretMonthly, ttl)
	}
	flags := []string{"fallback_pricing"}
	if request.Spec.Capacity.MaxReplicas >= 8 {
		flags = append(flags, "high_max_replicas")
	}
	if milliCPU > 0 && memoryMiB*1000/milliCPU >= 4096 {
		flags = append(flags, "memory_cpu_imbalance")
	}
	canonical, err := json.Marshal(struct {
		Model   string          `json:"model"`
		Region  string          `json:"region"`
		Request json.RawMessage `json:"request"`
		Prices  Prices          `json:"prices"`
	}{ModelVersion, input.Region, canonicalRequest, input.Prices})
	if err != nil {
		return Estimate{}, err
	}
	digest := sha256.Sum256(canonical)
	return Estimate{
		ModelVersion: ModelVersion, Digest: "sha256:" + hex.EncodeToString(digest[:]), Region: input.Region, Currency: "USD", CalculatedAt: input.CalculatedAt.UTC(), PriceSource: "versioned-fallback", PriceVersion: input.Prices.Version,
		SharedPlatform: amount(input.Prices.SharedPlatformMonthlyCents), IncrementalService: amount(incremental), PreviewScenario: amount(preview),
		Assumptions: []string{"730 hours per month", "minimum replicas run for the full month", "one preview active for its configured TTL", "shared baseline is context and is not added to incremental cost", "taxes, discounts, free tiers, credits, and data transfer are excluded"}, Flags: flags,
	}, nil
}

func resourceCost(milliCPU, memoryMiB, replicas, hours int64, prices Prices) int64 {
	milliCents := ((milliCPU*prices.VCPUHourMilliCents)/1000 + (memoryMiB*prices.GiBHourMilliCents)/1024) * replicas * hours
	return (milliCents + 999) / 1000
}

func prorate(monthlyCents, hours int64) int64 { return (monthlyCents*hours + 729) / 730 }

func amount(cents int64) MonthlyAmount {
	return MonthlyAmount{AmountCents: cents, Display: fmt.Sprintf("$%d.%02d", cents/100, cents%100)}
}

func parseCPU(value string) (int64, error) {
	if !strings.HasSuffix(value, "m") {
		return 0, fmt.Errorf("unsupported CPU quantity %q", value)
	}
	result, err := strconv.ParseInt(strings.TrimSuffix(value, "m"), 10, 64)
	if err != nil || result <= 0 {
		return 0, fmt.Errorf("invalid CPU quantity %q", value)
	}
	return result, nil
}

func parseMemory(value string) (int64, error) {
	if !strings.HasSuffix(value, "Mi") {
		return 0, fmt.Errorf("unsupported memory quantity %q", value)
	}
	result, err := strconv.ParseInt(strings.TrimSuffix(value, "Mi"), 10, 64)
	if err != nil || result <= 0 {
		return 0, fmt.Errorf("invalid memory quantity %q", value)
	}
	return result, nil
}
