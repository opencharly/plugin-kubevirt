package kubevirt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt/params"
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// dispatch_test.go — the dispatch surfaces: the deep OpValidate rules, the kind OpLoad
// echo, the class routing, and the verb's box-mode skip. These are cluster-free.

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestValidateKubeVirtDeep covers every XOR rule.
func TestValidateKubeVirtDeep(t *testing.T) {
	cases := []struct {
		name      string
		body      map[string]any
		wantPaths []string
	}{
		{
			name: "container_disk clean",
			body: map[string]any{"source": map[string]any{"kind": "container_disk", "image": "x"}},
		},
		{
			name:      "empty source kind",
			body:      map[string]any{"source": map[string]any{}},
			wantPaths: []string{"source.kind"},
		},
		{
			name:      "unknown source kind",
			body:      map[string]any{"source": map[string]any{"kind": "floppy"}},
			wantPaths: []string{"source.kind"},
		},
		{
			name:      "container_disk with storage_class",
			body:      map[string]any{"source": map[string]any{"kind": "container_disk", "image": "x", "storage_class": "sc"}},
			wantPaths: []string{"source.storage_class"},
		},
		{
			name: "gpu both set",
			body: map[string]any{
				"source": map[string]any{"kind": "container_disk", "image": "x"},
				"gpus":   []map[string]any{{"resource_name": "nvidia.com/gpu", "device_name": "0000:01:00.0"}},
			},
			wantPaths: []string{"gpus[0]"},
		},
		{
			name: "gpu neither set",
			body: map[string]any{
				"source": map[string]any{"kind": "container_disk", "image": "x"},
				"gpus":   []map[string]any{{}},
			},
			wantPaths: []string{"gpus[0]"},
		},
		{
			name: "gpu resource only clean",
			body: map[string]any{
				"source": map[string]any{"kind": "container_disk", "image": "x"},
				"gpus":   []map[string]any{{"resource_name": "nvidia.com/gpu"}},
			},
		},
		{
			name: "instancetype with cpu",
			body: map[string]any{
				"source":       map[string]any{"kind": "container_disk", "image": "x"},
				"instancetype": "u1.medium",
				"cpu":          map[string]any{"cores": 2},
			},
			wantPaths: []string{"instancetype"},
		},
		{
			name: "instancetype without cpu clean",
			body: map[string]any{
				"source":       map[string]any{"kind": "container_disk", "image": "x"},
				"instancetype": "u1.medium",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags, err := validateKubeVirtDeep(mustJSON(t, tc.body))
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			if len(diags.Items) != len(tc.wantPaths) {
				t.Fatalf("diags = %+v, want paths %v", diags.Items, tc.wantPaths)
			}
			for i, want := range tc.wantPaths {
				if diags.Items[i].Path != want {
					t.Errorf("diag[%d].Path = %q, want %q", i, diags.Items[i].Path, want)
				}
				if diags.Items[i].Severity != "error" {
					t.Errorf("diag[%d].Severity = %q, want error", i, diags.Items[i].Severity)
				}
			}
		})
	}
}

// TestKindLoad_TemplateEcho asserts the host-pre-decoded template is echoed verbatim.
func TestKindLoad_TemplateEcho(t *testing.T) {
	tmpl := []byte(`{"source":{"kind":"container_disk","image":"x"},"memory":"2G"}`)
	env := spec.StructuralKindLoadEnv{Standalone: &spec.StandaloneLoad{Shape: "template", Template: tmpl}}
	reply, err := kindLoad(&pb.InvokeRequest{Op: sdk.OpLoad, EnvJson: mustJSON(t, env)})
	if err != nil {
		t.Fatalf("kindLoad: %v", err)
	}
	if string(reply.ResultJson) != string(tmpl) {
		t.Errorf("echo = %s, want %s", reply.ResultJson, tmpl)
	}
}

// TestKindLoad_DeployEcho asserts the host-pre-decoded deploy node is echoed.
func TestKindLoad_DeployEcho(t *testing.T) {
	dep := &spec.Deploy{From: "myvm", Target: "kubevirt"}
	env := spec.StructuralKindLoadEnv{Standalone: &spec.StandaloneLoad{Shape: "deploy", Deploy: dep}}
	reply, err := kindLoad(&pb.InvokeRequest{Op: sdk.OpLoad, EnvJson: mustJSON(t, env)})
	if err != nil {
		t.Fatalf("kindLoad: %v", err)
	}
	var got spec.Deploy
	if err := json.Unmarshal(reply.ResultJson, &got); err != nil {
		t.Fatal(err)
	}
	if got.From != "myvm" || got.Target != "kubevirt" {
		t.Errorf("deploy echo = %+v", got)
	}
}

// TestKindLoad_MissingEnv asserts a clear error when the host threaded nothing.
func TestKindLoad_MissingEnv(t *testing.T) {
	if _, err := kindLoad(&pb.InvokeRequest{Op: sdk.OpLoad}); err == nil {
		t.Fatal("expected error for missing standalone env")
	}
}

// TestProvider_ClassRouting asserts each class reaches its handler (and unknown ops are
// rejected), without needing a live cluster.
func TestProvider_ClassRouting(t *testing.T) {
	p := provider{}

	// kind OpValidate → Diagnostics reply.
	body := map[string]any{"source": map[string]any{"kind": "container_disk", "image": "x"}}
	reply, err := p.Invoke(context.Background(), &pb.InvokeRequest{
		Class: "kind", Op: sdk.OpValidate, ParamsJson: mustJSON(t, body),
	})
	if err != nil {
		t.Fatalf("kind OpValidate: %v", err)
	}
	var diags spec.Diagnostics
	if err := json.Unmarshal(reply.ResultJson, &diags); err != nil {
		t.Fatal(err)
	}
	if len(diags.Items) != 0 {
		t.Errorf("expected no diagnostics, got %+v", diags.Items)
	}

	// kind OpLoad via provider → delegates to kindLoad.
	env := spec.StructuralKindLoadEnv{Standalone: &spec.StandaloneLoad{Shape: "template", Template: []byte(`{"a":1}`)}}
	if _, err := p.Invoke(context.Background(), &pb.InvokeRequest{Class: "kind", Op: sdk.OpLoad, EnvJson: mustJSON(t, env)}); err != nil {
		t.Fatalf("kind OpLoad: %v", err)
	}

	// kind unsupported op.
	if _, err := p.Invoke(context.Background(), &pb.InvokeRequest{Class: "kind", Op: sdk.OpEmit}); err == nil {
		t.Fatal("expected error for kind OpEmit")
	}

	// command unsupported op.
	if _, err := p.Invoke(context.Background(), &pb.InvokeRequest{Class: "command", Op: sdk.OpEmit}); err == nil {
		t.Fatal("expected error for command OpEmit")
	}

	// deploy unsupported op.
	if _, err := p.Invoke(context.Background(), &pb.InvokeRequest{Class: "deploy", Op: sdk.OpEmit}); err == nil {
		t.Fatal("expected error for deploy OpEmit")
	}

	// unknown class.
	if _, err := p.Invoke(context.Background(), &pb.InvokeRequest{Class: "nope", Op: sdk.OpRun}); err == nil {
		t.Fatal("expected error for unknown class")
	}
}

// TestVerb_BoxModeSkips asserts a `kubevirt:` step skips under charly check box.
func TestVerb_BoxModeSkips(t *testing.T) {
	op := spec.Op{Plugin: "kubevirt", PluginInput: map[string]any{"method": "vm", "name": "x"}}
	reply, err := invokeVerb(context.Background(), &pb.InvokeRequest{
		Class: "verb", Op: sdk.OpRun, ParamsJson: mustJSON(t, op), EnvJson: mustJSON(t, kubevirtEnv{Mode: "box"}),
	})
	if err != nil {
		t.Fatalf("invokeVerb: %v", err)
	}
	if !strings.Contains(string(reply.ResultJson), "skip") {
		t.Errorf("expected skip, got %s", reply.ResultJson)
	}
}

// TestDispatch_UnknownMethod asserts the dispatcher rejects an unknown method before any
// cluster access.
func TestDispatch_UnknownMethod(t *testing.T) {
	conn := &clusterConn{}
	_, err := dispatch(conn, &spec.Op{}, &params.KubeVirtInput{Method: "nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown kubevirt method") {
		t.Fatalf("err = %v, want unknown-method", err)
	}
}

// TestDispatch_RequiresModifier asserts the required-modifier check fires before cluster
// access (a method that needs `name` with no name).
func TestDispatch_RequiresModifier(t *testing.T) {
	conn := &clusterConn{}
	_, err := dispatch(conn, &spec.Op{}, &params.KubeVirtInput{Method: "vm"})
	if err == nil || !strings.Contains(err.Error(), "missing required modifier") {
		t.Fatalf("err = %v, want missing-modifier", err)
	}
}
