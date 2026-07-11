package report

import (
	"fmt"
	"html/template"
	"io"

	"github.com/thetonymaster/mentat/internal/core"
)

var htmlTmpl = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Mentat report</title>
<style>body{font-family:system-ui,sans-serif;margin:2rem}table{border-collapse:collapse}
td,th{border:1px solid #ccc;padding:.25rem .5rem}.fail{color:#b00}.pass{color:#080}</style>
</head><body>
<h1>Mentat run</h1>
<p>{{.Total}} scenarios — <span class="pass">{{.Passed}} passed</span>,
<span class="fail">{{.Failed}} failed</span> — total cost ${{printf "%.4f" .TotalCost}}</p>
{{range .Scenarios}}
<h2 class="{{if .Pass}}pass{{else}}fail{{end}}">{{.Name}}</h2>
<p>cost ${{printf "%.4f" .Cost}}{{if .Sequence}} — sequence: {{range .Sequence}}{{.}} {{end}}{{end}}</p>
{{if .DerivationNote}}<p class="derivation-note">derivation note: {{.DerivationNote}}</p>{{end}}
{{if not .Pass}}<ul>{{range .Reasons}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .Aggregate}}<p>{{.Aggregate.Macro}} = {{printf "%.2f" .Aggregate.Computed}}, want {{.Aggregate.Op}} {{printf "%.2f" .Aggregate.Expected}}</p>{{end}}
{{if .Runs}}<table><tr><th>run</th><th>passed</th><th>kind</th><th>latency ms</th><th>cost</th></tr>
{{range .Runs}}<tr><td>{{.RunID}}</td><td>{{.Passed}}</td><td>{{.FailureKind}}</td><td>{{.LatencyMS}}</td><td>{{printf "%.4f" .Cost}}</td></tr>{{end}}
</table>{{end}}
{{end}}
</body></html>`))

type htmlReporter struct{}

func (htmlReporter) Report(rep core.RunReport, w io.Writer) error {
	if err := htmlTmpl.Execute(w, rep); err != nil {
		return fmt.Errorf("report: executing html template: %w", err)
	}
	return nil
}
