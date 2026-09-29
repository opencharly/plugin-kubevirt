# AGENTS.md — plugin-kubevirt

Standalone plugin repo owning ALL KubeVirt interaction out-of-process: the
`kind:kubevirt` deploy substrate, the `deploy:kubevirt` venue lifecycle, the
declarative `kubevirt:` cluster-probe check verb, and the `charly kubevirt` CLI
family (`command:kubevirt`). The plugin is a Go module at
`candy/plugin-kubevirt/` (module path
`github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt`); the root
`charly.yml` declares `discover: candy` so the repo is a project and its candy is
scanned.

Canonical files:

- `candy/plugin-kubevirt/charly.yml` — the `plugin-kubevirt:` candy entity
  (`plugin:` block, `plan:` checks) **and** the `kubevirt-skill`,
  `check-kubevirt` skill, and `kubevirt-operator` skill entities (the corpus
  sources for the `/charly-kubevirt:*` skills).
- `candy/plugin-kubevirt/plugin.go`, `provider.go`, `kind.go`, `deploy.go`,
  `lifecycle.go`, `methods.go`, `command.go` — the provider, the deploy
  substrate, the venue lifecycle, the 13 verb methods, and the CLI.
- `candy/plugin-kubevirt/cluster.go`, `cluster_ops.go`, `render.go`,
  `migrate.go`, `gpu.go`, `vmshared_seams.go` — kubeconfig/GVR resolution, live
  CR operations, the pure CR renderer, migration, GPU passthrough, and the
  egress seam.
- `candy/plugin-kubevirt/schema/kubevirt.cue` + `params/cue_types_gen.go`.
- `candy/plugin-kubevirt/cmd/serve/main.go` — the out-of-process serve shim.
- `.github/workflows/tag-on-merge.yml`.
- `README.md` — user overview only; never agent guidance.

## Load these skills first (R0)

- `/charly-kubernetes:kubernetes` — the Kubernetes deploy substrate and cluster
  templates the KubeVirt substrate sits beside.
- `/charly-internals:plugin` — the plugin authoring reference: the `plugin:`
  block, the unified Provider model, the per-plugin CUE-schema contract,
  placement. Load before touching the provider or schema.
- `/charly-kubevirt:kubevirt` — this repo's own owning skill (the `kind:
  kubevirt` substrate, the `charly kubevirt` CLI, the VirtualMachine CR
  lifecycle). The repo's candy carries the `skill:` entities that project it, so
  there is no #291 gap here.
- `/charly-kubevirt:check-kubevirt` — the `kubevirt:` check-verb surface.
- `/charly-internals:git-workflow` — before any git/PR action.

## Build / validate / test

- `cd candy/plugin-kubevirt && go build ./...` — compile the plugin module.
- `cd candy/plugin-kubevirt && go vet ./...` and `go test ./...` — vet + the
  pure renderer and provider tests.
- `cd candy/plugin-kubevirt && gofmt -l .` — formatting (empty output passes).
- `charly box validate` at the repo root — the structural check (the candy +
  `plugin:` block, CUE schema, the skill entities).
- The live cluster acceptance (verb dispatch, VM lifecycle, migration) is carried
  by the `check-kubevirt-operator` / `check-kubevirt-vm` R10 beds, not the candy's
  build-context `plan:` checks.
- The merge gate is the **org-wide** `charly/pr-validator` (required check
  `validate / validate`, defined in `opencharly/.github`); this repo has **no**
  per-repo candy gate and ships only `.github/workflows/tag-on-merge.yml`.

## Modify this repo

- Edit the `plugin-kubevirt:` candy entity, the Go source, and
  `schema/kubevirt.cue` **together** — the schema is the single source for the
  generated params.
- Keep the shared seams shared: reuse `sdk/vmshared.RenderCloudInit` and
  `sdk/kit.WalkPlans` (the same generic seams `deploy:vm` uses); only the venue's
  boot path (the KubeVirt CR + `virtctl`) belongs here.
- A change to the KubeVirt surface belongs in BOTH the candy and the `skill:`
  entity body, since the skill entity is the corpus source for
  `/charly-kubevirt:*`.

## Landing

Every change lands through a pull request gated by the org-required
`charly/pr-validator`. The landing mechanics — the `feat/` branch, the PR-only
rule, `CHANGELOG/` history, and the tag-on-merge CalVer — are owned by
`/charly-internals:git-workflow` and the umbrella `AGENTS.md` /
`charly/AGENTS.md`; this signpost points at them and does not restate them.
