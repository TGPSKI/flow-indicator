package harness

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestFileHarnessesShareBootstrapBoundary(t *testing.T) {
	lines := map[string]string{
		"generic":     `{"speaker":"user","text":"Implement the parser."}`,
		"claude-code": `{"type":"user","uuid":"u1","message":{"role":"user","content":"Implement the parser."}}`,
		"qwen":        `{"type":"user","uuid":"u1","provenance":"real_user","message":{"role":"user","parts":[{"text":"Implement the parser."}]}}`,
		"codex":       `{"type":"event_msg","payload":{"type":"user_message","message":"Implement the parser."}}`,
	}
	for name, line := range lines {
		t.Run(name, func(t *testing.T) {
			path := writeRollout(t, []string{line})
			live, err := Follow(name, path, "", "common", 0)
			if err != nil {
				t.Fatal(err)
			}
			defer live.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			old, err := live.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(old) != 1 || !old[0].IsOperatorTurn() || !live.Historical(old[0]) {
				t.Fatalf("bootstrap=%+v", old)
			}
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString(line + "\n")
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := live.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(fresh) != 1 || !fresh[0].IsOperatorTurn() || live.Historical(fresh[0]) {
				t.Fatalf("arrival=%+v", fresh)
			}
		})
	}
}
