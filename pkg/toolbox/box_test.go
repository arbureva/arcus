package toolbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arbureva/arcus/pkg/skill"
	"github.com/arbureva/arcus/pkg/tool"
)

func stub(name string) tool.Tool {
	return tool.Func(name, "stub "+name, nil,
		func(context.Context, json.RawMessage) (*tool.Result, error) {
			return tool.Text("ran " + name), nil
		})
}

func names(raw []interface{}) []string {
	defs, _ := tool.DefinitionsOf(raw)
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

func TestBoxFoldsAndUnfolds(t *testing.T) {
	box := New()
	box.Add(stub("clock"))
	box.Namespace("fs", "File access.", Tools(stub("read_file"), stub("write_file")),
		Instructions("Paths are project-relative."))
	box.Namespace("web", "Web browsing.", Tools(stub("fetch")))

	got := names(box.RequestTools())
	want := []string{"clock", DefaultMetaName}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("folded tools = %v", got)
	}

	// Meta-tool definition must list the folded namespaces.
	defs, _ := tool.DefinitionsOf(box.RequestTools())
	meta := defs[len(defs)-1]
	if !strings.Contains(meta.Description, "fs: File access. (2 tools)") {
		t.Fatalf("meta description = %q", meta.Description)
	}
	if !strings.Contains(string(meta.Schema), `"enum":["fs","web"]`) {
		t.Fatalf("meta schema = %s", meta.Schema)
	}

	// The model opens fs.
	res, err := box.Invoke(context.Background(), DefaultMetaName, json.RawMessage(`{"namespace":"fs"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Content, "Paths are project-relative.") {
		t.Fatalf("activation result = %+v", res)
	}
	if !strings.Contains(res.Content, "read_file, write_file") {
		t.Fatalf("activation result lacks tool list: %q", res.Content)
	}

	got = names(box.RequestTools())
	want = []string{"clock", "read_file", "write_file", DefaultMetaName}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after activation = %v", got)
	}

	// Opening the last namespace removes the meta tool entirely.
	if err := box.Activate("web"); err != nil {
		t.Fatal(err)
	}
	got = names(box.RequestTools())
	for _, n := range got {
		if n == DefaultMetaName {
			t.Fatalf("meta tool still advertised with nothing folded: %v", got)
		}
	}

	// Dispatch reaches namespace tools.
	res, err = box.Invoke(context.Background(), "fetch", json.RawMessage(`{}`))
	if err != nil || res.Content != "ran fetch" {
		t.Fatalf("fetch: %v %+v", err, res)
	}
}

func TestBoxUnknownNamespaceIsModelVisible(t *testing.T) {
	box := New()
	box.Namespace("fs", "Files.", Tools(stub("read_file")))
	res, err := box.Invoke(context.Background(), DefaultMetaName, json.RawMessage(`{"namespace":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError, got %+v", res)
	}
}

func TestBoxSkillAndClone(t *testing.T) {
	sk := skill.MustNew(skill.Skill{
		Name:         "invoice",
		Description:  "Generate invoices.",
		Instructions: "Always use template A.",
		Tools:        []tool.Tool{stub("render_pdf")},
	})
	tmpl := New()
	tmpl.AddSkill(sk)

	a, b := tmpl.Clone(), tmpl.Clone()
	if err := a.Activate("invoice"); err != nil {
		t.Fatal(err)
	}
	if len(a.Active()) != 1 || len(b.Active()) != 0 {
		t.Fatalf("activation leaked across clones: a=%v b=%v", a.Active(), b.Active())
	}
	res, _ := a.Invoke(context.Background(), a.metaName, json.RawMessage(`{"namespace":"invoice"}`))
	if !strings.Contains(res.Content, "Always use template A.") {
		t.Fatalf("skill instructions missing: %q", res.Content)
	}
}
