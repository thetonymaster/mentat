package report

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/thetonymaster/mentat/internal/core"
)

// junitReporter renders a RunReport as JUnit XML from the collector's data (rather
// than delegating to godog), so mentat owns the shape and can carry the feature-003
// suite-level interrupted marker as a <property>. One <testcase> per scenario; a
// failed scenario's Reasons become its <failure> body.
type junitReporter struct{}

type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Properties []junitProperty `xml:"properties>property,omitempty"`
	Cases      []junitCase     `xml:"testcase"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name    string        `xml:"name,attr"`
	Failure *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

func (junitReporter) Report(rep core.RunReport, w io.Writer) error {
	suite := junitSuite{Name: "mentat", Tests: rep.Total, Failures: rep.Failed}
	// The interrupted marker is a suite-level property, present only when set — its
	// absence means the run completed (mirrors the JSON omitempty marker).
	if rep.Interrupted {
		suite.Properties = append(suite.Properties, junitProperty{Name: "interrupted", Value: "true"})
	}
	for _, s := range rep.Scenarios {
		c := junitCase{Name: s.Name}
		if !s.Pass {
			c.Failure = &junitFailure{Message: "scenario failed", Body: strings.Join(s.Reasons, "; ")}
		}
		suite.Cases = append(suite.Cases, c)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return fmt.Errorf("report: writing junit header: %w", err)
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(junitSuites{Suites: []junitSuite{suite}}); err != nil {
		return fmt.Errorf("report: encoding junit: %w", err)
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return fmt.Errorf("report: writing junit trailer: %w", err)
	}
	return nil
}
