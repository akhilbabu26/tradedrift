package recovery

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// ResetAuthResult represents validated empty-book reset authorization credentials.
type ResetAuthResult struct {
	Authorized           bool
	Generation           string
	TokenHash            string
	TokenFingerprint     string
	ExpectedCheckpoint   *int64  // Precondition: current DB checkpoint must match if specified
	ExpectedSequence     *uint64 // Precondition: current DB sequence must match if specified
	ExpectedLogEndOffset *int64  // Captured HWM: Kafka HWM at approval time
	ManifestChecksum     string  // SHA-256 of canonical reconciliation manifest
}

// ResetAuthorizer determines whether an uncheckpointed partition with existing Kafka commands
// is explicitly authorized to bootstrap with an empty order book.
type ResetAuthorizer interface {
	AuthorizeEmptyReset(ctx context.Context, marketID string, partition int, logEndOffset int64) (ResetAuthResult, error)
}

func maskToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return "***" + token[len(token)-4:]
}

// EnvResetAuthorizer authorizes an empty book reset via environment variable ME_ALLOW_EMPTY_RESET.
// Formats:
//   - "MARKET:PARTITION:GENERATION"
//   - "MARKET:PARTITION:GENERATION:SECRET"
//   - "MARKET:PARTITION:GENERATION:EXPECTED_CHECKPOINT:EXPECTED_SEQUENCE"
//   - "MARKET:PARTITION:GENERATION:EXPECTED_CHECKPOINT:EXPECTED_SEQUENCE:SECRET"
//   - "MARKET:PARTITION:GENERATION:EXPECTED_CHECKPOINT:EXPECTED_SEQUENCE:EXPECTED_HWM:SECRET"
//   - "MARKET:PARTITION:GENERATION:EXPECTED_CHECKPOINT:EXPECTED_SEQUENCE:EXPECTED_HWM:MANIFEST_CHECKSUM:SECRET"
//
// Preconditions EXPECTED_CHECKPOINT and EXPECTED_SEQUENCE may be numeric or "*" (wildcard).
// Enforces generation scoping, state preconditions, boundary contract, and single-use claim within process lifetime.
type EnvResetAuthorizer struct {
	mu       sync.Mutex
	consumed map[string]bool
}

// NewEnvResetAuthorizer returns a new EnvResetAuthorizer.
func NewEnvResetAuthorizer() *EnvResetAuthorizer {
	return &EnvResetAuthorizer{
		consumed: make(map[string]bool),
	}
}

// AuthorizeEmptyReset validates the environment variable against the target market and partition.
func (a *EnvResetAuthorizer) AuthorizeEmptyReset(ctx context.Context, marketID string, partition int, logEndOffset int64) (ResetAuthResult, error) {
	raw := os.Getenv("ME_ALLOW_EMPTY_RESET")
	if raw == "" {
		return ResetAuthResult{}, nil
	}
	parts := strings.Split(raw, ":")
	if len(parts) < 3 || len(parts) > 8 {
		return ResetAuthResult{}, fmt.Errorf("invalid ME_ALLOW_EMPTY_RESET format (expected MARKET:PARTITION:GENERATION[:EXPECTED_CHECKPOINT:EXPECTED_SEQUENCE][:EXPECTED_HWM][:MANIFEST_CHECKSUM][:SECRET], got %q)", raw)
	}
	authMarket := strings.TrimSpace(parts[0])
	authPartStr := strings.TrimSpace(parts[1])
	generation := strings.TrimSpace(parts[2])

	authPart, err := strconv.Atoi(authPartStr)
	if err != nil {
		return ResetAuthResult{}, fmt.Errorf("invalid partition in ME_ALLOW_EMPTY_RESET: %w", err)
	}

	if authMarket != marketID || authPart != partition {
		return ResetAuthResult{}, nil
	}
	if generation == "" {
		return ResetAuthResult{}, fmt.Errorf("generation in ME_ALLOW_EMPTY_RESET cannot be empty")
	}

	secret := generation
	var expectedCheckpoint *int64
	var expectedSequence *uint64
	var expectedHWM *int64
	var manifestChecksum string

	if len(parts) == 4 {
		secret = strings.TrimSpace(parts[3])
		if secret == "" {
			return ResetAuthResult{}, fmt.Errorf("secret token in ME_ALLOW_EMPTY_RESET cannot be empty when provided")
		}
	} else if len(parts) >= 5 {
		chkStr := strings.TrimSpace(parts[3])
		if chkStr != "*" && chkStr != "" {
			chkVal, err := strconv.ParseInt(chkStr, 10, 64)
			if err != nil {
				return ResetAuthResult{}, fmt.Errorf("invalid expected_checkpoint in ME_ALLOW_EMPTY_RESET: %w", err)
			}
			expectedCheckpoint = &chkVal
		}

		seqStr := strings.TrimSpace(parts[4])
		if seqStr != "*" && seqStr != "" {
			seqVal, err := strconv.ParseUint(seqStr, 10, 64)
			if err != nil {
				return ResetAuthResult{}, fmt.Errorf("invalid expected_sequence in ME_ALLOW_EMPTY_RESET: %w", err)
			}
			expectedSequence = &seqVal
		}

		if len(parts) == 6 {
			secret = strings.TrimSpace(parts[5])
			if secret == "" {
				return ResetAuthResult{}, fmt.Errorf("secret token in ME_ALLOW_EMPTY_RESET cannot be empty when provided")
			}
		} else if len(parts) == 7 {
			hwmStr := strings.TrimSpace(parts[5])
			if hwmStr != "*" && hwmStr != "" {
				hwmVal, err := strconv.ParseInt(hwmStr, 10, 64)
				if err != nil {
					return ResetAuthResult{}, fmt.Errorf("invalid expected_hwm in ME_ALLOW_EMPTY_RESET: %w", err)
				}
				expectedHWM = &hwmVal
			}
			secret = strings.TrimSpace(parts[6])
			if secret == "" {
				return ResetAuthResult{}, fmt.Errorf("secret token in ME_ALLOW_EMPTY_RESET cannot be empty when provided")
			}
		} else if len(parts) == 8 {
			hwmStr := strings.TrimSpace(parts[5])
			if hwmStr != "*" && hwmStr != "" {
				hwmVal, err := strconv.ParseInt(hwmStr, 10, 64)
				if err != nil {
					return ResetAuthResult{}, fmt.Errorf("invalid expected_hwm in ME_ALLOW_EMPTY_RESET: %w", err)
				}
				expectedHWM = &hwmVal
			}
			manifestChecksum = strings.TrimSpace(parts[6])
			secret = strings.TrimSpace(parts[7])
			if secret == "" {
				return ResetAuthResult{}, fmt.Errorf("secret token in ME_ALLOW_EMPTY_RESET cannot be empty when provided")
			}
		}
	}

	if expectedHWM == nil && os.Getenv("ME_RESET_EXPECTED_HWM") != "" {
		hwmVal, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("ME_RESET_EXPECTED_HWM")), 10, 64)
		if err == nil {
			expectedHWM = &hwmVal
		}
	}
	if manifestChecksum == "" && os.Getenv("ME_RESET_MANIFEST_CHECKSUM") != "" {
		manifestChecksum = strings.TrimSpace(os.Getenv("ME_RESET_MANIFEST_CHECKSUM"))
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	key := fmt.Sprintf("%s:%d:%s", marketID, partition, generation)
	if a.consumed[key] {
		return ResetAuthResult{}, fmt.Errorf("reset authorization generation %q for %s partition %d was already consumed", generation, marketID, partition)
	}
	a.consumed[key] = true

	hash := sha256.Sum256([]byte(secret))
	tokenHash := fmt.Sprintf("%x", hash)
	tokenFingerprint := maskToken(secret)

	return ResetAuthResult{
		Authorized:           true,
		Generation:           generation,
		TokenHash:            tokenHash,
		TokenFingerprint:     tokenFingerprint,
		ExpectedCheckpoint:   expectedCheckpoint,
		ExpectedSequence:     expectedSequence,
		ExpectedLogEndOffset: expectedHWM,
		ManifestChecksum:     manifestChecksum,
	}, nil
}
