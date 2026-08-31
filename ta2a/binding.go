package ta2a

type AgentBinding struct {
	Role string
}

func (b AgentBinding) String() string {
	return "a2a: " + b.Role
}

func Role(role string) AgentBinding {
	return AgentBinding{Role: role}
}
