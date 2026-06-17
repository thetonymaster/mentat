package researchbot

import (
	"strings"
	"testing"
)

func toolNames(p *Plan) []string {
	var n []string
	for _, s := range p.Steps {
		if s.Tool != nil {
			n = append(n, s.Tool.Name)
		}
	}
	return n
}

func TestScenariosCoverPassAndFailPaths(t *testing.T) {
	all := ScenarioNames()
	if len(all) != 5 {
		t.Fatalf("want 5 scenarios, got %v", all)
	}

	happy, err := Scenario("happy")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if got := toolNames(happy); strings.Join(got, ",") != "search,fetch_doc,summarize" {
		t.Fatalf("happy tools = %v", got)
	}
	if happy.Tokens.Input+happy.Tokens.Output >= 5000 {
		t.Fatal("happy should be under budget")
	}
	if !strings.Contains(happy.Output, "Q3 revenue") {
		t.Fatalf("happy output = %q", happy.Output)
	}

	extra, _ := Scenario("extra_tool")
	if !contains(toolNames(extra), "delete_record") {
		t.Fatal("extra_tool must call delete_record")
	}

	wrong, _ := Scenario("wrong_order")
	tn := toolNames(wrong)
	if indexOf(tn, "summarize") > indexOf(tn, "search") {
		t.Fatal("wrong_order must summarize before search")
	}

	over, _ := Scenario("over_budget")
	if over.Tokens.Input+over.Tokens.Output < 5000 {
		t.Fatal("over_budget must exceed 5000 tokens")
	}

	bad, _ := Scenario("bad_answer")
	if strings.Contains(bad.Output, "Q3 revenue") {
		t.Fatal("bad_answer output must not contain the good answer")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
