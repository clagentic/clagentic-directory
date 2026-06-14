package selfbuild_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/clagentic/clagentic-directory/internal/selfbuild"
	"gopkg.in/yaml.v3"
)

// fixture: two events — one agent-def file, one unrelated file.
var sourceFixtureEvents = []selfbuild.SourceEvent{
	{
		ID:        "ev-001",
		Timestamp: time.Now(),
		Kind:      "file-diff",
		FilePath:  "/workspace/agents/builder/SKILL.md",
		Agent:     "builder",
		Diff: `--- a/SKILL.md
+++ b/SKILL.md
@@ -1,3 +1,6 @@
+## Trigger
+Use when: user asks to build something
+
+## Capability: code-builder
+Returns structured-markdown with implementation notes
 # Builder Agent`,
	},
	{
		ID:        "ev-002",
		Timestamp: time.Now(),
		Kind:      "file-diff",
		FilePath:  "/workspace/agents/builder/README.md", // not a SKILL.md / AGENT.md
		Agent:     "builder",
		Diff:      "+some readme change",
	},
	{
		ID:        "ev-003",
		Timestamp: time.Now(),
		Kind:      "file-diff",
		FilePath:  "/workspace/agents/diagnostician/AGENT.md",
		// Agent field empty — should be extracted from path.
		Diff: `+## Trigger
+intent: diagnose-failure
+
+## Capability: root-cause
+Returns json with root cause details`,
	},
}

func TestSourceWatcher_ProcessEvents(t *testing.T) {
	baseDir := t.TempDir()
	w := selfbuild.NewSourceWatcher(selfbuild.SourceWatchConfig{
		MemoryAPIURL: "http://localhost:9100",
		BaseDir:      baseDir,
		PollInterval: time.Hour, // don't actually poll in tests
		RateWindow:   time.Hour,
	})

	written, err := w.ProcessEvents(sourceFixtureEvents)
	if err != nil {
		t.Fatalf("ProcessEvents: %v", err)
	}

	// Two agent-def files: builder (SKILL.md) and diagnostician (AGENT.md). README.md skipped.
	if len(written) != 2 {
		t.Fatalf("len(written) = %d, want 2", len(written))
	}

	// All files must be inside proposed_changes/.
	for _, path := range written {
		if filepath.Dir(path) != filepath.Join(baseDir, "proposed_changes") {
			t.Errorf("file outside proposed_changes/: %s", path)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var pc selfbuild.ProposedChange
		if err := yaml.Unmarshal(data, &pc); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
		if pc.Source != "source-watch" {
			t.Errorf("Source = %q, want source-watch", pc.Source)
		}
		if pc.AgentName == "" {
			t.Errorf("AgentName is empty for %s", path)
		}
	}
}

func TestSourceWatcher_SkipsNonAgentDefFiles(t *testing.T) {
	baseDir := t.TempDir()
	w := selfbuild.NewSourceWatcher(selfbuild.SourceWatchConfig{
		BaseDir:    baseDir,
		RateWindow: time.Hour,
	})

	nonDefEvents := []selfbuild.SourceEvent{
		{
			ID:       "ev-readme",
			FilePath: "/workspace/foo/README.md",
			Agent:    "foo",
			Diff:     "+some line",
		},
		{
			ID:       "ev-go",
			FilePath: "/workspace/foo/main.go",
			Agent:    "foo",
			Diff:     "+func main() {}",
		},
	}

	written, err := w.ProcessEvents(nonDefEvents)
	if err != nil {
		t.Fatalf("ProcessEvents: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected no proposed changes for non-agent-def files, got %d", len(written))
	}
}

func TestSourceWatcher_RateLimit(t *testing.T) {
	baseDir := t.TempDir()
	// Rate window larger than test duration → duplicates are suppressed.
	w := selfbuild.NewSourceWatcher(selfbuild.SourceWatchConfig{
		BaseDir:    baseDir,
		RateWindow: time.Hour,
	})

	sameAgentEvents := []selfbuild.SourceEvent{
		{ID: "a", FilePath: "/workspace/agents/builder/SKILL.md", Agent: "builder", Diff: "+trigger: build"},
		{ID: "b", FilePath: "/workspace/agents/builder/SKILL.md", Agent: "builder", Diff: "+trigger: review"},
	}

	written, err := w.ProcessEvents(sameAgentEvents)
	if err != nil {
		t.Fatalf("ProcessEvents: %v", err)
	}
	// Second event for same agent within rate window must be suppressed.
	if len(written) != 1 {
		t.Errorf("expected 1 proposed change (rate-limited), got %d", len(written))
	}
}

func TestSourceWatcher_NoDirectRegistryWrite(t *testing.T) {
	baseDir := t.TempDir()
	w := selfbuild.NewSourceWatcher(selfbuild.SourceWatchConfig{
		BaseDir:    baseDir,
		RateWindow: time.Hour,
	})

	events := []selfbuild.SourceEvent{
		{ID: "x", FilePath: "/workspace/agents/merge-gate/SKILL.md", Agent: "merge-gate", Diff: "+trigger: merge-pr"},
	}
	_, err := w.ProcessEvents(events)
	if err != nil {
		t.Fatalf("ProcessEvents: %v", err)
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "proposed_changes" {
			t.Errorf("unexpected file in baseDir: %s", e.Name())
		}
	}
}
