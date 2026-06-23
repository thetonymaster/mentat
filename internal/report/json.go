package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/thetonymaster/mentat/internal/core"
)

type jsonReporter struct{}

func (jsonReporter) Report(rep core.RunReport, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("report: encoding json: %w", err)
	}
	return nil
}
