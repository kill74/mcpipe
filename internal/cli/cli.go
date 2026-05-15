package cli

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"mcpipe/internal/config"
	"mcpipe/internal/llm"
	"mcpipe/internal/mcp"
	"mcpipe/internal/runtime"
	"mcpipe/internal/sample"
	"mcpipe/internal/security"
)

type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
}

type PipelineOptions struct {
	File                string
	InputFile           string
	Inputs              map[string]string
	Mock                bool
	JSON                bool
	OutputDir           string
	AuditDir            string
	NoAudit             bool
	Lockfile            string
	Locked              bool
	RequireConfirmation bool
	Yes                 bool
	MaxPromptChars      int
	MaxResponseChars    int
	MaxToolResultBytes  int
	MaxConcurrentSteps  int
	MaxRunDuration      time.Duration
	PolicyPreset        string
}

type GraphOptions struct {
	File   string
	Format string
}

type NewOptions struct {
	Template string
	Path     string
	List     bool
}

type DiffOptions struct {
	OldFile string
	NewFile string
}

type BundleOptions struct {
	File      string
	Out       string
	InputFile string
	Inputs    map[string]string
}

type EcosystemOptions struct {
	File string
	Mock bool
}

type InspectOptions struct {
	File string
}

type SchemaDocsOptions struct {
	Schema string
	Out    string
}

type LockOptions struct {
	File   string
	Out    string
	Verify bool
}

type KeygenOptions struct {
	Private string
	Public  string
}

type SignOptions struct {
	File string
	Key  string
	Out  string
	Sig  string
}

type InputFlags struct {
	values map[string]string
}

func (f *InputFlags) String() string {
	if f == nil || len(f.values) == 0 {
		return ""
	}
	var parts []string
	for key, value := range f.values {
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func (f *InputFlags) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("input must use key=value form, got %q", value)
	}
	if f.values == nil {
		f.values = map[string]string{}
	}
	f.values[strings.TrimSpace(key)] = val
	return nil
}

func (f *InputFlags) Values() map[string]string {
	out := map[string]string{}
	if f == nil {
		return out
	}
	for key, value := range f.values {
		out[key] = value
	}
	return out
}

func (a App) Init(path string) error {
	return a.writeTemplate(sample.ResearchDigestPipeline, path)
}

func (a App) New(opts NewOptions) error {
	if opts.List {
		for _, name := range sample.TemplateNames() {
			tpl, _ := sample.TemplateByName(name)
			fmt.Fprintf(a.stdout(), "%s: %s\n", tpl.Name, tpl.Description)
		}
		return nil
	}
	if strings.TrimSpace(opts.Template) == "" {
		opts.Template = "research"
	}
	if strings.TrimSpace(opts.Path) == "" {
		opts.Path = "pipeline.json"
	}
	tpl, ok := sample.TemplateByName(opts.Template)
	if !ok {
		return fmt.Errorf("unknown template %q; run mcpipe new --list", opts.Template)
	}
	return a.writeTemplate(tpl.Body, opts.Path)
}

func (a App) writeTemplate(body, path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout(), "created %s\n", path)
	return nil
}

func (a App) Validate(opts PipelineOptions) error {
	p, err := loadAndValidate(opts.File)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout(), "valid: %s (%d step%s)\n", opts.File, len(p.Steps), plural(len(p.Steps)))
	for _, warning := range p.RuntimeWarnings() {
		fmt.Fprintf(a.stdout(), "warning: %s\n", warning)
	}
	return nil
}

func (a App) Vet(opts PipelineOptions) error {
	p, err := loadAndValidate(opts.File)
	if err != nil {
		return err
	}
	policy := securityPolicyFromPipeline(p, opts, nil)
	findings := security.LintPipeline(p, policy)
	if len(findings) == 0 {
		fmt.Fprintln(a.stdout(), "vet: no findings")
		return nil
	}
	for _, finding := range findings {
		fmt.Fprintf(a.stdout(), "[%s] %s %s: %s\n", strings.ToUpper(string(finding.Severity)), finding.Code, finding.Subject, finding.Message)
	}
	if security.HasBlockingFindings(findings) {
		return errors.New("vet found blocking security findings")
	}
	return nil
}

func (a App) DryRun(ctx context.Context, opts PipelineOptions) error {
	p, err := loadAndValidate(opts.File)
	if err != nil {
		return err
	}
	rawInputs, err := loadRawInputs(opts)
	if err != nil {
		return err
	}
	inputs, err := p.ResolveInputs(rawInputs)
	if err != nil {
		return err
	}
	out, err := runtime.DryRun(p, inputs, a.now())
	if err != nil {
		return err
	}
	policy := securityPolicyFromPipeline(p, opts, inputs)
	out = policy.Redactor.RedactString(out)
	_, err = io.WriteString(a.stdout(), out)
	return err
}

func (a App) Explain(opts PipelineOptions) error {
	p, err := loadAndValidate(opts.File)
	if err != nil {
		return err
	}
	out, err := explainPipeline(p)
	if err != nil {
		return err
	}
	_, err = io.WriteString(a.stdout(), out)
	return err
}

func (a App) Doctor(ctx context.Context, opts PipelineOptions) error {
	p, err := config.LoadFile(opts.File)
	if err != nil {
		return err
	}
	report := doctorReport{}
	if err := p.Validate(); err != nil {
		report.add("pipeline validation", "fail", err.Error())
	} else {
		report.add("pipeline validation", "ok", fmt.Sprintf("%d steps are valid", len(p.Steps)))
	}
	if rawInputs, err := loadRawInputs(opts); err != nil {
		report.add("inputs", "fail", err.Error())
	} else if _, err := p.ResolveInputs(rawInputs); err != nil {
		report.add("inputs", "fail", err.Error())
	} else {
		report.add("inputs", "ok", "required inputs are satisfied")
	}
	for _, warning := range p.RuntimeWarnings() {
		report.add("v1 boundary", "warn", warning)
	}
	reportProviderChecks(ctx, &report, p, opts.Mock)
	reportMCPChecks(&report, p, opts.Mock)
	reportEnvChecks(&report, p)
	redactor := securityPolicyFromPipeline(p, opts, nil).Redactor
	if _, err := io.WriteString(a.stdout(), redactor.RedactString(report.String())); err != nil {
		return err
	}
	if report.failed() {
		return errors.New("doctor found failing checks")
	}
	return nil
}

func (a App) Graph(opts GraphOptions) error {
	p, err := loadAndValidate(opts.File)
	if err != nil {
		return err
	}
	out, err := runtime.Graph(p, opts.Format)
	if err != nil {
		return err
	}
	_, err = io.WriteString(a.stdout(), out)
	return err
}

func (a App) Tools(ctx context.Context, opts PipelineOptions) error {
	p, err := loadAndValidate(opts.File)
	if err != nil {
		return err
	}
	var manager mcp.Manager
	if opts.Mock {
		manager = mcp.NewMockManager(p.MCPServers)
	} else {
		manager = mcp.NewStdioManagerWithOptions(p.MCPServers, mcp.SecureOptions())
	}
	defer manager.Close()

	var b strings.Builder
	b.WriteString("Tools\n")
	for _, step := range p.Steps {
		tools, err := manager.AllowedTools(ctx, step.Tools)
		if err != nil {
			return fmt.Errorf("list tools for step %q: %w", step.ID, err)
		}
		b.WriteString("  ")
		b.WriteString(step.ID)
		b.WriteString(":\n")
		b.WriteString("    allow: ")
		b.WriteString(ruleList(step.Tools.Allow))
		b.WriteByte('\n')
		b.WriteString("    deny: ")
		b.WriteString(ruleList(step.Tools.Deny))
		b.WriteByte('\n')
		if len(tools) == 0 {
			b.WriteString("    resolved: none\n")
			continue
		}
		b.WriteString("    resolved:\n")
		for _, tool := range tools {
			b.WriteString("      - ")
			b.WriteString(tool.Name)
			if tool.Description != "" {
				b.WriteString(": ")
				b.WriteString(tool.Description)
			}
			b.WriteByte('\n')
		}
	}
	redactor := securityPolicyFromPipeline(p, opts, nil).Redactor
	_, err = io.WriteString(a.stdout(), redactor.RedactString(b.String()))
	return err
}

func (a App) Diff(opts DiffOptions) error {
	if opts.OldFile == "" || opts.NewFile == "" {
		return errors.New("diff requires two pipeline files")
	}
	oldPipeline, err := loadAndValidate(opts.OldFile)
	if err != nil {
		return fmt.Errorf("old pipeline: %w", err)
	}
	newPipeline, err := loadAndValidate(opts.NewFile)
	if err != nil {
		return fmt.Errorf("new pipeline: %w", err)
	}
	var b strings.Builder
	b.WriteString("Pipeline Diff\n")
	changes := comparePipelines(oldPipeline, newPipeline)
	if len(changes) == 0 {
		b.WriteString("  no semantic changes\n")
	} else {
		for _, change := range changes {
			b.WriteString("  - ")
			b.WriteString(change)
			b.WriteByte('\n')
		}
	}
	_, err = io.WriteString(a.stdout(), b.String())
	return err
}

func (a App) Bundle(opts BundleOptions) error {
	p, err := loadAndValidate(opts.File)
	if err != nil {
		return err
	}
	rawInputs, err := loadRawInputs(PipelineOptions{InputFile: opts.InputFile, Inputs: opts.Inputs})
	if err != nil {
		return err
	}
	inputs, err := p.ResolveInputs(rawInputs)
	if err != nil {
		return err
	}
	out := opts.Out
	if out == "" {
		out = strings.TrimSuffix(filepath.Base(opts.File), filepath.Ext(opts.File)) + ".mcpipebundle"
	}
	bundle, err := buildBundle(p, opts.File, out, inputs, a.now())
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout(), "bundle written: %s\n", bundle)
	return nil
}

func (a App) Providers(ctx context.Context, action string, opts EcosystemOptions) error {
	switch action {
	case "", "list":
		fmt.Fprintln(a.stdout(), "Providers")
		fmt.Fprintln(a.stdout(), "  - mock: deterministic local test provider")
		fmt.Fprintln(a.stdout(), "  - ollama: local HTTP provider, default http://localhost:11434")
		fmt.Fprintln(a.stdout(), "  - anthropic: hosted Messages API, requires ANTHROPIC_API_KEY")
		return nil
	case "doctor":
		p, err := config.LoadFile(opts.File)
		if err != nil {
			return err
		}
		report := doctorReport{}
		reportProviderChecks(ctx, &report, p, opts.Mock)
		_, err = io.WriteString(a.stdout(), report.String())
		return err
	default:
		return fmt.Errorf("unknown providers action %q", action)
	}
}

func (a App) MCP(action string, opts EcosystemOptions) error {
	p, err := config.LoadFile(opts.File)
	if err != nil {
		return err
	}
	switch action {
	case "", "list":
		var b strings.Builder
		b.WriteString("MCP Servers\n")
		if len(p.MCPServers) == 0 {
			b.WriteString("  none\n")
		}
		for _, name := range sortedMapKeys(p.MCPServers) {
			server := p.MCPServers[name]
			b.WriteString("  - ")
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(server.Transport)
			if server.Command != "" {
				b.WriteString(" ")
				b.WriteString(server.Command)
			}
			if server.URL != "" {
				b.WriteString(" ")
				b.WriteString(server.URL)
			}
			b.WriteByte('\n')
		}
		_, err := io.WriteString(a.stdout(), b.String())
		return err
	case "doctor":
		report := doctorReport{}
		reportMCPChecks(&report, p, opts.Mock)
		redactor := securityPolicyFromPipeline(p, PipelineOptions{}, nil).Redactor
		_, err := io.WriteString(a.stdout(), redactor.RedactString(report.String()))
		return err
	default:
		return fmt.Errorf("unknown mcp action %q", action)
	}
}

func (a App) Plugins(action string, opts EcosystemOptions) error {
	p, err := config.LoadFile(opts.File)
	if err != nil {
		return err
	}
	switch action {
	case "", "list":
		var b strings.Builder
		b.WriteString("Plugins\n")
		if len(p.Plugins) == 0 {
			b.WriteString("  none\n")
		}
		for _, name := range sortedMapKeys(p.Plugins) {
			plugin := p.Plugins[name]
			b.WriteString("  - ")
			b.WriteString(name)
			if plugin.Description != "" {
				b.WriteString(": ")
				b.WriteString(plugin.Description)
			}
			if len(plugin.Tools.Allow) > 0 {
				b.WriteString(" tools=")
				b.WriteString(ruleList(plugin.Tools.Allow))
			}
			b.WriteByte('\n')
		}
		_, err := io.WriteString(a.stdout(), b.String())
		return err
	default:
		return fmt.Errorf("unknown plugins action %q", action)
	}
}

func (a App) Agents(action string, opts EcosystemOptions) error {
	p, err := config.LoadFile(opts.File)
	if err != nil {
		return err
	}
	switch action {
	case "", "list":
		var b strings.Builder
		b.WriteString("Agents\n")
		if len(p.Agents) == 0 {
			b.WriteString("  none\n")
		}
		for _, name := range sortedMapKeys(p.Agents) {
			agent := p.Agents[name]
			b.WriteString("  - ")
			b.WriteString(name)
			if agent.Description != "" {
				b.WriteString(": ")
				b.WriteString(agent.Description)
			}
			if agent.LLM != nil && (agent.LLM.Backend != "" || agent.LLM.Model != "") {
				b.WriteString(" llm=")
				b.WriteString(agent.LLM.Backend)
				b.WriteString("/")
				b.WriteString(agent.LLM.Model)
			}
			b.WriteByte('\n')
		}
		_, err := io.WriteString(a.stdout(), b.String())
		return err
	default:
		return fmt.Errorf("unknown agents action %q", action)
	}
}

func (a App) InspectRun(opts InspectOptions) error {
	if strings.TrimSpace(opts.File) == "" {
		return errors.New("inspect run requires --file")
	}
	file, err := os.Open(opts.File)
	if err != nil {
		return err
	}
	defer file.Close()

	summary := auditSummary{Steps: map[string]*auditStep{}}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return fmt.Errorf("decode audit line %d: %w", lineNo, err)
		}
		summary.Observe(event)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err = io.WriteString(a.stdout(), summary.String())
	return err
}

func (a App) Replay(ctx context.Context, opts PipelineOptions) error {
	opts.Mock = true
	opts.NoAudit = true
	return a.Run(ctx, opts)
}

func (a App) SchemaDocs(opts SchemaDocsOptions) error {
	if strings.TrimSpace(opts.Schema) == "" {
		opts.Schema = "schemas/pipeline/v1.json"
	}
	data, err := os.ReadFile(opts.Schema)
	if err != nil {
		return err
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	out := renderSchemaDocs(schema, opts.Schema)
	if strings.TrimSpace(opts.Out) != "" {
		if err := os.WriteFile(opts.Out, []byte(out), 0644); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout(), "schema docs written: %s\n", opts.Out)
		return nil
	}
	_, err = io.WriteString(a.stdout(), out)
	return err
}

func (a App) Run(ctx context.Context, opts PipelineOptions) error {
	p, err := loadAndValidate(opts.File)
	if err != nil {
		return err
	}
	if p.Output.Destination != "" && p.Output.Destination != "stdout" {
		return fmt.Errorf("output destination %q is parsed but not executable in v1", p.Output.Destination)
	}
	if opts.Locked {
		lockPath := opts.Lockfile
		if lockPath == "" {
			lockPath = security.DefaultLockPath(opts.File)
		}
		if err := security.VerifyLock(lockPath, opts.File); err != nil {
			return fmt.Errorf("locked run refused: %w", err)
		}
	}
	rawInputs, err := loadRawInputs(opts)
	if err != nil {
		return err
	}
	inputs, err := p.ResolveInputs(rawInputs)
	if err != nil {
		return err
	}
	policy := securityPolicyFromPipeline(p, opts, inputs)
	if opts.RequireConfirmation {
		plan, err := confirmationPlan(p, opts, policy)
		if err != nil {
			return err
		}
		fmt.Fprint(a.stdout(), plan)
		if !opts.Yes {
			return errors.New("confirmation required; rerun with --yes to execute this plan")
		}
	}
	var manager mcp.Manager
	if opts.Mock {
		manager = mcp.NewMockManager(p.MCPServers)
	} else {
		manager = mcp.NewStdioManagerWithOptions(p.MCPServers, mcp.SecureOptions())
	}
	defer manager.Close()

	engine := &runtime.Engine{
		Pipeline: p,
		Inputs:   inputs,
		LLM:      llm.NewRouter(opts.Mock),
		MCP:      manager,
		Now:      a.Now,
		Security: policy,
	}
	if !opts.JSON {
		engine.ProgressWriter = a.stdout()
	}
	runCtx := ctx
	cancel := func() {}
	if opts.MaxRunDuration > 0 {
		runCtx, cancel = context.WithTimeout(ctx, opts.MaxRunDuration)
	}
	defer cancel()
	result, err := engine.Run(runCtx)
	if err != nil {
		if result != nil && p.ErrorHandling.OnPipelineFailure != nil && p.ErrorHandling.OnPipelineFailure.Notify != nil {
			fmt.Fprintf(a.stderr(), "pipeline failed: %v\n", policy.Redactor.Error(err))
		}
		return policy.Redactor.Error(err)
	}
	var output string
	if opts.JSON {
		output, err = runtime.FormatJSONOutput(result)
	} else {
		output = runtime.FormatOutput(p, result)
	}
	if err != nil {
		return err
	}
	output = policy.Redactor.RedactString(output)
	_, err = io.WriteString(a.stdout(), output)
	return err
}

func (a App) Lock(opts LockOptions) error {
	p, err := loadAndValidate(opts.File)
	if err != nil {
		return err
	}
	path := opts.Out
	if path == "" {
		path = security.DefaultLockPath(opts.File)
	}
	if opts.Verify {
		if err := security.VerifyLock(path, opts.File); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout(), "lock verified: %s\n", path)
		return nil
	}
	lock, err := security.BuildLock(opts.File, p, a.now())
	if err != nil {
		return err
	}
	if err := security.WriteLock(path, lock); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout(), "lock written: %s\n", path)
	return nil
}

func (a App) Keygen(opts KeygenOptions) error {
	if opts.Private == "" || opts.Public == "" {
		return errors.New("keygen requires --private and --public")
	}
	if err := security.GenerateKeypair(opts.Private, opts.Public); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout(), "keys written: %s %s\n", opts.Private, opts.Public)
	return nil
}

func (a App) Sign(opts SignOptions) error {
	if opts.File == "" || opts.Key == "" {
		return errors.New("sign requires -f and --key")
	}
	out := opts.Out
	if out == "" {
		out = opts.File + ".sig"
	}
	if err := security.SignFile(opts.File, opts.Key, out); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout(), "signature written: %s\n", out)
	return nil
}

func (a App) Verify(opts SignOptions) error {
	if opts.File == "" || opts.Key == "" || opts.Sig == "" {
		return errors.New("verify requires -f, --key, and --sig")
	}
	if err := security.VerifySignature(opts.File, opts.Key, opts.Sig); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout(), "signature verified: %s\n", opts.File)
	return nil
}

func loadAndValidate(path string) (*config.Pipeline, error) {
	p, err := config.LoadFile(path)
	if err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func loadRawInputs(opts PipelineOptions) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(opts.InputFile) != "" {
		data, err := os.ReadFile(opts.InputFile)
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode input file: %w", err)
		}
		for key, value := range raw {
			switch typed := value.(type) {
			case string:
				out[key] = typed
			case float64, bool:
				out[key] = fmt.Sprint(typed)
			case nil:
				return nil, fmt.Errorf("input file value %q cannot be null", key)
			default:
				return nil, fmt.Errorf("input file value %q must be a scalar", key)
			}
		}
	}
	for key, value := range opts.Inputs {
		out[key] = value
	}
	return out, nil
}

func (a App) stdout() io.Writer {
	if a.Stdout != nil {
		return a.Stdout
	}
	return os.Stdout
}

func (a App) stderr() io.Writer {
	if a.Stderr != nil {
		return a.Stderr
	}
	return os.Stderr
}

func (a App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func explainPipeline(p *config.Pipeline) (string, error) {
	levels, err := runtime.Levels(p.Steps)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	name := "unnamed"
	id := ""
	if p.Metadata != nil {
		if p.Metadata.Name != "" {
			name = p.Metadata.Name
		}
		id = p.Metadata.ID
	}
	b.WriteString("Pipeline\n")
	b.WriteString("  name: ")
	b.WriteString(name)
	b.WriteByte('\n')
	if id != "" {
		b.WriteString("  id: ")
		b.WriteString(id)
		b.WriteByte('\n')
	}
	b.WriteString("  version: ")
	b.WriteString(p.Version)
	b.WriteString("\n\n")

	b.WriteString("Inputs\n")
	inputNames := sortedMapKeys(p.Inputs)
	if len(inputNames) == 0 {
		b.WriteString("  none\n")
	}
	for _, name := range inputNames {
		spec := p.Inputs[name]
		required := "optional"
		if spec.Required {
			required = "required"
		}
		b.WriteString(fmt.Sprintf("  %s: %s, %s", name, spec.Type, required))
		if spec.Default != nil {
			b.WriteString(fmt.Sprintf(", default=%v", spec.Default))
		}
		if len(spec.Values) > 0 {
			b.WriteString(", values=")
			b.WriteString(strings.Join(spec.Values, "|"))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	b.WriteString("Execution\n")
	for i, level := range levels {
		ids := make([]string, 0, len(level))
		for _, step := range level {
			ids = append(ids, step.ID)
		}
		b.WriteString(fmt.Sprintf("  level %d: %s\n", i+1, strings.Join(ids, ", ")))
		for _, step := range level {
			effective := p.EffectiveStep(step)
			b.WriteString(fmt.Sprintf("    - %s: %s/%s", step.ID, effective.LLM.Backend, effective.LLM.Model))
			if len(step.Tools.Allow) > 0 {
				b.WriteString(", tools=")
				b.WriteString(strings.Join(step.Tools.Allow, ","))
			}
			if len(step.Outputs) > 0 {
				b.WriteString(", outputs=")
				b.WriteString(strings.Join(sortedStringMapKeys(step.Outputs), ","))
			}
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')

	b.WriteString("MCP Servers\n")
	serverNames := sortedMapKeys(p.MCPServers)
	if len(serverNames) == 0 {
		b.WriteString("  none\n")
	}
	for _, name := range serverNames {
		server := p.MCPServers[name]
		b.WriteString(fmt.Sprintf("  %s: %s", name, server.Transport))
		if server.Command != "" {
			b.WriteString(" command=")
			b.WriteString(server.Command)
		}
		if server.URL != "" {
			b.WriteString(" url=")
			b.WriteString(server.URL)
		}
		b.WriteByte('\n')
	}
	warnings := p.RuntimeWarnings()
	if len(warnings) > 0 {
		b.WriteString("\nWarnings\n")
		for _, warning := range warnings {
			b.WriteString("  - ")
			b.WriteString(warning)
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

type doctorReport struct {
	checks []doctorCheck
}

type doctorCheck struct {
	Name   string
	Status string
	Detail string
}

func (r *doctorReport) add(name, status, detail string) {
	r.checks = append(r.checks, doctorCheck{Name: name, Status: status, Detail: detail})
}

func (r doctorReport) failed() bool {
	for _, check := range r.checks {
		if check.Status == "fail" {
			return true
		}
	}
	return false
}

func (r doctorReport) String() string {
	var b strings.Builder
	b.WriteString("mcpipe doctor\n\n")
	for _, check := range r.checks {
		b.WriteString("[")
		b.WriteString(strings.ToUpper(check.Status))
		b.WriteString("] ")
		b.WriteString(check.Name)
		if check.Detail != "" {
			b.WriteString(": ")
			b.WriteString(check.Detail)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func reportProviderChecks(ctx context.Context, report *doctorReport, p *config.Pipeline, mock bool) {
	backends := map[string]bool{}
	for _, step := range p.Steps {
		effective := p.EffectiveStep(step)
		if effective.LLM.Backend != "" {
			backends[strings.ToLower(effective.LLM.Backend)] = true
		}
	}
	if mock {
		report.add("llm providers", "ok", "mock mode skips live provider checks")
		return
	}
	if backends["anthropic"] {
		if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" {
			report.add("anthropic", "fail", "ANTHROPIC_API_KEY is not set")
		} else {
			report.add("anthropic", "ok", "ANTHROPIC_API_KEY is set")
		}
	}
	if backends["ollama"] {
		url := strings.TrimRight(os.Getenv("OLLAMA_HOST"), "/")
		if url == "" {
			url = "http://localhost:11434"
		} else if !strings.Contains(url, "://") {
			url = "http://" + url
		}
		checkCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		defer cancel()
		req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, url+"/api/tags", nil)
		if err != nil {
			report.add("ollama", "warn", err.Error())
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			report.add("ollama", "warn", "could not reach "+url+"; runs may still work if Ollama starts later")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			report.add("ollama", "ok", "reachable at "+url)
		} else {
			report.add("ollama", "warn", fmt.Sprintf("%s returned HTTP %d", url, resp.StatusCode))
		}
	}
}

func reportMCPChecks(report *doctorReport, p *config.Pipeline, mock bool) {
	if len(p.MCPServers) == 0 {
		report.add("mcp servers", "ok", "no MCP servers configured")
		return
	}
	if mock {
		report.add("mcp servers", "ok", "mock mode skips MCP process and secret checks")
		return
	}
	for _, name := range sortedMapKeys(p.MCPServers) {
		server := p.MCPServers[name]
		switch server.Transport {
		case "stdio":
			command := expandEnvRefs(server.Command)
			if command == "" {
				report.add("mcp "+name, "fail", "stdio command is empty after env expansion")
				continue
			}
			if _, err := exec.LookPath(command); err != nil {
				report.add("mcp "+name, "fail", fmt.Sprintf("command %q not found on PATH", command))
			} else {
				report.add("mcp "+name, "ok", fmt.Sprintf("stdio command %q is available", command))
			}
			for key, value := range server.Env {
				if strings.Contains(value, "${env:") && expandEnvRefs(value) == "" {
					report.add("mcp "+name+" env", "fail", fmt.Sprintf("%s references an unset environment variable", key))
				}
			}
		case "sse":
			report.add("mcp "+name, "warn", "sse transport is parsed but not executable in v1")
		default:
			report.add("mcp "+name, "fail", "unsupported transport "+server.Transport)
		}
	}
}

var tplEnvRefPattern = regexp.MustCompile(`\{\{\s*env\.([A-Za-z_][A-Za-z0-9_]*)\s*[\}|]`)

func reportEnvChecks(report *doctorReport, p *config.Pipeline) {
	refs := map[string]bool{}
	for _, step := range p.Steps {
		for _, match := range tplEnvRefPattern.FindAllStringSubmatch(step.Prompt.System, -1) {
			refs[match[1]] = true
		}
		for _, match := range tplEnvRefPattern.FindAllStringSubmatch(step.Prompt.User, -1) {
			refs[match[1]] = true
		}
		for _, expr := range step.Outputs {
			for _, match := range tplEnvRefPattern.FindAllStringSubmatch(expr, -1) {
				refs[match[1]] = true
			}
		}
	}
	if len(refs) == 0 {
		return
	}
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	sort.Strings(names)
	var missing []string
	for _, name := range names {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		report.add("env vars", "ok", fmt.Sprintf("%d referenced, all set", len(names)))
	} else {
		report.add("env vars", "warn", fmt.Sprintf("unset: %s", strings.Join(missing, ", ")))
	}
}

var envRefPattern = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnvRefs(input string) string {
	return envRefPattern.ReplaceAllStringFunc(input, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "${env:"), "}")
		return os.Getenv(name)
	})
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func securityPolicyFromPipeline(p *config.Pipeline, opts PipelineOptions, inputs map[string]any) *security.Policy {
	policy := security.DefaultPolicy()
	applyPolicyPreset(&policy, opts.PolicyPreset)
	if opts.OutputDir != "" {
		policy.OutputDir = opts.OutputDir
	}
	if opts.AuditDir != "" {
		policy.AuditDir = opts.AuditDir
	}
	policy.NoAudit = opts.NoAudit
	if opts.MaxPromptChars > 0 {
		policy.MaxPromptChars = opts.MaxPromptChars
	}
	if opts.MaxResponseChars > 0 {
		policy.MaxResponseChars = opts.MaxResponseChars
	}
	if opts.MaxToolResultBytes > 0 {
		policy.MaxToolResultBytes = opts.MaxToolResultBytes
	}
	if opts.MaxConcurrentSteps > 0 {
		policy.MaxConcurrentSteps = opts.MaxConcurrentSteps
	}
	for key, value := range p.Policy {
		policy.ToolPolicies[key] = security.ToolPolicy{
			AllowedPaths: value.AllowedPaths,
			MaxBytes:     value.MaxBytes,
			MaxCalls:     value.MaxCalls,
		}
	}
	if opts.OutputDir != "" {
		item := policy.ToolPolicies["filesystem.write_file"]
		item.AllowedPaths = []string{opts.OutputDir}
		policy.ToolPolicies["filesystem.write_file"] = item
	}
	redactor := security.NewRedactor()
	for _, server := range p.MCPServers {
		for key, value := range server.Env {
			redactor.AddNamedValue(key, expandEnvRefs(value))
		}
		for key, value := range server.Headers {
			redactor.AddNamedValue(key, expandEnvRefs(value))
		}
	}
	for key, value := range inputs {
		redactor.AddNamedValue(key, fmt.Sprint(value))
	}
	for _, kv := range os.Environ() {
		if key, val, ok := strings.Cut(kv, "="); ok && security.IsSensitiveName(key) {
			redactor.AddValue(val)
		}
	}
	policy.Redactor = redactor
	policy.Normalize()
	return &policy
}

func applyPolicyPreset(policy *security.Policy, preset string) {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "", "default":
		return
	case "strict":
		policy.MaxPromptChars = 50000
		policy.MaxResponseChars = 200000
		policy.MaxToolResultBytes = 1000000
		policy.MaxConcurrentSteps = 2
		policy.ToolPolicies["filesystem.write_file"] = security.ToolPolicy{AllowedPaths: []string{security.DefaultOutputDir}, MaxBytes: 500000, MaxCalls: 1}
	case "ci":
		policy.NoAudit = true
		policy.MaxPromptChars = 100000
		policy.MaxResponseChars = 300000
		policy.MaxToolResultBytes = 1000000
		policy.MaxConcurrentSteps = 4
	case "local-dev":
		policy.MaxConcurrentSteps = 4
	case "unsafe-lab":
		policy.MaxPromptChars = 1000000
		policy.MaxResponseChars = 5000000
		policy.MaxToolResultBytes = 25000000
		policy.MaxConcurrentSteps = 16
	}
}

func confirmationPlan(p *config.Pipeline, opts PipelineOptions, policy *security.Policy) (string, error) {
	levels, err := runtime.Levels(p.Steps)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("mcpipe execution plan\n\n")
	if opts.Mock {
		b.WriteString("mode: mock\n")
	} else {
		b.WriteString("mode: live\n")
	}
	b.WriteString("output_dir: ")
	b.WriteString(policy.OutputDir)
	b.WriteByte('\n')
	b.WriteString("audit_dir: ")
	if policy.NoAudit {
		b.WriteString("<disabled>")
	} else {
		b.WriteString(policy.AuditDir)
	}
	b.WriteString("\n\n")
	b.WriteString("levels:\n")
	for i, level := range levels {
		var ids []string
		for _, step := range level {
			ids = append(ids, step.ID)
		}
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, strings.Join(ids, ", ")))
	}
	b.WriteString("\nproviders:\n")
	providers := map[string]bool{}
	for _, step := range p.Steps {
		effective := p.EffectiveStep(step)
		providers[effective.LLM.Backend+"/"+effective.LLM.Model] = true
	}
	for _, provider := range sortedBoolMapKeys(providers) {
		b.WriteString("  - ")
		b.WriteString(provider)
		b.WriteByte('\n')
	}
	b.WriteString("\nmcp servers:\n")
	if len(p.MCPServers) == 0 {
		b.WriteString("  none\n")
	}
	for _, name := range sortedMapKeys(p.MCPServers) {
		server := p.MCPServers[name]
		b.WriteString(fmt.Sprintf("  - %s: %s %s\n", name, server.Transport, server.Command))
	}
	b.WriteString("\ntool policies:\n")
	if len(policy.ToolPolicies) == 0 {
		b.WriteString("  defaults\n")
	}
	for _, name := range sortedSecurityPolicyKeys(policy.ToolPolicies) {
		item := policy.ToolPolicies[name]
		b.WriteString(fmt.Sprintf("  - %s: max_calls=%d max_bytes=%d paths=%s\n", name, item.MaxCalls, item.MaxBytes, strings.Join(item.AllowedPaths, ",")))
	}
	return policy.Redactor.RedactString(b.String()), nil
}

func sortedBoolMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedSecurityPolicyKeys(m map[string]security.ToolPolicy) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func ruleList(rules []string) string {
	if len(rules) == 0 {
		return "none"
	}
	clone := append([]string(nil), rules...)
	sort.Strings(clone)
	return strings.Join(clone, ", ")
}

func comparePipelines(oldPipeline, newPipeline *config.Pipeline) []string {
	var changes []string
	oldSteps := stepMap(oldPipeline.Steps)
	newSteps := stepMap(newPipeline.Steps)
	for _, id := range sortedStepIDs(oldSteps, newSteps) {
		oldStep, oldOK := oldSteps[id]
		newStep, newOK := newSteps[id]
		switch {
		case !oldOK:
			changes = append(changes, fmt.Sprintf("step added: %s", id))
			continue
		case !newOK:
			changes = append(changes, fmt.Sprintf("step removed: %s", id))
			continue
		}
		if oldStep.Prompt.System != newStep.Prompt.System || oldStep.Prompt.User != newStep.Prompt.User {
			changes = append(changes, fmt.Sprintf("step %s prompt changed", id))
		}
		oldLLM := oldPipeline.EffectiveStep(oldStep).LLM
		newLLM := newPipeline.EffectiveStep(newStep).LLM
		if oldLLM.Backend != newLLM.Backend || oldLLM.Model != newLLM.Model {
			changes = append(changes, fmt.Sprintf("step %s model changed: %s/%s -> %s/%s", id, oldLLM.Backend, oldLLM.Model, newLLM.Backend, newLLM.Model))
		}
		if strings.Join(oldStep.DependsOn, ",") != strings.Join(newStep.DependsOn, ",") {
			changes = append(changes, fmt.Sprintf("step %s dependencies changed", id))
		}
		if ruleList(oldStep.Tools.Allow) != ruleList(newStep.Tools.Allow) || ruleList(oldStep.Tools.Deny) != ruleList(newStep.Tools.Deny) {
			changes = append(changes, fmt.Sprintf("step %s tool rules changed", id))
		}
		if strings.Join(sortedStringMapKeys(oldStep.Outputs), ",") != strings.Join(sortedStringMapKeys(newStep.Outputs), ",") {
			changes = append(changes, fmt.Sprintf("step %s outputs changed", id))
		}
	}
	if strings.Join(oldPipeline.Output.Fields, ",") != strings.Join(newPipeline.Output.Fields, ",") {
		changes = append(changes, "selected output fields changed")
	}
	for _, name := range sortedMCPNames(oldPipeline.MCPServers, newPipeline.MCPServers) {
		oldServer, oldOK := oldPipeline.MCPServers[name]
		newServer, newOK := newPipeline.MCPServers[name]
		switch {
		case !oldOK:
			changes = append(changes, "mcp server added: "+name)
		case !newOK:
			changes = append(changes, "mcp server removed: "+name)
		case oldServer.Transport != newServer.Transport || oldServer.Command != newServer.Command || strings.Join(oldServer.Args, "\x00") != strings.Join(newServer.Args, "\x00"):
			changes = append(changes, "mcp server changed: "+name)
		}
	}
	sort.Strings(changes)
	return changes
}

func stepMap(steps []config.Step) map[string]config.Step {
	out := map[string]config.Step{}
	for _, step := range steps {
		out[step.ID] = step
	}
	return out
}

func sortedStepIDs(left, right map[string]config.Step) []string {
	seen := map[string]bool{}
	for id := range left {
		seen[id] = true
	}
	for id := range right {
		seen[id] = true
	}
	return sortedBoolMapKeys(seen)
}

func sortedMCPNames(left, right map[string]config.MCPServer) []string {
	seen := map[string]bool{}
	for id := range left {
		seen[id] = true
	}
	for id := range right {
		seen[id] = true
	}
	return sortedBoolMapKeys(seen)
}

type bundleManifest struct {
	Version   string            `json:"version"`
	CreatedAt string            `json:"created_at"`
	Pipeline  string            `json:"pipeline"`
	Files     map[string]string `json:"files_sha256"`
}

func buildBundle(p *config.Pipeline, pipelinePath, out string, inputs map[string]any, now time.Time) (string, error) {
	pipelineData, err := os.ReadFile(pipelinePath)
	if err != nil {
		return "", err
	}
	lock, err := security.BuildLock(pipelinePath, p, now)
	if err != nil {
		return "", err
	}
	lockData, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return "", err
	}
	graph, err := runtime.Graph(p, "mermaid")
	if err != nil {
		return "", err
	}
	dryRun, err := runtime.DryRun(p, inputs, now)
	if err != nil {
		return "", err
	}
	files := map[string][]byte{
		"pipeline.json": []byte(pipelineData),
		"pipeline.lock": lockData,
		"graph.mmd":     []byte(graph),
		"dry-run.txt":   []byte(dryRun),
	}
	manifest := bundleManifest{
		Version:   "1",
		CreatedAt: now.UTC().Format(time.RFC3339),
		Pipeline:  filepath.Base(pipelinePath),
		Files:     map[string]string{},
	}
	for name, data := range files {
		manifest.Files[name] = sha256Hex(data)
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	files["manifest.json"] = manifestData
	if err := os.MkdirAll(filepath.Dir(absOrClean(out)), 0755); err != nil {
		return "", err
	}
	file, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer file.Close()
	zw := zip.NewWriter(file)
	for _, name := range sortedByteMapKeys(files) {
		w, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			return "", err
		}
		if _, err := w.Write(files[name]); err != nil {
			_ = zw.Close()
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return out, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sortedByteMapKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func absOrClean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

type auditSummary struct {
	RunID     string
	StartedAt string
	EndedAt   string
	Status    string
	Steps     map[string]*auditStep
	Events    int
	Failures  []string
}

type auditStep struct {
	ID       string
	Status   string
	Attempts int
	Skipped  bool
	Error    string
}

func (s *auditSummary) Observe(event map[string]any) {
	s.Events++
	kind := stringValue(event["kind"])
	if s.StartedAt == "" {
		s.StartedAt = stringValue(event["ts"])
	}
	if ts := stringValue(event["ts"]); ts != "" {
		s.EndedAt = ts
	}
	if runID := stringValue(event["run_id"]); runID != "" && s.RunID == "" {
		s.RunID = runID
	}
	stepID := stringValue(event["step_id"])
	if stepID == "" {
		stepID = stringValue(event["step"])
	}
	if stepID != "" {
		step := s.step(stepID)
		switch kind {
		case "step_start":
			step.Status = "running"
			step.Attempts++
		case "step_success":
			step.Status = "success"
		case "step_skip":
			step.Status = "skipped"
			step.Skipped = true
		case "step_failure", "step_error":
			step.Status = "failed"
			step.Error = firstNonEmpty(stringValue(event["error"]), stringValue(event["message"]))
			if step.Error != "" {
				s.Failures = append(s.Failures, stepID+": "+step.Error)
			}
		case "step_end":
			if skipped, _ := event["skipped"].(bool); skipped {
				step.Status = "skipped"
				step.Skipped = true
			} else if msg := stringValue(event["error"]); msg != "" {
				step.Status = "failed"
				step.Error = msg
				s.Failures = append(s.Failures, stepID+": "+msg)
			} else {
				step.Status = "success"
			}
		}
	}
	switch kind {
	case "run_start":
		s.Status = "running"
	case "run_end":
		if s.Status == "" || s.Status == "running" {
			s.Status = firstNonEmpty(stringValue(event["status"]), "success")
		}
	case "run_failure", "run_error":
		s.Status = "failed"
		if msg := firstNonEmpty(stringValue(event["error"]), stringValue(event["message"])); msg != "" {
			s.Failures = append(s.Failures, msg)
		}
	}
}

func (s *auditSummary) step(id string) *auditStep {
	if s.Steps == nil {
		s.Steps = map[string]*auditStep{}
	}
	if existing := s.Steps[id]; existing != nil {
		return existing
	}
	step := &auditStep{ID: id}
	s.Steps[id] = step
	return step
}

func (s auditSummary) String() string {
	var b strings.Builder
	b.WriteString("Run Inspection\n")
	if s.RunID != "" {
		b.WriteString("  run_id: ")
		b.WriteString(s.RunID)
		b.WriteByte('\n')
	}
	b.WriteString("  status: ")
	b.WriteString(firstNonEmpty(s.Status, "unknown"))
	b.WriteByte('\n')
	if s.StartedAt != "" {
		b.WriteString("  started_at: ")
		b.WriteString(s.StartedAt)
		b.WriteByte('\n')
	}
	if s.EndedAt != "" {
		b.WriteString("  last_event_at: ")
		b.WriteString(s.EndedAt)
		b.WriteByte('\n')
	}
	b.WriteString(fmt.Sprintf("  events: %d\n\n", s.Events))
	b.WriteString("Steps\n")
	names := make([]string, 0, len(s.Steps))
	for name := range s.Steps {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		b.WriteString("  none\n")
	}
	for _, name := range names {
		step := s.Steps[name]
		b.WriteString("  - ")
		b.WriteString(step.ID)
		b.WriteString(": ")
		b.WriteString(firstNonEmpty(step.Status, "observed"))
		if step.Attempts > 0 {
			b.WriteString(fmt.Sprintf(" (%d attempt%s)", step.Attempts, plural(step.Attempts)))
		}
		if step.Error != "" {
			b.WriteString(" error=")
			b.WriteString(step.Error)
		}
		b.WriteByte('\n')
	}
	if len(s.Failures) > 0 {
		b.WriteString("\nFailures\n")
		for _, failure := range s.Failures {
			b.WriteString("  - ")
			b.WriteString(failure)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func renderSchemaDocs(schema map[string]any, source string) string {
	var b strings.Builder
	title := stringValue(schema["title"])
	if title == "" {
		title = "mcpipe Pipeline Schema"
	}
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString("Source: `")
	b.WriteString(source)
	b.WriteString("`\n\n")
	if desc := stringValue(schema["description"]); desc != "" {
		b.WriteString(desc)
		b.WriteString("\n\n")
	}
	if version := stringValue(schema["$id"]); version != "" {
		b.WriteString("Schema ID: `")
		b.WriteString(version)
		b.WriteString("`\n\n")
	}
	b.WriteString("## Top-Level Fields\n\n")
	if props, ok := schema["properties"].(map[string]any); ok {
		writeSchemaProperties(&b, props, requiredSet(schema["required"]))
	}
	if defs, ok := schema["$defs"].(map[string]any); ok && len(defs) > 0 {
		b.WriteString("\n## Definitions\n\n")
		names := sortedAnyMapKeys(defs)
		for _, name := range names {
			def, _ := defs[name].(map[string]any)
			b.WriteString("### ")
			b.WriteString(name)
			b.WriteString("\n\n")
			if desc := stringValue(def["description"]); desc != "" {
				b.WriteString(desc)
				b.WriteString("\n\n")
			}
			if props, ok := def["properties"].(map[string]any); ok {
				writeSchemaProperties(&b, props, requiredSet(def["required"]))
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func writeSchemaProperties(b *strings.Builder, props map[string]any, required map[string]bool) {
	names := sortedAnyMapKeys(props)
	for _, name := range names {
		prop, _ := props[name].(map[string]any)
		marker := "optional"
		if required[name] {
			marker = "required"
		}
		b.WriteString("- `")
		b.WriteString(name)
		b.WriteString("` ")
		b.WriteString(marker)
		if typ := schemaType(prop); typ != "" {
			b.WriteString(", ")
			b.WriteString(typ)
		}
		if desc := stringValue(prop["description"]); desc != "" {
			b.WriteString(": ")
			b.WriteString(desc)
		}
		b.WriteByte('\n')
	}
}

func schemaType(prop map[string]any) string {
	if typ := stringValue(prop["type"]); typ != "" {
		return typ
	}
	if ref := stringValue(prop["$ref"]); ref != "" {
		return ref
	}
	if _, ok := prop["anyOf"]; ok {
		return "anyOf"
	}
	if _, ok := prop["oneOf"]; ok {
		return "oneOf"
	}
	return ""
}

func requiredSet(raw any) map[string]bool {
	out := map[string]bool{}
	items, _ := raw.([]any)
	for _, item := range items {
		if name, ok := item.(string); ok {
			out[name] = true
		}
	}
	return out
}

func sortedAnyMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
