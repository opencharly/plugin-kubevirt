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

// TestPlatformWaitReady_WaivesNameRequirement pins the wait-ready platform arm's
// required-modifier behavior: a platform CR wait (selected by the canonical install
// namespace kubevirt/cdi, no explicit name) does NOT require `name`, while a VMI wait
// still does. Without this the operator bed's `kubevirt: wait-ready` failed with
// "missing required modifier(s): name" even though the platform CR name is derived.
func TestPlatformWaitReady_WaivesNameRequirement(t *testing.T) {
	// Platform arm: namespace kubevirt/cdi → no `name` requirement.
	for _, ns := range []string{"kubevirt", "cdi"} {
		in := &params.KubeVirtInput{Method: "wait-ready", Namespace: ns}
		if !platformWaitReady(in) {
			t.Errorf("platformWaitReady(ns=%q) = false, want true", ns)
		}
	}
	// VMI arm: any other namespace (incl. default) still requires `name`.
	for _, ns := range []string{"", "default", "my-vms"} {
		in := &params.KubeVirtInput{Method: "wait-ready", Namespace: ns}
		if platformWaitReady(in) {
			t.Errorf("platformWaitReady(ns=%q) = true, want false", ns)
		}
	}
}

// KubeVirt CR (kubevirt ns) and the CDI CR (cdi ns) resolve to their platform arm, and
// any other namespace (a VMI's default/namespace) falls through to the VMI arm. This is
// the selector that lets `kubevirt: wait-ready` assert the operator PLATFORM the
// layer-kubevirt-operator plan requires, with no VM to name.
func TestPlatformCRForNamespace(t *testing.T) {
	cases := []struct {
		ns       string
		wantOK   bool
		wantKind string
	}{
		{"kubevirt", true, "KubeVirt"},
		{"cdi", true, "CDI"},
		{"default", false, ""},
		{"my-vms", false, ""},
		{"", false, ""},
	}
	for _, tc := range cases {
		cr, ok := platformCRForNamespace(tc.ns)
		if ok != tc.wantOK {
			t.Errorf("platformCRForNamespace(%q) ok=%v, want %v", tc.ns, ok, tc.wantOK)
			continue
		}
		if ok && cr.kind != tc.wantKind {
			t.Errorf("platformCRForNamespace(%q).kind = %q, want %q", tc.ns, cr.kind, tc.wantKind)
		}
	}
	// The API-path scope is per CR and MUST match the real cluster (RDD-measured:
	// `kubectl api-resources` — kubevirts NAMESPACED=true, cdis NAMESPACED=false). A
	// namespaced Get on the cluster-scoped CDI CR returns "NotFound: the server could not
	// find the requested resource", reading as "phase=not found" forever.
	kv, _ := platformCRForNamespace("kubevirt")
	if kv.clusterScoped {
		t.Error("KubeVirt CR is namespaced (kubevirts NAMESPACED=true); clusterScoped must be false")
	}
	cdi, _ := platformCRForNamespace("cdi")
	if !cdi.clusterScoped {
		t.Error("CDI CR is cluster-scoped (cdis NAMESPACED=false); clusterScoped must be true")
	}
}

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
