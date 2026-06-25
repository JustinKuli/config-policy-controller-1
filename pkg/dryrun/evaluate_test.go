// Copyright Contributors to the Open Cluster Management project

package dryrun

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
	"testing"

	policyv1 "open-cluster-management.io/config-policy-controller/api/v1"
)

func TestEvaluate(t *testing.T) {
	noTestsRun := true

	err := fs.WalkDir(testfiles, ".", func(scenarioPath string, file fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if file.IsDir() && strings.HasPrefix(file.Name(), "test_") {
			testName, _ := strings.CutPrefix(file.Name(), "test_")
			noTestsRun = false

			t.Run(testName, evaluateTest(scenarioPath))
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if noTestsRun {
		t.Fatal("No evaluate tests were run")
	}
}

func evaluateTest(scenarioPath string) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		if PathExists(path.Join(scenarioPath, "desired_status.yaml")) {
			t.Skip("desired status comparison is CLI-only")
		}

		policyYAML, err := testfiles.ReadFile(path.Join(scenarioPath, "policy.yaml"))
		if err != nil {
			t.Fatal(err)
		}

		scenarioFiles, err := testfiles.ReadDir(scenarioPath)
		if err != nil {
			t.Fatal(err)
		}

		var resourcesYAML []byte

		for _, f := range scenarioFiles {
			if !strings.HasPrefix(f.Name(), "input_") {
				continue
			}

			if f.Name() == "input_stdin.yaml" {
				t.Skip("stdin input scenarios are CLI-only")
			}

			inputPath := path.Join(scenarioPath, f.Name())

			info, err := fs.Stat(testfiles, inputPath)
			if err != nil {
				t.Fatal(err)
			}

			if info.IsDir() {
				t.Skip("directory input scenarios are CLI-only")
			}

			input, err := testfiles.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}

			if len(resourcesYAML) > 0 {
				resourcesYAML = append(resourcesYAML, []byte("\n---\n")...)
			}

			resourcesYAML = append(resourcesYAML, input...)
		}

		d := DryRunner{
			noColors:   false,
			printDiffs: true,
			fullDiffs:  true,
		}

		mappingsPath := path.Join(scenarioPath, "mappings.yaml")
		if PathExists(mappingsPath) {
			d.mappingsPath = mappingsPath
		}

		result, err := d.EvaluateFromYAML(context.Background(), EvaluateInput{
			PolicyYAML:    string(policyYAML),
			ResourcesYAML: string(resourcesYAML),
		})
		if err != nil && !errors.Is(err, ErrNonCompliant) {
			t.Fatal(err)
		}

		wanted, err := testfiles.ReadFile(path.Join(scenarioPath, "output.txt"))
		if err != nil {
			t.Fatal(err)
		}

		got := []byte(result.Output(false, true))

		if !bytes.Equal(wanted, got) {
			if testing.Verbose() {
				t.Log("\nWanted:\n" + string(wanted))
				t.Log("\nGot:\n" + string(got))
			}

			t.Fatalf("Evaluate output mismatch for %s", scenarioPath)
		}

		if errors.Is(err, ErrNonCompliant) && result.ComplianceState == policyv1.Compliant {
			t.Fatalf("expected non-compliant result for %s", scenarioPath)
		}
	}
}
