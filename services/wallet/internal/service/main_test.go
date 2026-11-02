package service_test

import (
	"fmt"
	"os"
	"testing"

	"tradedrift/services/wallet/internal/testhelper"
)

func TestMain(m *testing.M) {
	unlock, err := testhelper.AcquireTestLock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: wallet service test lock setup failed: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	unlock()
	os.Exit(code)
}
