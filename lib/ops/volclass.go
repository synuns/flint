package ops

import "strings"

const (
	VolClassImmutable   = "vol:immutable"
	VolClassVolatile    = "vol:volatile"
	VolClassConditional = "vol:conditional"
)

var volImmutableKeywords = []string{
	"adr", "decision", "decided", "history", "immutable",
	"결정", "역사",
}

var volVolatileKeywords = []string{
	"version", "path", "command", "tool",
	"버전", "경로", "명령", "도구",
}

func volKeywordMatch(topic, tags string, keywords []string) bool {
	hay := strings.ToLower(topic + " " + tags)
	for _, k := range keywords {
		if strings.Contains(hay, k) {
			return true
		}
	}
	return false
}

func SuggestVolClass(topic, tags string) string {
	switch {
	case volKeywordMatch(topic, tags, volImmutableKeywords):
		return VolClassImmutable
	case volKeywordMatch(topic, tags, volVolatileKeywords):
		return VolClassVolatile
	default:
		return VolClassConditional
	}
}

var claimDefaultVol = map[string]string{
	"decision":  VolClassImmutable,
	"procedure": VolClassConditional,
	"lesson":    VolClassConditional,
	"event":     VolClassImmutable,
}

func ClaimDefaultVol(claim string) string {
	if v, ok := claimDefaultVol[claim]; ok {
		return v
	}
	return VolClassConditional
}

func SuggestVolClassForSource(topic, tags, source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "human", "correction":
		return VolClassImmutable
	case "import", "vault":
		return VolClassConditional
	default:
		return SuggestVolClass(topic, tags)
	}
}

func ExistingVolTag(tags string) string {
	for _, v := range recheckVolDict {
		if strings.Contains(tags, ":"+v+":") {
			return v
		}
	}
	return ""
}
