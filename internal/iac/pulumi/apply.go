package pulumi

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

// Apply runs `pulumi up --json --yes` for a single stack. Parses the
// final "summary" event from Pulumi's engine event stream to produce
// counts; captures all stdout+stderr for the PR comment (redacted
// upstream).
func (e *Engine) Apply(ctx context.Context, stack discovery.Stack, opts iac.ApplyOpts) (iac.ApplyResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = stack.Path
	}
	args := []string{"up", "--stack", stack.Name, "--yes", "--non-interactive", "--json"}
	args = append(args, opts.ExtraArgs...)

	timeout := time.Duration(opts.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	// #nosec G204 -- e.Binary is engine.binary.path from operator config; args are built by this
	// adapter and passed as argv with no shell
	cmd := exec.CommandContext(runCtx, e.Binary, args...)
	iac.SetupGracefulStop(cmd, 0)
	cmd.Dir = cwd
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), flattenEnv(opts.Env)...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	dur := time.Since(start).Milliseconds()

	result := iac.ApplyResult{
		DurationMS: dur,
		Output:     stderr.String() + stdout.String(),
	}

	counts, summaryErr := parseApply(stdout.Bytes())
	result.Counts = counts

	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		result.Error = firstLine(msg)
	} else if summaryErr != "" {
		result.Error = summaryErr
	}
	return result, nil
}

// parseApply scans the apply output for the per-op counts. Pulumi's
// --json mode emits a stream of engine events; the final ResOutputsEvent
// carries a changes map. We also fall back to the textual "Resources:"
// summary in case --json is not honored by the user's pulumi version.
func parseApply(out []byte) (summary.Counts, string) {
	var counts summary.Counts

	// Try JSON event stream first - one JSON object per line.
	scanner := byteLineScanner(out)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var evt struct {
			SummaryEvent *struct {
				ResourceChanges map[string]int `json:"resourceChanges"`
			} `json:"summaryEvent"`
			DiagnosticEvent *struct {
				Severity string `json:"severity"`
				Message  string `json:"message"`
			} `json:"diagnosticEvent"`
		}
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if evt.SummaryEvent != nil {
			rc := evt.SummaryEvent.ResourceChanges
			counts.Add += rc["create"] + rc["import"]
			counts.Change += rc["update"]
			counts.Delete += rc["delete"]
			counts.Replace += rc["replace"] + rc["create-replacement"]
		}
	}
	if counts.Total() > 0 {
		return counts, ""
	}

	// Fallback: parse textual "Resources:\n    + N created" lines.
	text := string(out)
	counts.Add += parseLineCount(text, `\+\s+(\d+)\s+created`)
	counts.Change += parseLineCount(text, `~\s+(\d+)\s+updated`)
	counts.Delete += parseLineCount(text, `-\s+(\d+)\s+deleted`)
	counts.Replace += parseLineCount(text, `\+-\s+(\d+)\s+replaced`)
	return counts, ""
}

func parseLineCount(s, re string) int {
	r := regexp.MustCompile(re)
	m := r.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func byteLineScanner(b []byte) *byteScanner { return &byteScanner{data: b} }

type byteScanner struct {
	data []byte
	line []byte
}

func (s *byteScanner) Scan() bool {
	if len(s.data) == 0 {
		return false
	}
	idx := bytes.IndexByte(s.data, '\n')
	if idx < 0 {
		s.line = s.data
		s.data = nil
		return true
	}
	s.line = s.data[:idx]
	s.data = s.data[idx+1:]
	return true
}

func (s *byteScanner) Bytes() []byte { return s.line }

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		return s[:idx]
	}
	return s
}
