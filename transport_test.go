package transport_test

import (
	"testing"

	"github.com/nexssp/transport"
	"github.com/nexssp/transportai/ta2a"
	"github.com/nexssp/transportai/tmcp"
)

func TestTransportsImplementInterface(t *testing.T) {
	var _ transport.Transport = (*tmcp.Transport)(nil)
	var _ transport.Transport = (*ta2a.Transport)(nil)
}
