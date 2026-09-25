// Command serve is the OUT-OF-PROCESS entrypoint for the kubevirt plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for
// command:kubevirt dispatch when the plugin is not compiled-in (→ CliMain); the serve
// half backs the out-of-process provider placement. The SAME NewProvider()/NewMeta()
// compile INTO charly in-process when listed in compiled_plugins.
package main

import (
	kubevirt "github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt"
	"github.com/opencharly/sdk"
)

func main() {
	sdk.Main(kubevirt.NewProvider(), kubevirt.NewMeta(), kubevirt.CliMain)
}
