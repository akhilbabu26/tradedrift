package test

import (
	"math/rand"
	"testing"
	"time"

	"tradedrift/services/controlled-taker/internal/engine"
)

func TestRegimeValidation_ValidDefaultConfigs(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	if err := engine.ValidateRegimeConfigs(configs); err != nil {
		t.Fatalf("expected valid default regime configs, got error: %v", err)
	}
}

func TestRegimeValidation_NilConfigs_Rejected(t *testing.T) {
	if err := engine.ValidateRegimeConfigs(nil); err == nil {
		t.Fatalf("expected error for nil regime configs, got nil")
	}
}

func TestRegimeValidation_MissingRegime_Rejected(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	delete(configs, engine.RegimeBurst)
	if err := engine.ValidateRegimeConfigs(configs); err == nil {
		t.Fatalf("expected error when BURST regime is missing, got nil")
	}
}

func TestRegimeValidation_ExtraRegime_Rejected(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	configs[engine.MarketRegime("HYPER_BURST")] = configs[engine.RegimeBurst]
	if err := engine.ValidateRegimeConfigs(configs); err == nil {
		t.Fatalf("expected error when unknown extra regime is provided, got nil")
	}
}

func TestRegimeValidation_ZeroInterval_Rejected(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	norm := configs[engine.RegimeNormal]
	norm.Interval.MinInterval = 0
	configs[engine.RegimeNormal] = norm

	if err := engine.ValidateRegimeConfigs(configs); err == nil {
		t.Fatalf("expected error when MinInterval == 0, got nil")
	}
}

func TestRegimeValidation_InvertedInterval_Rejected(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	norm := configs[engine.RegimeNormal]
	norm.Interval.MinInterval = 10 * time.Second
	norm.Interval.BaseInterval = 5 * time.Second
	configs[engine.RegimeNormal] = norm

	if err := engine.ValidateRegimeConfigs(configs); err == nil {
		t.Fatalf("expected error when MinInterval > BaseInterval, got nil")
	}

	norm.Interval.MinInterval = 2 * time.Second
	norm.Interval.BaseInterval = 20 * time.Second
	norm.Interval.MaxInterval = 15 * time.Second
	configs[engine.RegimeNormal] = norm

	if err := engine.ValidateRegimeConfigs(configs); err == nil {
		t.Fatalf("expected error when BaseInterval > MaxInterval, got nil")
	}
}

func TestRegimeValidation_InvertedDwell_Rejected(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	norm := configs[engine.RegimeNormal]
	norm.DwellMin = 10 * time.Minute
	norm.DwellMax = 5 * time.Minute
	configs[engine.RegimeNormal] = norm

	if err := engine.ValidateRegimeConfigs(configs); err == nil {
		t.Fatalf("expected error when DwellMin > DwellMax, got nil")
	}

	norm.DwellMin = 0
	configs[engine.RegimeNormal] = norm

	if err := engine.ValidateRegimeConfigs(configs); err == nil {
		t.Fatalf("expected error when DwellMin <= 0, got nil")
	}
}

func TestRegimeValidation_ZeroWeightSum_Rejected(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	norm := configs[engine.RegimeNormal]
	norm.WeightLow = 0.0
	norm.WeightMid = 0.0
	norm.WeightHigh = 0.0
	configs[engine.RegimeNormal] = norm

	if err := engine.ValidateRegimeConfigs(configs); err == nil {
		t.Fatalf("expected error when sum of weights is 0, got nil")
	}
}

func TestRegimeValidation_NegativeWeight_Rejected(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	norm := configs[engine.RegimeNormal]
	norm.WeightLow = -0.5
	configs[engine.RegimeNormal] = norm

	if err := engine.ValidateRegimeConfigs(configs); err == nil {
		t.Fatalf("expected error when weight is negative, got nil")
	}
}

func TestNewRegimeManager_PanicsOnInvalidConfig(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected NewRegimeManager to panic on invalid config, but it did not")
		}
	}()

	invalidConfigs := engine.DefaultRegimeConfigs()
	delete(invalidConfigs, engine.RegimeQuiet)

	engine.NewRegimeManager(time.Now(), rand.New(rand.NewSource(1)), invalidConfigs)
}
