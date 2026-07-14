package capabilityadvisor

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxGoalIDBytes          = 128
	maxObjectiveBytes       = 512
	maxPhaseBytes           = 96
	maxCurrentActivityBytes = 640
	maxDesiredOutcomeBytes  = 512
	maxInputKinds           = 16
	maxInputKindBytes       = 48
	maxResolverQueryBytes   = 2048
)

var (
	urlPattern                 = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s]+`)
	sensitiveAssignmentPattern = regexp.MustCompile(`(?i)\b(?:authorization|proxy-authorization|password|passwd|secret|api[ _-]?key|access[ _-]?token|refresh[ _-]?token|session[ _-]?token|cookie|credential)\b\s*[:=]\s*\S+`)
	bearerPattern              = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`)
	knownTokenPattern          = regexp.MustCompile(`\b(?:AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{16,})\b`)
	signedQueryPattern         = regexp.MustCompile(`(?i)[?&](?:x-amz-[^=]*|x-goog-[^=]*|signature|sig|access_token|token)=[^\s&]+`)
)

type preparedRequest struct {
	query string
	key   cacheKey
}

func prepareRequest(request Request) (preparedRequest, error) {
	goalID, err := exactHostID(request.GoalID)
	if err != nil {
		return preparedRequest{}, err
	}
	objective, err := safeSummary("objective", request.Activity.Objective, maxObjectiveBytes)
	if err != nil {
		return preparedRequest{}, err
	}
	phase, err := safeSummary("phase", request.Activity.Phase, maxPhaseBytes)
	if err != nil {
		return preparedRequest{}, err
	}
	current, err := safeSummary("current activity", request.Activity.CurrentActivity, maxCurrentActivityBytes)
	if err != nil {
		return preparedRequest{}, err
	}
	outcome, err := safeSummary("desired outcome", request.Activity.DesiredOutcome, maxDesiredOutcomeBytes)
	if err != nil {
		return preparedRequest{}, err
	}
	kinds, err := safeInputKinds(request.Activity.AvailableInputKinds)
	if err != nil {
		return preparedRequest{}, err
	}
	available := "none"
	if len(kinds) > 0 {
		available = strings.Join(kinds, ", ")
	}
	query := fmt.Sprintf(
		"Goal: %s. Phase: %s. Current activity: %s. Desired outcome: %s. Available inputs: %s.",
		objective, phase, current, outcome, available,
	)
	if len(query) > maxResolverQueryBytes {
		return preparedRequest{}, errors.New("resolver query exceeds bounded size")
	}

	return preparedRequest{
		query: query,
		key:   activityCacheKey(goalID, objective, phase, current, outcome, kinds, request.CacheDiscriminator),
	}, nil
}

func exactHostID(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("goal id is not valid UTF-8")
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxGoalIDBytes {
		return "", errors.New("goal id is empty or too long")
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.New("goal id contains whitespace or control characters")
		}
	}
	return value, nil
}

// safeSummary accepts one short host-authored sentence. Multiline or JSON
// documents are rejected rather than guessing whether they are raw file/tool
// content. URL query strings and fragments are removed before dispatch.
func safeSummary(field, value string, maxBytes int) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s is not valid UTF-8", field)
	}
	if strings.ContainsAny(value, "\r\n") || strings.Contains(value, "```") {
		return "", fmt.Errorf("%s must be a single host-authored summary", field)
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxBytes {
		return "", fmt.Errorf("%s is empty or too long", field)
	}
	if (strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[")) && json.Valid([]byte(value)) {
		return "", fmt.Errorf("%s looks like raw structured content", field)
	}
	// Preserve enough URL context to describe the activity while ensuring query
	// parameters and fragments never cross the resolver boundary.
	value = stripURLQueries(value)
	if strings.Contains(strings.ToUpper(value), "-----BEGIN ") ||
		sensitiveAssignmentPattern.MatchString(value) || bearerPattern.MatchString(value) ||
		knownTokenPattern.MatchString(value) || signedQueryPattern.MatchString(value) {
		return "", fmt.Errorf("%s contains credential-like material", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%s contains control characters", field)
		}
	}
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "", fmt.Errorf("%s is empty after privacy filtering", field)
	}
	return value, nil
}

func stripURLQueries(value string) string {
	return urlPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if index := strings.IndexAny(candidate, "?#"); index >= 0 {
			return candidate[:index]
		}
		return candidate
	})
}

func safeInputKinds(values []string) ([]string, error) {
	if len(values) > maxInputKinds {
		return nil, errors.New("too many available input kinds")
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !utf8.ValidString(value) {
			return nil, errors.New("input kind is not valid UTF-8")
		}
		value = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), "_"))
		if value == "" || len(value) > maxInputKindBytes {
			return nil, errors.New("input kind is empty or too long")
		}
		for _, r := range value {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("_-.", r) {
				return nil, errors.New("input kind must be a label, not an input value")
			}
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func activityCacheKey(goalID, objective, phase, current, outcome string, kinds []string, discriminator [32]byte) cacheKey {
	hash := sha256.New()
	for _, value := range []string{
		goalID,
		normalizeMaterialText(objective),
		normalizeMaterialText(phase),
		normalizeMaterialText(current),
		normalizeMaterialText(outcome),
		strings.Join(kinds, "\x00"),
	} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	_, _ = hash.Write(discriminator[:])
	var key cacheKey
	copy(key[:], hash.Sum(nil))
	return key
}

// normalizeMaterialText makes case, spacing, and punctuation-only edits reuse
// the same route while preserving word and number changes as cache misses.
func normalizeMaterialText(value string) string {
	var builder strings.Builder
	space := true
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			builder.WriteRune(r)
			space = false
			continue
		}
		if !space {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}
