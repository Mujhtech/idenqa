package experience

import (
	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
)

// ResolutionRequest carries the bounded, non-secret dimensions a capture client
// supplies at bootstrap. An empty dimension cannot satisfy a rule that
// constrains it, so unproven targeting fails closed.
type ResolutionRequest struct {
	Workflow      string
	Country       string
	ApplicationID string
	Origin        string
	SDKVersion    string
	Locale        string
}

// MatchDocument returns the specificity of the first matching rule. Specificity
// is the number of constrained dimensions; a document without rules is a
// catch-all with specificity zero. The second result reports whether any rule
// matched.
func MatchDocument(document contract.Document, request ResolutionRequest) (int, bool) {
	if len(document.Targeting) == 0 {
		return 0, true
	}
	for _, rule := range document.Targeting {
		if matchesRule(rule, request) {
			return specificity(rule), true
		}
	}
	return 0, false
}

func matchesRule(rule contract.Target, request ResolutionRequest) bool {
	if rule.Workflow != "" && rule.Workflow != request.Workflow {
		return false
	}
	if len(rule.Countries) > 0 && (request.Country == "" || !contains(rule.Countries, request.Country)) {
		return false
	}
	if len(rule.ApplicationIDs) > 0 && (request.ApplicationID == "" || !contains(rule.ApplicationIDs, request.ApplicationID)) {
		return false
	}
	if len(rule.Origins) > 0 && (request.Origin == "" || !contains(rule.Origins, request.Origin)) {
		return false
	}
	if rule.SDKVersionMin != "" {
		if request.SDKVersion == "" || compareVersions(request.SDKVersion, rule.SDKVersionMin) < 0 || compareVersions(request.SDKVersion, rule.SDKVersionMax) > 0 {
			return false
		}
	}
	return true
}

func specificity(rule contract.Target) int {
	score := 0
	for _, constrained := range []bool{
		rule.Workflow != "",
		len(rule.Countries) > 0,
		len(rule.ApplicationIDs) > 0,
		len(rule.Origins) > 0,
		rule.SDKVersionMin != "",
	} {
		if constrained {
			score++
		}
	}
	return score
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func compareVersions(left string, right string) int {
	leftParts := splitVersion(left)
	rightParts := splitVersion(right)
	for index := 0; index < 3; index++ {
		if leftParts[index] != rightParts[index] {
			if leftParts[index] < rightParts[index] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVersion(value string) [3]int {
	var result [3]int
	segment := 0
	current := 0
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '.' {
			if segment < 2 {
				result[segment] = current
			}
			segment++
			current = 0
			continue
		}
		if character >= '0' && character <= '9' {
			current = current*10 + int(character-'0')
		}
	}
	if segment < 3 {
		result[segment] = current
	}
	return result
}

// Resolve returns the single most specific published match. No match reports
// false; equally specific matches from different experiences return
// ErrResolutionConflict so callers fall back to the signed safe default.
func Resolve(candidates []Published, request ResolutionRequest) (Published, bool, error) {
	bestScore := -1
	var best Published
	ambiguous := false
	for _, candidate := range candidates {
		score, matched := MatchDocument(candidate.Manifest.Document, request)
		if !matched {
			continue
		}
		switch {
		case score > bestScore:
			bestScore, best, ambiguous = score, candidate, false
		case score == bestScore && best.ExperienceID != candidate.ExperienceID:
			ambiguous = true
		}
	}
	if bestScore < 0 {
		return Published{}, false, nil
	}
	if ambiguous {
		return Published{}, false, ErrResolutionConflict
	}
	return best, true, nil
}
