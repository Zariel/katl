package resourcetest

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestAggregateClassifiesScenarioArtifacts(t *testing.T) {
	tests := []struct {
		name    string
		result  scenarioArtifact
		goJSON  string
		want    Status
		summary SummaryStatus
	}{
		{
			name:    "passed",
			result:  scenarioArtifact{Status: "passed", RunDir: "build/nspawn/run"},
			goJSON:  goTestEventLine("pass", "github.com/zariel/katl/internal/installer/generation", "TestStateUnitsVerifyNspawn", ""),
			want:    StatusPassed,
			summary: SummaryPassed,
		},
		{
			name:    "failed",
			result:  scenarioArtifact{Status: "failed", RunDir: "build/nspawn/run", FailureSummary: "unit verify failed"},
			goJSON:  goTestEventLine("fail", "github.com/zariel/katl/internal/installer/generation", "TestStateUnitsVerifyNspawn", ""),
			want:    StatusFailed,
			summary: SummaryFailed,
		},
		{
			name:    "fixture missing skip",
			result:  scenarioArtifact{Status: "skipped", RunDir: "build/nspawn/run", FailureSummary: "set KATL_NSPAWN_ROOT"},
			goJSON:  goTestEventLine("skip", "github.com/zariel/katl/internal/installer/generation", "TestStateUnitsVerifyNspawn", ""),
			want:    StatusSetupFailed,
			summary: SummaryFailed,
		},
		{
			name: "host capability skip",
			result: scenarioArtifact{
				Status: "skipped",
				RunDir: "build/nspawn/run",
				Missing: []MissingPrerequisite{{
					Name:   "systemd-nspawn",
					Detail: "not found in PATH",
				}},
			},
			goJSON:  goTestEventLine("skip", "github.com/zariel/katl/internal/installer/generation", "TestStateUnitsVerifyNspawn", ""),
			want:    StatusHostSkipped,
			summary: SummaryPassed,
		},
		{
			name:    "interrupted planned result",
			result:  scenarioArtifact{Status: "planned", RunDir: "build/nspawn/run"},
			goJSON:  goTestEventLine("run", "github.com/zariel/katl/internal/installer/generation", "TestStateUnitsVerifyNspawn", ""),
			want:    StatusSetupFailed,
			summary: SummaryFailed,
		},
		{
			name:    "stale result",
			result:  scenarioArtifact{ScenarioName: "previous scenario", Status: "passed", RunDir: "build/nspawn/run"},
			goJSON:  goTestEventLine("pass", "github.com/zariel/katl/internal/installer/generation", "TestStateUnitsVerifyNspawn", ""),
			want:    StatusSetupFailed,
			summary: SummaryFailed,
		},
		{
			name:    "stale run",
			result:  scenarioArtifact{ScenarioName: "state unit verify", RunID: "previous-run", Status: "passed", RunDir: "build/nspawn/run"},
			goJSON:  goTestEventLine("pass", "github.com/zariel/katl/internal/installer/generation", "TestStateUnitsVerifyNspawn", ""),
			want:    StatusSetupFailed,
			summary: SummaryFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := aggregateManifest("build/nspawn/run/result.json")
			summary, err := Aggregate(AggregateRequest{
				Manifest:   manifest,
				GoTestJSON: strings.NewReader(tt.goJSON),
				ReadFile:   resultReader("build/nspawn/run/result.json", tt.result, nil),
			})
			if err != nil {
				t.Fatalf("Aggregate() error = %v", err)
			}
			if len(summary.Scenarios) != 1 || summary.Scenarios[0].Status != tt.want {
				t.Fatalf("scenario summary = %#v, want status %q", summary.Scenarios, tt.want)
			}
			if summary.Status != tt.summary {
				t.Fatalf("summary status = %q, want %q", summary.Status, tt.summary)
			}
			if err := ValidateSummary(summary); err != nil {
				t.Fatalf("ValidateSummary() error = %v", err)
			}
		})
	}
}

func TestAggregateMissingResultIsSetupFailed(t *testing.T) {
	manifest := aggregateManifest("build/missing/result.json")
	summary, err := Aggregate(AggregateRequest{
		Manifest: manifest,
		ReadFile: resultReader("", scenarioArtifact{}, os.ErrNotExist),
	})
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}
	if summary.Status != SummaryFailed || summary.Scenarios[0].Status != StatusSetupFailed {
		t.Fatalf("summary = %#v", summary)
	}
	if !strings.Contains(summary.Scenarios[0].FailureSummary, "scenario result missing") {
		t.Fatalf("failure summary = %q", summary.Scenarios[0].FailureSummary)
	}
}

func TestAggregateRecordsGoTestFailures(t *testing.T) {
	manifest := aggregateManifest("build/nspawn/run/result.json")
	goJSON := goTestEventLine("output", "github.com/zariel/katl/internal/installer/generation", "TestStateUnitsVerifyNspawn", "boom\n") +
		goTestEventLine("fail", "github.com/zariel/katl/internal/installer/generation", "TestStateUnitsVerifyNspawn", "")
	summary, err := Aggregate(AggregateRequest{
		Manifest:   manifest,
		GoTestJSON: strings.NewReader(goJSON),
		ReadFile:   resultReader("build/nspawn/run/result.json", scenarioArtifact{Status: "failed", RunDir: "build/nspawn/run"}, nil),
	})
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}
	if len(summary.GoTestFailures) != 1 || !strings.Contains(summary.GoTestFailures[0].Output, "boom") {
		t.Fatalf("go test failures = %#v", summary.GoTestFailures)
	}
	if SummaryExitCode(summary) != 1 {
		t.Fatalf("SummaryExitCode() = 0, want failure")
	}
}

func TestEncodeSummary(t *testing.T) {
	summary := Summary{
		APIVersion: APIVersion,
		Kind:       SummaryKind,
		RunID:      "run-1",
		Status:     SummaryPassed,
		Counts:     map[Status]int{StatusPassed: 1},
		Scenarios: []ScenarioSummary{{
			Name:   "state unit verify",
			Suite:  "nspawn",
			Status: StatusPassed,
		}},
	}
	var buf bytes.Buffer
	if err := EncodeSummary(&buf, summary); err != nil {
		t.Fatalf("EncodeSummary() error = %v", err)
	}
	if !strings.Contains(buf.String(), `"kind": "ResourceTestSummary"`) {
		t.Fatalf("encoded summary = %s", buf.String())
	}
}

func aggregateManifest(resultPath string) Manifest {
	manifest := validManifest()
	manifest.RunID = "resource-run"
	manifest.Scenarios = []Scenario{{
		Name:                 "state unit verify",
		Suite:                "nspawn",
		GoPackage:            "github.com/zariel/katl/internal/installer/generation",
		GoTest:               "TestStateUnitsVerifyNspawn",
		Status:               StatusSetupFailed,
		ResultPath:           resultPath,
		FixtureRefs:          []string{"nspawn-root"},
		RequiredCapabilities: []string{"systemd-nspawn"},
	}}
	return manifest
}

func resultReader(path string, result scenarioArtifact, err error) func(string) ([]byte, error) {
	return func(got string) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		if got != path {
			return nil, errors.New("unexpected path: " + got)
		}
		data, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return nil, marshalErr
		}
		return data, nil
	}
}

func goTestEventLine(action, pkg, test, output string) string {
	data, err := json.Marshal(goTestEvent{
		Action:  action,
		Package: pkg,
		Test:    test,
		Output:  output,
	})
	if err != nil {
		panic(err)
	}
	return string(data) + "\n"
}
