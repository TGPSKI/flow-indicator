package render

import (
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/store"
)

func TestSemanticOperationsAreReportedApartFromMetrics(t *testing.T) {
	session, err := store.OpenSession(t.TempDir(), "semantic-report", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, completion := range []classify.Completion{
		{JobID: "done", StreamID: "semantic-report", Seq: 1, Classifier: "local", ClassifierVersion: "1", ClassifierHash: "aaa", Status: classify.CompletionCompleted, LatencyMS: 10},
		{JobID: "timeout", StreamID: "semantic-report", Seq: 2, Classifier: "local", ClassifierVersion: "1", ClassifierHash: "aaa", Status: classify.CompletionTimedOut, LatencyMS: 30},
		{JobID: "failure", StreamID: "semantic-report", Seq: 3, Classifier: "local", ClassifierVersion: "2", ClassifierHash: "bbb", Status: classify.CompletionFailed, Error: "endpoint returned status 500", LatencyMS: 20},
	} {
		if err := session.Append(event.NewWithIdentity("semantic-report", completion.Seq, 0, completion.Source,
			event.KindSemanticCompleted, event.ClassClassified, completion.JobID, completion)); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(session.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Semantic; got.Recorded != 3 || got.Completed != 1 || got.TimedOut != 1 || got.Failed != 1 {
		t.Fatalf("semantic operations = %+v", got)
	}
	if loaded.Semantic.P50MS == nil || *loaded.Semantic.P50MS != 20 || loaded.Semantic.P95MS == nil || *loaded.Semantic.P95MS != 30 {
		t.Fatalf("semantic latency = p50 %v p95 %v", loaded.Semantic.P50MS, loaded.Semantic.P95MS)
	}
	if loaded.Final().TurnIndex != 0 {
		t.Fatalf("semantic completions changed the interaction projection: final = %+v", loaded.Final())
	}
	summary := loaded.Summarize()
	if summary.Semantic == nil || summary.Semantic.Recorded != 3 {
		t.Fatalf("summary semantic operations = %+v", summary.Semantic)
	}
	report := loaded.Report()
	for _, want := range []string{"## semantic operations", "| 3 | 1 | 1 | 1 | 0 | 20 ms | 30 ms |", "local/1/aaa", "local/2/bbb", "latest outcome: source 3, failed: endpoint returned status 500"} {
		if !strings.Contains(report, want) {
			t.Errorf("report misses %q:\n%s", want, report)
		}
	}
}
