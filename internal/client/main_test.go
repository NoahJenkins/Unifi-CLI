package client

import (
	"os"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ClearController()
	os.Exit(m.Run())
}
