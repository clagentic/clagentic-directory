package selfbuild

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// agentDeclBlock is the on-disk schema for a clagentic-directory self-declaration
// block embedded in an AGENT.md or SKILL.md file.
//
// Agents embed the block as an HTML comment to keep it invisible in rendered docs:
//
//	<!-- clagentic-directory
//	capabilities:
//	  - id: review-pr
//	    name: Review PR
//	    description: Performs structured code review
//	    intents: [code-review, review-pr]
//	    conversation_kinds: [review]
//	    format: structured-markdown
//	-->
//
// All fields are optional; omitted fields yield zero values in the proposal.
type agentDeclBlock struct {
	Capabilities []agentDeclCapability `yaml:"capabilities"`
}

type agentDeclCapability struct {
	ID                string   `yaml:"id"`
	Name              string   `yaml:"name"`
	Description       string   `yaml:"description"`
	Intents           []string `yaml:"intents"`
	ConversationKinds []string `yaml:"conversation_kinds"`
	AfterRoles        []string `yaml:"after_roles"`
	AfterAgents       []string `yaml:"after_agents"`
	Format            string   `yaml:"format"`
	VerdictField      string   `yaml:"verdict_field"`
}

const (
	declBlockOpen  = "<!-- clagentic-directory"
	declBlockClose = "-->"
)

// extractDeclBlock scans text (typically the full content of an AGENT.md or the
// "+"-prefixed lines of a diff) for a <!-- clagentic-directory ... --> block and
// returns the YAML inside it.  Returns "" if no block is found.
func extractDeclBlock(text string) string {
	start := strings.Index(text, declBlockOpen)
	if start == -1 {
		return ""
	}
	inner := text[start+len(declBlockOpen):]
	end := strings.Index(inner, declBlockClose)
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(inner[:end])
}

// parseAgentDecl parses a clagentic-directory YAML block extracted from an
// AGENT.md or SKILL.md file.  Returns nil if the YAML is empty or unparseable.
func parseAgentDecl(yamlText string) *agentDeclBlock {
	if yamlText == "" {
		return nil
	}
	var block agentDeclBlock
	if err := yaml.Unmarshal([]byte(yamlText), &block); err != nil {
		return nil
	}
	if len(block.Capabilities) == 0 {
		return nil
	}
	return &block
}

// declToProposedCapabilities converts a parsed agent declaration to ProposedCapability
// slice with ConfidenceExtracted on all directly-specified fields.
func declToProposedCapabilities(block *agentDeclBlock) []ProposedCapability {
	out := make([]ProposedCapability, 0, len(block.Capabilities))
	for _, c := range block.Capabilities {
		pc := ProposedCapability{
			ID:          AnnotatedString{Value: c.ID, Confidence: ConfidenceExtracted},
			Name:        AnnotatedString{Value: c.Name, Confidence: ConfidenceExtracted},
			Description: AnnotatedString{Value: c.Description, Confidence: ConfidenceExtracted},
			Format:      AnnotatedString{Value: c.Format, Confidence: ConfidenceExtracted},
		}

		intents := c.Intents
		if len(intents) == 0 {
			intents = nil
		}
		pc.Intents = AnnotatedStrings{Values: intents, Confidence: ConfidenceExtracted}

		if len(c.ConversationKinds) > 0 {
			pc.ConversationKinds = &AnnotatedStrings{Values: c.ConversationKinds, Confidence: ConfidenceExtracted}
		}
		if len(c.AfterRoles) > 0 {
			pc.AfterRoles = &AnnotatedStrings{Values: c.AfterRoles, Confidence: ConfidenceExtracted}
		}
		if len(c.AfterAgents) > 0 {
			pc.AfterAgents = &AnnotatedStrings{Values: c.AfterAgents, Confidence: ConfidenceExtracted}
		}
		if c.VerdictField != "" {
			pc.VerdictField = &AnnotatedString{Value: c.VerdictField, Confidence: ConfidenceExtracted}
		}

		out = append(out, pc)
	}
	return out
}

// stripDiffPrefixes returns a copy of text with unified-diff `+` and `-` line
// prefixes removed, so that a <!-- clagentic-directory --> block embedded in a
// diff is found by the same extractDeclBlock logic as a full file.
func stripDiffPrefixes(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			out = append(out, l[1:])
		} else {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// extractCapabilitiesFromText is the unified entry point for capability extraction
// from AGENT.md / SKILL.md text (full file content or diff "+"-lines).
//
// Priority:
//  1. If a <!-- clagentic-directory ... --> block is present (in raw text or after
//     stripping unified-diff prefixes), extract with ConfidenceExtracted — the
//     agent explicitly declared its capabilities.
//  2. Otherwise, fall back to heuristic diff extraction with ConfidenceInferred.
func extractCapabilitiesFromText(text string) ([]ProposedCapability, bool) {
	// Try raw text first (full file content).
	if yamlText := extractDeclBlock(text); yamlText != "" {
		if block := parseAgentDecl(yamlText); block != nil {
			return declToProposedCapabilities(block), true
		}
	}
	// Try after stripping unified-diff +/- prefixes.
	stripped := stripDiffPrefixes(text)
	if yamlText := extractDeclBlock(stripped); yamlText != "" {
		if block := parseAgentDecl(yamlText); block != nil {
			return declToProposedCapabilities(block), true
		}
	}
	return extractCapabilitiesFromDiff(text), false // heuristic
}
