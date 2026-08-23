package disttest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The server and watcher ship as separate images from one Dockerfile, so the
// entry points and the migration binary the deployment invokes are contracts
// between files that nothing else compiles together.

func TestContainerTargets(t *testing.T) {
	targets, order := dockerTargets(t, readFile(t, "Dockerfile"))
	if got, want := order[len(order)-1], "server"; got != want {
		t.Fatalf("default Docker target = %q, want %q", got, want)
	}
	assertStageRuntime(t, targets["server"], []string{
		"COPY --from=build /lode-server /lode-server",
		"COPY --from=build /lode-migrate /lode-migrate",
		"COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg",
		"COPY deploy/base/migrations /migrations",
	}, "ENTRYPOINT [\"/lode-server\"]")
	assertStageRuntime(t, targets["watcher"], []string{
		"COPY --from=build /lode-watch /lode-watch",
	}, "ENTRYPOINT [\"/lode-watch\"]")
}

func TestContainerRuntimeMappings(t *testing.T) {
	var compose struct {
		Services map[string]composeService `yaml:"services"`
	}
	readYAML(t, "docker-compose.yml", &compose)
	assertService(t, compose.Services["worklode"], []string{"/lode-server"}, nil)
	assertService(t, compose.Services["migrate"], []string{"/lode-migrate"}, []string{
		"--dsn", "postgres://postgres:postgres@postgres:5432/postgres?sslmode=disable", "--migrations-path", "/migrations",
	})

	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					InitContainers []container `yaml:"initContainers"`
					Containers     []container `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	readYAML(t, filepath.Join("deploy", "base", "deployment.yaml"), &deployment)
	assertContainer(t, deployment.Spec.Template.Spec.Containers, "worklode", nil)
	assertContainer(t, deployment.Spec.Template.Spec.InitContainers, "migrate", []string{"/lode-migrate", "--migrations-path", "/migrations"})
}

// The release workflow must build both targets; without the watcher step the
// split is invisible outside the Dockerfile.
func TestImageWorkflowBuildsBothTargets(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct {
			Steps []workflowStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	readYAML(t, filepath.Join(".github", "workflows", "_build-image.yml"), &workflow)
	want := map[string]string{
		"server":  "${{ steps.tags.outputs.list }}",
		"watcher": "${{ steps.tags.outputs.watcher-list }}",
	}
	got := map[string]string{}
	for _, step := range workflow.Jobs["build"].Steps {
		if target, ok := step.With["target"].(string); ok {
			got[target], _ = step.With["tags"].(string)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("built targets = %v, want %v", got, want)
	}
}

type composeService struct {
	Entrypoint []string `yaml:"entrypoint"`
	Command    []string `yaml:"command"`
}

type container struct {
	Name    string   `yaml:"name"`
	Command []string `yaml:"command"`
}

type workflowStep struct {
	With map[string]any `yaml:"with"`
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readYAML(t *testing.T, path string, value any) {
	t.Helper()
	if err := yaml.Unmarshal([]byte(readFile(t, path)), value); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// dockerTargets splits a Dockerfile into its named stages, in file order.
func dockerTargets(t *testing.T, dockerfile string) (map[string][]string, []string) {
	t.Helper()
	targets := map[string][]string{}
	var order []string
	var target string
	for _, line := range strings.Split(dockerfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && strings.EqualFold(fields[0], "FROM") && strings.EqualFold(fields[len(fields)-2], "AS") {
			target = fields[len(fields)-1]
			targets[target] = nil
			order = append(order, target)
			continue
		}
		if target != "" {
			targets[target] = append(targets[target], line)
		}
	}
	return targets, order
}

func assertStageRuntime(t *testing.T, lines, wantCopies []string, wantEntrypoint string) {
	t.Helper()
	var copies, entrypoints []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "COPY ") {
			copies = append(copies, line)
		}
		if strings.HasPrefix(line, "ENTRYPOINT ") {
			entrypoints = append(entrypoints, line)
		}
	}
	if !reflect.DeepEqual(copies, wantCopies) || !reflect.DeepEqual(entrypoints, []string{wantEntrypoint}) {
		t.Errorf("stage runtime copies = %q, entrypoints = %q; want copies %q, entrypoint %q", copies, entrypoints, wantCopies, wantEntrypoint)
	}
}

func assertService(t *testing.T, got composeService, entrypoint, command []string) {
	t.Helper()
	if !reflect.DeepEqual(got.Entrypoint, entrypoint) || !reflect.DeepEqual(got.Command, command) {
		t.Errorf("compose service = %+v, want entrypoint %q command %q", got, entrypoint, command)
	}
}

func assertContainer(t *testing.T, containers []container, name string, command []string) {
	t.Helper()
	for _, got := range containers {
		if got.Name == name {
			if !reflect.DeepEqual(got.Command, command) {
				t.Errorf("container %q command = %q, want %q", name, got.Command, command)
			}
			return
		}
	}
	t.Errorf("container %q not found", name)
}
