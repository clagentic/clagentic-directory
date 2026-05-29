package selfbuild

import (
	"testing"
)

const agentMDWithDecl = `# My Agent

Does useful things.

<!-- clagentic-directory
capabilities:
  - id: review-pr
    name: Review PR
    description: Performs structured code review
    intents: [code-review, review-pr]
    conversation_kinds: [review, build]
    after_agents: [builder]
    verdict_field: review_result
    format: structured-markdown
  - id: review-commit
    name: Review Commit
    description: Reviews a single commit
    intents: [review-commit]
    format: structured-markdown
-->

## When to use

Use this agent for code review.
`

const agentMDWithoutDecl = `# My Agent

Does useful things.

## Trigger

Use when: user asks to review code
`

const diffWithDecl = `--- a/AGENT.md
+++ b/AGENT.md
@@ -1,3 +1,12 @@
+<!-- clagentic-directory
+capabilities:
+  - id: review-pr
+    name: Review PR
+    description: Structured code review
+    intents: [code-review]
+    conversation_kinds: [review]
+    format: structured-markdown
+-->
 # My Agent`

func TestExtractDeclBlock_Present(t *testing.T) {
	got := extractDeclBlock(agentMDWithDecl)
	if got == "" {
		t.Fatal("extractDeclBlock: expected non-empty result")
	}
	if !contains(got, "review-pr") {
		t.Errorf("extractDeclBlock: want 'review-pr' in output, got: %s", got)
	}
}

func TestExtractDeclBlock_Absent(t *testing.T) {
	got := extractDeclBlock(agentMDWithoutDecl)
	if got != "" {
		t.Errorf("extractDeclBlock: expected empty for file without block, got: %q", got)
	}
}

func TestParseAgentDecl_Full(t *testing.T) {
	yaml := extractDeclBlock(agentMDWithDecl)
	block := parseAgentDecl(yaml)
	if block == nil {
		t.Fatal("parseAgentDecl: returned nil")
	}
	if len(block.Capabilities) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(block.Capabilities))
	}
	c := block.Capabilities[0]
	if c.ID != "review-pr" {
		t.Errorf("ID: want review-pr, got %s", c.ID)
	}
	if len(c.Intents) != 2 {
		t.Errorf("Intents: want 2, got %d", len(c.Intents))
	}
	if len(c.ConversationKinds) != 2 {
		t.Errorf("ConversationKinds: want 2, got %d", len(c.ConversationKinds))
	}
	if len(c.AfterAgents) != 1 || c.AfterAgents[0] != "builder" {
		t.Errorf("AfterAgents: want [builder], got %v", c.AfterAgents)
	}
	if c.VerdictField != "review_result" {
		t.Errorf("VerdictField: want review_result, got %s", c.VerdictField)
	}
}

func TestDeclToProposedCapabilities_Confidence(t *testing.T) {
	yaml := extractDeclBlock(agentMDWithDecl)
	block := parseAgentDecl(yaml)
	if block == nil {
		t.Fatal("parseAgentDecl: returned nil")
	}
	caps := declToProposedCapabilities(block)
	if len(caps) != 2 {
		t.Fatalf("expected 2 caps, got %d", len(caps))
	}
	c := caps[0]
	if c.ID.Confidence != ConfidenceExtracted {
		t.Errorf("ID.Confidence: want extracted, got %s", c.ID.Confidence)
	}
	if c.Intents.Confidence != ConfidenceExtracted {
		t.Errorf("Intents.Confidence: want extracted, got %s", c.Intents.Confidence)
	}
	if c.ConversationKinds == nil {
		t.Fatal("ConversationKinds should not be nil")
	}
	if c.ConversationKinds.Confidence != ConfidenceExtracted {
		t.Errorf("ConversationKinds.Confidence: want extracted, got %s", c.ConversationKinds.Confidence)
	}
	if c.AfterAgents == nil {
		t.Fatal("AfterAgents should not be nil")
	}
	if len(c.AfterAgents.Values) != 1 {
		t.Errorf("AfterAgents.Values: want 1, got %d", len(c.AfterAgents.Values))
	}
	if c.VerdictField == nil {
		t.Fatal("VerdictField should not be nil")
	}
	if c.VerdictField.Value != "review_result" {
		t.Errorf("VerdictField.Value: want review_result, got %s", c.VerdictField.Value)
	}
}

func TestExtractCapabilitiesFromText_Declared(t *testing.T) {
	caps, declared := extractCapabilitiesFromText(agentMDWithDecl)
	if !declared {
		t.Error("declared: want true, got false")
	}
	if len(caps) != 2 {
		t.Errorf("caps: want 2, got %d", len(caps))
	}
	if caps[0].ID.Confidence != ConfidenceExtracted {
		t.Errorf("confidence: want extracted, got %s", caps[0].ID.Confidence)
	}
}

func TestExtractCapabilitiesFromText_Heuristic(t *testing.T) {
	_, declared := extractCapabilitiesFromText(agentMDWithoutDecl)
	if declared {
		t.Error("declared: want false for file without block")
	}
}

func TestExtractCapabilitiesFromText_DiffWithDecl(t *testing.T) {
	caps, declared := extractCapabilitiesFromText(diffWithDecl)
	if !declared {
		t.Error("declared: want true when diff contains decl block")
	}
	if len(caps) == 0 {
		t.Error("expected at least 1 cap from diff with decl block")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
