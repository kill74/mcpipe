package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"mcpipe/internal/cli"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mcpipe:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errors.New("missing command")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	app := cli.App{
		Stdout: stdout,
		Stderr: stderr,
		Now:    time.Now,
	}

	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		fs.SetOutput(stderr)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path := "pipeline.json"
		if fs.NArg() > 0 {
			path = fs.Arg(0)
		}
		return app.Init(path)
	case "new":
		opts, err := parseNewFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return app.New(opts)
	case "validate":
		opts, err := parsePipelineFlags("validate", args[1:], stderr, pipelineFlagSet{})
		if err != nil {
			return err
		}
		return app.Validate(opts)
	case "vet":
		opts, err := parsePipelineFlags("vet", args[1:], stderr, pipelineFlagSet{})
		if err != nil {
			return err
		}
		return app.Vet(opts)
	case "dry-run":
		opts, err := parsePipelineFlags("dry-run", args[1:], stderr, pipelineFlagSet{})
		if err != nil {
			return err
		}
		return app.DryRun(ctx, opts)
	case "run":
		opts, err := parsePipelineFlags("run", args[1:], stderr, pipelineFlagSet{mock: true, json: true})
		if err != nil {
			return err
		}
		return app.Run(ctx, opts)
	case "lock":
		opts, err := parseLockFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return app.Lock(opts)
	case "keygen":
		opts, err := parseKeygenFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return app.Keygen(opts)
	case "sign":
		opts, err := parseSignFlags("sign", args[1:], stderr, false)
		if err != nil {
			return err
		}
		return app.Sign(opts)
	case "verify":
		opts, err := parseSignFlags("verify", args[1:], stderr, true)
		if err != nil {
			return err
		}
		return app.Verify(opts)
	case "explain":
		opts, err := parsePipelineFlags("explain", args[1:], stderr, pipelineFlagSet{})
		if err != nil {
			return err
		}
		return app.Explain(opts)
	case "doctor":
		opts, err := parsePipelineFlags("doctor", args[1:], stderr, pipelineFlagSet{mock: true})
		if err != nil {
			return err
		}
		return app.Doctor(ctx, opts)
	case "graph":
		opts, err := parseGraphFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return app.Graph(opts)
	case "tools":
		opts, err := parsePipelineFlags("tools", args[1:], stderr, pipelineFlagSet{mock: true})
		if err != nil {
			return err
		}
		return app.Tools(ctx, opts)
	case "bundle":
		opts, err := parseBundleFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return app.Bundle(opts)
	case "providers":
		action, opts, err := parseEcosystemFlags("providers", args[1:], stderr)
		if err != nil {
			return err
		}
		return app.Providers(ctx, action, opts)
	case "mcp":
		action, opts, err := parseEcosystemFlags("mcp", args[1:], stderr)
		if err != nil {
			return err
		}
		return app.MCP(action, opts)
	case "plugins":
		action, opts, err := parseEcosystemFlags("plugins", args[1:], stderr)
		if err != nil {
			return err
		}
		return app.Plugins(action, opts)
	case "agents":
		action, opts, err := parseEcosystemFlags("agents", args[1:], stderr)
		if err != nil {
			return err
		}
		return app.Agents(action, opts)
	case "diff":
		opts, err := parseDiffFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return app.Diff(opts)
	case "replay":
		opts, err := parsePipelineFlags("replay", args[1:], stderr, pipelineFlagSet{json: true})
		if err != nil {
			return err
		}
		return app.Replay(ctx, opts)
	case "inspect":
		opts, err := parseInspectFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return app.InspectRun(opts)
	case "schema-docs":
		opts, err := parseSchemaDocsFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return app.SchemaDocs(opts)
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	case "version", "--version":
		fmt.Fprintf(stdout, "mcpipe %s\n", version)
		return nil
	case "completion":
		if len(args) < 2 {
			return errors.New("completion requires a shell: bash, zsh, or powershell")
		}
		script, err := completionScript(args[1])
		if err != nil {
			return err
		}
		fmt.Fprint(stdout, script)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type pipelineFlagSet struct {
	mock bool
	json bool
}

func parseNewFlags(args []string, stderr io.Writer) (cli.NewOptions, error) {
	opts := cli.NewOptions{}
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&opts.List, "list", false, "list available templates")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		opts.Template = fs.Arg(0)
	}
	if fs.NArg() > 1 {
		opts.Path = fs.Arg(1)
	}
	return opts, nil
}

func parseDiffFlags(args []string, stderr io.Writer) (cli.DiffOptions, error) {
	opts := cli.DiffOptions{}
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 2 {
		return opts, errors.New("diff requires OLD and NEW pipeline files")
	}
	opts.OldFile = fs.Arg(0)
	opts.NewFile = fs.Arg(1)
	return opts, nil
}

func parsePipelineFlags(name string, args []string, stderr io.Writer, flags pipelineFlagSet) (cli.PipelineOptions, error) {
	var inputs cli.InputFlags
	opts := cli.PipelineOptions{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.File, "f", "", "pipeline JSON file")
	fs.StringVar(&opts.File, "file", "", "pipeline JSON file")
	fs.StringVar(&opts.InputFile, "input-file", "", "JSON file containing pipeline inputs")
	fs.StringVar(&opts.OutputDir, "output-dir", "", "sandbox root for file-writing tools")
	fs.StringVar(&opts.AuditDir, "audit-dir", "", "directory for redacted JSONL audit logs")
	fs.BoolVar(&opts.NoAudit, "no-audit", false, "disable audit log writing")
	fs.StringVar(&opts.Lockfile, "lockfile", "", "pipeline lockfile path")
	fs.BoolVar(&opts.Locked, "locked", false, "refuse to run if the pipeline lockfile does not match")
	fs.BoolVar(&opts.RequireConfirmation, "require-confirmation", false, "print a live execution plan before running")
	fs.BoolVar(&opts.Yes, "yes", false, "confirm a --require-confirmation execution plan")
	fs.IntVar(&opts.MaxPromptChars, "max-prompt-chars", 200000, "maximum rendered prompt size")
	fs.IntVar(&opts.MaxResponseChars, "max-response-chars", 1000000, "maximum LLM response size")
	fs.IntVar(&opts.MaxToolResultBytes, "max-tool-result-bytes", 5000000, "maximum serialized tool result size")
	fs.IntVar(&opts.MaxConcurrentSteps, "max-concurrent-steps", 8, "maximum concurrently executing steps")
	fs.DurationVar(&opts.MaxRunDuration, "max-run-duration", 0, "maximum total run duration, e.g. 10m")
	fs.StringVar(&opts.PolicyPreset, "policy", "default", "policy preset: default, strict, ci, local-dev, unsafe-lab")
	fs.Var(&inputs, "input", "pipeline input in key=value form; may be repeated")
	if flags.mock {
		fs.BoolVar(&opts.Mock, "mock", false, "use deterministic mock LLM and MCP adapters")
	}
	if flags.json {
		fs.BoolVar(&opts.JSON, "json", false, "emit structured JSON output")
	}
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	opts.Inputs = inputs.Values()
	if strings.TrimSpace(opts.File) == "" {
		return opts, errors.New("missing -f pipeline file")
	}
	if !validPolicyPreset(opts.PolicyPreset) {
		return opts, fmt.Errorf("unsupported --policy %q", opts.PolicyPreset)
	}
	return opts, nil
}

func validPolicyPreset(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "default", "strict", "ci", "local-dev", "unsafe-lab":
		return true
	default:
		return false
	}
}

func parseBundleFlags(args []string, stderr io.Writer) (cli.BundleOptions, error) {
	var inputs cli.InputFlags
	opts := cli.BundleOptions{}
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.File, "f", "", "pipeline JSON file")
	fs.StringVar(&opts.File, "file", "", "pipeline JSON file")
	fs.StringVar(&opts.Out, "out", "", "bundle output path")
	fs.StringVar(&opts.InputFile, "input-file", "", "JSON file containing pipeline inputs")
	fs.Var(&inputs, "input", "pipeline input in key=value form; may be repeated")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	opts.Inputs = inputs.Values()
	if strings.TrimSpace(opts.File) == "" {
		return opts, errors.New("missing -f pipeline file")
	}
	return opts, nil
}

func parseEcosystemFlags(name string, args []string, stderr io.Writer) (string, cli.EcosystemOptions, error) {
	opts := cli.EcosystemOptions{}
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.File, "f", "", "pipeline JSON file")
	fs.StringVar(&opts.File, "file", "", "pipeline JSON file")
	fs.BoolVar(&opts.Mock, "mock", false, "skip live checks")
	if err := fs.Parse(args); err != nil {
		return action, opts, err
	}
	if action == "doctor" || name == "mcp" || name == "plugins" || name == "agents" {
		if strings.TrimSpace(opts.File) == "" {
			return action, opts, errors.New("missing -f pipeline file")
		}
	}
	return action, opts, nil
}

func parseLockFlags(args []string, stderr io.Writer) (cli.LockOptions, error) {
	opts := cli.LockOptions{}
	fs := flag.NewFlagSet("lock", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.File, "f", "", "pipeline JSON file")
	fs.StringVar(&opts.File, "file", "", "pipeline JSON file")
	fs.StringVar(&opts.Out, "out", "", "lockfile path")
	fs.BoolVar(&opts.Verify, "verify", false, "verify an existing lockfile")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if strings.TrimSpace(opts.File) == "" {
		return opts, errors.New("missing -f pipeline file")
	}
	return opts, nil
}

func parseKeygenFlags(args []string, stderr io.Writer) (cli.KeygenOptions, error) {
	opts := cli.KeygenOptions{}
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Private, "private", "", "private key output path")
	fs.StringVar(&opts.Public, "public", "", "public key output path")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	return opts, nil
}

func parseSignFlags(name string, args []string, stderr io.Writer, includeSig bool) (cli.SignOptions, error) {
	opts := cli.SignOptions{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.File, "f", "", "pipeline file")
	fs.StringVar(&opts.File, "file", "", "pipeline file")
	fs.StringVar(&opts.Key, "key", "", "private key for sign, public key for verify")
	fs.StringVar(&opts.Out, "out", "", "signature output path")
	if includeSig {
		fs.StringVar(&opts.Sig, "sig", "", "signature file")
	}
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	return opts, nil
}

func parseGraphFlags(args []string, stderr io.Writer) (cli.GraphOptions, error) {
	opts := cli.GraphOptions{Format: "mermaid"}
	fs := flag.NewFlagSet("graph", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.File, "f", "", "pipeline JSON file")
	fs.StringVar(&opts.File, "file", "", "pipeline JSON file")
	fs.StringVar(&opts.Format, "format", "mermaid", "graph format: mermaid or dot")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if strings.TrimSpace(opts.File) == "" {
		return opts, errors.New("missing -f pipeline file")
	}
	return opts, nil
}

func parseInspectFlags(args []string, stderr io.Writer) (cli.InspectOptions, error) {
	opts := cli.InspectOptions{}
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.File, "f", "", "redacted audit JSONL file")
	fs.StringVar(&opts.File, "file", "", "redacted audit JSONL file")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 && opts.File == "" {
		switch fs.Arg(0) {
		case "run":
			if fs.NArg() > 1 {
				opts.File = fs.Arg(1)
			}
		default:
			opts.File = fs.Arg(0)
		}
	}
	if strings.TrimSpace(opts.File) == "" {
		return opts, errors.New("missing audit file")
	}
	return opts, nil
}

func parseSchemaDocsFlags(args []string, stderr io.Writer) (cli.SchemaDocsOptions, error) {
	opts := cli.SchemaDocsOptions{Schema: "schemas/pipeline/v1.json"}
	fs := flag.NewFlagSet("schema-docs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Schema, "schema", opts.Schema, "JSON schema path")
	fs.StringVar(&opts.Out, "out", "", "write Markdown docs to this path")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	return opts, nil
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `mcpipe executes JSON LLM/MCP pipelines.

Usage:
  mcpipe init [path]
  mcpipe new [template] [path]
  mcpipe validate -f pipeline.json
  mcpipe vet -f pipeline.json
  mcpipe explain -f pipeline.json
  mcpipe doctor -f pipeline.json [--input-file inputs.json] [--input key=value] [--mock]
  mcpipe graph -f pipeline.json [--format mermaid|dot]
  mcpipe tools -f pipeline.json [--mock]
  mcpipe bundle -f pipeline.json [--input key=value] [--out run.mcpipebundle]
  mcpipe providers list|doctor [-f pipeline.json] [--mock]
  mcpipe mcp list|doctor -f pipeline.json [--mock]
  mcpipe plugins list -f pipeline.json
  mcpipe agents list -f pipeline.json
  mcpipe diff old.pipeline.json new.pipeline.json
  mcpipe inspect run audit.jsonl
  mcpipe schema-docs [--schema schemas/pipeline/v1.json] [--out docs/schema.md]
  mcpipe dry-run -f pipeline.json --input topic="quantum computing" [--input-file inputs.json]
  mcpipe replay -f pipeline.json --input topic="quantum computing" [--json]
  mcpipe run -f pipeline.json --input topic="quantum computing" [--input-file inputs.json] [--mock] [--json] [--locked]
  mcpipe lock -f pipeline.json [--out pipeline.lock] [--verify]
  mcpipe keygen --private mcpipe.key --public mcpipe.pub
  mcpipe sign -f pipeline.json --key mcpipe.key [--out pipeline.sig]
  mcpipe verify -f pipeline.json --key mcpipe.pub --sig pipeline.sig
  mcpipe completion bash|zsh|powershell
  mcpipe version`)
}

func completionScript(shell string) (string, error) {
	commands := "init new validate vet explain doctor graph tools bundle providers mcp plugins agents diff inspect schema-docs dry-run replay run lock keygen sign verify completion version help"
	commonFlags := "-f --file --input --input-file --mock --json --format --output-dir --audit-dir --no-audit --locked --lockfile --require-confirmation --yes --private --public --key --sig --out --schema --list --policy"
	switch strings.ToLower(shell) {
	case "bash":
		return `_mcpipe_completion() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local prev="${COMP_WORDS[COMP_CWORD-1]}"
  if [[ $COMP_CWORD -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "` + commands + `" -- "$cur") )
    return 0
  fi
  case "$prev" in
    --format) COMPREPLY=( $(compgen -W "mermaid dot" -- "$cur") ); return 0 ;;
    completion) COMPREPLY=( $(compgen -W "bash zsh powershell" -- "$cur") ); return 0 ;;
  esac
  COMPREPLY=( $(compgen -W "` + commonFlags + `" -- "$cur") )
}
complete -F _mcpipe_completion mcpipe
`, nil
	case "zsh":
		return `#compdef mcpipe
_mcpipe() {
  local -a commands flags formats shells
  commands=(` + strings.ReplaceAll(commands, " ", " ") + `)
  flags=(-f --file --input --input-file --mock --json --format --output-dir --audit-dir --no-audit --locked --lockfile --require-confirmation --yes --private --public --key --sig --out --schema --list --policy)
  formats=(mermaid dot)
  shells=(bash zsh powershell)
  if (( CURRENT == 2 )); then
    _describe 'command' commands
    return
  fi
  case "$words[CURRENT-1]" in
    --format) _describe 'format' formats; return ;;
    completion) _describe 'shell' shells; return ;;
  esac
  _describe 'flag' flags
}
_mcpipe "$@"
`, nil
	case "powershell", "pwsh":
		return `Register-ArgumentCompleter -Native -CommandName mcpipe -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $commands = @('init','new','validate','vet','explain','doctor','graph','tools','bundle','providers','mcp','plugins','agents','diff','inspect','schema-docs','dry-run','replay','run','lock','keygen','sign','verify','completion','version','help')
  $flags = @('-f','--file','--input','--input-file','--mock','--json','--format','--output-dir','--audit-dir','--no-audit','--locked','--lockfile','--require-confirmation','--yes','--private','--public','--key','--sig','--out','--schema','--list','--policy')
  $tokens = $commandAst.CommandElements | ForEach-Object { $_.ToString() }
  if ($tokens.Count -le 2) {
    $commands | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
    return
  }
  $previous = if ($tokens.Count -gt 1) { $tokens[-2] } else { '' }
  $values = switch ($previous) {
    '--format' { @('mermaid','dot') }
    'completion' { @('bash','zsh','powershell') }
    default { $flags }
  }
  $values | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
}
`, nil
	default:
		return "", fmt.Errorf("unsupported completion shell %q", shell)
	}
}
