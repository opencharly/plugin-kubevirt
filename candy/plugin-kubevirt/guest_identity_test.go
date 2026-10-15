package kubevirt

import (
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// guest_identity_test.go pins the external-cluster fix. Before it, resolveGuestIdentity
// returned an UNCONDITIONAL error for every non-containerDisk arm — even when cloud_init was
// set — so the CDI `data_volume` boot path (the only one on an externally-managed cluster that
// needs neither an image registry nor node SSH, because the containerDisk delivery is skipped
// there by design) could not be deployed at all. The pair now comes from the entity, with the
// VM box's OCI labels as the containerDisk fallback.

// stubBoxMetadata swaps the box-metadata seam for the duration of one test (the seam the code
// itself declares "so tests stub it instead of needing a live podman").
func stubBoxMetadata(t *testing.T, meta *spec.VmBoxMetadata, err error) {
	t.Helper()
	prev := resolveVmBoxMetadata
	resolveVmBoxMetadata = func(string) (*spec.VmBoxMetadata, error) { return meta, err }
	t.Cleanup(func() { resolveVmBoxMetadata = prev })
}

func newContainerDiskKV(image string) *spec.KubeVirt {
	kv := &spec.KubeVirt{}
	kv.Source.Kind = "container_disk"
	kv.Source.Image = image
	return kv
}

func TestResolveGuestIdentity_ExplicitPairWinsOverBoxLabels(t *testing.T) {
	stubBoxMetadata(t, &spec.VmBoxMetadata{Distro: "arch", SSHUser: "arch"}, nil)
	kv := newContainerDiskKV("localhost/box:latest")
	kv.Distro, kv.SSHUser = "ubuntu", "ubuntu"

	distro, user, err := resolveGuestIdentity(kv, "")
	if err != nil {
		t.Fatalf("resolveGuestIdentity: %v", err)
	}
	if distro != "ubuntu" || user != "ubuntu" {
		t.Errorf("got (%q,%q), want the explicit pair to win over box metadata", distro, user)
	}
}

func TestResolveGuestIdentity_ContainerDiskFallsBackToBoxLabels(t *testing.T) {
	stubBoxMetadata(t, &spec.VmBoxMetadata{Distro: "arch", SSHUser: "arch"}, nil)
	kv := newContainerDiskKV("localhost/box:latest")

	distro, user, err := resolveGuestIdentity(kv, "")
	if err != nil {
		t.Fatalf("resolveGuestIdentity: %v", err)
	}
	if distro != "arch" || user != "arch" {
		t.Errorf("got (%q,%q), want the box labels", distro, user)
	}
}

func TestResolveGuestIdentity_PartialOverrideIsFilledFromBoxLabels(t *testing.T) {
	stubBoxMetadata(t, &spec.VmBoxMetadata{Distro: "arch", SSHUser: "arch"}, nil)
	kv := newContainerDiskKV("localhost/box:latest")
	kv.SSHUser = "ubuntu" // distro still from the box

	distro, user, err := resolveGuestIdentity(kv, "")
	if err != nil {
		t.Fatalf("resolveGuestIdentity: %v", err)
	}
	if distro != "arch" || user != "ubuntu" {
		t.Errorf("got (%q,%q), want arch + the explicit ubuntu ssh user", distro, user)
	}
}

func TestResolveGuestIdentity_DataVolumeRequiresThePair(t *testing.T) {
	stubBoxMetadata(t, nil, errors.New("image not local"))
	kv := &spec.KubeVirt{}
	kv.Source.Kind = "data_volume"

	_, _, err := resolveGuestIdentity(kv, "")
	if err == nil {
		t.Fatal("expected an error for a data_volume source with no guest identity")
	}
	msg := err.Error()
	if !strings.Contains(msg, "distro") || !strings.Contains(msg, "ssh_user") {
		t.Errorf("the error must name BOTH missing fields, got %q", msg)
	}

	kv.Distro, kv.SSHUser = "ubuntu", "ubuntu"
	distro, user, err := resolveGuestIdentity(kv, "")
	if err != nil {
		t.Fatalf("resolveGuestIdentity with the pair declared: %v", err)
	}
	if distro != "ubuntu" || user != "ubuntu" {
		t.Errorf("got (%q,%q), want the declared pair", distro, user)
	}
}

func TestResolveGuestIdentity_DistroOnlyStillErrors(t *testing.T) {
	stubBoxMetadata(t, nil, errors.New("not local"))
	kv := &spec.KubeVirt{}
	kv.Source.Kind = "pvc"
	kv.Distro = "ubuntu" // ssh_user missing

	_, _, err := resolveGuestIdentity(kv, "")
	if err == nil {
		t.Fatal("expected an error when ssh_user is missing")
	}
	if !strings.Contains(err.Error(), "ssh_user") {
		t.Errorf("the error must name the missing field, got %q", err.Error())
	}
}
