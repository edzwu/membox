package agent

import _ "embed"

// ExtensionSource is the membox Pi extension TypeScript source embedded in the
// mm binary. Companion materializes it atomically before launching workers.
//
//go:embed assets/membox.ts
var ExtensionSource []byte
