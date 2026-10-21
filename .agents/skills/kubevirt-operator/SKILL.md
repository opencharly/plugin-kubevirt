---
name: kubevirt-operator
description: KubeVirt + CDI platform install on a Kubernetes cluster — the `candy/kubevirt-operator`
  layer downloads the pinned operator + CR manifests and applies them with the `kube:`
  verb, then waits for the KubeVirt/CDI CRs with the `kubevirt:` verb. Use before
  installing KubeVirt on a k3s/kind/kubernetes venue, authoring the platform leg of
  a kubevirt bed, or authoring the kubevirt-vm cluster entity.
---

# layer-kubevirt-operator — the KubeVirt + CDI platform install

`candy/kubevirt-operator` (standalone repo `opencharly/layer-kubevirt-operator`)
turns a Kubernetes cluster into a virtualization platform. It:

1. `download:`s the pinned `kubevirt-operator.yaml` + `kubevirt-cr.yaml`
   (github.com/kubevirt/kubevirt releases) and `cdi-operator.yaml` + `cdi-cr.yaml`
   (github.com/kubevirt/containerized-data-importer releases);
2. applies them with the `kube:` check verb (`method: apply`);
3. waits for the KubeVirt CR to report `Deployed` via the `kubevirt:` verb
   (`method: wait-ready`).

It runs in a venue that ALREADY has a cluster reachable via kubeconfig — a
`k3s-server` VM, a `kindcluster`, or a `target: kubernetes` host. It uses only
existing charly verbs (`kube:` apply + the `kubevirt:` wait probe) — no new
mechanism, no ad-hoc `kubectl`.

The client candy is `candy/kubevirt` (virtctl at `/usr/bin/virtctl`). The R10
consumers are the `check-kubevirt-operator` and `check-kubevirt-vm` beds.
