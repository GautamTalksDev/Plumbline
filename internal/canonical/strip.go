package canonical

import (
	"bytes"
	"fmt"
)

// ToolJSON strips the version prefix from a Canonicalize() result, returning
// the JCS tool object bytes suitable for classify.Assess / JSON parse.
func ToolJSON(canonical []byte) ([]byte, error) {
	if !bytes.HasPrefix(canonical, []byte(VersionPrefix)) {
		return nil, fmt.Errorf("canonical: missing version prefix")
	}
	return canonical[len(VersionPrefix):], nil
}
