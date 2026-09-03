package ta2a

import "fmt"

// HITLConfig configures declarative human-in-the-loop pauses on specific role triggers.
type HITLConfig struct {
	TriggerWords []string `json:"triggerWords"`
	Prompt       string   `json:"prompt"`
	Options      []string `json:"options,omitempty"` // Defaults to ["approve", "reject"]
}

// AgentBinding declares an action's binding to an A2A agent role with optional configuration.
type AgentBinding struct {
	Role            string
	Description     string
	Examples        []string
	Args            any
	DefaultArgs     any
	ArtifactName    string
	ArtifactMime    string
	SummaryTemplate string
	HITL            *HITLConfig
}

func (b AgentBinding) String() string {
	return fmt.Sprintf("a2a: role=%s", b.Role)
}

// Role starts declaring an A2A agent role route binding.
func Role(role string) AgentBinding {
	return AgentBinding{Role: role}
}

// WithArgs defines strict argument overrides applied after client input.
func (b AgentBinding) WithArgs(args any) AgentBinding {
	b.Args = args
	return b
}

// WithDefaultArgs defines fallback defaults that client input can override.
func (b AgentBinding) WithDefaultArgs(defaults any) AgentBinding {
	b.DefaultArgs = defaults
	return b
}

// WithArtifact specifies the output artifact name and MIME type for generated content.
func (b AgentBinding) WithArtifact(name, mimeType string) AgentBinding {
	b.ArtifactName = name
	b.ArtifactMime = mimeType
	return b
}

// WithSummaryTemplate formats task.Text using result placeholders (e.g. ${FUNC_COUNT}, ${FILE_COUNT}).
func (b AgentBinding) WithSummaryTemplate(tmpl string) AgentBinding {
	b.SummaryTemplate = tmpl
	return b
}

// WithHumanInTheLoop attaches declarative pause-and-approval protection to the role.
func (b AgentBinding) WithHumanInTheLoop(cfg HITLConfig) AgentBinding {
	if len(cfg.Options) == 0 {
		cfg.Options = []string{"approve", "reject"}
	}
	b.HITL = &cfg
	return b
}

func (b AgentBinding) WithDescription(description string) AgentBinding {
	b.Description = description
	return b
}

func (b AgentBinding) WithExamples(examples ...string) AgentBinding {
	b.Examples = append(b.Examples, examples...)
	return b
}
