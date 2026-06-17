package researchbot

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed scenarios/*.yaml
var scenarioFS embed.FS

func Scenario(name string) (*Plan, error) {
	if name == "" {
		return nil, fmt.Errorf("scenario name required; one of %v", ScenarioNames())
	}
	data, err := scenarioFS.ReadFile("scenarios/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("unknown scenario %q; one of %v", name, ScenarioNames())
	}
	return LoadPlan(data)
}

func ScenarioNames() []string {
	var names []string
	_ = fs.WalkDir(scenarioFS, "scenarios", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".yaml") {
			names = append(names, strings.TrimSuffix(strings.TrimPrefix(p, "scenarios/"), ".yaml"))
		}
		return nil
	})
	sort.Strings(names)
	return names
}
