package kubevirt

import "github.com/opencharly/spec/spec"

// kubevirtspec.go — this plugin's OWN view of the `kubevirt:` VM template.
//
// opencharly/spec#304 unit 6 moves the authored `#KubeVirt` body (and the Go type generated from
// it) into the plugin that SERVES the kind, so this plugin can no longer decode a template into
// `spec.KubeVirt`. The body arrives as raw JSON (the venue and the resolve envelope both carry
// `Raw`), so the plugin decodes it into the view below.
//
// The field NAMES and JSON TAGS are the authored ones, which is what lets the renderer read this
// view without a line of its logic changing: `kv.Source`, `kv.Cluster`, `kv.CloudInit` … all
// address the same fields they addressed before.
//
// The sub-types that MOVED with the body are this file's own (kubevirtCPU, kubevirtFirmware,
// kubevirtDevices, kubevirtGPU, kubevirtNetwork, kubevirtPort, kubevirtMigration,
// kubevirtSnapshot, kubevirtSource); the ones the contract KEEPS are used as `spec.` — the shared
// `#Step`, `#VmSize` and `#VmCloudInit`.

// kubevirtSpec is the kubevirt VM template this plugin renders, validates and deploys.
type kubevirtSpec struct {
	Cluster                       string             `json:"cluster,omitempty"`
	KubeContext                   string             `json:"kube_context,omitempty"`
	Namespace                     string             `json:"namespace,omitempty"`
	StorageClass                  string             `json:"storage_class,omitempty"`
	Description                   string             `json:"description,omitempty"`
	Plan                          []spec.Step        `json:"plan,omitempty"`
	Source                        kubevirtSource     `json:"source"`
	Distro                        string             `json:"distro,omitempty"`
	SSHUser                       string             `json:"ssh_user,omitempty"`
	Memory                        spec.VmSize        `json:"memory,omitempty"`
	CPU                           *kubevirtCPU       `json:"cpu,omitempty"`
	Machine                       string             `json:"machine,omitempty"`
	Firmware                      *kubevirtFirmware  `json:"firmware,omitempty"`
	Devices                       *kubevirtDevices   `json:"devices,omitempty"`
	GPUs                          []kubevirtGPU      `json:"gpus,omitempty"`
	CloudInit                     *spec.VmCloudInit  `json:"cloud_init,omitempty"`
	Network                       *kubevirtNetwork   `json:"network,omitempty"`
	Instancetype                  string             `json:"instancetype,omitempty"`
	Preference                    string             `json:"preference,omitempty"`
	RunStrategy                   string             `json:"run_strategy,omitempty"`
	EvictionStrategy              string             `json:"eviction_strategy,omitempty"`
	TerminationGracePeriodSeconds int                `json:"termination_grace_period_seconds,omitempty"`
	NodeSelector                  map[string]string  `json:"node_selector,omitempty"`
	Affinity                      map[string]any     `json:"affinity,omitempty"`
	Migration                     *kubevirtMigration `json:"migration,omitempty"`
	Snapshots                     []kubevirtSnapshot `json:"snapshot,omitempty"`
}

// kubevirtSource is the flat, hand-written boot-medium union (the authortype's own shape: a CUE
// union degrades under gengotypes, so the authored contract hand-built it — and this view
// carries the same fields so `Source.Kind` keeps selecting arms).
type kubevirtSource struct {
	Kind            string               `json:"kind"`
	Image           string               `json:"image,omitempty"`
	PullPolicy      string               `json:"pull_policy,omitempty"`
	PullSecret      string               `json:"pull_secret,omitempty"`
	DiskPathInImage string               `json:"disk_path_in_image,omitempty"`
	DataVolume      map[string]any       `json:"data_volume,omitempty"`
	Size            string               `json:"size,omitempty"`
	StorageClass    string               `json:"storage_class,omitempty"`
	ContentType     string               `json:"content_type,omitempty"`
	PVC             string               `json:"pvc,omitempty"`
	Clone           *kubevirtCloneSource `json:"clone,omitempty"`
}

// kubevirtCPU is the cpu sub-block.
type kubevirtCPU struct {
	Cores                 int    `json:"cores,omitempty"`
	Sockets               int    `json:"sockets,omitempty"`
	Threads               int    `json:"threads,omitempty"`
	Model                 string `json:"model,omitempty"`
	DedicatedCPUPlacement bool   `json:"dedicated_cpu_placement,omitempty"`
}

// kubevirtFirmware is the firmware sub-block.
type kubevirtFirmware struct {
	Bootloader    string `json:"bootloader"`
	EFISecureBoot bool   `json:"efi_secure_boot,omitempty"`
}

// kubevirtDevices is the devices sub-block. Disks are the extra (non-boot) volumes,
// carried as authored JSON objects.
type kubevirtDevices struct {
	AutoattachPodInterface   bool             `json:"autoattach_pod_interface,omitempty"`
	AutoattachGraphicsDevice bool             `json:"autoattach_graphics_device,omitempty"`
	AutoattachSerialConsole  bool             `json:"autoattach_serial_console,omitempty"`
	Rng                      bool             `json:"rng,omitempty"`
	Disks                    []map[string]any `json:"disks,omitempty"`

	Host_devices []kubevirtHostDevice `json:"host_devices,omitempty"`
	Inputs       []kubevirtInput      `json:"inputs,omitempty"`
	Watchdog     *kubevirtWatchdog    `json:"watchdog,omitempty"`
}

// kubevirtGPU is one GPU passthrough entry.
type kubevirtGPU struct {
	ResourceName string `json:"resource_name,omitempty"`
	DeviceName   string `json:"device_name,omitempty"`
}

// kubevirtNetwork is the network sub-block.
type kubevirtNetwork struct {
	Interface string         `json:"interface,omitempty"`
	Model     string         `json:"model,omitempty"`
	Ports     []kubevirtPort `json:"ports,omitempty"`
}

// kubevirtPort is one forwarded port.
type kubevirtPort struct {
	Name     string `json:"name,omitempty"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
}

// kubevirtMigration is the live-migration policy.
type kubevirtMigration struct {
	AllowAutoConverge        bool   `json:"allow_auto_converge,omitempty"`
	AllowPostCopy            bool   `json:"allow_post_copy,omitempty"`
	BandwidthPerMigration    string `json:"bandwidth_per_migration,omitempty"`
	CompletionTimeoutSeconds int    `json:"completion_timeout_seconds,omitempty"`
}

// kubevirtSnapshot is a requested snapshot.
type kubevirtSnapshot struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	SnapshotClass string `json:"snapshot_class,omitempty"`
}

// kubevirtInput is one input device (tablet/keyboard).
type kubevirtInput struct {
	Type string `json:"type"`
	Bus  string `json:"bus,omitempty"`
}

// kubevirtHostDevice is one host device passthrough.
type kubevirtHostDevice struct {
	Device_name string `json:"device_name"`
	Name        string `json:"name,omitempty"`
}

// kubevirtWatchdog is the watchdog device.
type kubevirtWatchdog struct {
	Model  string `json:"model"`
	Action string `json:"action,omitempty"`
}

// kubevirtCloneSource is the clone arm's source reference (the contract's hand-written
// KubevirtCloneSource, carried here with the body).
type kubevirtCloneSource struct {
	From string `json:"from"`
}
