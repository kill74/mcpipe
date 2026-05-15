package template

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Context struct {
	Inputs      map[string]any
	StepOutputs map[string]map[string]string
	Response    Response
	Now         time.Time
	Env         map[string]string
}

type Response struct {
	Text        string
	ToolResults []map[string]any
}

var interpolationPattern = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

func RenderString(input string, ctx Context) (string, error) {
	var firstErr error
	rendered := interpolationPattern.ReplaceAllStringFunc(input, func(match string) string {
		if firstErr != nil {
			return match
		}
		parts := interpolationPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			firstErr = fmt.Errorf("invalid template expression %q", match)
			return match
		}
		value, err := Eval(parts[1], ctx)
		if err != nil {
			firstErr = err
			return match
		}
		return fmt.Sprint(value)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return rendered, nil
}

func Eval(expr string, ctx Context) (any, error) {
	parts := splitPipeline(expr)
	if len(parts) == 0 {
		return "", nil
	}
	value, err := resolveValue(strings.TrimSpace(parts[0]), ctx)
	if err != nil {
		return nil, err
	}
	for _, filter := range parts[1:] {
		value, err = applyFilter(value, strings.TrimSpace(filter))
		if err != nil {
			return nil, err
		}
	}
	return value, nil
}

func splitPipeline(expr string) []string {
	var parts []string
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range expr {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && quote != 0:
			b.WriteRune(r)
			escaped = true
		case quote != 0:
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			b.WriteRune(r)
			quote = r
		case r == '|':
			parts = append(parts, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	parts = append(parts, strings.TrimSpace(b.String()))
	return parts
}

func resolveValue(expr string, ctx Context) (any, error) {
	if expr == "" {
		return "", nil
	}
	if expr == "now" {
		if ctx.Now.IsZero() {
			return time.Now(), nil
		}
		return ctx.Now, nil
	}
	if value, ok, err := parseQuoted(expr); ok || err != nil {
		return value, err
	}
	if strings.HasPrefix(expr, "inputs.") {
		name := strings.TrimPrefix(expr, "inputs.")
		value, ok := ctx.Inputs[name]
		if !ok {
			return nil, fmt.Errorf("unknown input reference %q", expr)
		}
		return value, nil
	}
	if strings.HasPrefix(expr, "steps.") {
		return resolveStepOutput(expr, ctx)
	}
	if expr == "response.text" {
		return ctx.Response.Text, nil
	}
	if strings.HasPrefix(expr, "response.tool_results[") {
		return resolveToolResult(expr, ctx)
	}
	if strings.HasPrefix(expr, "env.") {
		name := strings.TrimPrefix(expr, "env.")
		if ctx.Env != nil {
			if value, ok := ctx.Env[name]; ok {
				return value, nil
			}
		}
		if value := os.Getenv(name); value != "" {
			return value, nil
		}
		return "", nil
	}
	return nil, fmt.Errorf("unsupported template expression %q", expr)
}

func resolveStepOutput(expr string, ctx Context) (string, error) {
	parts := strings.Split(expr, ".")
	if len(parts) != 4 || parts[0] != "steps" || parts[2] != "outputs" {
		return "", fmt.Errorf("invalid step output reference %q", expr)
	}
	stepOutputs, ok := ctx.StepOutputs[parts[1]]
	if !ok {
		return "", fmt.Errorf("step %q has no outputs in scope", parts[1])
	}
	value, ok := stepOutputs[parts[3]]
	if !ok {
		return "", fmt.Errorf("step %q output %q is not available", parts[1], parts[3])
	}
	return value, nil
}

var toolResultPattern = regexp.MustCompile(`^response\.tool_results\[(\d+)\]\.([A-Za-z0-9_-]+)$`)

func resolveToolResult(expr string, ctx Context) (any, error) {
	match := toolResultPattern.FindStringSubmatch(expr)
	if len(match) != 3 {
		return nil, fmt.Errorf("invalid tool result reference %q", expr)
	}
	index, err := strconv.Atoi(match[1])
	if err != nil {
		return nil, fmt.Errorf("invalid tool result index in %q: %w", expr, err)
	}
	if index < 0 || index >= len(ctx.Response.ToolResults) {
		return nil, fmt.Errorf("tool result index %d out of range", index)
	}
	value, ok := ctx.Response.ToolResults[index][match[2]]
	if !ok {
		return nil, fmt.Errorf("tool result %d has no field %q", index, match[2])
	}
	return value, nil
}

func applyFilter(value any, filter string) (any, error) {
	name, arg, hasArg := strings.Cut(filter, ":")
	name = strings.TrimSpace(name)
	switch name {
	case "slugify":
		if hasArg {
			return nil, fmt.Errorf("slugify filter does not accept arguments")
		}
		return Slugify(fmt.Sprint(value)), nil
	case "date":
		if !hasArg {
			return nil, fmt.Errorf("date filter requires a format argument")
		}
		t, ok := value.(time.Time)
		if !ok {
			return nil, fmt.Errorf("date filter requires a time value")
		}
		format, ok, err := parseQuoted(strings.TrimSpace(arg))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("date filter format must be quoted")
		}
		return t.Format(strftimeToGo(fmt.Sprint(format))), nil
	case "upper":
		if hasArg {
			return nil, fmt.Errorf("upper filter does not accept arguments")
		}
		return strings.ToUpper(fmt.Sprint(value)), nil
	case "lower":
		if hasArg {
			return nil, fmt.Errorf("lower filter does not accept arguments")
		}
		return strings.ToLower(fmt.Sprint(value)), nil
	case "trim":
		if hasArg {
			return nil, fmt.Errorf("trim filter does not accept arguments")
		}
		return strings.TrimSpace(fmt.Sprint(value)), nil
	case "truncate":
		if !hasArg {
			return nil, fmt.Errorf("truncate filter requires a length argument")
		}
		limit, err := strconv.Atoi(strings.TrimSpace(arg))
		if err != nil {
			return nil, fmt.Errorf("truncate filter requires a numeric length: %w", err)
		}
		s := fmt.Sprint(value)
		if len(s) <= limit {
			return s, nil
		}
		if limit <= 3 {
			return s[:limit], nil
		}
		return s[:limit-3] + "...", nil
	case "json":
		if hasArg {
			return nil, fmt.Errorf("json filter does not accept arguments")
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("json filter: %w", err)
		}
		return string(data), nil
	case "base64":
		if hasArg {
			return nil, fmt.Errorf("base64 filter does not accept arguments")
		}
		return base64.StdEncoding.EncodeToString([]byte(fmt.Sprint(value))), nil
	default:
		return nil, fmt.Errorf("unsupported filter %q", name)
	}
}

func parseQuoted(expr string) (string, bool, error) {
	if len(expr) < 2 {
		return "", false, nil
	}
	if (expr[0] != '"' || expr[len(expr)-1] != '"') && (expr[0] != '\'' || expr[len(expr)-1] != '\'') {
		return "", false, nil
	}
	if expr[0] == '\'' {
		return expr[1 : len(expr)-1], true, nil
	}
	value, err := strconv.Unquote(expr)
	if err != nil {
		return "", true, fmt.Errorf("invalid quoted string %q: %w", expr, err)
	}
	return value, true, nil
}

func Slugify(input string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(input) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func strftimeToGo(format string) string {
	replacements := []struct {
		from string
		to   string
	}{
		{"%Y", "2006"},
		{"%y", "06"},
		{"%m", "01"},
		{"%d", "02"},
		{"%H", "15"},
		{"%M", "04"},
		{"%S", "05"},
		{"%z", "-0700"},
	}
	out := format
	for _, replacement := range replacements {
		out = strings.ReplaceAll(out, replacement.from, replacement.to)
	}
	return out
}
