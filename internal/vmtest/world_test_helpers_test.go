package vmtest

import "testing"

func failWorldSetup(t *testing.T, scenario *WorldScenario, err error) {
	t.Helper()
	if scenario == nil {
		t.Fatalf("%v", err)
	}
	if writeErr := scenario.WriteSetupFailure(err); writeErr != nil {
		t.Fatalf("write VM world setup failure: %v; original error: %v", writeErr, err)
	}
	t.Fatalf("%v\nworld scenario dir: %s", err, scenario.Dir)
}
