package sample

import (
	"fmt"
	"strconv"
)

type Template struct {
	Name        string
	Description string
	Body        string
}

func TemplateByName(name string) (Template, bool) {
	tpl, ok := templates()[name]
	return tpl, ok
}

func TemplateNames() []string {
	return []string{"research", "code-review", "docs-digest", "extract"}
}

func templates() map[string]Template {
	return map[string]Template{
		"research": {
			Name:        "research",
			Description: "Multi-source research digest with MCP tools.",
			Body:        ResearchDigestPipeline,
		},
		"code-review": {
			Name:        "code-review",
			Description: "Local code review pipeline for focused findings.",
			Body:        simplePipeline("Code Review", "repository_path", "Review the codebase at {{ inputs.repository_path }}. Return prioritized bugs, risks, and missing tests.", "review"),
		},
		"docs-digest": {
			Name:        "docs-digest",
			Description: "Summarize a documentation topic into an implementation digest.",
			Body:        simplePipeline("Docs Digest", "topic", "Create a concise implementation digest for {{ inputs.topic }}. Include decisions, examples, and gotchas.", "digest"),
		},
		"extract": {
			Name:        "extract",
			Description: "Extract structured facts from pasted text.",
			Body:        simplePipeline("Structured Extraction", "text", "Extract names, dates, amounts, decisions, and open questions from this text:\n\n{{ inputs.text }}", "facts"),
		},
	}
}

func simplePipeline(name, input, prompt, output string) string {
	return fmt.Sprintf(`{
  "$schema": "https://mcpipe.dev/schemas/pipeline/v1.json",
  "version": "1.0.0",
  "metadata": {
    "id": "%s",
    "name": "%s"
  },
  "defaults": {
    "timeout_ms": 30000,
    "retry": {
      "max_attempts": 2,
      "backoff": "exponential",
      "backoff_base_ms": 500,
      "retryable_errors": ["timeout", "server_error", "rate_limit"]
    },
    "llm": {
      "backend": "ollama",
      "model": "qwen3:7b",
      "temperature": 0.2,
      "max_tokens": 2048
    }
  },
  "inputs": {
    "%s": {
      "type": "string",
      "required": true
    }
  },
  "steps": [
    {
      "id": "%s",
      "prompt": {
        "system": "You are precise, practical, and concise.",
        "user": "%s"
      },
      "outputs": {
        "%s": "{{ response.text }}"
      }
    }
  ],
  "output": {
    "format": "markdown",
    "destination": "stdout",
    "include_run_metadata": true,
    "fields": ["steps.%s.outputs.%s"]
  }
}
`, slug(name), name, input, output, escapeJSON(prompt), output, output, output)
}

func slug(value string) string {
	out := ""
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out += string(r)
		case r >= 'A' && r <= 'Z':
			out += string(r + 32)
		default:
			if out != "" && out[len(out)-1] != '-' {
				out += "-"
			}
		}
	}
	return out
}

func escapeJSON(value string) string {
	quoted := strconv.Quote(value)
	return quoted[1 : len(quoted)-1]
}

const ResearchDigestPipeline = `{
  "$schema": "https://mcpipe.dev/schemas/pipeline/v1.json",
  "version": "1.0.0",

  "metadata": {
    "id": "research-digest-pipeline",
    "name": "Research & Digest",
    "description": "Multi-source research, parallel summarization, and final synthesis into a structured digest.",
    "author": "jdoe",
    "tags": ["research", "summarization", "multi-llm"],
    "created_at": "2026-05-09T08:00:00Z",
    "updated_at": "2026-05-09T12:34:00Z"
  },

  "schedule": {
    "enabled": true,
    "cron": "0 8 * * MON-FRI",
    "timezone": "Europe/Lisbon",
    "on_failure": "notify_and_skip"
  },

  "defaults": {
    "timeout_ms": 30000,
    "retry": {
      "max_attempts": 3,
      "backoff": "exponential",
      "backoff_base_ms": 500,
      "retryable_errors": ["rate_limit", "timeout", "server_error"]
    },
    "llm": {
      "backend": "ollama",
      "model": "qwen3:7b",
      "temperature": 0.3,
      "max_tokens": 2048,
      "stream": true
    }
  },

  "inputs": {
    "topic": {
      "type": "string",
      "description": "The subject to research.",
      "required": true,
      "example": "quantum computing breakthroughs 2026"
    },
    "depth": {
      "type": "enum",
      "values": ["brief", "standard", "deep"],
      "default": "standard"
    },
    "output_lang": {
      "type": "string",
      "default": "en",
      "pattern": "^[a-z]{2}$"
    }
  },

  "mcp_servers": {
    "brave_search": {
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-brave-search"],
      "env": {
        "BRAVE_API_KEY": "${env:BRAVE_API_KEY}"
      },
      "health_check": {
        "enabled": true,
        "interval_ms": 60000
      }
    },
    "filesystem": {
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/mcpipe-outputs"],
      "env": {}
    },
    "arxiv": {
      "transport": "sse",
      "url": "http://localhost:7860/sse",
      "headers": {
        "Authorization": "Bearer ${env:ARXIV_MCP_TOKEN}"
      },
      "reconnect": {
        "enabled": true,
        "max_attempts": 5,
        "delay_ms": 2000
      }
    }
  },

  "steps": [
    {
      "id": "web_search",
      "name": "Web search",
      "description": "Broad web search for recent developments on the topic.",
      "llm": {
        "backend": "ollama",
        "model": "qwen3:7b",
        "temperature": 0.1
      },
      "prompt": {
        "system": "You are a research assistant. Extract only factual, verifiable information. Do not speculate.",
        "user": "Search the web for the most recent and relevant information about: {{ inputs.topic }}. Focus on results from the last 90 days. Return a structured list of findings."
      },
      "tools": {
        "allow": ["brave_search.*"],
        "deny": []
      },
      "agent": {
        "enabled": true,
        "max_iterations": 5,
        "stop_on": "no_tool_call"
      },
      "outputs": {
        "web_findings": "{{ response.text }}"
      }
    },

    {
      "id": "academic_search",
      "name": "Academic paper search",
      "description": "Pulls recent peer-reviewed papers from arXiv on the topic.",
      "parallel_group": "research_phase",
      "depends_on": [],
      "llm": {
        "backend": "ollama",
        "model": "qwen3:7b",
        "temperature": 0.1
      },
      "prompt": {
        "system": "You are a scientific literature assistant. Retrieve and summarize academic papers.",
        "user": "Find the 5 most relevant recent arXiv papers on: {{ inputs.topic }}. For each, extract: title, authors, date, abstract, and key contributions."
      },
      "tools": {
        "allow": ["arxiv.*"]
      },
      "agent": {
        "enabled": true,
        "max_iterations": 3,
        "stop_on": "no_tool_call"
      },
      "outputs": {
        "academic_findings": "{{ response.text }}"
      }
    },

    {
      "id": "web_summarize",
      "name": "Summarize web results",
      "description": "Condenses raw web findings into key points.",
      "parallel_group": "summarize_phase",
      "depends_on": ["web_search"],
      "llm": {
        "backend": "anthropic",
        "model": "claude-sonnet-4-20250514",
        "temperature": 0.4,
        "max_tokens": 1024
      },
      "prompt": {
        "system": "You are a senior analyst. Produce concise, high-signal summaries. Cut filler.",
        "user": "Summarize the following web research findings into 5-7 bullet points. Highlight novel or surprising insights.\n\n{{ steps.web_search.outputs.web_findings }}"
      },
      "tools": {
        "allow": []
      },
      "outputs": {
        "web_summary": "{{ response.text }}"
      }
    },

    {
      "id": "academic_summarize",
      "name": "Summarize academic papers",
      "description": "Distills arXiv findings for a technical audience.",
      "parallel_group": "summarize_phase",
      "depends_on": ["academic_search"],
      "llm": {
        "backend": "anthropic",
        "model": "claude-sonnet-4-20250514",
        "temperature": 0.3,
        "max_tokens": 1024
      },
      "prompt": {
        "system": "You are a technical research summarizer. Be precise. Use domain terminology correctly.",
        "user": "Summarize the following academic paper findings. Group related papers thematically and highlight consensus vs. disagreement.\n\n{{ steps.academic_search.outputs.academic_findings }}"
      },
      "tools": {
        "allow": []
      },
      "outputs": {
        "academic_summary": "{{ response.text }}"
      }
    },

    {
      "id": "synthesis",
      "name": "Synthesize digest",
      "description": "Merges all summaries into a final structured digest.",
      "depends_on": ["web_summarize", "academic_summarize"],
      "llm": {
        "backend": "anthropic",
        "model": "claude-sonnet-4-20250514",
        "temperature": 0.5,
        "max_tokens": 4096
      },
      "prompt": {
        "system": "You are a senior researcher writing a professional digest. Your output must be structured, authoritative, and actionable. Language: {{ inputs.output_lang }}.",
        "user": "Synthesize the following two research summaries into a unified digest on: {{ inputs.topic }}.\n\n## Web research\n{{ steps.web_summarize.outputs.web_summary }}\n\n## Academic research\n{{ steps.academic_summarize.outputs.academic_summary }}\n\nOutput format:\n1. Executive summary (3 sentences max)\n2. Key findings (bullet list)\n3. Open questions / unknowns\n4. Recommended next steps\n5. Sources and references"
      },
      "tools": {
        "allow": []
      },
      "outputs": {
        "digest": "{{ response.text }}"
      }
    },

    {
      "id": "persist",
      "name": "Write output to disk",
      "description": "Saves the final digest as a markdown file.",
      "depends_on": ["synthesis"],
      "llm": {
        "backend": "ollama",
        "model": "qwen3:1.7b",
        "temperature": 0.0
      },
      "prompt": {
        "system": "You are a file-writing assistant. Follow instructions exactly.",
        "user": "Write the following content to a file named 'digest-{{ inputs.topic | slugify }}-{{ now | date: \"%Y%m%d\" }}.md':\n\n{{ steps.synthesis.outputs.digest }}"
      },
      "tools": {
        "allow": ["filesystem.write_file"]
      },
      "agent": {
        "enabled": true,
        "max_iterations": 2,
        "stop_on": "no_tool_call"
      },
      "outputs": {
        "file_path": "{{ response.tool_results[0].path }}"
      }
    }
  ],

  "error_handling": {
    "on_step_failure": "fail_fast",
    "fallback_steps": {
      "academic_search": {
        "skip_if_error": true,
        "reason": "arXiv server is optional; pipeline proceeds without academic data"
      }
    },
    "on_pipeline_failure": {
      "notify": {
        "channel": "stderr",
        "include_run_id": true,
        "include_failed_step": true
      }
    }
  },

  "policy": {
    "filesystem.write_file": {
      "allowed_paths": [".mcpipe/outputs"],
      "max_bytes": 1000000,
      "max_calls": 2
    },
    "brave_search.*": {
      "max_calls": 5
    },
    "arxiv.*": {
      "max_calls": 5
    }
  },

  "output": {
    "format": "markdown",
    "destination": "stdout",
    "include_run_metadata": true,
    "fields": [
      "steps.synthesis.outputs.digest",
      "steps.persist.outputs.file_path"
    ]
  },

  "observability": {
    "run_history": {
      "enabled": true,
      "storage": "sqlite",
      "path": "~/.mcpipe/history.db",
      "retention_days": 90
    },
    "metrics": {
      "enabled": true,
      "emit": ["step_duration_ms", "token_counts", "tool_call_counts", "retry_counts"]
    },
    "dry_run": {
      "show_prompt_previews": true,
      "show_tool_call_plan": true,
      "show_variable_bindings": true
    }
  }
}
`
