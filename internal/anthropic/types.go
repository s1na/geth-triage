package anthropic

// AnalysisResult is the structured response from Claude.
type AnalysisResult struct {
	Category   string   `json:"category"`
	Confidence float64  `json:"confidence"`
	Explanation string  `json:"explanation"`
	RelatedPRs []int    `json:"related_prs"`
}

// Valid categories for PR triage.
var ValidCategories = map[string]bool{
	"closeable":       true,
	"high-priority":   true,
	"duplicate":       true,
	"needs-attention": true,
	"normal":          true,
}
