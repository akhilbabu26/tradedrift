package test

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"tradedrift/services/controlled-taker/internal/config"
	"tradedrift/services/controlled-taker/internal/engine"
)

// TestRegimeManager_DeterministicTransitions verifies:
// 1. Valid transitions only according to the Markov transition matrix.
// 2. Dwell duration stays within the configured [min, max] range for each regime.
// 3. No invalid enum/state.
// 4. Seeded RNG produces reproducible results.
func TestRegimeManager_DeterministicTransitions(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()

	// Valid transition rules:
	// QUIET  -> QUIET, NORMAL
	// NORMAL -> QUIET, NORMAL, ACTIVE
	// ACTIVE -> NORMAL, ACTIVE, BURST
	// BURST  -> ACTIVE, NORMAL
	isValidTransition := func(from, to engine.MarketRegime) bool {
		switch from {
		case engine.RegimeQuiet:
			return to == engine.RegimeQuiet || to == engine.RegimeNormal
		case engine.RegimeNormal:
			return to == engine.RegimeQuiet || to == engine.RegimeNormal || to == engine.RegimeActive
		case engine.RegimeActive:
			return to == engine.RegimeNormal || to == engine.RegimeActive || to == engine.RegimeBurst
		case engine.RegimeBurst:
			return to == engine.RegimeActive || to == engine.RegimeNormal
		default:
			return false
		}
	}

	seed := int64(424242)
	rng1 := rand.New(rand.NewSource(seed))
	rng2 := rand.New(rand.NewSource(seed))

	now := time.Now()
	rm1 := engine.NewRegimeManager(now, rng1, configs)
	rm2 := engine.NewRegimeManager(now, rng2, configs)

	// Check initial state
	if rm1.CurrentRegime() != engine.RegimeNormal {
		t.Fatalf("expected initial regime NORMAL, got %s", rm1.CurrentRegime())
	}

	// Verify reproducible starting dwell
	if !rm1.DwellUntil().Equal(rm2.DwellUntil()) {
		t.Fatalf("expected seeded RNG to produce identical dwellUntil, got %v vs %v", rm1.DwellUntil(), rm2.DwellUntil())
	}

	simTime := now
	transitionsCount := 0
	const targetTransitions = 500

	for transitionsCount < targetTransitions {
		prevRegime := rm1.CurrentRegime()
		// Jump past dwell timer to trigger next transition
		simTime = rm1.DwellUntil().Add(time.Second)

		next1, transitioned1 := rm1.Tick(simTime, rng1)
		next2, transitioned2 := rm2.Tick(simTime, rng2)

		if !transitioned1 || !transitioned2 {
			t.Fatalf("expected transition after dwell expiration at simTime %v", simTime)
		}

		if next1 != next2 {
			t.Fatalf("seeded reproducibility failed: rm1=%s, rm2=%s", next1, next2)
		}

		if !isValidTransition(prevRegime, next1) {
			t.Fatalf("invalid transition from %s to %s", prevRegime, next1)
		}

		// Verify dwell duration within config bounds
		dwellDuration := rm1.DwellUntil().Sub(simTime)
		cfg := configs[next1]
		if dwellDuration < cfg.DwellMin || dwellDuration > cfg.DwellMax {
			t.Fatalf("dwell duration %v for regime %s out of bounds [%v, %v]",
				dwellDuration, next1, cfg.DwellMin, cfg.DwellMax)
		}

		transitionsCount++
	}
}

// TestRegimeManager_ProfileDistributions runs 20,000 samples per regime to verify
// that profile probabilities match configured distributions within statistical tolerances.
func TestRegimeManager_ProfileDistributions(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	rng := rand.New(rand.NewSource(98765))
	now := time.Now()
	rm := engine.NewRegimeManager(now, rng, configs)

	const samples = 20000

	type expectedDistribution struct {
		regime engine.MarketRegime
		pLow   float64
		pMid   float64
		pHigh  float64
		tol    float64 // acceptable deviation
	}

	tests := []expectedDistribution{
		{
			regime: engine.RegimeQuiet,
			pLow:   0.85,
			pMid:   0.15,
			pHigh:  0.00,
			tol:    0.015, // +/- 1.5%
		},
		{
			regime: engine.RegimeNormal,
			pLow:   0.65,
			pMid:   0.30,
			pHigh:  0.05,
			tol:    0.015,
		},
		{
			regime: engine.RegimeActive,
			pLow:   0.40,
			pMid:   0.45,
			pHigh:  0.15,
			tol:    0.015,
		},
		{
			regime: engine.RegimeBurst,
			pLow:   0.20,
			pMid:   0.50,
			pHigh:  0.30,
			tol:    0.015,
		},
	}

	for _, tc := range tests {
		t.Run(string(tc.regime), func(t *testing.T) {
			rm.SetRegime(tc.regime, now.Add(10*time.Hour))

			counts := map[config.ProfileType]int{
				config.ProfileLow:  0,
				config.ProfileMid:  0,
				config.ProfileHigh: 0,
			}

			for i := 0; i < samples; i++ {
				p := rm.ChooseProfile(rng, true, true)
				counts[p]++
			}

			obsLow := float64(counts[config.ProfileLow]) / float64(samples)
			obsMid := float64(counts[config.ProfileMid]) / float64(samples)
			obsHigh := float64(counts[config.ProfileHigh]) / float64(samples)

			if tc.pHigh == 0.0 {
				if counts[config.ProfileHigh] != 0 {
					t.Fatalf("regime %s: expected strictly 0 HIGH samples, got %d", tc.regime, counts[config.ProfileHigh])
				}
			} else if math.Abs(obsHigh-tc.pHigh) > tc.tol {
				t.Errorf("regime %s HIGH: expected %.3f, observed %.3f (diff %.4f > tol %.4f)",
					tc.regime, tc.pHigh, obsHigh, math.Abs(obsHigh-tc.pHigh), tc.tol)
			}

			if math.Abs(obsLow-tc.pLow) > tc.tol {
				t.Errorf("regime %s LOW: expected %.3f, observed %.3f (diff %.4f > tol %.4f)",
					tc.regime, tc.pLow, obsLow, math.Abs(obsLow-tc.pLow), tc.tol)
			}

			if math.Abs(obsMid-tc.pMid) > tc.tol {
				t.Errorf("regime %s MID: expected %.3f, observed %.3f (diff %.4f > tol %.4f)",
					tc.regime, tc.pMid, obsMid, math.Abs(obsMid-tc.pMid), tc.tol)
			}
		})
	}
}

// TestRegimeManager_CooldownRenormalization verifies that when HIGH or MID are on cooldown:
// 1. Ineligible profiles are NEVER selected.
// 2. Weights are properly renormalized across the eligible profiles.
// 3. When both HIGH and MID are in cooldown, LOW is selected 100% of the time.
func TestRegimeManager_CooldownRenormalization(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	rng := rand.New(rand.NewSource(12345))
	now := time.Now()
	rm := engine.NewRegimeManager(now, rng, configs)

	const samples = 20000

	// Case 1: HIGH is in cooldown (canHigh = false)
	// For BURST: base weights are LOW=0.20, MID=0.50, HIGH=0.30.
	// When canHigh=false, total = 0.70.
	// Renormalized LOW = 0.20 / 0.70 = ~28.57%
	// Renormalized MID = 0.50 / 0.70 = ~71.43%
	rm.SetRegime(engine.RegimeBurst, now.Add(10*time.Hour))
	counts := map[config.ProfileType]int{}
	for i := 0; i < samples; i++ {
		p := rm.ChooseProfile(rng, true, false)
		counts[p]++
	}

	if counts[config.ProfileHigh] != 0 {
		t.Fatalf("expected 0 HIGH profiles when canHigh=false, got %d", counts[config.ProfileHigh])
	}
	obsBurstLow := float64(counts[config.ProfileLow]) / float64(samples)
	obsBurstMid := float64(counts[config.ProfileMid]) / float64(samples)
	expectedBurstLow := 0.20 / 0.70
	expectedBurstMid := 0.50 / 0.70

	if math.Abs(obsBurstLow-expectedBurstLow) > 0.015 {
		t.Errorf("BURST with canHigh=false: expected LOW %.3f, got %.3f", expectedBurstLow, obsBurstLow)
	}
	if math.Abs(obsBurstMid-expectedBurstMid) > 0.015 {
		t.Errorf("BURST with canHigh=false: expected MID %.3f, got %.3f", expectedBurstMid, obsBurstMid)
	}

	// Case 2: Both MID and HIGH in cooldown (canMid = false, canHigh = false)
	// Should yield 100% LOW across all regimes
	regimes := []engine.MarketRegime{engine.RegimeQuiet, engine.RegimeNormal, engine.RegimeActive, engine.RegimeBurst}
	for _, reg := range regimes {
		rm.SetRegime(reg, now.Add(10*time.Hour))
		for i := 0; i < 500; i++ {
			p := rm.ChooseProfile(rng, false, false)
			if p != config.ProfileLow {
				t.Fatalf("regime %s: expected ProfileLow when both MID and HIGH in cooldown, got %s", reg, p)
			}
		}
	}
}

// TestRegimeManager_DwellLifecycleIntegrity verifies that:
// 1. Regime remains unchanged throughout the dwell duration.
// 2. High-frequency cycles do NOT trigger transitions while dwell timer is active.
// 3. Transition is only evaluated after the dwell timer expires.
func TestRegimeManager_DwellLifecycleIntegrity(t *testing.T) {
	configs := engine.DefaultRegimeConfigs()
	rng := rand.New(rand.NewSource(55555))
	now := time.Now()
	rm := engine.NewRegimeManager(now, rng, configs)

	// Force into QUIET regime with exactly 180s dwell
	dwellUntil := now.Add(180 * time.Second)
	rm.SetRegime(engine.RegimeQuiet, dwellUntil)

	// Simulate high-frequency CTS loop every 5 seconds up to 175 seconds
	for elapsed := 5 * time.Second; elapsed <= 175*time.Second; elapsed += 5 * time.Second {
		curTime := now.Add(elapsed)
		regime, transitioned := rm.Tick(curTime, rng)
		if transitioned {
			t.Fatalf("unexpected transition at elapsed %v before dwell expiration (180s)", elapsed)
		}
		if regime != engine.RegimeQuiet {
			t.Fatalf("expected regime to remain QUIET, got %s", regime)
		}
	}

	// Now advance to 181 seconds (past dwell)
	curTime := now.Add(181 * time.Second)
	nextRegime, transitioned := rm.Tick(curTime, rng)
	if !transitioned {
		t.Fatalf("expected transition after dwell expiration at 181s")
	}

	// In QUIET, transitions can either stay QUIET or move to NORMAL
	if nextRegime != engine.RegimeQuiet && nextRegime != engine.RegimeNormal {
		t.Fatalf("invalid next regime from QUIET: %s", nextRegime)
	}

	// Verify new dwellUntil is set into the future
	if !rm.DwellUntil().After(curTime) {
		t.Fatalf("expected new dwellUntil (%v) to be after curTime (%v)", rm.DwellUntil(), curTime)
	}
}
