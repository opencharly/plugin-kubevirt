package kubevirt

import (
	"testing"
)

// plugin_test.go — the plugin's Describe surface: the capability set must build (this is
// what the host calls at registration), the command model must reflect a valid Kong
// grammar, and the CUE schema must be non-empty + compile.

func TestNewMeta_BuildsCapabilities(t *testing.T) {
	meta := NewMeta()
	if meta == nil {
		t.Fatal("NewMeta returned nil")
	}
	// Describe is where all fallible reflection + schema compilation happens; it must
	// succeed for the plugin to register.
	caps, err := meta.Describe(nil, nil)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(caps.Provided) != 4 {
		t.Fatalf("expected 4 capabilities, got %d", len(caps.Provided))
	}
	want := map[string]bool{"kind:kubevirt": false, "deploy:kubevirt": false, "verb:kubevirt": false, "command:kubevirt": false}
	for _, c := range caps.Provided {
		key := c.Class + ":" + c.Word
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected capability %q", key)
			continue
		}
		want[key] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing capability %q", k)
		}
	}
	// The verb must declare its input def + primary; the kind must be structural +
	// validating with the ssh/image-backed traits.
	for _, c := range caps.Provided {
		switch c.Class {
		case "verb":
			if c.InputDef == "" || c.Primary != "method" {
				t.Errorf("verb capability = %+v, want InputDef + Primary=method", c)
			}
		case "kind":
			if !c.Structural || !c.Validates {
				t.Errorf("kind capability = %+v, want Structural+Validates", c)
			}
			if c.DeployTraits == nil || c.DeployTraits.Venue != "ssh" || !c.DeployTraits.ImageBacked || c.DeployTraits.ExclusiveVenue {
				t.Errorf("kind traits = %+v, want ssh/image_backed/no exclusive", c.DeployTraits)
			}
		case "deploy":
			if !c.Lifecycle || !c.Preresolve {
				t.Errorf("deploy capability = %+v, want Lifecycle+Preresolve", c)
			}
		}
	}
	if caps.SchemaCue == "" {
		t.Error("Describe produced an empty CUE schema")
	}
}

// TestCommandModel_Builds asserts the Kong grammar reflects into a valid CLIModel.
func TestCommandModel_Builds(t *testing.T) {
	model, err := commandModel()
	if err != nil {
		t.Fatalf("commandModel: %v", err)
	}
	if model.Name != "kubevirt" {
		t.Errorf("model name = %q, want kubevirt", model.Name)
	}
	if len(model.Leaves) == 0 {
		t.Error("command model carried no leaves")
	}
}

// TestNewProvider_NonNil asserts the provider constructor.
func TestNewProvider_NonNil(t *testing.T) {
	if NewProvider() == nil {
		t.Fatal("NewProvider returned nil")
	}
}
